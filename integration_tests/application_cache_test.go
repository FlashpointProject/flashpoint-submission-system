package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/FlashpointProject/flashpoint-submission-system/appcache"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

func TestApplicationCacheSubmissionCommitsAndViewerIsolation(t *testing.T) {
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009201, []int64{roleIDCurator}, "owner")
	reviewer := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009202, []int64{roleIDModerator}, "reviewer")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	api := func(user *extendedTestUser) *types.ViewSubmissionPageData {
		t.Helper()
		r := getWithCookie(t, l, app, user.Cookie, fmt.Sprintf("/api/submission/%d", sid))
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		var v types.ViewSubmissionPageData
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &v))
		return &v
	}
	v := api(owner)
	require.Equal(t, owner.ID, v.UserID)
	require.True(t, v.IsUserSubscribed)
	v = api(reviewer)
	require.Equal(t, reviewer.ID, v.UserID)
	require.False(t, v.IsUserSubscribed)
	before := app.Service.ApplicationCacheStats()
	selectCount := func() uint64 {
		var label string
		var count uint64
		require.NoError(t, maria.QueryRow("SHOW GLOBAL STATUS LIKE 'Com_select'").Scan(&label, &count))
		return count
	}
	selects := selectCount()
	v = api(owner)
	_ = api(reviewer)
	for name, a := range before {
		require.Equal(t, a.Loads, app.Service.ApplicationCacheStats()[name].Loads, "warm %s", name)
	}
	// HTML uses the same content and the correct viewer.
	r := getWithCookie(t, l, app, reviewer.Cookie, fmt.Sprintf("/web/submission/%d", sid))
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, before["submissions"].Loads, app.Service.ApplicationCacheStats()["submissions"].Loads)
	require.Equal(t, selects, selectCount(), "warm owner/reviewer API and HTML requests execute zero SELECTs")
	actor := addContextValues(ctx, l, reviewer.ID, "cache-mutations")
	require.NoError(t, app.Service.UpdateSubscriptionSettings(actor, reviewer.ID, sid, true))
	require.True(t, api(reviewer).IsUserSubscribed)
	r = addComment(t, l, app, reviewer.Cookie, sid, constants.ActionAssignTesting, "cache regression")
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, api(owner).Submissions[0].AssignedTestingUserIDs, reviewer.ID)
	r = addComment(t, l, app, reviewer.Cookie, sid, constants.ActionComment, "new visible comment")
	require.Equal(t, 200, r.Code, r.Body.String())
	v = api(owner)
	require.Contains(t, *v.Comments[len(v.Comments)-1].Message, "new visible comment")
	cid := v.Comments[len(v.Comments)-1].CommentID
	require.NoError(t, app.Service.SaveSystemUser(actor, &types.DiscordUser{ID: reviewer.ID, Username: "renamed reviewer", Avatar: "new-avatar"}))
	v = api(owner)
	require.Equal(t, "renamed reviewer", v.Comments[len(v.Comments)-1].Username)
	require.NoError(t, app.Service.FreezeSubmission(actor, sid))
	require.True(t, api(owner).Submissions[0].IsFrozen)
	require.NoError(t, app.Service.UnfreezeSubmission(actor, sid))
	require.False(t, api(owner).Submissions[0].IsFrozen)
	require.NoError(t, app.Service.OverrideBot(actor, sid))
	_ = api(owner)
	// Updating the submission invalidates detail and files and auto-subscriptions.
	oldFile := api(owner).Submissions[0].FileID
	_ = getWithCookie(t, l, app, reviewer.Cookie, fmt.Sprintf("/api/submission/%d/files", sid))
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, &sid)
	require.NotEqual(t, oldFile, api(owner).Submissions[0].FileID)
	// Rejected batch rolls back source writes and leaves resident content valid.
	resident := api(owner)
	loads := app.Service.ApplicationCacheStats()["submissions"].Loads
	require.Error(t, app.Service.ReceiveComments(actor, reviewer.ID, []int64{sid, 999999999}, constants.ActionComment, "rollback", "false", "", "", "", "", nil))
	require.Equal(t, resident, api(owner))
	require.Equal(t, loads, app.Service.ApplicationCacheStats()["submissions"].Loads)
	// Navigation must change when a neighboring submission is inserted/deleted.
	next := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	require.Equal(t, &next, api(owner).NextSubmissionID)
	require.NoError(t, app.Service.SoftDeleteSubmission(actor, next, "cache navigation"))
	require.Nil(t, api(owner).NextSubmissionID)
	// Delete a warm comment and the newest file, then observe the surviving revision.
	v = api(owner)
	require.NoError(t, app.Service.SoftDeleteComment(actor, cid, "cache deletion"))
	for _, c := range api(owner).Comments {
		require.NotEqual(t, cid, c.CommentID)
	}
	latestFile := api(owner).Submissions[0].FileID
	require.NoError(t, app.Service.SoftDeleteSubmissionFile(actor, latestFile, "cache deletion"))
	require.Equal(t, oldFile, api(owner).Submissions[0].FileID)
	// Source read is the oracle; the read snapshot is not used for validation.
	s, e := db.NewSession(actor)
	require.NoError(t, e)
	defer s.Rollback()
	rows, _, e := db.SearchSubmissions(s, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
	require.NoError(t, e)
	require.Equal(t, rows, api(owner).Submissions)
	var count int
	require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM submission_file WHERE fk_submission_id=? AND deleted_at IS NULL", sid).Scan(&count))
	require.Equal(t, 1, count)
}

func TestApplicationCacheSessionRevocation(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTest(t)
	token := createTestUser(t, ctx, l, app, db, pgdb, 100009301, []int64{roleIDCurator})
	ctx = addContextValues(ctx, l, 100009301, "cache-auth")
	info, ok, e := app.Service.GetSessionAuthInfo(ctx, token.Secret)
	require.NoError(t, e)
	require.True(t, ok)
	_, ok, e = app.Service.GetSessionAuthInfo(ctx, token.Secret)
	require.NoError(t, e)
	require.True(t, ok)
	require.EqualValues(t, 1, app.Service.ApplicationCacheStats()["sessions"].Loads)
	require.NoError(t, app.Service.RevokeSession(ctx, info.UID, info.ID))
	_, ok, _ = app.Service.GetSessionAuthInfo(ctx, token.Secret)
	require.False(t, ok)
	for _, kind := range []string{"logout", "user", "ban", "global"} {
		mapped, e := app.Service.GenAuthToken(ctx, info.UID, types.AuthScopeAll, "cache-test", "127.0.0.1")
		require.NoError(t, e)
		secret := mapped["Secret"]
		require.NotEmpty(t, secret)
		_, ok, e = app.Service.GetSessionAuthInfo(ctx, secret)
		require.NoError(t, e)
		require.True(t, ok)
		switch kind {
		case "logout":
			tok := *token
			tok.Secret = secret
			require.NoError(t, app.Service.Logout(ctx, &tok))
		case "user":
			_, e = app.Service.DeleteUserSessions(ctx, info.UID)
			require.NoError(t, e)
		case "ban":
			_, e = app.Service.UserBan(ctx, info.UID)
			require.NoError(t, e)
		case "global":
			require.NoError(t, app.Service.NukeSessionTable(ctx))
		}
		_, ok, _ = app.Service.GetSessionAuthInfo(ctx, secret)
		require.False(t, ok, kind)
	}
}

func TestSiteStatisticsAggregateMatchesSearch(t *testing.T) {
	_, f, _ := newCommentTransactionFixture(t)
	f.InTx(t, func(s database.DBSession) {
		want, e := f.DB.GetSiteStatistics(s)
		require.NoError(t, e)
		approved, verified := "approved", "verified"
		for _, c := range []struct {
			filter *types.SubmissionsFilter
			want   int64
		}{
			{nil, want.SubmissionCount}, {&types.SubmissionsFilter{BotActions: []string{"approve"}}, want.SubmissionCountBotHappy},
			{&types.SubmissionsFilter{BotActions: []string{"request-changes"}}, want.SubmissionCountBotSad},
			{&types.SubmissionsFilter{ApprovalsStatus: &approved}, want.SubmissionCountApproved},
			{&types.SubmissionsFilter{VerificationStatus: &verified}, want.SubmissionCountVerified},
			{&types.SubmissionsFilter{DistinctActions: []string{"reject"}}, want.SubmissionCountRejected},
			{&types.SubmissionsFilter{DistinctActions: []string{"mark-added"}}, want.SubmissionCountInFlashpoint},
		} {
			_, n, e := f.DB.SearchSubmissions(s, c.filter)
			require.NoError(t, e)
			require.Equal(t, n, c.want)
		}
		all, e := f.DB.GetAllUserStatistics(s)
		require.NoError(t, e)
		for _, user := range all {
			var uid int64
			_, e = fmt.Sscan(user.UserID, &uid)
			require.NoError(t, e)
			one, e := f.DB.GetUserStatisticsAggregate(s, uid)
			require.NoError(t, e)
			require.Equal(t, []*types.UserStatistics{user}, one)
		}
	})
}

func TestApplicationCacheUploadCompletionIsIdempotent(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var validations atomic.Int32
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{BeforeValidate: func() {
		if validations.Add(1) == 1 {
			close(started)
		}
		<-release
	}})
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009401, []int64{roleIDCurator}, "upload owner")
	ctx = addContextValues(ctx, l, user.ID, "duplicate-upload")
	content, e := os.ReadFile("./test_files/Warpstar4K.7z")
	require.NoError(t, e)
	p := types.ResumableParams{ResumableIdentifier: "duplicate-completion", ResumableFilename: "test.7z", ResumableChunkNumber: 1, ResumableTotalChunks: 1, ResumableTotalSize: int64(len(content)), ResumableCurrentChunkSize: int64(len(content))}
	name, e := app.Service.ReceiveSubmissionChunk(ctx, nil, &p, content)
	require.NoError(t, e)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("validator did not start")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same, e := app.Service.ReceiveSubmissionChunk(ctx, nil, &p, content)
			require.NoError(t, e)
			require.Equal(t, name, same)
		}()
	}
	wg.Wait()
	close(release)
	status := quotaWait(t, app, *name)
	require.Equal(t, "success", status.Status)
	again, e := app.Service.ReceiveSubmissionChunk(ctx, nil, &p, content)
	require.NoError(t, e)
	require.Equal(t, name, again)
	require.EqualValues(t, 1, validations.Load())
	var count int
	require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM submission_file").Scan(&count))
	require.Equal(t, 1, count)
}

func TestApplicationCacheLoginRefreshesRolesAndProfile(t *testing.T) {
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
	const uid = int64(100009501)
	token := createTestUser(t, ctx, l, app, db, pgdb, uid, []int64{roleIDModerator})
	ctx = addContextValues(ctx, l, uid, "role-cache")
	allowed := getWithCookie(t, l, app, createTestCookie(t, l, token), "/api/activity-events")
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
	roles, err := app.Service.GetUserRoles(ctx, uid)
	require.NoError(t, err)
	require.True(t, constants.IsStaff(roles))
	_, err = app.Service.GetDiscordUser(ctx, uid)
	require.NoError(t, err)
	// The Discord mock returns member role ID 1, replacing the previous staff role.
	_, err = maria.Exec("INSERT INTO discord_role (id,name,color) VALUES (1,'Cache Reader',0)")
	require.NoError(t, err)
	_, err = app.Service.SaveUser(ctx, &types.DiscordUser{ID: uid, Username: "refreshed login"}, types.AuthScopeAll, "cache-test", "127.0.0.1")
	require.NoError(t, err)
	roles, err = app.Service.GetUserRoles(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, []string{"Cache Reader"}, roles)
	profile, err := app.Service.GetDiscordUser(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, "refreshed login", profile.Username)
	// The existing session now receives the new permissions too.
	denied := getWithCookie(t, l, app, createTestCookie(t, l, token), "/api/activity-events")
	require.Equal(t, http.StatusUnauthorized, denied.Code, denied.Body.String())
}

func TestApplicationCacheIndependentDatabaseCommitAndRollback(t *testing.T) {
	_, l, ctx, _, _, maria, postgres := setupIntegrationTest(t)
	ctx = addContextValues(ctx, l, 100009601, "independent-commit")
	coord := &appcache.Coordinator{}
	db := database.NewMysqlDAL(maria, coord)
	pgdb := database.NewPostgresDAL(postgres, coord)
	users := appcache.New[int](coord, 2, 100)
	metadata := appcache.New[*types.MetadataStatsPageDataBare](coord, 1, 4096)
	loadUser := func(context.Context) (appcache.Snapshot[int], error) {
		return appcache.Snapshot[int]{Value: 1, Dependencies: []appcache.Dependency{appcache.Key("user", 100009601)}}, nil
	}
	loadMetadata := func(ctx context.Context) (appcache.Snapshot[*types.MetadataStatsPageDataBare], error) {
		tx, err := pgdb.NewSession(ctx)
		if err != nil {
			return appcache.Snapshot[*types.MetadataStatsPageDataBare]{}, err
		}
		defer tx.Rollback()
		v, err := pgdb.GetMetadataStats(tx)
		return appcache.Snapshot[*types.MetadataStatsPageDataBare]{Value: v, Dependencies: []appcache.Dependency{"metadata"}}, err
	}
	_, err := users.Get(ctx, "user", loadUser)
	require.NoError(t, err)
	before, err := metadata.Get(ctx, "stats", loadMetadata)
	require.NoError(t, err)
	mariaTx, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer mariaTx.Rollback()
	pgTx, err := pgdb.NewSession(ctx)
	require.NoError(t, err)
	defer pgTx.Rollback()
	require.NoError(t, db.StoreDiscordUser(mariaTx, &types.DiscordUser{ID: 100009601, Username: "rolled back"}))
	_, err = pgdb.GetOrCreatePlatform(pgTx, "Cache Platform", "cache-test", 100009601)
	require.NoError(t, err)
	require.NoError(t, pgTx.Commit())
	// A later database failure must not undo the first database's invalidation.
	require.NoError(t, mariaTx.Rollback())
	require.Error(t, mariaTx.Commit())
	after, err := metadata.Get(ctx, "stats", loadMetadata)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	require.EqualValues(t, 2, metadata.Stats().Loads)
	require.Equal(t, 0, users.Stats().Entries, "ambiguous failed commit conservatively evicts")
}

func TestStatisticsPageCacheSeparatesViewersAndSurvivesWrites(t *testing.T) {
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
	first := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009701, []int64{roleIDCurator}, "first")
	second := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009702, []int64{roleIDModerator}, "second")
	read := func(user *extendedTestUser) *types.StatisticsPageData {
		response := getWithCookie(t, l, app, user.Cookie, "/api/statistics")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var data types.StatisticsPageData
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &data))
		require.Equal(t, user.ID, data.UserID)
		return &data
	}
	initial := read(first)
	read(second)
	count := func() uint64 {
		var label string
		var n uint64
		require.NoError(t, maria.QueryRow("SHOW GLOBAL STATUS LIKE 'Com_select'").Scan(&label, &n))
		return n
	}
	before := count()
	read(first)
	read(second)
	require.Equal(t, before, count(), "warm site statistics and viewer data need no SELECTs")
	require.EqualValues(t, 1, app.Service.ApplicationCacheStats()["site-statistics"].Loads)
	actor := addContextValues(ctx, l, second.ID, "statistics-ttl")
	require.NoError(t, app.Service.SaveSystemUser(actor, &types.DiscordUser{ID: 100009703, Username: "new user"}))
	cached := read(first)
	require.Equal(t, initial.UserCount, cached.UserCount, "statistics remain stable until their TTL expires")
	require.EqualValues(t, 1, app.Service.ApplicationCacheStats()["site-statistics"].Loads)
	response := getWithCookie(t, l, app, second.Cookie, "/web/statistics")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, app.Service.ApplicationCacheStats()["site-statistics"].Loads, "HTML shares the aggregate")
}
