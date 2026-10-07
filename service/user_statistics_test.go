package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

func TestUserStatisticsCacheSharesRefreshAndAllowsCancellation(t *testing.T) {
	var cache userStatisticsCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) ([]*types.UserStatistics, error) {
		calls.Add(1)
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > userStatisticsTimeout {
			return nil, errors.New("refresh must have bounded deadline")
		}
		return []*types.UserStatistics{{UserID: "123", UserCommentedCount: 7}}, nil
	}
	leader, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := cache.get(leader, load); first <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	const waiters = 12
	var wg sync.WaitGroup
	results := make(chan *types.UserStatisticsResponse, waiters)
	errs := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s, e := cache.get(context.Background(), load); results <- s; errs <- e }()
	}
	close(release)
	wg.Wait()
	for i := 0; i < waiters; i++ {
		require.NoError(t, <-errs)
		require.EqualValues(t, 7, (<-results).Users[0].UserCommentedCount)
	}
	require.EqualValues(t, 1, calls.Load())
}

func TestUserStatisticsCacheRetriesFailuresAndRefreshesExpiredData(t *testing.T) {
	var cache userStatisticsCache
	cause := errors.New("database unavailable")
	_, err := cache.get(context.Background(), func(context.Context) ([]*types.UserStatistics, error) { return nil, cause })
	require.ErrorIs(t, err, cause)
	require.Nil(t, cache.fresh())
	first, err := cache.get(context.Background(), func(context.Context) ([]*types.UserStatistics, error) { return nil, nil })
	require.NoError(t, err)
	require.NotNil(t, first.Users, "empty result must encode as []")
	require.False(t, first.GeneratedAt.IsZero())
	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	next, err := cache.get(context.Background(), func(context.Context) ([]*types.UserStatistics, error) {
		return []*types.UserStatistics{{UserID: "42"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "42", next.Users[0].UserID)
	require.Empty(t, first.Users, "refresh does not mutate old snapshots")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cache.get(canceled, func(context.Context) ([]*types.UserStatistics, error) {
		t.Fatal("canceled request loaded data")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

type statisticsSession struct {
	database.DBSession
	closed bool
}

func (s *statisticsSession) Rollback() error { s.closed = true; return nil }

type statisticsPGSession struct {
	database.PGDBSession
	closed bool
}

func (s *statisticsPGSession) Rollback() error { s.closed = true; return nil }

type statisticsDAL struct {
	database.DAL
	session *statisticsSession
	failure string
	cause   error
}

func (d *statisticsDAL) NewSession(context.Context) (database.DBSession, error) {
	if d.failure == "maria-session" {
		return nil, d.cause
	}
	d.session = &statisticsSession{}
	return d.session, nil
}
func (d *statisticsDAL) GetAllUserStatistics(database.DBSession) ([]*types.UserStatistics, error) {
	if d.failure == "maria-query" {
		return nil, d.cause
	}
	return []*types.UserStatistics{{UserID: "1002", Role: "Staff", LastUserActivity: time.Unix(30, 0)}, {UserID: "1003", Role: "User", LastUserActivity: time.Unix(10, 0)}}, nil
}

type statisticsPGDAL struct {
	database.PGDAL
	maria   *statisticsDAL
	session *statisticsPGSession
	failure string
	cause   error
}

func (d *statisticsPGDAL) NewSession(context.Context) (database.PGDBSession, error) {
	if !d.maria.session.closed {
		panic("MariaDB session held across PostgreSQL work")
	}
	if d.failure == "pg-session" {
		return nil, d.cause
	}
	d.session = &statisticsPGSession{}
	return d.session, nil
}
func (d *statisticsPGDAL) GetLatestSubmissionActivity(_ database.PGDBSession, ids []int64) (map[int64]time.Time, error) {
	if d.failure == "pg-query" {
		return nil, d.cause
	}
	return map[int64]time.Time{ids[0]: time.Unix(20, 0), ids[1]: time.Unix(40, 0)}, nil
}

func TestUserStatisticsServiceMergesActivityAndReleasesSessions(t *testing.T) {
	for _, failure := range []string{"", "maria-session", "maria-query", "pg-session", "pg-query"} {
		t.Run(failure, func(t *testing.T) {
			cause := errors.New("injected failure")
			dal := &statisticsDAL{failure: failure, cause: cause}
			pgdal := &statisticsPGDAL{maria: dal, failure: failure, cause: cause}
			s := &SiteService{dal: dal, pgdal: pgdal}
			rows, err := s.loadAllUserStatistics(context.Background())
			if failure != "" {
				require.ErrorIs(t, err, cause)
				require.Nil(t, rows)
			} else {
				require.NoError(t, err)
				require.Equal(t, "User", rows[0].Role, "code-defined client roles override stored roles")
				require.Equal(t, "Staff", rows[1].Role)
				require.Equal(t, time.Unix(30, 0), rows[0].LastUserActivity, "old events do not replace newer comments")
				require.Equal(t, time.Unix(40, 0), rows[1].LastUserActivity)
			}
			if dal.session != nil {
				require.True(t, dal.session.closed)
			}
			if pgdal.session != nil {
				require.True(t, pgdal.session.closed)
			}
		})
	}
}
