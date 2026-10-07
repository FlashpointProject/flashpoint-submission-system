package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/FlashpointProject/flashpoint-submission-system/linkpreview"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/jackc/pgx/v5"
)

// GetSubmissionPreview reads fresh visibility on every request, including image
// requests. The crawler's User-Agent is not authentication: only this allowlisted
// summary is public. SearchSubmissions excludes deleted submissions/files and
// provides the same current-file and action-state semantics as the search table.
func (s *SiteService) GetSubmissionPreview(ctx context.Context, id int64) (*linkpreview.Card, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	defer dbs.Rollback()
	files, err := s.dal.GetExtendedSubmissionFilesBySubmissionID(dbs, id)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	rows, _, err := s.dal.SearchSubmissions(dbs, &types.SubmissionsFilter{SubmissionIDs: []int64{id}})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.SubmissionID == id {
			// A stale cache must not make a deleted or foreign file public.
			for _, file := range files {
				if file.FileID == row.FileID && file.SubmissionID == id {
					return linkpreview.Submission(row), nil
				}
			}
		}
	}
	return nil, nil
}

// Tag previews intentionally omit revision history, reasons and author IDs.
func (s *SiteService) GetTagPreview(ctx context.Context, key string) (*linkpreview.Card, error) {
	dbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	defer dbs.Rollback()
	var tag *types.Tag
	if id, e := strconv.ParseInt(key, 10, 64); e == nil {
		tag, err = s.pgdal.GetTag(dbs, id)
	} else {
		tag, err = s.pgdal.GetTagByName(dbs, key)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if tag == nil || tag.Deleted {
		return nil, nil
	}
	count, err := s.pgdal.GetGamesUsingTagTotal(dbs, tag.ID)
	if err != nil {
		return nil, err
	}
	return linkpreview.Tag(tag, count), nil
}
