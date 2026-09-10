package database

import "fmt"

// ListSubmissionIDsForCacheRebuild deliberately reads source rows rather than
// search results: submissions with missing cache rows or files need rebuilding too.
func (d *mysqlDAL) ListSubmissionIDsForCacheRebuild(dbs DBSession, afterID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("cache rebuild batch size must be positive")
	}
	rows, err := dbs.Tx().QueryContext(dbs.Ctx(), `SELECT id FROM submission WHERE deleted_at IS NULL AND id > ? ORDER BY id ASC LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RebuildSubmissionCacheTable repairs absent cache rows. The source row lock
// serializes rebuilds because the legacy cache table has no unique submission key.
// Duplicate cache rows are corruption requiring investigation, not a successful rebuild.
func (d *mysqlDAL) RebuildSubmissionCacheTable(dbs DBSession, sid int64) error {
	if err := LockSubmissions(dbs, sid); err != nil {
		return err
	}
	var count int
	if err := dbs.Tx().QueryRowContext(dbs.Ctx(), `SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id = ?`, sid).Scan(&count); err != nil {
		return err
	}
	if count > 1 {
		return fmt.Errorf("submission %d has %d cache rows; expected at most one", sid, count)
	}
	if count == 0 {
		if _, err := dbs.Tx().ExecContext(dbs.Ctx(), `INSERT INTO submission_cache (fk_submission_id) VALUES (?)`, sid); err != nil {
			return err
		}
	}
	return d.UpdateSubmissionCacheTable(dbs, sid)
}
