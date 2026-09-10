package database

import (
	"fmt"
	"slices"
)

// LockSubmissions serializes mutations of each active parent until commit or
// rollback. Call before ANY consistent read in the transaction: locking after a
// REPEATABLE READ snapshot exists cannot refresh that snapshot. Every writer of
// submission history/cache must follow this protocol. IDs are copied, sorted and
// deduplicated so overlapping batches acquire locks in the same order.
func LockSubmissions(dbs DBSession, ids ...int64) error {
	ordered := slices.Clone(ids)
	slices.Sort(ordered)
	for _, id := range slices.Compact(ordered) {
		var found int64
		if err := dbs.Tx().QueryRowContext(dbs.Ctx(), `SELECT id FROM submission WHERE id = `+SubmissionPlaceholder(dbs, 1)+` AND deleted_at IS NULL FOR UPDATE`, id).Scan(&found); err != nil {
			return fmt.Errorf("lock submission %d: %w", id, err)
		}
	}
	return nil
}
