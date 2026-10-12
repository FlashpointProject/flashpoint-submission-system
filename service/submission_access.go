package service

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/FlashpointProject/flashpoint-submission-system/appcache"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
)

// Only source facts are cached. The permission decision is rebuilt for the
// current viewer, using the independently invalidated user-role cache.
func (s *SiteService) canReadFrozenSubmission(ctx context.Context, uid, sid int64) (bool, error) {
	if uid == 0 {
		return false, nil
	}
	roles, err := s.GetUserRoles(ctx, uid)
	if err != nil {
		return false, err
	}
	if constants.IsFreezer(roles) {
		return true, nil
	}
	ids, err := s.readCaches().uploaders.Get(ctx, fmt.Sprint(sid), func(ctx context.Context) (appcache.Snapshot[[]int64], error) {
		dbs, err := s.dal.NewSession(ctx)
		if err != nil {
			return appcache.Snapshot[[]int64]{}, err
		}
		defer dbs.Rollback()
		ids, err := s.dal.GetSubmissionUploaderIDs(dbs, sid)
		return appcache.Snapshot[[]int64]{Value: ids, Dependencies: []appcache.Dependency{appcache.Key("submission", sid)}}, err
	})
	return slices.Contains(ids, uid), err
}

// CanReadSubmissionDetails also protects images and every archive version.
// Summaries/search remain available to all authenticated viewers.
func (s *SiteService) CanReadSubmissionDetails(ctx context.Context, uid, sid int64) (bool, error) {
	subs, err := s.GetSubmissionSummary(ctx, sid)
	if err != nil {
		return false, err
	}
	if len(subs) == 0 {
		return false, perr("submission not found", http.StatusNotFound)
	}
	if !subs[0].IsFrozen {
		return true, nil
	}
	return s.canReadFrozenSubmission(ctx, uid, sid)
}
