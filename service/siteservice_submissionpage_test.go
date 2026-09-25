package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

type submissionPageSession struct{ database.DBSession }

func (submissionPageSession) Rollback() error { return nil }

type submissionPageDAL struct {
	database.DAL
	sessions int
}

func (d *submissionPageDAL) NewSession(context.Context) (database.DBSession, error) {
	d.sessions++
	return submissionPageSession{}, nil
}

func (*submissionPageDAL) SearchSubmissions(database.DBSession, *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	return nil, 0, nil
}

type submissionPagePGDAL struct {
	database.PGDAL
	opened bool
}

func (d *submissionPagePGDAL) NewSession(context.Context) (database.PGDBSession, error) {
	d.opened = true
	return nil, errors.New("unexpected PostgreSQL transaction")
}

type unavailableTagValidator struct{ Validator }

func (unavailableTagValidator) GetTags(context.Context) ([]types.Tag, error) {
	return nil, errors.New("validator unavailable")
}

func TestSubmissionPageDoesNotOpenPostgresWhenValidatorFails(t *testing.T) {
	dal := &submissionPageDAL{}
	pgdal := &submissionPagePGDAL{}
	s := &SiteService{dal: dal, pgdal: pgdal, validator: unavailableTagValidator{}}

	_, err := s.GetViewSubmissionPageData(context.Background(), 0, 123)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "submission not found"), err)
	require.Equal(t, 2, dal.sessions)
	require.False(t, pgdal.opened)
}
