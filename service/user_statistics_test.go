package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

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
	result := map[int64]time.Time{ids[0]: time.Unix(20, 0)}
	if len(ids) > 1 {
		result[ids[1]] = time.Unix(40, 0)
	}
	return result, nil
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

func (d *statisticsDAL) GetUserStatisticsAggregate(_ database.DBSession, uid int64) ([]*types.UserStatistics, error) {
	if d.failure == "maria-query" {
		return nil, d.cause
	}
	return []*types.UserStatistics{{UserID: strconv.FormatInt(uid, 10)}}, nil
}
func (d *statisticsDAL) GetSiteStatistics(database.DBSession) (*types.StatisticsPageData, error) {
	if d.failure == "maria-query" {
		return nil, d.cause
	}
	return &types.StatisticsPageData{SubmissionCount: 42}, nil
}

func TestStatisticsCachesReuseDataAndRetryFailures(t *testing.T) {
	for _, kind := range []string{"site", "all-users", "user"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("database unavailable")
			dal := &statisticsDAL{failure: "maria-query", cause: failure}
			pgdal := &statisticsPGDAL{maria: dal}
			s := &SiteService{dal: dal, pgdal: pgdal}
			ctx := context.Background()
			load := func() (any, error) {
				switch kind {
				case "site":
					return s.GetStatisticsPageData(ctx)
				case "all-users":
					return s.GetAllUserStatistics(ctx)
				default:
					return s.GetUserStatistics(ctx, 1002)
				}
			}
			_, err := load()
			require.ErrorIs(t, err, failure)
			dal.failure = ""
			first, err := load()
			require.NoError(t, err)
			originalSession := dal.session
			// Cached calls must not touch the database, even if it subsequently fails.
			dal.failure = "maria-session"
			again, err := load()
			require.NoError(t, err)
			require.Equal(t, first, again)
			require.Same(t, originalSession, dal.session)
			switch v := first.(type) {
			case *types.StatisticsPageData:
				v.SubmissionCount = 999
			case *types.UserStatisticsResponse:
				v.Users[0].Username = "caller edit"
			case *types.UserStatistics:
				v.Username = "caller edit"
			}
			independent, err := load()
			require.NoError(t, err)
			require.Equal(t, again, independent)
			require.NotEqual(t, first, independent)
		})
	}
}
