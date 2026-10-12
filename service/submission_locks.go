package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
)

func lockSubmissionMutation(dbs database.DBSession, sid int64) error {
	if err := database.LockSubmissions(dbs, sid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return perr("submission not found", http.StatusNotFound)
		}
		return dberr(err)
	}
	return nil
}

// Read the current state under the same parent lock held through the mutation.
// In particular, queued upload validation must not race a freeze commit.
func lockUnfrozenSubmissionMutation(dbs database.DBSession, sid int64) error {
	if err := lockSubmissionMutation(dbs, sid); err != nil {
		return err
	}
	var frozen bool
	if err := dbs.Tx().QueryRowContext(dbs.Ctx(), `SELECT frozen_at IS NOT NULL FROM submission WHERE id = ? FOR UPDATE`, sid).Scan(&frozen); err != nil {
		return dberr(err)
	}
	if frozen {
		return perr("submission is frozen", http.StatusForbidden)
	}
	return nil
}

// Child-to-parent identity is immutable. Resolve it in a separate short read
// transaction so the mutation transaction can lock the parent BEFORE taking its
// snapshot. Re-read the child under that lock before validating or changing it.
func (s *SiteService) submissionIDForDeletion(ctx context.Context, id int64, file bool) (int64, error) {
	lookup, err := s.dal.NewSession(ctx)
	if err != nil {
		return 0, dberr(err)
	}
	defer lookup.Rollback()
	table := "comment"
	if file {
		table = "submission_file"
	}
	var sid int64
	err = lookup.Tx().QueryRowContext(ctx, "SELECT fk_submission_id FROM "+table+" WHERE id = ? AND deleted_at IS NULL", id).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, perr("submission item not found", http.StatusNotFound)
	}
	if err != nil {
		return 0, dberr(err)
	}
	return sid, nil
}
