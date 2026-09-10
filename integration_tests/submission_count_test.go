package integration_tests

import (
	"context"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestSubmissionCountFilters(t *testing.T) {
	f, _ := seedSearchFixture(t)
	alice := int64(1001)
	for _, tc := range []struct {
		name   string
		uid    int64
		filter *types.SubmissionsFilter
		want   int64
	}{
		{"default includes legacy", 0, nil, 5},
		{"live", 0, &types.SubmissionsFilter{ExcludeLegacy: true}, 3},
		{"platform includes legacy", 0, &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("Flash")}, 2},
		{"title", 0, &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Aurora")}, 2},
		{"uploader ID", 0, &types.SubmissionsFilter{SubmitterID: &alice}, 2},
		{"uploader name", 0, &types.SubmissionsFilter{SubmitterUsernamePartial: utils.StrPtr("Bob")}, 1},
		{"bot happy", 0, &types.SubmissionsFilter{BotActions: []string{"approve"}}, 2},
		{"bot unhappy", 0, &types.SubmissionsFilter{BotActions: []string{"request-changes"}}, 1},
		{"approved", 0, &types.SubmissionsFilter{ApprovalsStatus: utils.StrPtr("approved")}, 1},
		{"verified", 0, &types.SubmissionsFilter{VerificationStatus: utils.StrPtr("verified")}, 1},
		{"requested changes", 0, &types.SubmissionsFilter{RequestedChangedStatus: utils.StrPtr("ongoing")}, 1},
		{"rejected", 0, &types.SubmissionsFilter{DistinctActions: []string{"reject"}}, 1},
		{"imported", 0, &types.SubmissionsFilter{DistinctActions: []string{"mark-added"}}, 0},
		{"reviewer subscribed", 1004, &types.SubmissionsFilter{SubscribedMe: utils.StrPtr("yes")}, 1},
		{"verifier subscribed", 1005, &types.SubmissionsFilter{SubscribedMe: utils.StrPtr("yes")}, 2},
		{"reviewer approved", 1004, &types.SubmissionsFilter{ApprovalsStatusMe: utils.StrPtr("yes")}, 1},
		{"other reviewer approved", 1005, &types.SubmissionsFilter{ApprovalsStatusMe: utils.StrPtr("yes")}, 0},
		{"combined", 0, &types.SubmissionsFilter{SubmitterID: &alice, ApprovalsStatus: utils.StrPtr("approved")}, 1},
		{"pagination does not limit count", 0, &types.SubmissionsFilter{ResultsPerPage: utils.Int64Ptr(1), Page: utils.Int64Ptr(10), OrderBy: utils.StrPtr("size"), AscDesc: utils.StrPtr("asc")}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(f.Ctx, utils.CtxKeys.UserID, tc.uid)
			session, err := f.DB.NewSession(ctx)
			require.NoError(t, err)
			defer session.Rollback()
			count, err := database.CountSubmissions(f.DB, session, tc.filter)
			require.NoError(t, err)
			require.Equal(t, tc.want, count)
		})
	}
	session, err := f.DB.NewSession(f.Ctx)
	require.NoError(t, err)
	defer session.Rollback()
	_, err = database.CountSubmissions(f.DB, session, &types.SubmissionsFilter{ApprovalsStatus: utils.StrPtr("invalid")})
	require.Error(t, err)
}

func TestSubmissionCountTransactionVisibility(t *testing.T) {
	f, _ := seedSearchFixture(t)
	session, err := f.DB.NewSession(f.Ctx)
	require.NoError(t, err)
	defer session.Rollback()
	_, err = session.Tx().ExecContext(f.Ctx, testSQL(`UPDATE submission SET deleted_at=? WHERE id=101`), fixtureEpoch)
	require.NoError(t, err)
	count, err := database.CountSubmissions(f.DB, session, nil)
	require.NoError(t, err)
	if database.IsPostgresSubmissionSession(session) {
		// The dedicated PostgreSQL counter uses the caller's transaction, just as
		// PostgreSQL search does. MariaDB's unchanged fallback opens its own count
		// session, so it intentionally cannot see this uncommitted deletion.
		require.EqualValues(t, 4, count)
	} else {
		require.EqualValues(t, 5, count)
	}
	require.NoError(t, session.Rollback())
	after, err := f.DB.NewSession(f.Ctx)
	require.NoError(t, err)
	defer after.Rollback()
	count, err = database.CountSubmissions(f.DB, after, nil)
	require.NoError(t, err)
	require.EqualValues(t, 5, count)
}

func TestSubmissionCountUserActions(t *testing.T) {
	f, _ := seedSearchFixture(t)
	f.Comment(t, 50, 102, 1004, "approve", fixtureEpoch, nil)
	f.Comment(t, 51, 102, 1004, "approve", fixtureEpoch, nil)
	_, err := f.Maria.Exec(testSQL(`UPDATE comment SET deleted_at=? WHERE id=51`), fixtureEpoch)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		uid    int64
		action string
		want   int64
	}{
		{"known user and live actions", 1004, "approve", 2},
		{"case accent and padding equivalence", 1004, "ÁPPROVÉ  ", 2},
		{"other action", 1004, "request-changes", 1},
		{"other author", 1005, "approve", 0},
		{"unknown user", 999999, "approve", 0},
		{"unknown action", 1004, "missing-action", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := f.DB.NewSession(f.Ctx)
			require.NoError(t, err)
			defer session.Rollback()
			count, err := database.CountCommentsByUserIDAndAction(f.DB, session, tc.uid, tc.action)
			require.NoError(t, err)
			require.Equal(t, tc.want, count)
		})
	}
}
