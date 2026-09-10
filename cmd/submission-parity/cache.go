package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
)

type cacheDigest struct{ Raw, Normalized string }
type rebuildFailure struct {
	ID    int64
	Error string
}
type rebuildReport struct {
	Pass                                  int
	Eligible, Visited, Rebuilt            int64
	Seconds                               float64
	SHA256                                string
	RawDifferences, NormalizedDifferences []int64
	Failures                              []rebuildFailure
}

func snapshotCache(ctx context.Context, db *sql.DB, path string) (map[int64]cacheDigest, string, error) {
	rows, e := db.QueryContext(ctx, cacheSelectSQL)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return nil, "", e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, "", e
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	snapshot := map[int64]cacheDigest{}
	all := sha256.New()
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		dest := make([]any, len(vals))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, "", e
		}
		var id int64
		raw, _ := json.Marshal(canonicalCacheColumns(cols, vals, false))
		for i, c := range cols {
			if c == "fk_submission_id" {
				id, e = strconv.ParseInt(vals[i].String, 10, 64)
				if e != nil {
					return nil, "", e
				}
			}
		}
		if _, exists := snapshot[id]; exists {
			return nil, "", fmt.Errorf("duplicate cache for submission %d", id)
		}
		norm, _ := json.Marshal(canonicalCacheColumns(cols, vals, true))
		item := cacheDigest{fmt.Sprintf("%x", sha256.Sum256(raw)), fmt.Sprintf("%x", sha256.Sum256(norm))}
		snapshot[id] = item
		record := struct {
			ID int64
			cacheDigest
		}{id, item}
		if e = enc.Encode(record); e != nil {
			return nil, "", e
		}
		if e = json.NewEncoder(all).Encode(record); e != nil {
			return nil, "", e
		}
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	if e = f.Close(); e != nil {
		return nil, "", e
	}
	return snapshot, fmt.Sprintf("%x", all.Sum(nil)), nil
}

func cacheDiff(a, b map[int64]cacheDigest) (raw, normalized []int64) {
	for id, bv := range b {
		av, exists := a[id]
		if !exists || av.Raw != bv.Raw {
			raw = append(raw, id)
		}
		if !exists || av.Normalized != bv.Normalized {
			normalized = append(normalized, id)
		}
	}
	for id := range a {
		if _, exists := b[id]; !exists {
			raw = append(raw, id)
			normalized = append(normalized, id)
		}
	}
	slices.Sort(raw)
	slices.Sort(normalized)
	return
}

func rebuildCaches(ctx context.Context, db *sql.DB, out string) ([]rebuildReport, error) {
	previous, _, e := snapshotCache(ctx, db, filepath.Join(out, "cache-before-digests.jsonl"))
	if e != nil {
		return nil, e
	}
	dal := database.NewPostgresSubmissionDAL(db)
	var reports []rebuildReport
	for pass := 1; pass <= 2; pass++ {
		start := time.Now()
		r := rebuildReport{Pass: pass}
		if e = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission WHERE deleted_at IS NULL`).Scan(&r.Eligible); e != nil {
			return reports, e
		}
		visited, e := os.OpenFile(filepath.Join(out, fmt.Sprintf("cache-pass-%d-visited.jsonl", pass)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return reports, e
		}
		last := int64(0)
		for {
			s, err := dal.NewSession(ctx)
			if err != nil {
				visited.Close()
				return reports, err
			}
			ids, err := dal.ListSubmissionIDsForCacheRebuild(s, last, 1000)
			rollbackErr := s.Rollback()
			if err != nil {
				visited.Close()
				return reports, err
			}
			if rollbackErr != nil {
				visited.Close()
				return reports, rollbackErr
			}
			if len(ids) == 0 {
				break
			}
			failures := make([]error, len(ids))
			jobs := make(chan int)
			var wg sync.WaitGroup
			for worker := 0; worker < 4; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range jobs {
						txctx, cancel := context.WithTimeout(ctx, 30*time.Second)
						tx, err := dal.NewSession(txctx)
						if err == nil {
							err = dal.RebuildSubmissionCacheTable(tx, ids[i])
							if err == nil {
								err = tx.Commit()
							}
							_ = tx.Rollback()
						}
						cancel()
						failures[i] = err
					}
				}()
			}
			for i := range ids {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			enc := json.NewEncoder(visited)
			for i, id := range ids {
				if id <= last {
					visited.Close()
					return reports, fmt.Errorf("non-increasing traversal at %d", id)
				}
				r.Visited++
				last = id
				rec := rebuildFailure{ID: id}
				if failures[i] != nil {
					rec.Error = failures[i].Error()
					r.Failures = append(r.Failures, rec)
				} else {
					r.Rebuilt++
				}
				if e = enc.Encode(rec); e != nil {
					visited.Close()
					return reports, e
				}
			}
			fmt.Fprintf(os.Stderr, "cache pass %d visited %d/%d; rebuilt %d\n", pass, r.Visited, r.Eligible, r.Rebuilt)
		}
		if e = visited.Close(); e != nil {
			return reports, e
		}
		next, digest, e := snapshotCache(ctx, db, filepath.Join(out, fmt.Sprintf("cache-pass-%d-digests.jsonl", pass)))
		if e != nil {
			return reports, e
		}
		r.SHA256 = digest
		r.RawDifferences, r.NormalizedDifferences = cacheDiff(previous, next)
		r.Seconds = time.Since(start).Seconds()
		reports = append(reports, r)
		if e = writeJSON(filepath.Join(out, "cache-rebuild.json"), reports); e != nil {
			return reports, e
		}
		if r.Visited != r.Eligible || r.Rebuilt != r.Eligible || len(r.Failures) > 0 {
			return reports, fmt.Errorf("cache rebuild pass %d incomplete", pass)
		}
		if pass == 2 && (len(r.NormalizedDifferences) > 0 || len(r.RawDifferences) > 0) {
			return reports, fmt.Errorf("second cache rebuild changed persisted cache")
		}
		previous = next
	}
	return reports, nil
}
