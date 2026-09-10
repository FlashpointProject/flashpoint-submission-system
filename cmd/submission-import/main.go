// submission-import copies only MariaDB submission tables into a disposable,
// already migrated PostgreSQL clone. No application services or workers run.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

var tables = []string{"discord_user", "discord_role", "action", "submission_level", "submission_notification_type", "curation_image_type", "discord_user_role", "submission", "submission_file", "curation_meta", "curation_image", "comment", "submission_notification_subscription", "notification_settings", "submission_notification", "masterdb_game", "submission_cache", "session", "oauth_client"}
var seeds = map[string]bool{"discord_user": true, "action": true, "submission_level": true, "submission_notification_type": true, "curation_image_type": true}
var ctx = context.Background()

type tableResult struct {
	ElapsedMS                               float64
	SourceRows, ImportedRows, DiscardedRows int64
	SourceSHA256, TargetSHA256              string
	SequenceNext                            int64
}
type manifest struct {
	Started                        string
	SourceVersion, TargetVersion   int64
	SourceDatabase, TargetDatabase string
	Tables                         map[string]tableResult
	Checks                         map[string]int64
	ExistingBefore, ExistingAfter  map[string]string
	Complete                       bool
	Error                          string
}
type column struct{ name, kind string }

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func mq(s string) string    { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func die(e error) {
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func main() {
	out := flag.String("out", "import-results", "manifest and discarded subscription mapping directory")
	flag.Parse()
	die(os.MkdirAll(*out, 0700))
	m := manifest{Started: time.Now().UTC().Format(time.RFC3339), Tables: map[string]tableResult{}, Checks: map[string]int64{}}
	err := run(*out, &m)
	if err != nil {
		m.Error = err.Error()
	}
	b, e := json.MarshalIndent(m, "", "  ")
	die(e)
	die(os.WriteFile(filepath.Join(*out, "manifest.json"), append(b, '\n'), 0600))
	die(err)
	fmt.Println("Import committed; table checksums and preserved PostgreSQL fingerprints match.")
}
func run(out string, m *manifest) error {
	cfg, e := mysql.ParseDSN(os.Getenv("SUBMISSION_IMPORT_MARIA_DSN"))
	if e != nil {
		return errors.New("invalid source DSN")
	}
	if e = validateSource(cfg); e != nil {
		return e
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	cfg.Params["tx_read_only"] = "1"
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	source, e := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return e
	}
	defer source.Rollback()
	pc, e := pgx.ParseConfig(os.Getenv("SUBMISSION_IMPORT_PG_DSN"))
	if e != nil {
		return errors.New("invalid target DSN")
	}
	if e = validateTarget(pc); e != nil {
		return e
	}
	p, e := pgx.ConnectConfig(ctx, pc)
	if e != nil {
		return e
	}
	defer p.Close(ctx)
	m.SourceDatabase = cfg.DBName
	m.TargetDatabase = pc.Database
	var dirty bool
	if e = source.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&m.SourceVersion, &dirty); e != nil {
		return e
	}
	if dirty || (m.SourceVersion != 27 && m.SourceVersion != 28) {
		return errors.New("source migration must be clean version 27 or 28")
	}
	if e = p.QueryRow(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&m.TargetVersion, &dirty); e != nil {
		return e
	}
	if dirty || (m.TargetVersion < 14 || m.TargetVersion > 17) {
		return errors.New("target migration must be clean version 14 through 17")
	}
	for _, t := range []string{"session", "oauth_client"} {
		var n int64
		if e = source.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+mq(t)).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return fmt.Errorf("source auth table %s must be empty", t)
		}
	}
	if e = preflight(source, m); e != nil {
		return e
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Advisory lock serializes importer attempts. Imported rows are transactional;
	// failed runs restore bootstrap rows, but setval changes survive rollback.
	// A retry reseeds those sequences again before committing.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7374696)"); e != nil {
		return e
	}
	for _, t := range append(append([]string{}, tables...), "submission_creation_lock") {
		var n int64
		if e = tx.QueryRow(ctx, "SELECT COUNT(*) FROM public."+quote(t)).Scan(&n); e != nil {
			return e
		}
		if n > 0 && !seeds[t] {
			return fmt.Errorf("refusing populated target submission table %s", t)
		}
	}
	cols := map[string][]column{}
	for _, t := range tables {
		cols[t], e = columns(source, cfg.DBName, t)
		if e != nil {
			return e
		}
		if e = verifyTargetColumns(tx, t, cols[t]); e != nil {
			return e
		}
		if seeds[t] {
			if e = verifySeeds(source, tx, t, cols[t]); e != nil {
				return e
			}
		}
	}
	m.ExistingBefore, e = fingerprintExisting(tx)
	if e != nil {
		return e
	}
	// Delete only verified bootstrap rows, in reverse dependency order. Existing
	// catalog tables never appear in any mutation statement issued by this tool.
	for i := len(tables) - 1; i >= 0; i-- {
		if seeds[tables[i]] {
			if _, e = tx.Exec(ctx, "DELETE FROM public."+quote(tables[i])); e != nil {
				return e
			}
		}
	}
	if e = writeDiscardMapping(source, out, m); e != nil {
		return e
	}
	for _, t := range tables {
		start := time.Now()
		cs := cols[t]
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.name
		}
		var original int64
		if e = source.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+mq(t)).Scan(&original); e != nil {
			return e
		}
		q := sourceQuery(t, cs)
		rows, e := source.QueryContext(ctx, q)
		if e != nil {
			return e
		}
		reader := &copyReader{rows: rows, columns: cs, h: sha256.New(), table: t}
		n, e := tx.CopyFrom(ctx, pgx.Identifier{"public", t}, names, reader)
		rows.Close()
		if e != nil {
			return fmt.Errorf("copy %s: %w", t, e)
		}
		r := tableResult{SourceRows: original, ImportedRows: n, DiscardedRows: original - n, SourceSHA256: hex.EncodeToString(reader.h.Sum(nil))}
		r.TargetSHA256, e = targetDigest(tx, t, cs)
		if e != nil {
			return e
		}
		if r.SourceSHA256 != r.TargetSHA256 {
			return fmt.Errorf("checksum mismatch for %s: %s", t, mismatch(source, tx, t, cs))
		}
		if t == "submission_notification_subscription" && r.DiscardedRows != m.Checks["discarded_subscription_mapping_rows"] {
			return errors.New("subscription mapping does not account for all discarded rows")
		}
		r.SequenceNext, e = reseed(source, tx, cfg.DBName, t)
		if e != nil {
			return e
		}
		r.ElapsedMS = float64(time.Since(start).Microseconds()) / 1000
		m.Tables[t] = r
		fmt.Printf("%s: %d -> %d rows; checksum verified (%s)\n", t, original, n, time.Since(start).Round(time.Millisecond))
	}
	if e = auditCrossDomain(tx, m); e != nil {
		return e
	}
	m.ExistingAfter, e = fingerprintExisting(tx)
	if e != nil {
		return e
	}
	a, _ := json.Marshal(m.ExistingBefore)
	b, _ := json.Marshal(m.ExistingAfter)
	if string(a) != string(b) {
		return errors.New("existing PostgreSQL tables changed during import")
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	m.Complete = true
	return nil
}

// COPY names columns explicitly, so order may differ. Require complete name
// sets: otherwise an omitted source field could silently acquire a target
// default/NULL and escape source-column-only checksum verification.
func compareColumnNames(table string, source []column, target []string) error {
	sourceNames, targetNames := map[string]bool{}, map[string]bool{}
	for _, c := range source {
		sourceNames[c.name] = true
	}
	for _, name := range target {
		targetNames[name] = true
	}
	var missing, extra []string
	for name := range targetNames {
		if !sourceNames[name] {
			missing = append(missing, name)
		}
	}
	for name := range sourceNames {
		if !targetNames[name] {
			extra = append(extra, name)
		}
	}
	if len(sourceNames) == 0 || len(targetNames) == 0 || len(missing) > 0 || len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		return fmt.Errorf("source/target column mismatch for %s: missing source columns=%v; extra source columns=%v (source=%d target=%d)", table, missing, extra, len(sourceNames), len(targetNames))
	}
	return nil
}

func verifyTargetColumns(tx pgx.Tx, table string, source []column) error {
	rows, err := tx.Query(ctx, `SELECT column_name FROM information_schema.columns
WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	var target []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		target = append(target, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return compareColumnNames(table, source, target)
}

func columns(s *sql.Tx, db, t string) ([]column, error) {
	r, e := s.QueryContext(ctx, "SELECT COLUMN_NAME,DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY ORDINAL_POSITION", db, t)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var cs []column
	for r.Next() {
		var c column
		if e = r.Scan(&c.name, &c.kind); e != nil {
			return nil, e
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("missing source table %s", t)
	}
	return cs, r.Err()
}
func sourceQuery(t string, cs []column) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = mq(c.name)
	}
	q := "SELECT " + strings.Join(names, ",") + " FROM " + mq(t)
	if t == "submission_notification_subscription" {
		q += " s WHERE NOT EXISTS (SELECT 1 FROM submission_notification_subscription w WHERE w.fk_user_id=s.fk_user_id AND w.fk_submission_id=s.fk_submission_id AND (w.created_at<s.created_at OR (w.created_at=s.created_at AND w.id<s.id)))"
	}
	order := "id"
	if t == "submission_cache" {
		order = "fk_submission_id"
	}
	if t == "oauth_client" {
		order = "client_id"
	}
	return q + " ORDER BY " + mq(order)
}
func normalize(v any, c column) (any, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	// The MySQL text protocol returns BIGINT as bytes when parseTime is
	// enabled for this query. Parse directly to int64, never through float64.
	if x, ok := v.(string); ok && (c.kind == "bigint" || c.kind == "tinyint" || c.kind == "int") {
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return nil, errors.New("integer outside signed 64-bit domain")
		}
		v = n
	}
	if t, ok := v.(time.Time); ok {
		if t.IsZero() {
			return nil, errors.New("zero timestamp is not representable")
		}
		return t.UTC(), nil
	}
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) || strings.IndexByte(x, 0) >= 0 {
			return nil, errors.New("invalid UTF-8 or embedded NUL")
		}
		if c.name == "additional_applications" && !json.Valid([]byte(x)) {
			return nil, errors.New("invalid JSON")
		}
	case int64:
		if c.name == "game_exists" || c.name == "should_autofreeze" || c.name == "mfa_enabled" {
			if x != 0 && x != 1 {
				return nil, errors.New("boolean outside 0/1 domain")
			}
			return x == 1, nil
		}
	}
	return v, nil
}
func hashRow(h hash.Hash, values []any) error {
	vs := make([]any, len(values))
	for i, v := range values {
		if t, ok := v.(time.Time); ok {
			vs[i] = t.UTC().Format("2006-01-02T15:04:05.000000")
		} else {
			vs[i] = v
		}
	}
	return json.NewEncoder(h).Encode(vs)
}

type copyReader struct {
	rows    *sql.Rows
	columns []column
	h       hash.Hash
	table   string
	err     error
}

func (r *copyReader) Next() bool { return r.err == nil && r.rows.Next() }
func (r *copyReader) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.rows.Err()
}
func (r *copyReader) Values() ([]any, error) {
	v := make([]any, len(r.columns))
	ptr := make([]any, len(v))
	for i := range v {
		ptr[i] = &v[i]
	}
	if e := r.rows.Scan(ptr...); e != nil {
		return nil, e
	}
	for i, c := range r.columns {
		x, e := normalize(v[i], c)
		if e != nil {
			return nil, fmt.Errorf("%s column %s row ID %v: %w", r.table, c.name, v[0], e)
		}
		v[i] = x
	}
	r.err = hashRow(r.h, v)
	return v, r.err
}
func targetDigest(tx pgx.Tx, t string, cs []column) (string, error) {
	names := make([]string, len(cs))
	for i, c := range cs {
		n := quote(c.name)
		if c.name == "additional_applications" {
			n += "::text"
		}
		names[i] = n
	}
	order := "id"
	if t == "submission_cache" {
		order = "fk_submission_id"
	}
	if t == "oauth_client" {
		order = "client_id"
	}
	r, e := tx.Query(ctx, "SELECT "+strings.Join(names, ",")+" FROM public."+quote(t)+" ORDER BY "+quote(order))
	if e != nil {
		return "", e
	}
	defer r.Close()
	h := sha256.New()
	for r.Next() {
		v, e := r.Values()
		if e != nil {
			return "", e
		}
		if e = hashRow(h, v); e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), r.Err()
}
func verifySeeds(s *sql.Tx, tx pgx.Tx, t string, cs []column) error {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = quote(c.name)
	}
	rows, e := tx.Query(ctx, "SELECT "+strings.Join(names, ",")+" FROM public."+quote(t))
	if e != nil {
		return e
	}
	var existing [][]any
	for rows.Next() {
		v, e := rows.Values()
		if e != nil {
			rows.Close()
			return e
		}
		existing = append(existing, v)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	for _, v := range existing {
		if t == "discord_user" {
			id, ok := v[0].(int64)
			if !ok || (id != 810112564787675166 && id != 844246603102945333) {
				return fmt.Errorf("refusing non-bootstrap target user ID %v", v[0])
			}
		}
		q := sourceQuery(t, cs)
		q = strings.Split(q, " ORDER BY ")[0] + " WHERE id=?"
		rr, e := s.QueryContext(ctx, q, v[0])
		if e != nil {
			return e
		}
		r := &copyReader{rows: rr, columns: cs, h: sha256.New(), table: t}
		if !r.Next() {
			rr.Close()
			return fmt.Errorf("bootstrap %s id %v absent from source", t, v[0])
		}
		src, e := r.Values()
		rr.Close()
		if e != nil {
			return e
		}
		// Bootstrap user profiles are mutable, unlike enum definitions. The
		// source profile is authoritative for those two known bootstrap IDs.
		if t == "discord_user" {
			continue
		}
		ha, hb := sha256.New(), sha256.New()
		hashRow(ha, src)
		hashRow(hb, v)
		if !strings.EqualFold(hex.EncodeToString(ha.Sum(nil)), hex.EncodeToString(hb.Sum(nil))) {
			return fmt.Errorf("bootstrap %s id %v conflicts with source", t, v[0])
		}
	}
	return nil
}
func reseed(s *sql.Tx, tx pgx.Tx, db, t string) (int64, error) {
	var next sql.NullInt64
	if e := s.QueryRowContext(ctx, "SELECT AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?", db, t).Scan(&next); e != nil {
		return 0, e
	}
	if !next.Valid {
		return 0, nil
	}
	var seq *string
	if e := tx.QueryRow(ctx, "SELECT pg_get_serial_sequence($1,'id')", "public."+t).Scan(&seq); e != nil {
		return 0, e
	}
	if seq == nil {
		return 0, fmt.Errorf("source auto increment %s lacks target sequence", t)
	}
	var max int64
	if e := tx.QueryRow(ctx, "SELECT COALESCE(MAX(id),0) FROM public."+quote(t)).Scan(&max); e != nil {
		return 0, e
	}
	n, e := nextIdentity(next.Int64, max)
	if e != nil {
		return 0, fmt.Errorf("sequence %s: %w", t, e)
	}
	_, e = tx.Exec(ctx, "SELECT setval($1::regclass,$2,false)", *seq, n)
	return n, e
}
func fingerprintExisting(tx pgx.Tx) (map[string]string, error) {
	rows, e := tx.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
	if e != nil {
		return nil, e
	}
	var names []string
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		names = append(names, n)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	skip := map[string]bool{"schema_migrations": true, "submission_creation_lock": true}
	for _, t := range tables {
		skip[t] = true
	}
	result := map[string]string{}
	for _, t := range names {
		if skip[t] {
			continue
		}
		var n int64
		var sum string
		if e = tx.QueryRow(ctx, "SELECT COUNT(*), COALESCE(SUM(hashtextextended(row_to_json(t)::text,0)::numeric),0)::text FROM public."+quote(t)+" t").Scan(&n, &sum); e != nil {
			return nil, e
		}
		result[t] = strconv.FormatInt(n, 10) + ":" + sum
	}
	return result, nil
}

// These are source comparison semantics frozen by migrations 15 and 16.
// Inspect column collations, not just database/table defaults: OAuth inherited
// general_ci, while MariaDB's JSON alias explicitly uses utf8mb4_bin.
type sourceTextColumn struct{ table, name, collation string }

func validateSourceTextCollations(columns []sourceTextColumn) error {
	for _, c := range columns {
		want := "utf8mb4_unicode_ci"
		switch {
		case c.table == "oauth_client" && (c.name == "client_id" || c.name == "client_secret"):
			want = "utf8mb4_general_ci"
		case c.table == "curation_meta" && c.name == "additional_applications":
			want = "utf8mb4_bin"
		}
		if c.collation != want {
			return fmt.Errorf("source column %s.%s collation=%s expected=%s", c.table, c.name, c.collation, want)
		}
	}
	return nil
}

func verifySourceCollations(s *sql.Tx) error {
	rows, err := s.QueryContext(ctx, `SELECT TABLE_NAME,COLUMN_NAME,COLLATION_NAME
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA=DATABASE() AND COLLATION_NAME IS NOT NULL
 AND TABLE_NAME <> 'schema_migrations'
ORDER BY TABLE_NAME,ORDINAL_POSITION`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var columns []sourceTextColumn
	for rows.Next() {
		var c sourceTextColumn
		if err := rows.Scan(&c.table, &c.name, &c.collation); err != nil {
			return err
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return validateSourceTextCollations(columns)
}

func preflight(s *sql.Tx, m *manifest) error {
	if err := verifySourceCollations(s); err != nil {
		return err
	}
	known := map[string]bool{"schema_migrations": true, "submission_creation_lock": true}
	for _, t := range tables {
		known[t] = true
	}
	inventory, e := s.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE'")
	if e != nil {
		return e
	}
	for inventory.Next() {
		var t string
		if e = inventory.Scan(&t); e != nil {
			inventory.Close()
			return e
		}
		if !known[t] {
			inventory.Close()
			return fmt.Errorf("unmapped source table %s", t)
		}
	}
	inventory.Close()
	if inventory.Err() != nil {
		return inventory.Err()
	}
	checks := map[string]string{
		"cache_duplicate_parent":    "SELECT COUNT(*) FROM (SELECT fk_submission_id FROM submission_cache GROUP BY fk_submission_id HAVING COUNT(*)>1) x",
		"cache_null_parent":         "SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id IS NULL",
		"metadata_duplicate_file":   "SELECT COUNT(*) FROM (SELECT fk_submission_file_id FROM curation_meta GROUP BY fk_submission_file_id HAVING COUNT(*)>1) x",
		"preference_duplicate_pair": "SELECT COUNT(*) FROM (SELECT fk_user_id,fk_action_id FROM notification_settings GROUP BY fk_user_id,fk_action_id HAVING COUNT(*)>1) x",
		"role_duplicate_pair":       "SELECT COUNT(*) FROM (SELECT fk_uid,fk_rid FROM discord_user_role GROUP BY fk_uid,fk_rid HAVING COUNT(*)>1) x",
	}
	for name, q := range checks {
		var n int64
		if e := s.QueryRowContext(ctx, q).Scan(&n); e != nil {
			return e
		}
		m.Checks[name] = n
		if n != 0 {
			return fmt.Errorf("preflight %s has %d ambiguous rows/groups", name, n)
		}
	}
	rows, e := s.QueryContext(ctx, "SELECT TABLE_NAME,COLUMN_NAME,REFERENCED_TABLE_NAME,REFERENCED_COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND REFERENCED_TABLE_NAME IS NOT NULL")
	if e != nil {
		return e
	}
	var fks [][4]string
	for rows.Next() {
		var f [4]string
		if e = rows.Scan(&f[0], &f[1], &f[2], &f[3]); e != nil {
			return e
		}
		fks = append(fks, f)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	for _, f := range fks {
		q := "SELECT COUNT(*) FROM " + mq(f[0]) + " c LEFT JOIN " + mq(f[2]) + " p ON c." + mq(f[1]) + "=p." + mq(f[3]) + " WHERE c." + mq(f[1]) + " IS NOT NULL AND p." + mq(f[3]) + " IS NULL"
		var n int64
		if e = s.QueryRowContext(ctx, q).Scan(&n); e != nil {
			return e
		}
		name := "orphan_" + f[0] + "_" + f[1]
		m.Checks[name] = n
		if n != 0 {
			return fmt.Errorf("preflight %s has %d rows", name, n)
		}
	}
	return nil
}
func writeDiscardMapping(s *sql.Tx, out string, m *manifest) error {
	f, e := os.OpenFile(filepath.Join(out, "discarded-subscriptions.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	r, e := s.QueryContext(ctx, `SELECT id,winner FROM (SELECT id,FIRST_VALUE(id) OVER (PARTITION BY fk_user_id,fk_submission_id ORDER BY created_at,id) winner FROM submission_notification_subscription) x WHERE id<>winner ORDER BY id`)
	if e != nil {
		return e
	}
	defer r.Close()
	enc := json.NewEncoder(f)
	for r.Next() {
		var id, winner int64
		if e = r.Scan(&id, &winner); e != nil {
			return e
		}
		m.Checks["discarded_subscription_mapping_rows"]++
		if e = enc.Encode(struct{ DiscardedID, RetainedID int64 }{id, winner}); e != nil {
			return e
		}
	}
	return r.Err()
}

// Missing cross-domain relationships are reported, not repaired or rejected:
// history may legitimately outlive catalog objects, and metadata game_exists is
// a persisted upload-time fact rather than a live foreign key.
func auditCrossDomain(tx pgx.Tx, m *manifest) error {
	queries := map[string]string{
		"existing_additional_app_missing_game":           `SELECT COUNT(*) FROM additional_app a WHERE NOT EXISTS (SELECT 1 FROM game g WHERE g.id=a.parent_game_id)`,
		"activity_uid_missing_discord_user":              `SELECT COUNT(*) FROM activity_events e WHERE NOT EXISTS (SELECT 1 FROM discord_user u WHERE u.id=e.uid)`,
		"metadata_uuid_null":                             `SELECT COUNT(*) FROM curation_meta WHERE uuid IS NULL`,
		"metadata_uuid_empty":                            `SELECT COUNT(*) FROM curation_meta WHERE uuid=''`,
		"metadata_uuid_noncanonical":                     `SELECT COUNT(*) FROM curation_meta WHERE uuid IS NOT NULL AND uuid<>'' AND uuid !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'`,
		"legacy_uuid_noncanonical":                       `SELECT COUNT(*) FROM masterdb_game WHERE uuid !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'`,
		"metadata_uuid_missing_catalog_case_insensitive": `SELECT COUNT(*) FROM curation_meta m WHERE m.uuid IS NOT NULL AND m.uuid<>'' AND NOT EXISTS(SELECT 1 FROM game g WHERE g.id=m.uuid::citext)`,
		"legacy_uuid_missing_catalog_case_insensitive":   `SELECT COUNT(*) FROM masterdb_game m WHERE NOT EXISTS(SELECT 1 FROM game g WHERE g.id=m.uuid::citext)`,
	}
	for key, table := range map[string]string{"submission_id": "submission", "comment_id": "comment", "file_id": "submission_file"} {
		// Numeric JSON identifiers are compared as numeric, avoiding bigint overflow
		// from malformed/unexpected legacy event payloads.
		queries["activity_"+key+"_missing_parent"] = `SELECT COUNT(*) FROM activity_events e WHERE e.event_data->>'` + key + `' ~ '^[0-9]+$' AND NOT EXISTS(SELECT 1 FROM ` + quote(table) + ` p WHERE p.id::numeric=(e.event_data->>'` + key + `')::numeric)`
	}
	for name, q := range queries {
		var n int64
		if e := tx.QueryRow(ctx, q).Scan(&n); e != nil {
			return fmt.Errorf("audit %s: %w", name, e)
		}
		m.Checks[name] = n
	}
	return nil
}

func mismatch(s *sql.Tx, tx pgx.Tx, table string, cs []column) string {
	mr, e := s.QueryContext(ctx, sourceQuery(table, cs))
	if e != nil {
		return e.Error()
	}
	defer mr.Close()
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = quote(c.name)
	}
	pr, e := tx.Query(ctx, "SELECT "+strings.Join(names, ",")+" FROM public."+quote(table)+" ORDER BY "+quote(cs[0].name))
	if e != nil {
		return e.Error()
	}
	defer pr.Close()
	reader := &copyReader{rows: mr, columns: cs, h: sha256.New(), table: table}
	for reader.Next() {
		sv, e := reader.Values()
		if e != nil {
			return e.Error()
		}
		if !pr.Next() {
			return "target ended early"
		}
		pv, e := pr.Values()
		if e != nil {
			return e.Error()
		}
		for i := range sv {
			a, _ := json.Marshal(sv[i])
			b, _ := json.Marshal(pv[i])
			if string(a) != string(b) {
				return fmt.Sprintf("row ID %v column %s source type %T target type %T", sv[0], cs[i].name, sv[i], pv[i])
			}
		}
	}
	return "rows match individually; stream hashing error"
}

func validateSource(c *mysql.Config) error {
	if c.Net != "tcp" || c.Addr != "mariadb:3306" || (c.DBName != "fpfss" && c.DBName != "snapshot_cache_work_scoped") {
		return errors.New("source must be an isolated snapshot MariaDB database")
	}
	return nil
}
func validateTarget(c *pgx.ConnConfig) error {
	if c.Host != "postgres" || !strings.HasPrefix(c.Database, "submission_import_") {
		return errors.New("target must be postgres host and a disposable submission_import_* database")
	}
	return nil
}

func nextIdentity(sourceNext, maximum int64) (int64, error) {
	if maximum == math.MaxInt64 {
		return 0, errors.New("signed bigint identity space exhausted")
	}
	n := sourceNext
	if n <= maximum {
		n = maximum + 1
	}
	if n < 1 {
		n = 1
	}
	return n, nil
}
