// snapshot-search-baseline records the current DAL against immutable local snapshots.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/go-sql-driver/mysql"
	"github.com/sirupsen/logrus"
)

type query struct {
	SQL  string
	Args []any
}
type capture struct {
	sync.Mutex
	Queries map[string]query
}
type captureKey struct{}
type recordingDriver struct{}
type recordingConn struct{ driver.Conn }

func (d recordingDriver) Open(s string) (driver.Conn, error) {
	c, e := (&mysql.MySQLDriver{}).Open(s)
	if e != nil {
		return nil, e
	}
	return recordingConn{c}, nil
}
func (c recordingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if r, ok := ctx.Value(captureKey{}).(*capture); ok {
		a := make([]any, len(args))
		for i, v := range args {
			a[i] = v.Value
		}
		kind := "page"
		if strings.HasPrefix(q, "SELECT COUNT(*)") {
			kind = "count"
		}
		r.Lock()
		r.Queries[kind] = query{q, a}
		r.Unlock()
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c recordingConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c recordingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

type workload struct {
	Name   string
	UserID int64
	Filter types.SubmissionsFilter
}
type corpus struct {
	Count int64
	Rows  []*types.ExtendedSubmission
}
type measurement struct {
	Name, Variant, Error string
	Count                int64
	Rows                 int
	Digest               string
	FirstObservedMS      float64
	WarmMS               []float64
	PageMS, CountMS      float64
}
type report struct {
	Started, Conditions string
	Variants            []string
	Repetitions         int
	Variables           map[string]string
	Results             []measurement
	CacheDifferences    []string
}

func ptr[T any](v T) *T { return &v }
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func canonical(rows []*types.ExtendedSubmission) {
	for _, r := range rows {
		slices.Sort(r.AssignedTestingUserIDs)
		slices.Sort(r.AssignedVerificationUserIDs)
		slices.Sort(r.RequestedChangesUserIDs)
		slices.Sort(r.ApprovedUserIDs)
		slices.Sort(r.VerifiedUserIDs)
		slices.Sort(r.DistinctActions)
	}
}
func digest(c corpus) string { b, _ := json.Marshal(c); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func openDB(dsn, name string) (*sql.DB, error) {
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		return nil, fmt.Errorf("invalid snapshot DSN")
	}
	if cfg.Net != "tcp" || cfg.Addr != "mariadb:3306" {
		return nil, fmt.Errorf("only isolated mariadb:3306 is supported")
	}
	cfg.DBName = name
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	// Every pooled connection, including the out-of-transaction count, is read-only.
	cfg.Params["tx_read_only"] = "1"
	db, e := sql.Open("snapshot-recording", cfg.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	return db, db.Ping()
}
func search(ctx context.Context, db *sql.DB, w workload, cap *capture) (corpus, float64, error) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, utils.CtxKeys.UserID, w.UserID), 90*time.Second)
	defer cancel()
	if cap != nil {
		ctx = context.WithValue(ctx, captureKey{}, cap)
	}
	d := database.NewMysqlDAL(db)
	start := time.Now()
	s, e := d.NewSession(ctx)
	if e != nil {
		return corpus{}, 0, e
	}
	defer s.Rollback()
	rows, n, e := d.SearchSubmissions(s, &w.Filter)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if e == nil {
		e = ctx.Err()
	}
	if e == nil && n < 0 {
		e = fmt.Errorf("count query failed")
	}
	if e == nil {
		e = checkPage(w.Filter, n, len(rows))
	}
	canonical(rows)
	return corpus{n, rows}, ms, e
}

// On an immutable snapshot, page cardinality must agree with the separate count.
func checkPage(f types.SubmissionsFilter, count int64, got int) error {
	limit, page := int64(100), int64(1)
	if f.ResultsPerPage != nil {
		limit = *f.ResultsPerPage
	}
	if f.Page != nil {
		page = *f.Page
	}
	want := max(int64(0), min(limit, count-(page-1)*limit))
	if int64(got) != want {
		return fmt.Errorf("page/count mismatch: got %d, want %d from total %d", got, want, count)
	}
	return nil
}
func drain(ctx context.Context, db *sql.DB, q query) (float64, error) {
	start := time.Now()
	rows, e := db.QueryContext(ctx, q.SQL, q.Args...)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return 0, e
	}
	dest := make([]any, len(cols))
	for i := range dest {
		dest[i] = new(any)
	}
	for rows.Next() {
		if e = rows.Scan(dest...); e != nil {
			return 0, e
		}
	}
	return float64(time.Since(start).Microseconds()) / 1000, rows.Err()
}
func workloads(db *sql.DB) ([]workload, error) {
	var uid, sid int64
	var title, md5 string
	if e := db.QueryRow(`SELECT f.fk_user_id FROM submission s JOIN submission_cache c ON c.fk_submission_id=s.id JOIN submission_file f ON f.id=c.fk_oldest_file_id WHERE s.deleted_at IS NULL GROUP BY f.fk_user_id ORDER BY COUNT(*) DESC,f.fk_user_id LIMIT 1`).Scan(&uid); e != nil {
		return nil, e
	}
	if e := db.QueryRow(`SELECT s.id,m.title,f.md5sum FROM submission s JOIN submission_cache c ON c.fk_submission_id=s.id JOIN submission_file f ON f.id=c.fk_newest_file_id JOIN curation_meta m ON m.fk_submission_file_id=f.id WHERE s.deleted_at IS NULL AND LENGTH(m.title)>8 AND LENGTH(f.md5sum)=32 ORDER BY s.id DESC LIMIT 1`).Scan(&sid, &title, &md5); e != nil {
		return nil, e
	}
	w := []workload{
		{"all-updated", uid, types.SubmissionsFilter{}},
		{"live-updated", uid, types.SubmissionsFilter{ExcludeLegacy: true}},
		{"all-page-2", uid, types.SubmissionsFilter{Page: ptr(int64(2))}},
		{"all-deep-page", uid, types.SubmissionsFilter{Page: ptr(int64(1000))}},
		{"all-size-asc", uid, types.SubmissionsFilter{OrderBy: ptr("size"), AscDesc: ptr("asc")}},
		{"all-uploaded-asc", uid, types.SubmissionsFilter{OrderBy: ptr("uploaded"), AscDesc: ptr("asc")}},
		{"title-selective", uid, types.SubmissionsFilter{TitlePartial: &title}},
		{"platform-broad", uid, types.SubmissionsFilter{PlatformPartial: ptr("Flash")}},
		{"platform-exclusion", uid, types.SubmissionsFilter{PlatformPartial: ptr("Flash,!Unity")}},
		{"submission-id", uid, types.SubmissionsFilter{SubmissionIDs: []int64{sid}}},
		{"busy-submitter", uid, types.SubmissionsFilter{SubmitterID: &uid}},
		{"hash-selective", uid, types.SubmissionsFilter{MD5SumPartialAny: &md5}},
		{"approved", uid, types.SubmissionsFilter{ApprovalsStatus: ptr("approved")}},
		{"unassigned-testing", uid, types.SubmissionsFilter{AssignedStatusTesting: ptr("unassigned")}},
		{"not-rejected", uid, types.SubmissionsFilter{DistinctActionsNot: []string{"reject"}, ExcludeLegacy: true}},
		{"subscribed-me", uid, types.SubmissionsFilter{SubscribedMe: ptr("yes")}},
		{"not-subscribed-me", uid, types.SubmissionsFilter{SubscribedMe: ptr("no")}},
		{"content-changes", uid, types.SubmissionsFilter{IsContentChange: ptr("yes")}},
	}
	// Select an actual member of each set rather than measuring only empty matches.
	for _, s := range []struct{ name, col, yes, no string }{
		{"testing", "active_assigned_testing_ids", "assigned", "unassigned"},
		{"verification-assigned", "active_assigned_verification_ids", "assigned", "unassigned"},
		{"changes", "active_requested_changes_ids", "ongoing", "none"},
		{"approval", "active_approved_ids", "yes", "no"},
		{"verified", "active_verified_ids", "yes", "no"},
	} {
		var member int64
		if e := db.QueryRow(`SELECT CAST(SUBSTRING_INDEX(` + s.col + `,',',1) AS SIGNED) FROM submission_cache WHERE ` + s.col + ` IS NOT NULL AND ` + s.col + `<>'' ORDER BY fk_submission_id LIMIT 1`).Scan(&member); e != nil {
			return nil, e
		}
		for _, status := range []string{s.yes, s.no} {
			f := types.SubmissionsFilter{}
			switch s.name {
			case "testing":
				f.AssignedStatusTestingMe = &status
			case "verification-assigned":
				f.AssignedStatusVerificationMe = &status
			case "changes":
				f.RequestedChangedStatusMe = &status
			case "approval":
				f.ApprovalsStatusMe = &status
			case "verified":
				f.VerificationStatusMe = &status
			}
			w = append(w, workload{"me-" + s.name + "-" + status, member, f})
		}
	}
	return w, nil
}

// Repair witnesses ensure a broad first-page corpus cannot hide known cache repairs.
// Filter values come from source files, never from repaired concatenated strings.
func repairWorkloads(db *sql.DB) ([]workload, error) {
	rows, e := db.Query(`SELECT a.fk_submission_id, f.original_filename, f.current_filename, f.md5sum, f.sha256sum
 FROM submission_cache a JOIN snapshot_cache_work_scoped.submission_cache b ON b.fk_submission_id=a.fk_submission_id
 JOIN submission_file f ON f.id=a.fk_newest_file_id
 WHERE NOT (BINARY a.original_filename_sequence <=> BINARY b.original_filename_sequence)
 OR NOT (BINARY a.current_filename_sequence <=> BINARY b.current_filename_sequence)
 OR NOT (BINARY a.md5sum_sequence <=> BINARY b.md5sum_sequence)
 OR NOT (BINARY a.sha256sum_sequence <=> BINARY b.sha256sum_sequence)
 ORDER BY a.fk_submission_id`)
	if e != nil {
		return nil, e
	}
	var w []workload
	for rows.Next() {
		var id int64
		var original, current, md5, sha string
		if e = rows.Scan(&id, &original, &current, &md5, &sha); e != nil {
			rows.Close()
			return nil, e
		}
		for _, x := range []struct{ name, value string }{{"original", original}, {"current", current}, {"md5", md5}, {"sha256", sha}} {
			f := types.SubmissionsFilter{SubmissionIDs: []int64{id}}
			switch x.name {
			case "original":
				f.OriginalFilenamePartialAny = &x.value
			case "current":
				f.CurrentFilenamePartialAny = &x.value
			case "md5":
				f.MD5SumPartialAny = &x.value
			case "sha256":
				f.SHA256SumPartialAny = &x.value
			}
			w = append(w, workload{fmt.Sprintf("repair-file-%d-%s", id, x.name), 0, f})
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = db.Query(`SELECT a.fk_submission_id FROM submission_cache a JOIN snapshot_cache_work_scoped.submission_cache b ON b.fk_submission_id=a.fk_submission_id
 WHERE NOT (a.fk_newest_comment_id <=> b.fk_newest_comment_id) OR NOT (BINARY a.active_approved_ids <=> BINARY b.active_approved_ids) ORDER BY a.fk_submission_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	// Bound IN-list size while retaining all witnesses and a full page per batch.
	for i := 0; i < len(ids); i += 100 {
		batch := ids[i:min(i+100, len(ids))]
		w = append(w, workload{fmt.Sprintf("repair-history-%03d", i/100), 0, types.SubmissionsFilter{SubmissionIDs: batch, ResultsPerPage: ptr(int64(100))}})
	}
	return w, nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	out := flag.String("out", "/results", "new artifact directory")
	reps := flag.Int("repetitions", 3, "warm repetitions per workload")
	suite := flag.String("suite", "standard", "standard, repairs, or ui (actual page flows and preset buttons)")
	variantsFlag := flag.String("variants", "original,rebuilt", "comma-separated snapshot variants: original,rebuilt or one of them")
	flag.Parse()
	variants, err := parseVariants(*variantsFlag)
	if err != nil {
		return err
	}
	if *reps < 1 {
		return fmt.Errorf("at least one repetition required")
	}
	if e := os.MkdirAll(*out, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(*out, "started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("use a fresh output directory: %w", e)
	}
	lock.Close()
	sql.Register("snapshot-recording", recordingDriver{})
	original, e := openDB(os.Getenv("SNAPSHOT_SEARCH_DSN"), "fpfss")
	if e != nil {
		return e
	}
	defer original.Close()
	rebuilt, e := openDB(os.Getenv("SNAPSHOT_SEARCH_DSN"), "snapshot_cache_work_scoped")
	if e != nil {
		return e
	}
	defer rebuilt.Close()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(log))
	var w []workload
	switch *suite {
	case "standard":
		w, e = workloads(original)
	case "repairs":
		w, e = repairWorkloads(original)
	case "ui":
		w, e = uiWorkloads(rebuilt)
	default:
		return fmt.Errorf("unknown suite %q", *suite)
	}
	if e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(*out, "workloads.json"), w); e != nil {
		return e
	}
	r := report{Started: time.Now().UTC().Format(time.RFC3339), Repetitions: *reps, Variants: variants, Conditions: "Static full-source datasets, one logical request at a time; current DAL runs page and count concurrently on separate connections. First observed is NOT cold (no restart/cache eviction). Warm timings follow a correctness/priming run. Standalone page/count measurements drain raw rows serially after warm runs and exclude DAL projection. No EXPLAIN ANALYZE, window-count rewrite, or load/concurrency benchmark. Collection order canonicalized without deduplication; result order and all projected fields retained. Reference fpfss versus rebuilt snapshot_cache_work_scoped. Corpus is captured behavior, not an independent correctness oracle.", Variables: map[string]string{}}
	for _, v := range []string{"version", "sql_mode", "time_zone", "tx_isolation", "tx_read_only", "group_concat_max_len", "character_set_connection", "collation_connection", "innodb_buffer_pool_size", "max_connections", "query_cache_type", "query_cache_size"} {
		var val string
		if e = original.QueryRow("SELECT @@" + v).Scan(&val); e != nil {
			return e
		}
		r.Variables[v] = val
	}
	failed := false
	for _, item := range w {
		hashes := map[string]string{}
		for _, variant := range variants {
			v := struct {
				name string
				db   *sql.DB
			}{variant, original}
			if variant == "rebuilt" {
				v.db = rebuilt
			}
			fmt.Fprintf(os.Stderr, "%s / %s\n", item.Name, v.name)
			cap := &capture{Queries: map[string]query{}}
			c, ms, err := search(ctx, v.db, item, cap)
			m := measurement{Name: item.Name, Variant: v.name, Count: c.Count, Rows: len(c.Rows), FirstObservedMS: ms}
			dir := filepath.Join(*out, item.Name, v.name)
			if e = os.MkdirAll(dir, 0700); e != nil {
				return e
			}
			if err == nil {
				m.Digest = digest(c)
				hashes[v.name] = m.Digest
				if e = writeJSON(filepath.Join(dir, "corpus.json"), c); e != nil {
					return e
				}
				for i := 0; i < *reps; i++ {
					var next corpus
					next, ms, err = search(ctx, v.db, item, nil)
					if err != nil {
						break
					}
					m.WarmMS = append(m.WarmMS, ms)
					if digest(next) != m.Digest {
						err = fmt.Errorf("repeat %d result/count drift", i+1)
						break
					}
				}
			}
			if err == nil {
				cap.Lock()
				qs := cap.Queries
				cap.Unlock()
				if len(qs) != 2 {
					return fmt.Errorf("expected page and count queries, got %d", len(qs))
				}
				if e = writeJSON(filepath.Join(dir, "queries.json"), qs); e != nil {
					return e
				}
				for _, kind := range []string{"page", "count"} {
					q := qs[kind]
					var plan string
					qctx, cancel := context.WithTimeout(ctx, 90*time.Second)
					e = v.db.QueryRowContext(qctx, "EXPLAIN FORMAT=JSON "+q.SQL, q.Args...).Scan(&plan)
					if e == nil {
						e = os.WriteFile(filepath.Join(dir, kind+"-plan.json"), []byte(plan), 0600)
					}
					if e == nil {
						ms, e = drain(qctx, v.db, q)
					}
					cancel()
					if e != nil {
						err = e
						break
					}
					if kind == "page" {
						m.PageMS = ms
					} else {
						m.CountMS = ms
					}
				}
			}
			if err != nil {
				m.Error = err.Error()
				failed = true
			}
			r.Results = append(r.Results, m)
			if e = writeJSON(filepath.Join(*out, "report.json"), r); e != nil {
				return e
			}
		}
		if hashes["original"] != "" && hashes["rebuilt"] != "" && hashes["original"] != hashes["rebuilt"] {
			r.CacheDifferences = append(r.CacheDifferences, item.Name)
		}
	}
	if e = writeJSON(filepath.Join(*out, "report.json"), r); e != nil {
		return e
	}
	if failed {
		return fmt.Errorf("baseline contains failed workloads; inspect report.json")
	}
	return os.WriteFile(filepath.Join(*out, "complete"), []byte("ok\n"), 0600)
}

// Reject misspellings and duplicate variants before starting any collection.
func parseVariants(value string) ([]string, error) {
	variants := strings.Split(value, ",")
	seen := map[string]bool{}
	for _, name := range variants {
		if (name != "original" && name != "rebuilt") || seen[name] {
			return nil, fmt.Errorf("invalid or repeated snapshot variant %q", name)
		}
		seen[name] = true
	}
	return variants, nil
}
