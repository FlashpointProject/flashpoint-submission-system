package database

import "fmt"

// LockSubmissionCreation serializes quota checks and creation for one uploader
// across processes. The upsert acquires an exclusive row lock even for an
// existing key; commit/rollback releases it. Call before the transaction's first
// consistent read, so a waiter sees the preceding creator's committed cache.
// Rows contain no quota state and need no backfill or reservation cleanup.
func LockSubmissionCreation(dbs DBSession, uid int64) error {
	q := `
		INSERT INTO submission_creation_lock (fk_user_id) VALUES (?)
		ON DUPLICATE KEY UPDATE fk_user_id = VALUES(fk_user_id)`
	if IsPostgresSubmissionSession(dbs) {
		q = `INSERT INTO submission_creation_lock (fk_user_id) VALUES ($1)
 ON CONFLICT (fk_user_id) DO UPDATE SET fk_user_id=EXCLUDED.fk_user_id`
	}
	_, err := dbs.Tx().ExecContext(dbs.Ctx(), q, uid)
	if err != nil {
		return fmt.Errorf("lock submission creation for user %d: %w", uid, err)
	}
	return nil
}
