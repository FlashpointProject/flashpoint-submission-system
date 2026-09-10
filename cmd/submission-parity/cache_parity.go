package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Deliberately explicit: submission_cache has no id column. The same projection
// names work on both engines regardless of physical schema column order.
const cacheSelectSQL = `SELECT active_approved_ids, active_assigned_testing_ids,
 active_assigned_verification_ids, active_requested_changes_ids, active_verified_ids,
 bot_action, current_filename_sequence, distinct_actions, fk_newest_comment_id,
 fk_newest_file_id, fk_oldest_file_id, fk_submission_id, md5sum_sequence,
 original_filename_sequence, sha256sum_sequence
 FROM submission_cache ORDER BY fk_submission_id`

func canonicalCacheColumns(cols []string, vals []sql.NullString, normalize bool) map[string]sql.NullString {
	result := make(map[string]sql.NullString, len(cols))
	for i, col := range cols {
		v := vals[i]
		if normalize && v.Valid && (strings.HasPrefix(col, "active_") || col == "distinct_actions" || col == "md5sum_sequence" || col == "sha256sum_sequence") {
			parts := strings.Split(v.String, ",")
			slices.Sort(parts)
			v.String = strings.Join(parts, ",")
		}
		result[col] = v
	}
	return result
}

type cacheParityDifference struct {
	ID                          int64
	RawFields, NormalizedFields []string
}
type cacheParityReport struct {
	Reference                             string
	MariaDBRows, PostgresRows             int64
	RawFieldCounts, NormalizedFieldCounts map[string]int64
	Differences                           []cacheParityDifference
	Complete                              bool
}

func openMariaCache(dsn string) (*sql.DB, error) {
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		return nil, fmt.Errorf("invalid MariaDB cache-reference DSN")
	}
	if cfg.Net != "tcp" || cfg.Addr != "mariadb:3306" || cfg.DBName != "snapshot_cache_work_scoped" {
		return nil, fmt.Errorf("cache reference must be mariadb:3306/snapshot_cache_work_scoped")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["tx_read_only"] = "1"
	cfg.Params["time_zone"] = "'+00:00'"
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if e = db.Ping(); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}

type cacheRowReader struct {
	rows  *sql.Rows
	cols  []string
	count int64
	last  int64
}
type cacheRow struct {
	id              int64
	raw, normalized map[string]sql.NullString
}

func newCacheReader(ctx context.Context, db *sql.DB) (*cacheRowReader, error) {
	rows, e := db.QueryContext(ctx, cacheSelectSQL)
	if e != nil {
		return nil, e
	}
	cols, e := rows.Columns()
	if e != nil {
		rows.Close()
		return nil, e
	}
	return &cacheRowReader{rows: rows, cols: cols}, nil
}
func (r *cacheRowReader) next() (*cacheRow, error) {
	if !r.rows.Next() {
		return nil, r.rows.Err()
	}
	vals := make([]sql.NullString, len(r.cols))
	dest := make([]any, len(vals))
	for i := range vals {
		dest[i] = &vals[i]
	}
	if e := r.rows.Scan(dest...); e != nil {
		return nil, e
	}
	raw := canonicalCacheColumns(r.cols, vals, false)
	id, e := strconv.ParseInt(raw["fk_submission_id"].String, 10, 64)
	if e != nil {
		return nil, fmt.Errorf("invalid cache submission ID")
	}
	if r.count > 0 && id <= r.last {
		return nil, fmt.Errorf("duplicate or unordered cache row %d", id)
	}
	r.last = id
	r.count++
	return &cacheRow{id: id, raw: raw, normalized: canonicalCacheColumns(r.cols, vals, true)}, nil
}

func compareFullCache(ctx context.Context, pg *sql.DB, dsn, out string) (result cacheParityReport, err error) {
	result = cacheParityReport{Reference: "mariadb:3306/snapshot_cache_work_scoped", RawFieldCounts: map[string]int64{}, NormalizedFieldCounts: map[string]int64{}}
	defer func() {
		if e := writeJSON(filepath.Join(out, "full-cache-parity.json"), result); err == nil && e != nil {
			err = e
		}
	}()
	maria, e := openMariaCache(dsn)
	if e != nil {
		return result, e
	}
	defer maria.Close()
	left, e := newCacheReader(ctx, maria)
	if e != nil {
		return result, e
	}
	defer left.rows.Close()
	right, e := newCacheReader(ctx, pg)
	if e != nil {
		return result, e
	}
	defer right.rows.Close()
	a, e := left.next()
	if e != nil {
		return result, e
	}
	b, e := right.next()
	if e != nil {
		return result, e
	}
	for a != nil || b != nil {
		diff := cacheParityDifference{}
		switch {
		case a == nil || b != nil && b.id < a.id:
			diff.ID = b.id
			diff.RawFields = []string{"missing_from_mariadb"}
			diff.NormalizedFields = slices.Clone(diff.RawFields)
			b, e = right.next()
		case b == nil || a.id < b.id:
			diff.ID = a.id
			diff.RawFields = []string{"missing_from_postgres"}
			diff.NormalizedFields = slices.Clone(diff.RawFields)
			a, e = left.next()
		default:
			diff.ID = a.id
			for col, v := range a.raw {
				if v != b.raw[col] {
					diff.RawFields = append(diff.RawFields, col)
				}
				if a.normalized[col] != b.normalized[col] {
					diff.NormalizedFields = append(diff.NormalizedFields, col)
				}
			}
			slices.Sort(diff.RawFields)
			slices.Sort(diff.NormalizedFields)
			a, e = left.next()
			if e == nil {
				b, e = right.next()
			}
		}
		for _, field := range diff.RawFields {
			result.RawFieldCounts[field]++
		}
		for _, field := range diff.NormalizedFields {
			result.NormalizedFieldCounts[field]++
		}
		if len(diff.RawFields) > 0 {
			result.Differences = append(result.Differences, diff)
		}
		result.MariaDBRows = left.count
		result.PostgresRows = right.count
		if e != nil {
			return result, e
		}
	}
	if result.MariaDBRows == 0 {
		return result, fmt.Errorf("cache reference is empty")
	}
	if len(result.NormalizedFieldCounts) > 0 {
		return result, fmt.Errorf("full cache differs semantically from rebuilt MariaDB; inspect full-cache-parity.json")
	}
	result.Complete = true
	return result, nil
}
