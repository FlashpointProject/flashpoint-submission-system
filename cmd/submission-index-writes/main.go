// submission-index-writes measures bounded write costs on an imported snapshot.
// Candidate DDL and every data mutation are rolled back, including on failure.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
)

const ready = `(c.active_verified_ids IS NOT NULL AND c.active_requested_changes_ids IS NULL AND c.bot_action='approve' AND c.distinct_actions !~* 'mark-added|reject')`

type cohort struct {
	ID       int64
	Kind     string
	Comments int64
	MetaID   int64
}
type sample struct {
	Phase     string
	Round     int
	ID        int64
	Cohort    string
	Operation string
	Iteration int
	MS        float64
	WALBytes  int64
}
type phase struct {
	Name       string
	Round      int
	DDLMS      float64
	IndexBytes map[string]int64
}
type report struct {
	Complete  bool
	Started   time.Time
	Finished  time.Time
	DDLSHA256 string
	Cohorts   []cohort
	Phases    []phase
	Samples   []sample
	Note      string
}

func save(path string, r report) error {
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

// This narrow benchmark accepts schema-only index/statistics experiments. It
// rejects transaction control, functions and arbitrary DML so supplied DDL
// cannot commit the surrounding transaction. Semicolons inside strings are
// deliberately unsupported; keep experimental DDL simple and reviewable.
func ddlStatements(data string) ([]string, error) {
	var cleaned []string
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	allowed := regexp.MustCompile(`(?is)^(CREATE\s+(UNIQUE\s+)?INDEX\s+|CREATE\s+STATISTICS\s+|CREATE\s+EXTENSION\s+|ANALYZE\s+)`)
	var statements []string
	for _, s := range strings.Split(strings.Join(cleaned, "\n"), ";") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !allowed.MatchString(s) {
			return nil, fmt.Errorf("DDL only permits CREATE INDEX, CREATE STATISTICS, CREATE EXTENSION and ANALYZE")
		}
		statements = append(statements, s)
	}
	if len(statements) == 0 {
		return nil, fmt.Errorf("empty candidate DDL")
	}
	return statements, nil
}
func indexes(s database.DBSession) (map[string]int64, error) {
	rows, e := s.Tx().QueryContext(s.Ctx(), `SELECT indexrelid::regclass::text,pg_relation_size(indexrelid) FROM pg_index JOIN pg_class t ON t.oid=indrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public'`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[string]int64{}
	for rows.Next() {
		var name string
		var size int64
		if e = rows.Scan(&name, &size); e != nil {
			return nil, e
		}
		m[name] = size
	}
	return m, rows.Err()
}
func selectCohorts(s database.DBSession, n int) ([]cohort, error) {
	result := []cohort{}
	for _, kind := range []string{"ready", "ordinary"} {
		predicate := ready
		if kind == "ordinary" {
			predicate = "NOT COALESCE(" + ready + ",FALSE)"
		}
		rows, e := s.Tx().QueryContext(s.Ctx(), `SELECT s.id,h.n,m.id FROM submission s JOIN submission_cache c ON c.fk_submission_id=s.id JOIN curation_meta m ON m.fk_submission_file_id=c.fk_newest_file_id CROSS JOIN LATERAL (SELECT count(*) n FROM comment WHERE fk_submission_id=s.id) h WHERE s.deleted_at IS NULL AND h.n BETWEEN 1 AND 50 AND `+predicate+` ORDER BY s.id LIMIT $1`, n)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			c := cohort{Kind: kind}
			if e = rows.Scan(&c.ID, &c.Comments, &c.MetaID); e != nil {
				rows.Close()
				return nil, e
			}
			result = append(result, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if len(result) != 2*n {
		return nil, fmt.Errorf("expected %d cohort rows, found %d", 2*n, len(result))
	}
	return result, nil
}
func cacheJSON(s database.DBSession, id int64) (string, error) {
	var value string
	e := s.Tx().QueryRowContext(s.Ctx(), `SELECT row_to_json(c)::text FROM submission_cache c WHERE fk_submission_id=$1`, id).Scan(&value)
	return value, e
}
func metaJSON(s database.DBSession, id int64) (string, error) {
	var value string
	e := s.Tx().QueryRowContext(s.Ctx(), `SELECT row_to_json(m)::text FROM curation_meta m WHERE id=$1`, id).Scan(&value)
	return value, e
}
func measure(s database.DBSession, dal database.DAL, c cohort, operation string, iteration int) (sample, error) {
	result := sample{ID: c.ID, Cohort: c.Kind, Operation: operation, Iteration: iteration}
	before, e := cacheJSON(s, c.ID)
	if e != nil {
		return result, e
	}
	metaBefore, e := metaJSON(s, c.MetaID)
	if e != nil {
		return result, e
	}
	if _, e = s.Tx().ExecContext(s.Ctx(), "SAVEPOINT write_probe"); e != nil {
		return result, e
	}
	// Put each row on the opposite side before timing, so both cohorts
	// measure a real partial-index membership transition. This setup is
	// restored by the same savepoint but excluded from time/WAL samples.
	if operation == "membership-enter" {
		_, e = s.Tx().ExecContext(s.Ctx(), `UPDATE submission_cache SET active_verified_ids=NULL WHERE fk_submission_id=$1`, c.ID)
	} else if operation == "membership-leave" {
		_, e = s.Tx().ExecContext(s.Ctx(), `UPDATE submission_cache SET active_verified_ids='1',active_requested_changes_ids=NULL,bot_action='approve',distinct_actions='approve,verify' WHERE fk_submission_id=$1`, c.ID)
	}
	if e != nil {
		return result, e
	}
	var wal string
	if e = s.Tx().QueryRowContext(s.Ctx(), "SELECT pg_current_wal_insert_lsn()::text").Scan(&wal); e != nil {
		return result, e
	}
	start := time.Now()
	switch operation {
	case "rebuild":
		e = dal.RebuildSubmissionCacheTable(s, c.ID)
	case "membership-enter":
		_, e = s.Tx().ExecContext(s.Ctx(), `UPDATE submission_cache SET active_verified_ids='1',active_requested_changes_ids=NULL,bot_action='approve',distinct_actions='approve,verify' WHERE fk_submission_id=$1`, c.ID)
	case "membership-leave":
		_, e = s.Tx().ExecContext(s.Ctx(), `UPDATE submission_cache SET active_verified_ids=NULL,active_requested_changes_ids='1',bot_action='request-changes',distinct_actions='request-changes' WHERE fk_submission_id=$1`, c.ID)
	case "metadata":
		_, e = s.Tx().ExecContext(s.Ctx(), `UPDATE curation_meta SET title=COALESCE(title,'') || ' benchmark title',alternate_titles=COALESCE(alternate_titles,'') || ' benchmark alternative',platform=COALESCE(platform,'') || ' benchmark platform' WHERE id=$1`, c.MetaID)
	default:
		return result, fmt.Errorf("unknown operation")
	}
	result.MS = float64(time.Since(start)) / float64(time.Millisecond)
	if e != nil {
		return result, e
	}
	if e = s.Tx().QueryRowContext(s.Ctx(), "SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),$1::pg_lsn)::bigint", wal).Scan(&result.WALBytes); e != nil {
		return result, e
	}
	if operation == "rebuild" {
		after, e := cacheJSON(s, c.ID)
		if e != nil {
			return result, e
		}
		if before != after {
			return result, fmt.Errorf("rebuild changes cache row for submission %d", c.ID)
		}
	}
	if _, e = s.Tx().ExecContext(s.Ctx(), "ROLLBACK TO SAVEPOINT write_probe"); e != nil {
		return result, e
	}
	if _, e = s.Tx().ExecContext(s.Ctx(), "RELEASE SAVEPOINT write_probe"); e != nil {
		return result, e
	}
	after, e := cacheJSON(s, c.ID)
	if e != nil {
		return result, e
	}
	metaAfter, e := metaJSON(s, c.MetaID)
	if e != nil {
		return result, e
	}
	if before != after || metaBefore != metaAfter {
		return result, fmt.Errorf("savepoint failed row restoration for submission %d", c.ID)
	}
	return result, nil
}
func run() error {
	ddl := flag.String("ddl", "", "candidate index/statistics SQL file (required)")
	out := flag.String("out", "", "new JSON report path (required)")
	n := flag.Int("cohort-size", 20, "rows per ready/ordinary cohort")
	reps := flag.Int("repetitions", 3, "measured repeats after iteration zero warmup")
	rounds := flag.Int("rounds", 2, "alternating baseline/candidate rounds")
	flag.Parse()
	if *ddl == "" || *out == "" || *n < 1 || *n > 20 || *reps < 1 || *reps > 10 || *rounds < 1 || *rounds > 3 {
		return fmt.Errorf("required ddl/out; cohort-size 1..20, repetitions 1..10, rounds 1..3")
	}
	data, e := os.ReadFile(*ddl)
	if e != nil {
		return e
	}
	statements, e := ddlStatements(string(data))
	if e != nil {
		return e
	}
	cfg, e := pgx.ParseConfig(os.Getenv("POSTGRES_PARITY_DSN"))
	if e != nil {
		return fmt.Errorf("invalid DSN")
	}
	if cfg.Host != "postgres" || !strings.HasPrefix(cfg.Database, "submission_import_") {
		return fmt.Errorf("only postgres host and submission_import_ database permitted")
	}
	cfg.RuntimeParams["timezone"] = "UTC"
	f, e := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	dal := database.NewPostgresSubmissionDAL(db)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	selection, e := dal.NewSession(ctx)
	if e != nil {
		return e
	}
	if _, e = selection.Tx().ExecContext(ctx, "SET TRANSACTION READ ONLY"); e != nil {
		selection.Rollback()
		return e
	}
	cohorts, e := selectCohorts(selection, *n)
	selection.Rollback()
	if e != nil {
		return e
	}
	r := report{Started: time.Now().UTC(), DDLSHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Cohorts: cohorts, Note: "Iteration zero is warmup. Timings cover DAL rebuild or low-level UPDATE only, not savepoint/validation overhead or service end-to-end latency. WAL is server-global insert-LSN difference; run without other DB activity. All DDL and row updates rolled back. Aborted writes can still create WAL and dead tuples, so this is not a pristine physical-state comparison."}
	for round := 0; round < *rounds; round++ {
		order := []string{"baseline", "candidate"}
		if round%2 == 1 {
			slices.Reverse(order)
		}
		for _, name := range order {
			s, e := dal.NewSession(ctx)
			if e != nil {
				return e
			}
			e = func() error {
				defer s.Rollback()
				p := phase{Name: name, Round: round}
				before, e := indexes(s)
				if e != nil {
					return e
				}
				if name == "candidate" {
					start := time.Now()
					for _, sql := range statements {
						if _, e = s.Tx().ExecContext(ctx, sql); e != nil {
							return e
						}
					}
					p.DDLMS = float64(time.Since(start)) / float64(time.Millisecond)
				}
				after, e := indexes(s)
				if e != nil {
					return e
				}
				p.IndexBytes = map[string]int64{}
				for index, size := range after {
					if _, exists := before[index]; !exists {
						p.IndexBytes[index] = size
					}
				}
				r.Phases = append(r.Phases, p)
				for _, c := range cohorts {
					for _, op := range []string{"rebuild", "membership-enter", "membership-leave", "metadata"} {
						for iteration := 0; iteration <= *reps; iteration++ {
							v, e := measure(s, dal, c, op, iteration)
							if e != nil {
								return e
							}
							v.Phase = name
							v.Round = round
							r.Samples = append(r.Samples, v)
						}
					}
				}
				return save(*out, r)
			}()
			if e != nil {
				save(*out, r)
				return fmt.Errorf("%s round %d: %w", name, round, e)
			}
		}
	}
	r.Complete = true
	r.Finished = time.Now().UTC()
	return save(*out, r)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
