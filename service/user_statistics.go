package service

import (
	"context"
	"strconv"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/appcache"
	"github.com/FlashpointProject/flashpoint-submission-system/clients"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// Statistics may be up to ten minutes old; ordinary writes do not invalidate them.
const statisticsCacheTTL = 10 * time.Minute

func (s *SiteService) GetAllUserStatistics(ctx context.Context) (*types.UserStatisticsResponse, error) {
	return s.readCaches().allUserStatistics.Get(ctx, "all", func(ctx context.Context) (appcache.Snapshot[*types.UserStatisticsResponse], error) {
		users, err := s.loadAllUserStatistics(ctx)
		if err != nil {
			return appcache.Snapshot[*types.UserStatisticsResponse]{}, err
		}
		if users == nil {
			users = make([]*types.UserStatistics, 0)
		}
		generated := time.Now().UTC()
		return appcache.Snapshot[*types.UserStatisticsResponse]{Value: &types.UserStatisticsResponse{Users: users, GeneratedAt: generated}, Expires: generated.Add(statisticsCacheTTL)}, nil
	})
}

func (s *SiteService) loadAllUserStatistics(ctx context.Context) ([]*types.UserStatistics, error) {
	return s.loadUserStatistics(ctx, nil)
}
func (s *SiteService) loadUserStatistics(ctx context.Context, uid *int64) ([]*types.UserStatistics, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Release MariaDB before acquiring PostgreSQL; neither transaction spans
	// work on the other database.
	users, err := func() ([]*types.UserStatistics, error) {
		dbs, err := s.dal.NewSession(ctx)
		if err != nil {
			return nil, err
		}
		defer dbs.Rollback()
		if uid != nil {
			return s.dal.GetUserStatisticsAggregate(dbs, *uid)
		}
		return s.dal.GetAllUserStatistics(dbs)
	}()
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return users, nil
	}
	ids := make([]int64, len(users))
	for i, user := range users {
		id, err := strconv.ParseInt(user.UserID, 10, 64)
		if err != nil {
			return nil, err
		}
		ids[i] = id
		// Client applications have code-defined roles, as in GetUserRoles.
		for _, client := range clients.ClientApps {
			if client.UserID != id {
				continue
			}
			user.Role = "User"
			if constants.IsTrialCurator(client.UserRoles) {
				user.Role = constants.RoleTrialCurator
			}
			if constants.IsStaff(client.UserRoles) {
				user.Role = "Staff"
			}
			break
		}
	}
	dbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	defer dbs.Rollback()
	activity, err := s.pgdal.GetLatestSubmissionActivity(dbs, ids)
	if err != nil {
		return nil, err
	}
	for i, user := range users {
		if at := activity[ids[i]]; at.After(user.LastUserActivity) {
			user.LastUserActivity = at
		}
	}
	return users, nil
}
