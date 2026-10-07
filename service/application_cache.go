package service

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/FlashpointProject/flashpoint-submission-system/appcache"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

type submissionSnapshot struct {
	Submissions []*types.ExtendedSubmission
	Meta        *types.CurationMeta
	Comments    []*types.ExtendedComment
	ImageIDs    []int64
}
type submissionNavigation struct{ Next, Previous *int64 }
type applicationCaches struct {
	sessions          *appcache.Cache[*types.SessionInfo]
	users             *appcache.Cache[*types.DiscordUser]
	roles             *appcache.Cache[[]string]
	submissions       *appcache.Cache[submissionSnapshot]
	summaries         *appcache.Cache[[]*types.ExtendedSubmission]
	files             *appcache.Cache[[]*types.ExtendedSubmissionFile]
	file              *appcache.Cache[[]*types.SubmissionFile]
	subscriptions     *appcache.Cache[bool]
	navigation        *appcache.Cache[submissionNavigation]
	metadata          *appcache.Cache[*types.MetadataStatsPageDataBare]
	siteStatistics    *appcache.Cache[*types.StatisticsPageData]
	allUserStatistics *appcache.Cache[*types.UserStatisticsResponse]
	userStatistics    *appcache.Cache[*types.UserStatistics]
}

func (s *SiteService) readCaches() *applicationCaches {
	s.cacheOnce.Do(func() {
		if s.cacheCoordinator == nil {
			s.cacheCoordinator = &appcache.Coordinator{}
		}
		c := s.cacheCoordinator
		s.caches = &applicationCaches{
			siteStatistics:    appcache.New[*types.StatisticsPageData](c, 1, 8192),
			allUserStatistics: appcache.New[*types.UserStatisticsResponse](c, 1, 16<<20),
			userStatistics:    appcache.New[*types.UserStatistics](c, 10000, 16<<20),
			sessions:          appcache.New[*types.SessionInfo](c, 10000, 8<<20), users: appcache.New[*types.DiscordUser](c, 10000, 8<<20), roles: appcache.New[[]string](c, 10000, 4<<20),
			submissions: appcache.New[submissionSnapshot](c, 20000, 512<<20), summaries: appcache.New[[]*types.ExtendedSubmission](c, 4000, 16<<20),
			files: appcache.New[[]*types.ExtendedSubmissionFile](c, 1000, 16<<20), file: appcache.New[[]*types.SubmissionFile](c, 4000, 8<<20),
			subscriptions: appcache.New[bool](c, 20000, 1<<20), navigation: appcache.New[submissionNavigation](c, 4000, 1<<20), metadata: appcache.New[*types.MetadataStatsPageDataBare](c, 1, 4096),
		}
	})
	return s.caches
}

// ApplicationCacheStats contains no user, token, or resource identifiers.
func (s *SiteService) ApplicationCacheStats() map[string]appcache.Stats {
	c := s.readCaches()
	return map[string]appcache.Stats{"site-statistics": c.siteStatistics.Stats(), "all-user-statistics": c.allUserStatistics.Stats(), "user-statistics": c.userStatistics.Stats(), "sessions": c.sessions.Stats(), "users": c.users.Stats(), "roles": c.roles.Stats(), "submissions": c.submissions.Stats(), "summaries": c.summaries.Stats(), "files": c.files.Stats(), "file": c.file.Stats(), "subscriptions": c.subscriptions.Stats(), "navigation": c.navigation.Stats(), "metadata": c.metadata.Stats()}
}
func sessionKey(secret string) string       { return fmt.Sprintf("%x", sha256.Sum256([]byte(secret))) }
func subscriptionKey(uid, sid int64) string { return fmt.Sprintf("%d:%d", uid, sid) }
func userDependencies(subs []*types.ExtendedSubmission, comments []*types.ExtendedComment) []appcache.Dependency {
	deps := []appcache.Dependency{}
	seen := make(map[appcache.Dependency]bool)
	add := func(uid int64) {
		d := appcache.Key("user", uid)
		if !seen[d] {
			seen[d] = true
			deps = append(deps, d)
		}
	}
	for _, sub := range subs {
		add(sub.SubmitterID)
		add(sub.UpdaterID)
	}
	for _, comment := range comments {
		add(comment.AuthorID)
	}
	return deps
}
func (s *SiteService) GetSubmissionSummary(ctx context.Context, sid int64) ([]*types.ExtendedSubmission, error) {
	return s.readCaches().summaries.Get(ctx, fmt.Sprint(sid), func(ctx context.Context) (appcache.Snapshot[[]*types.ExtendedSubmission], error) {
		subs, _, err := s.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
		deps := append(userDependencies(subs, nil), appcache.Key("submission", sid))
		return appcache.Snapshot[[]*types.ExtendedSubmission]{Value: subs, Dependencies: deps}, err
	})
}
