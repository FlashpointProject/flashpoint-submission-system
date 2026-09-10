package database

import (
	"fmt"
	"strings"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

// CountSubmissions avoids fetching and formatting a page when the caller only
// needs its total. Backends without a dedicated counter retain their existing
// search behavior, including the MariaDB count connection and snapshot rules.
func CountSubmissions(d DAL, dbs DBSession, filter *types.SubmissionsFilter) (int64, error) {
	if counter, ok := d.(interface {
		CountSubmissions(DBSession, *types.SubmissionsFilter) (int64, error)
	}); ok {
		return counter.CountSubmissions(dbs, filter)
	}
	_, count, err := d.SearchSubmissions(dbs, filter)
	return count, err
}

// CountSubmissions shares search's predicates but needs neither display fields
// nor ordering/pagination. Both live and legacy totals use the caller's single
// statement snapshot. Unique join keys let PostgreSQL remove unused joins.
func (d *postgresSubmissionDAL) CountSubmissions(dbs DBSession, filter *types.SubmissionsFilter) (int64, error) {
	q, err := newPGSubmissionSearch(filter, utils.UserID(dbs.Ctx()))
	if err != nil {
		return 0, err
	}
	submissionFrom := pgSubmissionSearchFrom
	if len(q.filters) > 0 {
		submissionFrom += " AND " + strings.Join(q.filters, " AND ")
	}
	legacyFrom := " FROM masterdb_game WHERE TRUE"
	if len(q.legacy) > 0 {
		legacyFrom += " AND " + strings.Join(q.legacy, " AND ")
	}
	var count int64
	err = dbs.Tx().QueryRowContext(dbs.Ctx(), "SELECT (SELECT count(*)"+submissionFrom+") + (SELECT count(*)"+legacyFrom+")", q.args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count submissions: %w", err)
	}
	return count, nil
}

// CountCommentsByUserIDAndAction avoids loading a user's complete action history
// for statistics. MariaDB retains its existing history query and semantics.
func CountCommentsByUserIDAndAction(d DAL, dbs DBSession, uid int64, action string) (int64, error) {
	if counter, ok := d.(interface {
		CountCommentsByUserIDAndAction(DBSession, int64, string) (int64, error)
	}); ok {
		return counter.CountCommentsByUserIDAndAction(dbs, uid, action)
	}
	comments, err := d.GetCommentsByUserIDAndAction(dbs, uid, action)
	return int64(len(comments)), err
}

func (d *postgresSubmissionDAL) CountCommentsByUserIDAndAction(dbs DBSession, uid int64, action string) (int64, error) {
	var count int64
	err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT count(*) FROM comment
  WHERE fk_user_id = ? AND deleted_at IS NULL
  AND fk_action_id = (SELECT id FROM action WHERE submission_text_key(name) = submission_text_key(?))`, uid, action).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count user actions: %w", err)
	}
	return count, nil
}
