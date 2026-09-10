// submission-parity compares the PostgreSQL port to saved MariaDB result corpora.
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
	// RegisterConnConfig stores configs on this exact global driver instance.
	c, e := stdlib.GetDefaultDriver().Open(s)
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
		if strings.HasPrefix(strings.TrimSpace(q), "SELECT COUNT(*)") {
			kind = "count"
		}
		r.Lock()
		r.Queries[kind] = query{q, a}
		r.Unlock()
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c recordingConn) CheckNamedValue(v *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(v)
	}
	return driver.ErrSkip
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
func openDB(dsn string, readOnly bool) (*sql.DB, error) {
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		return nil, fmt.Errorf("invalid PostgreSQL DSN")
	}
	if cfg.Host != "postgres" || !strings.HasPrefix(cfg.Database, "submission_import_") {
		return nil, fmt.Errorf("target must be postgres host and disposable submission_import_* database")
	}
	cfg.RuntimeParams["timezone"] = "UTC"
	if readOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	connstr := stdlib.RegisterConnConfig(cfg)
	db, e := sql.Open("parity-recording", connstr)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if e = db.Ping(); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func search(ctx context.Context, db *sql.DB, w workload, cap *capture) (corpus, float64, error) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, utils.CtxKeys.UserID, w.UserID), 90*time.Second)
	defer cancel()
	if cap != nil {
		ctx = context.WithValue(ctx, captureKey{}, cap)
	}
	d := database.NewPostgresSubmissionDAL(db)
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

type baselineFlags []string

func (b *baselineFlags) String() string     { return strings.Join(*b, ",") }
func (b *baselineFlags) Set(s string) error { *b = append(*b, s); return nil }

type parityMeasurement struct {
	Suite, Name, Error                               string
	ExpectedDigest, ActualDigest                     string
	Count                                            int64
	Rows                                             int
	FirstObservedMS                                  float64
	WarmMS                                           []float64
	MariaDBWarmMS                                    []float64
	MariaDBMedianMS, PostgresMedianMS, MedianSpeedup float64
	MatchingRepetitions                              int
}
type parityReport struct {
	Started, Finished, Conditions string
	Repetitions                   int
	Results                       []parityMeasurement
	Rebuild                       []rebuildReport
	FullCache                     cacheParityReport
	Complete                      bool
	Error                         string
}

func main() {
	sql.Register("parity-recording", recordingDriver{})
	var baselines baselineFlags
	flag.Var(&baselines, "baseline", "saved baseline directory (repeat for standard and repairs)")
	out := flag.String("out", "parity-results", "new report directory; must not already exist")
	repetitions := flag.Int("repetitions", 3, "warm repetitions per workload")
	rebuild := flag.Bool("rebuild", false, "rebuild every active submission cache twice before read-only search")
	explain := flag.Bool("explain", true, "save EXPLAIN ANALYZE BUFFERS JSON after warm measurements")
	flag.Parse()
	if len(baselines) == 0 || *repetitions < 1 {
		fmt.Fprintln(os.Stderr, "at least one -baseline and positive -repetitions required")
		os.Exit(1)
	}
	if e := os.Mkdir(*out, 0700); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	r := parityReport{Started: time.Now().UTC().Format(time.RFC3339), Repetitions: *repetitions, Conditions: "Saved MariaDB rebuilt-cache corpora are the reference. Complete result projection, NULLs, duplicate collection members and outer row ordering are retained. Only reviewer/action collections are sorted. First observed is not cold; three warm repetitions by default; no concurrent load. PostgreSQL query includes count and page in one statement. EXPLAIN ANALYZE follows timing. Target submission_import_* only; optional two-pass cache rebuild precedes all read-only queries. Full cache comparison reads only mariadb:3306/snapshot_cache_work_scoped, preserving filenames byte-exact and collection duplicates/NULLs. Existing PostgreSQL tables are not mutated."}
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(log))
	e := runParity(ctx, baselines, *out, *repetitions, *rebuild, *explain, &r)
	r.Finished = time.Now().UTC().Format(time.RFC3339)
	if e != nil {
		r.Error = e.Error()
	} else {
		r.Complete = true
	}
	if err := writeJSON(filepath.Join(*out, "report.json"), r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("PostgreSQL parity completed: all saved corpora and repetitions match.")
}

func runParity(ctx context.Context, baselines []string, out string, repetitions int, rebuild, explain bool, r *parityReport) error {
	// Validate and load every reference before touching the target.
	type suite struct {
		name      string
		workloads []workload
		refs      []corpus
		timings   map[string][]float64
	}
	var suites []suite
	for index, path := range baselines {
		b, e := os.ReadFile(filepath.Join(path, "workloads.json"))
		if e != nil {
			return e
		}
		s := suite{timings: map[string][]float64{}, name: fmt.Sprintf("%02d-%s", index+1, filepath.Base(filepath.Clean(path)))}
		saved, e := os.ReadFile(filepath.Join(path, "report.json"))
		if e != nil {
			return e
		}
		var previous struct {
			Results []struct {
				Name, Variant string
				WarmMS        []float64
			}
		}
		if e = json.Unmarshal(saved, &previous); e != nil {
			return e
		}
		for _, result := range previous.Results {
			if result.Variant == "rebuilt" {
				s.timings[result.Name] = result.WarmMS
			}
		}
		if e = json.Unmarshal(b, &s.workloads); e != nil {
			return e
		}
		if len(s.workloads) == 0 {
			return fmt.Errorf("empty workload suite %s", path)
		}
		seen := map[string]bool{}
		for _, w := range s.workloads {
			if w.Name == "" || filepath.Base(w.Name) != w.Name || w.Name == "." || w.Name == ".." || seen[w.Name] {
				return fmt.Errorf("invalid or duplicate workload name")
			}
			seen[w.Name] = true
			b, e = os.ReadFile(filepath.Join(path, w.Name, "rebuilt", "corpus.json"))
			if e != nil {
				return e
			}
			var c corpus
			if e = json.Unmarshal(b, &c); e != nil {
				return e
			}
			canonical(c.Rows)
			if e = checkPage(w.Filter, c.Count, len(c.Rows)); e != nil {
				return fmt.Errorf("reference %s: %w", w.Name, e)
			}
			s.refs = append(s.refs, c)
		}
		suites = append(suites, s)
	}
	mariaDSN := os.Getenv("MARIADB_PARITY_CACHE_DSN")
	if mariaDSN == "" {
		return fmt.Errorf("MARIADB_PARITY_CACHE_DSN is required for full cache parity")
	}
	dsn := os.Getenv("POSTGRES_PARITY_DSN")
	if rebuild {
		db, e := openDB(dsn, false)
		if e != nil {
			return e
		}
		r.Rebuild, e = rebuildCaches(ctx, db, out)
		if e == nil {
			// Rebuilt membership must be reflected in expression statistics before
			// measuring searches; do not depend on autovacuum scheduling.
			_, e = db.ExecContext(ctx, "ANALYZE submission_cache")
		}
		db.Close()
		if e != nil {
			return e
		}
	}
	db, e := openDB(dsn, true)
	if e != nil {
		return e
	}
	defer db.Close()
	r.FullCache, e = compareFullCache(ctx, db, mariaDSN, out)
	if e != nil {
		return e
	}
	variables := map[string]string{}
	for _, k := range []string{"server_version", "TimeZone", "default_transaction_read_only", "transaction_isolation", "work_mem", "shared_buffers"} {
		var v string
		if e = db.QueryRowContext(ctx, "SHOW "+k).Scan(&v); e != nil {
			return e
		}
		variables[k] = v
	}
	if e = writeJSON(filepath.Join(out, "postgres-settings.json"), variables); e != nil {
		return e
	}
	failed := false
	for _, s := range suites {
		suiteDir := filepath.Join(out, s.name)
		if e = os.Mkdir(suiteDir, 0700); e != nil {
			return e
		}
		if e = writeJSON(filepath.Join(suiteDir, "workloads.json"), s.workloads); e != nil {
			return e
		}
		for i, w := range s.workloads {
			dir := filepath.Join(suiteDir, w.Name)
			if e = os.Mkdir(dir, 0700); e != nil {
				return e
			}
			m := parityMeasurement{Suite: s.name, Name: w.Name, ExpectedDigest: digest(s.refs[i]), MariaDBWarmMS: s.timings[w.Name]}
			cap := &capture{Queries: map[string]query{}}
			got, ms, se := search(ctx, db, w, cap)
			m.FirstObservedMS = ms
			m.Count = got.Count
			m.Rows = len(got.Rows)
			m.ActualDigest = digest(got)
			if e = writeJSON(filepath.Join(dir, "expected.json"), s.refs[i]); e != nil {
				return e
			}
			if e = writeJSON(filepath.Join(dir, "actual.json"), got); e != nil {
				return e
			}
			if e = writeJSON(filepath.Join(dir, "differences.json"), corpusDifferences(s.refs[i], got)); e != nil {
				return e
			}
			if se == nil && m.ActualDigest != m.ExpectedDigest {
				se = fmt.Errorf("result differs from saved MariaDB corpus")
			}
			if se == nil {
				for n := 0; n < repetitions; n++ {
					next, ms, err := search(ctx, db, w, nil)
					m.WarmMS = append(m.WarmMS, ms)
					if err != nil {
						se = err
						break
					}
					if digest(next) != m.ExpectedDigest {
						if e = writeJSON(filepath.Join(dir, fmt.Sprintf("repetition-%d.json", n+1)), next); e != nil {
							return e
						}
						se = fmt.Errorf("repetition %d differs from saved corpus", n+1)
						break
					}
					m.MatchingRepetitions++
				}
			}
			if se == nil && explain {
				for kind, q := range cap.Queries {
					if e = writeJSON(filepath.Join(dir, kind+"-query.json"), q); e != nil {
						return e
					}
					var plan json.RawMessage
					qctx, cancel := context.WithTimeout(ctx, 90*time.Second)
					err := db.QueryRowContext(qctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+q.SQL, q.Args...).Scan(&plan)
					cancel()
					if err != nil {
						se = fmt.Errorf("explain %s: %w", kind, err)
						break
					}
					if e = writeJSON(filepath.Join(dir, kind+"-plan.json"), plan); e != nil {
						return e
					}
				}
			}
			if se != nil {
				m.Error = se.Error()
				failed = true
			}
			m.MariaDBMedianMS = median(m.MariaDBWarmMS)
			m.PostgresMedianMS = median(m.WarmMS)
			if m.PostgresMedianMS > 0 {
				m.MedianSpeedup = m.MariaDBMedianMS / m.PostgresMedianMS
			}
			r.Results = append(r.Results, m)
			if e = writeJSON(filepath.Join(out, "report.json"), r); e != nil {
				return e
			}
			fmt.Fprintf(os.Stderr, "%s/%s: expected=%s actual=%s error=%s\n", s.name, w.Name, m.ExpectedDigest, m.ActualDigest, m.Error)
		}
	}
	if failed {
		return fmt.Errorf("one or more PostgreSQL workloads failed; inspect report.json and persisted differences")
	}
	return nil
}

// Compare raw JSON values, avoiding float64 conversion of public int64 IDs.
func corpusDifferences(expected, actual corpus) map[string]any {
	diff := map[string]any{}
	if expected.Count != actual.Count {
		diff["count"] = []int64{expected.Count, actual.Count}
	}
	if len(expected.Rows) != len(actual.Rows) {
		diff["row_counts"] = []int{len(expected.Rows), len(actual.Rows)}
	}
	changed := map[int][]string{}
	for i := 0; i < min(len(expected.Rows), len(actual.Rows)); i++ {
		a, _ := json.Marshal(expected.Rows[i])
		b, _ := json.Marshal(actual.Rows[i])
		var am, bm map[string]json.RawMessage
		_ = json.Unmarshal(a, &am)
		_ = json.Unmarshal(b, &bm)
		for key, v := range am {
			if string(v) != string(bm[key]) {
				changed[i] = append(changed[i], key)
			}
		}
		slices.Sort(changed[i])
	}
	if len(changed) > 0 {
		diff["row_fields"] = changed
	}
	return diff
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	if len(sorted)%2 == 1 {
		return sorted[len(sorted)/2]
	}
	return (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
}
