package database

import (
	"fmt"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
)

// UpdateSubmissionCacheTable requires the parent mutation lock before reading
// history, just like the MariaDB implementation. All aggregates are restricted
// to this submission; absent histories produce SQL NULL rather than empty CSVs.
func (d *postgresSubmissionDAL) UpdateSubmissionCacheTable(dbs DBSession, sid int64) error {
	_, err := dbs.Tx().ExecContext(dbs.Ctx(), `
WITH live_files AS MATERIALIZED (
    SELECT * FROM submission_file WHERE fk_submission_id = $1 AND deleted_at IS NULL
), live_comments AS MATERIALIZED (
    SELECT c.*, a.name AS action_name FROM comment c
    LEFT JOIN action a ON a.id = c.fk_action_id
    WHERE c.fk_submission_id = $1 AND c.deleted_at IS NULL
), latest_file AS (
    SELECT id, created_at FROM live_files ORDER BY created_at DESC, id DESC LIMIT 1
), families(family, enabler, disablers, current_version_only) AS (
    VALUES (0, 'assign-testing', ARRAY['unassign-testing'], false),
           (1, 'assign-verification', ARRAY['unassign-verification'], false),
           (2, 'request-changes', ARRAY['approve', 'verify'], false),
           (3, 'approve', ARRAY['request-changes'], true),
           (4, 'verify', ARRAY['request-changes'], true)
), ranked_actions AS (
    SELECT f.family, f.enabler, c.action_name, c.fk_user_id,
           ROW_NUMBER() OVER (PARTITION BY f.family, c.fk_user_id
                              ORDER BY c.created_at DESC, c.id DESC) AS rn
    FROM live_comments c JOIN families f
      ON c.action_name = f.enabler OR c.action_name = ANY(f.disablers)
    WHERE c.fk_user_id <> $2
      -- Cross-table ties intentionally remain timestamp-only. A comment at
      -- precisely the upload timestamp does not approve/verify that version.
      AND (NOT f.current_version_only OR c.created_at > (SELECT created_at FROM latest_file))
), reviewers AS (
    SELECT STRING_AGG(fk_user_id::text, ',' ORDER BY fk_user_id) FILTER (WHERE family = 0) AS testing,
           STRING_AGG(fk_user_id::text, ',' ORDER BY fk_user_id) FILTER (WHERE family = 1) AS verification,
           STRING_AGG(fk_user_id::text, ',' ORDER BY fk_user_id) FILTER (WHERE family = 2) AS changes,
           STRING_AGG(fk_user_id::text, ',' ORDER BY fk_user_id) FILTER (WHERE family = 3) AS approved,
           STRING_AGG(fk_user_id::text, ',' ORDER BY fk_user_id) FILTER (WHERE family = 4) AS verified
    FROM ranked_actions WHERE rn = 1 AND action_name = enabler
), file_sequences AS (
    SELECT STRING_AGG(original_filename, ',' ORDER BY id) AS original_names,
           STRING_AGG(current_filename, ',' ORDER BY id) AS current_names,
           STRING_AGG(md5sum, ',' ORDER BY id) AS md5s,
           STRING_AGG(sha256sum, ',' ORDER BY id) AS sha256s
    FROM live_files
), actions AS (
    SELECT COALESCE(BOOL_OR(action_name = 'reject'), false) AS rejected,
           STRING_AGG(DISTINCT action_name, ',' ORDER BY action_name) AS names
    FROM live_comments
)
UPDATE submission_cache
SET fk_newest_file_id = (SELECT id FROM latest_file),
    fk_oldest_file_id = (SELECT id FROM live_files ORDER BY created_at ASC, id ASC LIMIT 1),
    fk_newest_comment_id = (SELECT id FROM live_comments ORDER BY created_at DESC, id DESC LIMIT 1),
    active_assigned_testing_ids = CASE WHEN a.rejected THEN NULL ELSE r.testing END,
    active_assigned_verification_ids = CASE WHEN a.rejected THEN NULL ELSE r.verification END,
    active_requested_changes_ids = CASE WHEN a.rejected THEN NULL ELSE r.changes END,
    active_approved_ids = CASE WHEN a.rejected THEN NULL ELSE r.approved END,
    active_verified_ids = CASE WHEN a.rejected THEN NULL ELSE r.verified END,
    original_filename_sequence = f.original_names,
    current_filename_sequence = f.current_names,
    md5sum_sequence = f.md5s,
    sha256sum_sequence = f.sha256s,
    bot_action = (SELECT action_name FROM live_comments WHERE fk_user_id = $2 ORDER BY created_at DESC, id DESC LIMIT 1),
    distinct_actions = CASE WHEN a.rejected THEN 'reject' ELSE a.names END
FROM reviewers r CROSS JOIN file_sequences f CROSS JOIN actions a
WHERE fk_submission_id = $1`, sid, int64(constants.ValidatorID))
	return err
}

// ListSubmissionIDsForCacheRebuild walks source records even when their caches
// or files are absent, with a bounded keyset rather than search pagination.
func (d *postgresSubmissionDAL) ListSubmissionIDsForCacheRebuild(dbs DBSession, afterID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("cache rebuild batch size must be positive")
	}
	rows, err := dbs.Tx().QueryContext(dbs.Ctx(), `SELECT id FROM submission WHERE deleted_at IS NULL AND id > $1 ORDER BY id ASC LIMIT $2`, afterID, limit)
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

func (d *postgresSubmissionDAL) RebuildSubmissionCacheTable(dbs DBSession, sid int64) error {
	if err := LockSubmissions(dbs, sid); err != nil {
		return err
	}
	var count int
	if err := dbs.Tx().QueryRowContext(dbs.Ctx(), `SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id = $1`, sid).Scan(&count); err != nil {
		return err
	}
	if count > 1 {
		return fmt.Errorf("submission %d has %d cache rows; expected at most one", sid, count)
	}
	if count == 0 {
		if _, err := dbs.Tx().ExecContext(dbs.Ctx(), `INSERT INTO submission_cache (fk_submission_id) VALUES ($1)`, sid); err != nil {
			return err
		}
	}
	return d.UpdateSubmissionCacheTable(dbs, sid)
}
