package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type extendedTestUser struct {
	ID        int64
	Name      string
	AuthToken *service.AuthToken
	Cookie    *http.Cookie
	Roles     []int64
}

func createExtendedTestUser(t *testing.T, ctx context.Context, l *logrus.Entry, app *transport.App, db database.DAL, pgdb database.PGDAL, uid int64, roles []int64, name string) *extendedTestUser {
	authToken := createTestUser(t, ctx, l, app, db, pgdb, uid, roles)
	cookie := createTestCookie(t, l, authToken)

	return &extendedTestUser{
		ID:        uid,
		Name:      name,
		AuthToken: authToken,
		Cookie:    cookie,
		Roles:     roles,
	}
}

func assertDistinctActions(t *testing.T, submission *types.ExtendedSubmission, actions []string) {
	require.Len(t, submission.DistinctActions, len(actions))
	for _, action := range actions {
		require.Contains(t, submission.DistinctActions, action)
	}
}

type actionCounters struct {
	AssignedTestingUserIDs      []int64
	RequestedChangesUserIDs     []int64
	ApprovedUserIDs             []int64
	AssignedVerificationUserIDs []int64
	VerifiedUserIDs             []int64
}

func assertActionCounters(t *testing.T, submission *types.ExtendedSubmission, counters *actionCounters) {
	require.Len(t, submission.AssignedTestingUserIDs, len(counters.AssignedTestingUserIDs), "AssignedTestingUserIDs")
	require.Len(t, submission.RequestedChangesUserIDs, len(counters.RequestedChangesUserIDs), "RequestedChangesUserIDs")
	require.Len(t, submission.ApprovedUserIDs, len(counters.ApprovedUserIDs), "ApprovedUserIDs")
	require.Len(t, submission.AssignedVerificationUserIDs, len(counters.AssignedVerificationUserIDs), "AssignedVerificationUserIDs")
	require.Len(t, submission.VerifiedUserIDs, len(counters.VerifiedUserIDs), "VerifiedUserIDs")

	for _, userID := range counters.AssignedTestingUserIDs {
		require.Contains(t, submission.AssignedTestingUserIDs, userID, "AssignedTestingUserIDs")
	}
	for _, userID := range counters.RequestedChangesUserIDs {
		require.Contains(t, submission.RequestedChangesUserIDs, userID, "RequestedChangesUserIDs")
	}
	for _, userID := range counters.ApprovedUserIDs {
		require.Contains(t, submission.ApprovedUserIDs, userID, "AssignedTestingUserIDs")
	}
	for _, userID := range counters.AssignedVerificationUserIDs {
		require.Contains(t, submission.AssignedVerificationUserIDs, userID, "AssignedVerificationUserIDs")
	}
	for _, userID := range counters.VerifiedUserIDs {
		require.Contains(t, submission.VerifiedUserIDs, userID, "VerifiedUserIDs")
	}
}

// TODO add test for freeze/unfreeze
// TODO add test for bot override
// TODO add test with fix uploads
// TODO add test with real validator and metadata edit
// TODO add test with access to submission version downloads
// TODO submission delete permissions
// TODO subscribe/unsubscribe button
// TODO tests for search

// TestSubmissionStateMachine_MainFlow upload,bot approve,assign,approve,(unassign),assign,verify,(unassign),mark added
func TestSubmissionStateMachine_MainFlow(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	// Role IDs
	const (
		roleCurator      = 442665038642413569
		roleTester       = 442988314480476170
		roleModerator    = 442462642599231499
		roleTrialCurator = 569328799318016018
		roleTrialEditor  = 1101806666380496926
	)

	// Create users
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000101), []int64{roleCurator}, "submitter/curator") // Submitter is staff, TODO submitter should also be trial
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000102), []int64{roleTester}, "tester/tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000103), []int64{roleTester}, "verifier/tester") // Verifiers are testers
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000104), []int64{roleModerator}, "adder/moderator")
	trialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000105), []int64{roleTrialCurator}, "trial curator")
	trialEditor := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000106), []int64{roleTrialEditor}, "trial editor")

	// Helper to check button visibility
	checkButtons := func(t *testing.T, user *extendedTestUser, sid int64, expectedButtons []string, unexpectedButtons []string, msg string) {
		rctx := addContextValues(ctx, l, user.ID, fmt.Sprintf("req_ReceiveComments_u%d_s%d", user.ID, sid))
		viewData, err := app.Service.GetViewSubmissionPageData(rctx, user.ID, sid)
		require.NoError(t, err)

		recorder := httptest.NewRecorder()
		app.RenderTemplates(rctx, recorder, nil, viewData,
			"templates/submission.gohtml",
			"templates/submission-table.gohtml",
			"templates/comment-form.gohtml",
			"templates/view-submission-nav.gohtml")

		body := recorder.Body.String()

		data, err := json.Marshal(viewData)
		require.NoError(t, err)

		for _, btn := range expectedButtons {
			if !strings.Contains(body, btn) {
				require.Fail(t, fmt.Sprintf("%s: User %s (%d) expected button %s but not found: %s\n----\n data: %s", msg, user.Name, user.ID, btn, body, data))
			}
		}
		for _, btn := range unexpectedButtons {
			if strings.Contains(body, btn) {
				require.Fail(t, fmt.Sprintf("%s: User %s (%d) found unexpected button %s: %s\n----\n data: %s", msg, user.Name, user.ID, btn, body, data))
			}
		}
	}

	// Buttons classes/identifiers
	// We don't care about auxiliary actions here like freeze
	btnAssignTesting := "button-assign-testing"
	btnUnassignTesting := "button-unassign-testing"
	btnApprove := "button-approve"
	btnAssignVerification := "button-assign-verification"
	btnUnassignVerification := "button-unassign-verification"
	btnVerify := "button-verify"
	btnMarkAdded := "button-mark-added"
	btnRequestChanges := "button-request-changes"
	btnReject := "button-reject"
	btnUpload := "button-upload-file"

	type userActions struct {
		user       *extendedTestUser
		allowed    []string
		disallowed []string
	}

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

	// 1. Upload
	t.Run("Check after uploaded", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{btnUpload, btnReject},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       tester,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       verifier,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       adder,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: uploaded"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		}
		assertActionCounters(t, submission, expectedCounters)
	})

	// 2. Assigned for testing
	rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, fmt.Sprintf("assigned for testing by the very lovely user %d", tester.ID))
	require.Equal(t, http.StatusOK, rr.Code, "comment failed: "+rr.Body.String())

	t.Run("Check after assigned for testing", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{btnUpload, btnReject},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       tester,
				allowed:    []string{btnUpload, btnReject, btnUnassignTesting, btnApprove, btnRequestChanges},
				disallowed: []string{btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       verifier,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       adder,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: assigned for testing"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove, constants.ActionAssignTesting})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{tester.ID},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		}
		assertActionCounters(t, submission, expectedCounters)
	})

	// 3. Approved, unassigned
	rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, fmt.Sprintf("approved by the very lovely user %d", tester.ID))
	require.Equal(t, http.StatusOK, rr.Code, "comment failed: "+rr.Body.String())

	t.Run("Check after approved unassigned", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{btnUpload, btnReject},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       tester,
				allowed:    []string{btnUpload, btnReject, btnRequestChanges},
				disallowed: []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       verifier,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       adder,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: approved, unassigned"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove, constants.ActionAssignTesting, constants.ActionUnassignTesting})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		}
		assertActionCounters(t, submission, expectedCounters)
	})

	// 4. Assigned for verification
	rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, fmt.Sprintf("assigned for verification by the very lovely user %d", tester.ID))
	require.Equal(t, http.StatusOK, rr.Code, "comment failed: "+rr.Body.String())

	t.Run("Check after assigned for verification", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{btnUpload, btnReject},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       tester,
				allowed:    []string{btnUpload, btnReject, btnRequestChanges},
				disallowed: []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       verifier,
				allowed:    []string{btnUpload, btnReject, btnUnassignVerification, btnRequestChanges, btnVerify},
				disallowed: []string{btnAssignTesting, btnAssignVerification, btnUnassignTesting, btnApprove, btnMarkAdded},
			},
			{
				user:       adder,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: approved, unassigned"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove, constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionAssignVerification})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{verifier.ID},
			VerifiedUserIDs:             []int64{},
		}
		assertActionCounters(t, submission, expectedCounters)
	})

	// 5. Verified
	rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, fmt.Sprintf("verified by the very lovely user %d", tester.ID))
	require.Equal(t, http.StatusOK, rr.Code, "comment failed: "+rr.Body.String())

	t.Run("Check after verified unassigned", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{btnUpload, btnReject},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       tester,
				allowed:    []string{btnUpload, btnReject, btnRequestChanges},
				disallowed: []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
			},
			{
				user:       verifier,
				allowed:    []string{btnUpload, btnReject, btnRequestChanges},
				disallowed: []string{btnAssignTesting, btnAssignVerification, btnVerify, btnUnassignTesting, btnUnassignVerification, btnApprove, btnMarkAdded},
			},
			{
				user:       adder,
				allowed:    []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification, btnMarkAdded},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: verified, unassigned"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove, constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionAssignVerification, constants.ActionVerify, constants.ActionUnassignVerification})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{verifier.ID},
		}
		assertActionCounters(t, submission, expectedCounters)
	})

	// 6. Marked added
	rr = addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, fmt.Sprintf("marked as added by the very lovely user %d", tester.ID))
	require.Equal(t, http.StatusOK, rr.Code, "comment failed: "+rr.Body.String())

	t.Run("Check after marked as added", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:       submitter,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded, btnReject, btnUpload},
			},
			{
				user:       tester,
				allowed:    []string{},
				disallowed: []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded, btnUpload, btnReject, btnRequestChanges},
			},
			{
				user:       verifier,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnAssignVerification, btnVerify, btnUnassignTesting, btnUnassignVerification, btnApprove, btnMarkAdded, btnUpload, btnReject, btnRequestChanges},
			},
			{
				user:       adder,
				allowed:    []string{},
				disallowed: []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnUpload, btnReject, btnAssignTesting, btnAssignVerification, btnMarkAdded},
			},
			{
				user:       trialCurator,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
			{
				user:       trialEditor,
				allowed:    []string{},
				disallowed: []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: marked as added"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that disallowed actions are actually disallowed on backend
		for _, user := range usersActions {
			for _, action := range user.disallowed {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.Equal(t, http.StatusUnauthorized, rr.Code, "request should have failed but did not: "+rr.Body.String())
			}
		}

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)

		submission := viewData.Submissions[0]

		// Boolean actions
		require.Equal(t, submission.BotAction, constants.ActionApprove)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionApprove, constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionAssignVerification, constants.ActionVerify, constants.ActionUnassignVerification, constants.ActionMarkAdded})

		expectedCounters := &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{tester.ID},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{verifier.ID},
		}
		assertActionCounters(t, submission, expectedCounters)
	})
}
