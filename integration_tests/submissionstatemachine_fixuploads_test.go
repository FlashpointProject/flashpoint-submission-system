package integration_tests

import (
	"context"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func assertSubmissionFileVersions(t *testing.T, ctx context.Context, db database.DAL, sid int64, expected int) {
	t.Helper()

	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	files, err := db.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
	require.NoError(t, err)
	require.Len(t, files, expected)
}

func uploadFixedVersion(t *testing.T, l *logrus.Entry, app *transport.App, submitter *extendedTestUser, sid int64) {
	t.Helper()

	updatedSubmissionID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, &sid)
	require.Equal(t, sid, updatedSubmissionID)
}

func TestSubmissionStateMachine_FixUploads(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{
		CurationWarningsSeq: [][]string{
			{"Validator requested a manual review", "Validator requested a second manual review"},
			{},
		},
	})
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const (
		roleCurator   = 442665038642413569
		roleTester    = 442988314480476170
		roleModerator = 442462642599231499
	)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000401), []int64{roleCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000402), []int64{roleTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000403), []int64{roleTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000404), []int64{roleModerator}, "adder")

	t.Run("BotRejectThenUploaderFixes", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		submission := searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionRequestChanges, submission.BotAction)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionRequestChanges})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 1, submission.FileCount)
		assertSubmissionFileVersions(t, ctx, db, sid, 1)

		uploadFixedVersion(t, l, app, submitter, sid)

		submission = searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionRequestChanges, constants.ActionApprove})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 2, submission.FileCount)
		assertSubmissionFileVersions(t, ctx, db, sid, 2)

		rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign after fix upload")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve fixed upload")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, "assign verification after fix upload")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify fixed upload")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added after fix upload")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission = searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionRequestChanges,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionUnassignTesting,
			constants.ActionAssignVerification,
			constants.ActionVerify,
			constants.ActionUnassignVerification,
			constants.ActionMarkAdded,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{verifier.ID},
		})
		require.EqualValues(t, 2, submission.FileCount)
	})

	t.Run("ApproverRequestsChangesThenUploaderFixes", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign testing before request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionRequestChanges, "needs fixes from testing")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission := searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionRequestChanges,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{tester.ID},
			RequestedChangesUserIDs:     []int64{tester.ID},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 1, submission.FileCount)

		uploadFixedVersion(t, l, app, submitter, sid)

		submission = searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionSystem,
			constants.ActionRequestChanges,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{tester.ID},
			RequestedChangesUserIDs:     []int64{tester.ID},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 2, submission.FileCount)
		assertSubmissionFileVersions(t, ctx, db, sid, 2)

		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve fixed upload after request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission = searchSubmissionByID(t, ctx, app, sid)
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})

		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, "assign verification after retest")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify after retest")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added after retest")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission = searchSubmissionByID(t, ctx, app, sid)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionSystem,
			constants.ActionRequestChanges,
			constants.ActionUnassignTesting,
			constants.ActionAssignVerification,
			constants.ActionVerify,
			constants.ActionUnassignVerification,
			constants.ActionMarkAdded,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{verifier.ID},
		})
		require.EqualValues(t, 2, submission.FileCount)
	})

	t.Run("VerifierRequestsChangesThenUploaderFixes", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign testing before verifier request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve before verifier request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, "assign verification before request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionRequestChanges, "verification found issues")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission := searchSubmissionByID(t, ctx, app, sid)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionUnassignTesting,
			constants.ActionAssignVerification,
			constants.ActionRequestChanges,
			constants.ActionSystem,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{verifier.ID},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{verifier.ID},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 1, submission.FileCount)

		uploadFixedVersion(t, l, app, submitter, sid)

		submission = searchSubmissionByID(t, ctx, app, sid)
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{verifier.ID},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{verifier.ID},
			VerifiedUserIDs:             []int64{},
		})
		require.EqualValues(t, 2, submission.FileCount)
		assertSubmissionFileVersions(t, ctx, db, sid, 2)

		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify before retest should fail")
		require.NotEqual(t, 200, rr.Code)
		require.Contains(t, rr.Body.String(), "not approved")

		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign testing after verifier request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve fixed upload after verifier request changes")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission = searchSubmissionByID(t, ctx, app, sid)
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{verifier.ID},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{verifier.ID},
			VerifiedUserIDs:             []int64{},
		})

		rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify fixed upload after retest")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		rr = addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added after verifier-requested fixes")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		submission = searchSubmissionByID(t, ctx, app, sid)
		assertDistinctActions(t, submission, []string{
			constants.ActionUpload,
			constants.ActionApprove,
			constants.ActionAssignTesting,
			constants.ActionUnassignTesting,
			constants.ActionAssignVerification,
			constants.ActionRequestChanges,
			constants.ActionSystem,
			constants.ActionVerify,
			constants.ActionUnassignVerification,
			constants.ActionMarkAdded,
		})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{verifier.ID},
		})
		require.EqualValues(t, 2, submission.FileCount)
	})
}
