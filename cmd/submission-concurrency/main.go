// Bounded closed-loop DAL load probe on a reconciled disposable snapshot.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
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

type workload struct {
	Name   string
	UserID int64
	Filter types.SubmissionsFilter
}
type sample struct {
	Kind                string
	MS, BeginMS, LockMS float64
	Error               string
}
type summary struct {
	N             int
	P50, P95, P99 float64
}
type scenario struct {
	Name                         string
	Clients, PoolLimit           int
	Seconds, OperationsPerSecond float64
	PoolWaitCount                int64
	PoolWaitMS                   float64
	MaxOpen                      int
	WaitObservations             map[string]int
	Samples                      []sample
	Latency                      map[string]summary
	Errors                       int
}
type report struct {
	Complete      bool
	Error         string
	Started       time.Time
	Scenarios     []scenario
	CacheRestored bool
}

func ms(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
func summarize(v []float64) summary {
	sort.Float64s(v)
	s := summary{N: len(v)}
	if len(v) == 0 {
		return s
	}
	q := func(p float64) float64 {
		i := int(float64(len(v)) * p)
		if i >= len(v) {
			i = len(v) - 1
		}
		return v[i]
	}
	s.P50 = q(.5)
	s.P95 = q(.95)
	s.P99 = q(.99)
	return s
}
func connect(dsn, app string) (*sql.DB, error) {
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		return nil, fmt.Errorf("invalid DSN")
	}
	if cfg.Host != "postgres" || !strings.HasPrefix(cfg.Database, "submission_import_") {
		return nil, fmt.Errorf("requires postgres / submission_import_* disposable target")
	}
	cfg.RuntimeParams["application_name"] = app
	cfg.RuntimeParams["timezone"] = "UTC"
	cfg.RuntimeParams["statement_timeout"] = "10000"
	return stdlib.OpenDB(*cfg), nil
}
func search(ctx context.Context, db *sql.DB, w workload) (sample, string) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, utils.CtxKeys.UserID, w.UserID), 12*time.Second)
	defer cancel()
	st := time.Now()
	r := sample{Kind: w.Name}
	d := database.NewPostgresSubmissionDAL(db)
	s, e := d.NewSession(ctx)
	r.BeginMS = ms(st)
	if e != nil {
		r.Error = e.Error()
		r.MS = ms(st)
		return r, ""
	}
	rows, n, e := d.SearchSubmissions(s, &w.Filter)
	re := s.Rollback()
	r.MS = ms(st)
	if e == nil {
		e = re
	}
	if e != nil {
		r.Error = e.Error()
		return r, ""
	}
	b, e := json.Marshal(struct {
		Count int64
		Rows  []*types.ExtendedSubmission
	}{n, rows})
	if e != nil {
		r.Error = e.Error()
	}
	return r, fmt.Sprintf("%x", sha256.Sum256(b))
}
func write(ctx context.Context, db *sql.DB, id int64) sample {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	st := time.Now()
	r := sample{Kind: "cache-write"}
	d := database.NewPostgresSubmissionDAL(db)
	s, e := d.NewSession(ctx)
	r.BeginMS = ms(st)
	if e == nil {
		lock := time.Now()
		e = database.LockSubmissions(s, id)
		r.LockMS = ms(lock)
		if e == nil {
			e = d.RebuildSubmissionCacheTable(s, id)
		}
		re := s.Rollback()
		if e == nil {
			e = re
		}
	}
	r.MS = ms(st)
	if e != nil {
		r.Error = e.Error()
	}
	return r
}
func cacheDigest(ctx context.Context, db *sql.DB) (string, error) {
	rows, e := db.QueryContext(ctx, `SELECT row_to_json(c)::text FROM submission_cache c ORDER BY fk_submission_id`)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return "", e
		}
		fmt.Fprintln(h, s)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), rows.Err()
}
func run(ctx context.Context, out, workloads string, duration time.Duration) (ret error) {
	r := report{Started: time.Now()}
	save := func() {
		b, _ := json.MarshalIndent(r, "", "  ")
		if e := os.WriteFile(out, b, 0600); e != nil && ret == nil {
			ret = e
		}
	}
	defer func() {
		if ret != nil {
			r.Error = ret.Error()
		}
		save()
	}()
	dsn := os.Getenv("POSTGRES_PARITY_DSN")
	monitor, e := connect(dsn, "submission-concurrency-monitor")
	if e != nil {
		return e
	}
	defer monitor.Close()
	monitor.SetMaxOpenConns(1)
	var version int
	var dirty bool
	if e = monitor.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); e != nil {
		return e
	}
	if version != 17 || dirty {
		return fmt.Errorf("requires clean migration 17")
	}
	b, e := os.ReadFile(workloads)
	if e != nil {
		return e
	}
	var all []workload
	if e = json.Unmarshal(b, &all); e != nil {
		return e
	}
	names := []string{"ui-default", "ui-default-next", "ui-platform-flash", "ui-platform-html5", "ui-title-mario", "ui-submitter-username", "ui-preset-ready-fp", "ui-preset-ready-testing"}
	var ws []workload
	for _, name := range names {
		for _, w := range all {
			if w.Name == name {
				ws = append(ws, w)
			}
		}
	}
	if len(ws) != len(names) {
		return fmt.Errorf("missing or duplicate UI workloads")
	}
	// Histories are intentionally ordinary and short, matching observed usage.
	rows, e := monitor.QueryContext(ctx, `SELECT s.id FROM submission s JOIN submission_cache c ON c.fk_submission_id=s.id WHERE s.deleted_at IS NULL AND (SELECT count(*) FROM comment WHERE fk_submission_id=s.id) BETWEEN 1 AND 50 ORDER BY s.id LIMIT 8`)
	if e != nil {
		return e
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(ids) != 8 {
		return fmt.Errorf("requires eight ordinary submissions")
	}
	before, e := cacheDigest(ctx, monitor)
	if e != nil {
		return e
	}
	expected := map[string]string{}
	for _, w := range ws {
		s, d := search(ctx, monitor, w)
		if s.Error != "" {
			return fmt.Errorf("warmup: %s", s.Error)
		}
		expected[w.Name] = d
	}
	configs := []struct {
		name       string
		n, pool    int
		mixed, hot bool
	}{
		{"reads-1", 1, 0, false, false}, {"mixed-1", 1, 0, true, false},
		{"reads-4", 4, 0, false, false}, {"mixed-4", 4, 0, true, false},
		{"reads-8", 8, 0, false, false}, {"mixed-8", 8, 0, true, false},
		{"mixed-8-pool-4", 8, 4, true, false}, {"mixed-8-same-parent", 8, 0, true, true},
	}
	for _, c := range configs {
		db, e := connect(dsn, "submission-concurrency-worker")
		if e != nil {
			return e
		}
		db.SetMaxOpenConns(c.pool)
		// Preserve database/sql production defaults: unlimited open, two idle.
		r.Scenarios = append(r.Scenarios, scenario{Name: c.name, Clients: c.n, PoolLimit: c.pool, WaitObservations: map[string]int{}, Latency: map[string]summary{}})
		sc := &r.Scenarios[len(r.Scenarios)-1]
		var mu sync.Mutex
		var wg sync.WaitGroup
		stopped := make(chan struct{})
		var mw sync.WaitGroup
		mw.Add(1)
		go func() {
			defer mw.Done()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopped:
					return
				case <-ticker.C:
					rr, err := monitor.QueryContext(ctx, `SELECT coalesce(wait_event_type,'CPU/runnable'),count(*) FROM pg_stat_activity WHERE application_name='submission-concurrency-worker' AND state='active' GROUP BY 1`)
					if err != nil {
						mu.Lock()
						sc.WaitObservations["monitor-error"]++
						mu.Unlock()
						continue
					}
					mu.Lock()
					for rr.Next() {
						var k string
						var n int
						if rr.Scan(&k, &n) == nil {
							sc.WaitObservations[k] += n
						}
					}
					if rr.Err() != nil {
						sc.WaitObservations["monitor-error"]++
					}
					rr.Close()
					if n := db.Stats().OpenConnections; n > sc.MaxOpen {
						sc.MaxOpen = n
					}
					mu.Unlock()
				}
			}
		}()
		start := time.Now()
		deadline := start.Add(duration)
		for worker := 0; worker < c.n; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := 0; time.Now().Before(deadline); i++ {
					var s sample
					if c.mixed && i%5 == 4 {
						id := ids[worker]
						if c.hot {
							id = ids[0]
						}
						s = write(ctx, db, id)
					} else {
						w := ws[(i+worker)%len(ws)]
						var d string
						s, d = search(ctx, db, w)
						if s.Error == "" && d != expected[w.Name] {
							s.Error = "result digest differs from serial reference"
						}
					}
					mu.Lock()
					sc.Samples = append(sc.Samples, s)
					mu.Unlock()
				}
			}(worker)
		}
		wg.Wait()
		sc.Seconds = time.Since(start).Seconds()
		close(stopped)
		mw.Wait()
		stats := db.Stats()
		sc.PoolWaitCount = stats.WaitCount
		sc.PoolWaitMS = float64(stats.WaitDuration.Microseconds()) / 1000
		db.Close()
		values := map[string][]float64{}
		for _, s := range sc.Samples {
			if s.Error != "" {
				sc.Errors++
			}
			values[s.Kind] = append(values[s.Kind], s.MS)
			values["begin"] = append(values["begin"], s.BeginMS)
			if s.Kind == "cache-write" {
				values["parent-lock"] = append(values["parent-lock"], s.LockMS)
			}
		}
		for k, v := range values {
			sc.Latency[k] = summarize(v)
		}
		sc.OperationsPerSecond = float64(len(sc.Samples)) / sc.Seconds
		fmt.Printf("%s: %d operations, %.1f/s, %d errors, pool waits=%d\n", c.name, len(sc.Samples), sc.OperationsPerSecond, sc.Errors, sc.PoolWaitCount)
		save()
		if sc.Errors != 0 || sc.WaitObservations["monitor-error"] != 0 {
			return fmt.Errorf("scenario %s failed", c.name)
		}
	}
	after, e := cacheDigest(ctx, monitor)
	if e != nil {
		return e
	}
	r.CacheRestored = before == after
	if !r.CacheRestored {
		return fmt.Errorf("cache changed despite rollback")
	}
	r.Complete = true
	return nil
}
func main() {
	out := flag.String("out", "", "new report path")
	ws := flag.String("workloads", "", "saved UI workloads.json")
	seconds := flag.Int("seconds", 12, "seconds per scenario (5–60)")
	flag.Parse()
	if *out == "" || *ws == "" || *seconds < 5 || *seconds > 60 {
		fmt.Fprintln(os.Stderr, "requires out, workloads, seconds 5–60")
		os.Exit(2)
	}
	f, e := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	f.Close()
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	if e = run(ctx, *out, *ws, time.Duration(*seconds)*time.Second); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
