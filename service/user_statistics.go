package service

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/clients"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"golang.org/x/sync/singleflight"
)

const userStatisticsTTL = time.Minute
const userStatisticsTimeout = 30 * time.Second

// Cached snapshots are immutable after publication and belong to this service,
// not the process-wide page cache. Never cache a failed or partial refresh.
type userStatisticsCache struct {
	mu       sync.Mutex
	snapshot *types.UserStatisticsResponse
	expires  time.Time
	refresh  singleflight.Group
}

func (c *userStatisticsCache) fresh() *types.UserStatisticsResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.snapshot
	}
	return nil
}

func (c *userStatisticsCache) get(ctx context.Context, load func(context.Context) ([]*types.UserStatistics, error)) (*types.UserStatisticsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot := c.fresh(); snapshot != nil {
		return snapshot, nil
	}
	result := c.refresh.DoChan("all", func() (interface{}, error) {
		if snapshot := c.fresh(); snapshot != nil {
			return snapshot, nil
		}
		// A disconnected browser must not cancel a refresh shared by others.
		// It still has a fixed deadline, and each waiter can cancel independently.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), userStatisticsTimeout)
		defer cancel()
		users, err := load(refreshCtx)
		if err != nil {
			return nil, err
		}
		if err := refreshCtx.Err(); err != nil {
			return nil, err
		}
		if users == nil {
			users = make([]*types.UserStatistics, 0)
		}
		snapshot := &types.UserStatisticsResponse{Users: users, GeneratedAt: time.Now().UTC()}
		c.mu.Lock()
		c.snapshot = snapshot
		c.expires = snapshot.GeneratedAt.Add(userStatisticsTTL)
		c.mu.Unlock()
		return snapshot, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*types.UserStatisticsResponse), nil
	}
}

func (s *SiteService) GetAllUserStatistics(ctx context.Context) (*types.UserStatisticsResponse, error) {
	return s.userStatisticsCache.get(ctx, s.loadAllUserStatistics)
}

func (s *SiteService) loadAllUserStatistics(ctx context.Context) ([]*types.UserStatistics, error) {
	// Release MariaDB before acquiring PostgreSQL; neither transaction spans
	// work on the other database.
	users, err := func() ([]*types.UserStatistics, error) {
		dbs, err := s.dal.NewSession(ctx)
		if err != nil {
			return nil, err
		}
		defer dbs.Rollback()
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
