package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/stretchr/testify/require"
)

type rebuildDAL struct {
	database.DAL
	ids       []int64
	committed []int64
	cursors   []int64
	failure   string
	sessions  int
	failureAt int64
	cause     error
}
type rebuildSession struct {
	database.DBSession
	dal        *rebuildDAL
	sid        int64
	rolledBack bool
}

func (d *rebuildDAL) NewSession(context.Context) (database.DBSession, error) {
	d.sessions++
	if (d.failure == "session" && d.sessions == 3) || (d.failure == "item-session" && d.sessions == 4) {
		return nil, d.cause
	}
	return &rebuildSession{dal: d}, nil
}
func (s *rebuildSession) Tx() *sql.Tx     { panic("unit fake does not execute SQL") }
func (s *rebuildSession) Rollback() error { s.rolledBack = true; return nil }
func (s *rebuildSession) Commit() error {
	if s.dal.failure == "commit" && s.sid == s.dal.failureAt {
		return s.dal.cause
	}
	s.dal.committed = append(s.dal.committed, s.sid)
	return nil
}
func (d *rebuildDAL) ListSubmissionIDsForCacheRebuild(_ database.DBSession, after int64, limit int) ([]int64, error) {
	d.cursors = append(d.cursors, after)
	if d.failure == "list" && after > 0 {
		return nil, d.cause
	}
	var out []int64
	for _, id := range d.ids {
		if id > after {
			out = append(out, id)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (d *rebuildDAL) RebuildSubmissionCacheTable(session database.DBSession, id int64) error {
	session.(*rebuildSession).sid = id
	if d.failure == "rebuild" && id == d.failureAt {
		return d.cause
	}
	return nil
}

func TestRecomputeSubmissionCacheAllTraversesBeyondOldLimit(t *testing.T) {
	for _, count := range []int{0, 1, 1000, 1001, 10001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			d := &rebuildDAL{}
			for i := 1; i <= count; i++ {
				d.ids = append(d.ids, int64(i*2))
			} // gaps are not offsets
			s := &SiteService{dal: d}
			result, err := s.RecomputeSubmissionCacheAll(context.Background())
			require.NoError(t, err)
			require.Equal(t, int64(count), result.Recomputed)
			require.Equal(t, d.ids, d.committed)
			require.Equal(t, int64(count*2), result.LastSubmissionID)
			require.Len(t, d.cursors, (count+999)/1000+1)
			// A fresh invocation repairs the same source rows exactly once again.
			d.committed = nil
			d.cursors = nil
			repeated, err := s.RecomputeSubmissionCacheAll(context.Background())
			require.NoError(t, err)
			require.Equal(t, result, repeated)
			require.Equal(t, d.ids, d.committed)
		})
	}
}

func TestRecomputeSubmissionCacheAllReportsCommittedProgress(t *testing.T) {
	for _, stage := range []string{"session", "item-session", "rebuild", "commit", "list"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("injected storage failure")
			d := &rebuildDAL{ids: []int64{10, 20, 30}, failure: stage, failureAt: 20, cause: cause}
			s := &SiteService{dal: d}
			result, err := s.recomputeSubmissionCacheAll(context.Background(), 1)
			require.ErrorIs(t, err, cause)
			require.Equal(t, SubmissionCacheRebuildResult{Recomputed: 1, LastSubmissionID: 10}, result)
			require.Equal(t, []int64{10}, d.committed)
			if stage == "rebuild" || stage == "commit" || stage == "item-session" {
				require.Contains(t, err.Error(), "submission 20")
			}
			require.Contains(t, err.Error(), "10")
			d.failure = ""
			d.committed = nil
			result, err = s.recomputeSubmissionCacheAll(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, int64(3), result.Recomputed)
			require.Equal(t, d.ids, d.committed)
		})
	}
}

func TestRecomputeSubmissionCacheAllCancellationAndInvalidBatch(t *testing.T) {
	d := &rebuildDAL{}
	s := &SiteService{dal: d}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.RecomputeSubmissionCacheAll(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, d.sessions)
	_, err = s.recomputeSubmissionCacheAll(context.Background(), 0)
	require.Error(t, err)
	require.Zero(t, d.sessions)
}
