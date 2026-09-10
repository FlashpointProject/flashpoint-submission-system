// snapshot-cache-audit rebuilds only an explicitly named disposable database.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/go-sql-driver/mysql"
	"github.com/sirupsen/logrus"
)

type row map[string]sql.NullString
type snapshot map[int64][]row
type difference struct {
	Rows      int            `json:"submissions"`
	Fields    map[string]int `json:"fields"`
	SampleIDs []int64        `json:"sample_submission_ids"`
}
type failure struct {
	ID    int64  `json:"submission_id"`
	Stage string `json:"stage"`
	Code  uint16 `json:"mysql_error_code,omitempty"`
}
type passReport struct {
	Pass                       int
	Eligible, Visited, Rebuilt int64
	Seconds                    float64
	Failures                   []failure
	Raw, Normalized            difference
}
type report struct {
	Method                      string
	SampleIDs                   []int64
	SampleRaw, SampleNormalized difference
	Workers                     int
	Started                     string
	Variables                   map[string]string
	OriginalRows                int64
	Passes                      []passReport
	Normalization               string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot cache audit failed:", err)
		os.Exit(1)
	}
}
func run() error {
	out := flag.String("out", "/results", "report directory")
	passes := flag.Int("passes", 2, "rebuild passes (at least two for repeatability)")
	workers := flag.Int("workers", 4, "concurrent independent submission transactions (1-16)")
	scoped := flag.Bool("scoped-inputs", false, "bound unchanged DAL SQL to per-batch source copies; sample-check against unscoped SQL")
	flag.Parse()
	if *workers < 1 || *workers > 16 {
		return fmt.Errorf("workers must be between 1 and 16")
	}
	if *passes < 2 {
		return fmt.Errorf("at least two passes required")
	}
	cfg, err := mysql.ParseDSN(os.Getenv("SNAPSHOT_CACHE_DSN"))
	if err != nil {
		return fmt.Errorf("invalid DSN")
	}
	if cfg.DBName != "snapshot_cache_work" && !(*scoped && cfg.DBName == "snapshot_cache_work_scoped") {
		return fmt.Errorf("DSN database must be snapshot_cache_work")
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("opening database failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(*workers)
	db.SetMaxIdleConns(*workers)
	if err = db.Ping(); err != nil {
		return fmt.Errorf("database connection failed")
	}
	if err = os.MkdirAll(*out, 0700); err != nil {
		return err
	}
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(log))
	r := report{Workers: *workers, Started: time.Now().UTC().Format(time.RFC3339), Variables: map[string]string{}, Normalization: "Sort comma-separated reviewer IDs, action names and hashes; retain NULL, empty values and duplicate elements. Filenames remain byte-exact because commas make tokenization ambiguous. Original SQL cache is preserved as submission_cache_snapshot_original; no values exported."}
	for _, v := range []string{"version", "sql_mode", "time_zone", "group_concat_max_len", "tx_isolation", "character_set_connection", "collation_connection"} {
		var s string
		if err = db.QueryRow("SELECT @@" + v).Scan(&s); err != nil {
			return fmt.Errorf("read variable %s failed", v)
		}
		r.Variables[v] = s
	}
	// Never overwrite evidence or repeat against an already mutated clone accidentally.
	if _, err = db.Exec("CREATE TABLE submission_cache_snapshot_original LIKE submission_cache"); err != nil {
		return fmt.Errorf("create original cache snapshot failed (use a fresh clone)")
	}
	if _, err = db.Exec("INSERT INTO submission_cache_snapshot_original SELECT * FROM submission_cache"); err != nil {
		return fmt.Errorf("preserve original cache failed")
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM submission_cache_snapshot_original").Scan(&r.OriginalRows); err != nil {
		return err
	}
	previous, err := readSnapshot(db)
	if err != nil {
		return err
	}
	dal := database.NewMysqlDAL(db)
	r.Method = "Full source tables; unchanged production DAL SQL."
	var scope *scopedPool
	if *scoped {
		r.Method = "Unchanged production DAL SQL against per-worker scratch submission/comment/submission_file tables containing every source record (including deleted) for each 100-ID batch; full cache and action exposed through views. Static correctness audit only; not a full-source performance or locking validation. Unscoped selected submissions cross-check this transformation before full scoped traversal."
		ids, e := scopeSamples(db)
		if e != nil {
			return e
		}
		r.SampleIDs = ids
		for i, id := range ids {
			if f := rebuildOne(ctx, db, id); f != nil {
				return fmt.Errorf("unscoped sample %d failed at %s code %d", id, f.Stage, f.Code)
			}
			fmt.Fprintf(os.Stderr, "unscoped sample %d/%d complete (submission %d)\n", i+1, len(ids), id)
		}
		sampled, e := readSnapshot(db)
		if e != nil {
			return e
		}
		scope, e = newScopedPool(cfg, *workers)
		if e != nil {
			return e
		}
		defer scope.close()
		if e = scope.prepare(ids); e != nil {
			return e
		}
		for _, f := range rebuildBatch(ids, *workers, func(id int64) *failure { return scope.rebuild(ctx, id) }) {
			if f != nil {
				return fmt.Errorf("scoped sample %d failed", f.ID)
			}
		}
		scopedSample, e := readSnapshot(db)
		if e != nil {
			return e
		}
		r.SampleRaw = compare(onlyIDs(sampled, ids), onlyIDs(scopedSample, ids), false)
		r.SampleNormalized = compare(onlyIDs(sampled, ids), onlyIDs(scopedSample, ids), true)
		data, _ := json.MarshalIndent(r, "", "  ")
		if e = os.WriteFile(filepath.Join(*out, "cache-reconciliation.json"), append(data, '\n'), 0600); e != nil {
			return e
		}
		if r.SampleNormalized.Rows > 0 {
			return fmt.Errorf("scoped sample differs semantically from unscoped SQL; see report")
		}
	}
	failed := false
	for p := 1; p <= *passes; p++ {
		pr := passReport{Pass: p}
		start := time.Now()
		if err = db.QueryRow("SELECT COUNT(*) FROM submission WHERE deleted_at IS NULL").Scan(&pr.Eligible); err != nil {
			return err
		}
		var last int64
		for {
			s, e := dal.NewSession(ctx)
			if e != nil {
				return fmt.Errorf("list session failed")
			}
			batchSize := 1000
			if scope != nil {
				batchSize = 100
			}
			ids, e := dal.ListSubmissionIDsForCacheRebuild(s, last, batchSize)
			_ = s.Rollback()
			if e != nil {
				return fmt.Errorf("list traversal failed")
			}
			if len(ids) == 0 {
				break
			}
			if scope != nil {
				if e := scope.prepare(ids); e != nil {
					return e
				}
			}
			outcomes := rebuildBatch(ids, *workers, func(id int64) *failure {
				if scope != nil {
					return scope.rebuild(ctx, id)
				}
				return rebuildOne(ctx, db, id)
			})
			// A whole batch finishes before traversal advances, preserving stable
			// progress and deterministic failure order despite concurrent work.
			for _, f := range outcomes {
				pr.Visited++
				if f == nil {
					pr.Rebuilt++
				} else {
					pr.Failures = append(pr.Failures, *f)
					failed = true
				}
			}
			last = ids[len(ids)-1]
			fmt.Fprintf(os.Stderr, "pass %d: visited %d/%d, rebuilt %d, failures %d, elapsed %s\n", p, pr.Visited, pr.Eligible, pr.Rebuilt, len(pr.Failures), time.Since(start).Round(time.Second))
		}
		current, e := readSnapshot(db)
		if e != nil {
			return e
		}
		pr.Raw = compare(previous, current, false)
		pr.Normalized = compare(previous, current, true)
		pr.Seconds = time.Since(start).Seconds()
		r.Passes = append(r.Passes, pr)
		if pr.Visited != pr.Eligible {
			failed = true
		}
		if p > 1 && pr.Raw.Rows > 0 {
			failed = true
		}
		previous = current
		data, e := json.MarshalIndent(r, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(*out, "cache-reconciliation.json"), append(data, '\n'), 0600); e != nil {
			return e
		}
	}
	if failed {
		return fmt.Errorf("rebuild failures, incomplete traversal or non-repeatability recorded in report")
	}
	return nil
}
func readSnapshot(db *sql.DB) (snapshot, error) {
	rows, e := db.Query("SELECT * FROM submission_cache ORDER BY fk_submission_id")
	if e != nil {
		return nil, fmt.Errorf("read cache failed")
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	s := snapshot{}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, fmt.Errorf("scan cache failed")
		}
		r := row{}
		var id int64
		for i, c := range cols {
			r[c] = vals[i]
			if c == "fk_submission_id" {
				id, _ = strconv.ParseInt(vals[i].String, 10, 64)
			}
		}
		s[id] = append(s[id], r)
	}
	return s, rows.Err()
}
func normal(c string, v sql.NullString) sql.NullString {
	if !v.Valid {
		return v
	}
	if strings.HasPrefix(c, "active_") || c == "distinct_actions" || c == "md5sum_sequence" || c == "sha256sum_sequence" {
		parts := strings.Split(v.String, ",")
		sort.Strings(parts)
		v.String = strings.Join(parts, ",")
	}
	return v
}
func values(rows []row, c string, norm bool) string {
	var vals []string
	for _, r := range rows {
		v := r[c]
		if norm {
			v = normal(c, v)
		}
		b, _ := json.Marshal(v)
		vals = append(vals, string(b))
	}
	sort.Strings(vals)
	b, _ := json.Marshal(vals)
	return string(b)
}
func compare(a, b snapshot, norm bool) difference {
	d := difference{Fields: map[string]int{}}
	ids := map[int64]bool{}
	for id := range a {
		ids[id] = true
	}
	for id := range b {
		ids[id] = true
	}
	ordered := []int64{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ordered {
		changed := false
		cols := map[string]bool{}
		for _, r := range append(append([]row{}, a[id]...), b[id]...) {
			for c := range r {
				cols[c] = true
			}
		}
		if len(a[id]) != len(b[id]) {
			d.Fields["cache_row_count"]++
			changed = true
		}
		for c := range cols {
			if values(a[id], c, norm) != values(b[id], c, norm) {
				d.Fields[c]++
				changed = true
			}
		}
		if changed {
			d.Rows++
			if len(d.SampleIDs) < 50 {
				d.SampleIDs = append(d.SampleIDs, id)
			}
		}
	}
	return d
}

// rebuildBatch assigns each distinct source ID exactly once. Each worker owns a
// separate transaction; only its own result slot is written until Wait returns.
func rebuildBatch(ids []int64, workers int, rebuild func(int64) *failure) []*failure {
	results := make([]*failure, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = rebuild(ids[i])
			}
		}()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}
