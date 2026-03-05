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
		require.Contains(t, submission.ApprovedUserIDs, userID, "ApprovedUserIDs")
	}
	for _, userID := range counters.AssignedVerificationUserIDs {
		require.Contains(t, submission.AssignedVerificationUserIDs, userID, "AssignedVerificationUserIDs")
	}
	for _, userID := range counters.VerifiedUserIDs {
		require.Contains(t, submission.VerifiedUserIDs, userID, "VerifiedUserIDs")
	}
}

// TODO add test for bot override
// TODO add test with fix uploads
// TODO add test with real validator and metadata edit
// TODO add test with access to submission version downloads
// TODO tests for search

// advanceToState creates a fresh submission and advances it to the given state.
// Returns the submission ID.
func advanceToState(t *testing.T, l *logrus.Entry, app *transport.App,
	state string, submitterCookie, testerCookie, verifierCookie *http.Cookie) int64 {

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitterCookie, nil)

	if state == "uploaded" {
		return sid
	}

	// Tester assigns for testing
	rr := addComment(t, l, app, testerCookie, sid, constants.ActionAssignTesting, "assign testing")
	require.Equal(t, http.StatusOK, rr.Code, "advance: assign-testing failed: "+rr.Body.String())

	if state == "assigned-testing" {
		return sid
	}

	// Tester approves (auto-unassigns)
	rr = addComment(t, l, app, testerCookie, sid, constants.ActionApprove, "approve")
	require.Equal(t, http.StatusOK, rr.Code, "advance: approve failed: "+rr.Body.String())

	if state == "approved" {
		return sid
	}

	// Verifier assigns for verification
	rr = addComment(t, l, app, verifierCookie, sid, constants.ActionAssignVerification, "assign verification")
	require.Equal(t, http.StatusOK, rr.Code, "advance: assign-verification failed: "+rr.Body.String())

	if state == "assigned-verification" {
		return sid
	}

	// Verifier verifies (auto-unassigns)
	rr = addComment(t, l, app, verifierCookie, sid, constants.ActionVerify, "verify")
	require.Equal(t, http.StatusOK, rr.Code, "advance: verify failed: "+rr.Body.String())

	if state == "verified" {
		return sid
	}

	t.Fatalf("unknown state: %s", state)
	return 0
}

// TestSubmissionStateMachine_MainFlow performs upload,bot approve,assign,approve,(unassign),assign,verify,(unassign),mark added.
// Verifies that the users involved see only the buttons they should in the UI.
// Verifies that the users involved cannot perform actions that they shouldn't be able to.
// Allowed actions are tested separately in TestSubmissionStateMachine_AllowedActions.
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

	// All comment actions that can be tested against the backend.
	// Upload is excluded because it uses a different endpoint (submission-receiver-resumable).
	allCommentActions := []string{
		constants.ActionAssignTesting, constants.ActionUnassignTesting,
		constants.ActionApprove, constants.ActionRequestChanges,
		constants.ActionAssignVerification, constants.ActionUnassignVerification,
		constants.ActionVerify, constants.ActionMarkAdded, constants.ActionReject,
	}

	// Trial users cannot perform any comment actions on submissions they don't own
	trialDisallowedActions := allCommentActions

	type userActions struct {
		user              *extendedTestUser
		allowed           []string // button CSS classes for template check
		disallowed        []string // button CSS classes for template check
		disallowedActions []string // actual action constants for backend authorization check
	}

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

	// 1. Upload
	t.Run("Check after uploaded", func(t *testing.T) {
		// Check Initial State (Uploaded)
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{btnUpload, btnReject},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              tester,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              verifier,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              adder,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: uploaded"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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
		// Check Assigned for Testing State
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{btnUpload, btnReject},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              tester,
				allowed:           []string{btnUpload, btnReject, btnUnassignTesting, btnApprove, btnRequestChanges},
				disallowed:        []string{btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              verifier,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              adder,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: assigned for testing"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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
		// Check Approved State
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{btnUpload, btnReject},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              tester,
				allowed:           []string{btnUpload, btnReject, btnRequestChanges},
				disallowed:        []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionApprove, constants.ActionUnassignTesting, constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              verifier,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              adder,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: approved, unassigned"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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
		// Check Assigned for Verification State
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{btnUpload, btnReject},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              tester,
				allowed:           []string{btnUpload, btnReject, btnRequestChanges},
				disallowed:        []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionApprove, constants.ActionUnassignTesting, constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              verifier,
				allowed:           []string{btnUpload, btnReject, btnUnassignVerification, btnRequestChanges, btnVerify},
				disallowed:        []string{btnAssignTesting, btnAssignVerification, btnUnassignTesting, btnApprove, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionMarkAdded},
			},
			{
				user:              adder,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: assigned for verification"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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
		// Check Verified State
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{btnUpload, btnReject},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              tester,
				allowed:           []string{btnUpload, btnReject, btnRequestChanges},
				disallowed:        []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded},
				disallowedActions: []string{constants.ActionApprove, constants.ActionUnassignTesting, constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionVerify, constants.ActionMarkAdded},
			},
			{
				user:              verifier,
				allowed:           []string{btnUpload, btnReject, btnRequestChanges},
				disallowed:        []string{btnAssignTesting, btnAssignVerification, btnVerify, btnUnassignTesting, btnUnassignVerification, btnApprove, btnMarkAdded},
				disallowedActions: []string{constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionVerify, constants.ActionUnassignTesting, constants.ActionUnassignVerification, constants.ActionApprove, constants.ActionMarkAdded},
			},
			{
				user:              adder,
				allowed:           []string{btnUpload, btnReject, btnAssignTesting, btnAssignVerification, btnMarkAdded},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify},
				disallowedActions: []string{constants.ActionUnassignTesting, constants.ActionApprove, constants.ActionRequestChanges, constants.ActionUnassignVerification, constants.ActionVerify},
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: verified, unassigned"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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
		// Check Marked Added State
		usersActions := []userActions{
			{
				user:              submitter,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded, btnReject, btnUpload},
				disallowedActions: allCommentActions,
			},
			{
				user:              tester,
				allowed:           []string{},
				disallowed:        []string{btnApprove, btnUnassignTesting, btnAssignTesting, btnAssignVerification, btnUnassignVerification, btnVerify, btnMarkAdded, btnUpload, btnReject, btnRequestChanges},
				disallowedActions: allCommentActions,
			},
			{
				user:              verifier,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnAssignVerification, btnVerify, btnUnassignTesting, btnUnassignVerification, btnApprove, btnMarkAdded, btnUpload, btnReject, btnRequestChanges},
				disallowedActions: allCommentActions,
			},
			{
				user:              adder,
				allowed:           []string{},
				disallowed:        []string{btnUnassignTesting, btnApprove, btnRequestChanges, btnUnassignVerification, btnVerify, btnUpload, btnReject, btnAssignTesting, btnAssignVerification, btnMarkAdded},
				disallowedActions: allCommentActions,
			},
			{
				user:              trialCurator,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
			{
				user:              trialEditor,
				allowed:           []string{},
				disallowed:        []string{btnAssignTesting, btnUnassignTesting, btnApprove, btnRequestChanges, btnAssignVerification, btnUnassignVerification, btnVerify, btnReject, btnMarkAdded, btnUpload},
				disallowedActions: trialDisallowedActions,
			},
		}

		// Check that only allowed actions have buttons displayed on frontend
		msg := "State: marked as added"
		for _, user := range usersActions {
			checkButtons(t, user.user, sid, user.allowed, user.disallowed, msg)
		}

		// Check that trial users (who don't own this submission) cannot perform any actions on the backend.
		// Staff users are not tested here because some hidden buttons correspond to actions the backend
		// still accepts, and sending those actions would mutate the submission state and corrupt subsequent tests.
		// TODO: add isolated backend authorization tests for staff users.
		for _, user := range usersActions {
			if user.user != trialCurator && user.user != trialEditor {
				continue
			}
			for _, action := range user.disallowedActions {
				rr := addComment(t, l, app, user.user.Cookie, sid, action, fmt.Sprintf("nasty comment that should not be here - user %d, submission %d, action %s", user.user.ID, sid, action))
				require.NotEqual(t, http.StatusOK, rr.Code, fmt.Sprintf("action %s should have been rejected for user %s (%d) but succeeded", action, user.user.Name, user.user.ID))
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

// TestSubmissionStateMachine_AllowedActions verifies that staff users can perform
// all actions that they should be able to at each submission state.
// Creates a fresh submission per (state, user, action) to avoid state corruption.
func TestSubmissionStateMachine_AllowedActions(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	_ = ctx

	// Role IDs
	const (
		roleCurator   = 442665038642413569
		roleTester    = 442988314480476170
		roleModerator = 442462642599231499
	)

	// Create users
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000101), []int64{roleCurator}, "submitter/curator")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000102), []int64{roleTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000103), []int64{roleTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000104), []int64{roleModerator}, "adder")

	type allowedAction struct {
		user   *extendedTestUser
		action string
	}

	type stateTest struct {
		state   string
		allowed []allowedAction
	}

	tests := []stateTest{
		{
			state: "uploaded",
			allowed: []allowedAction{
				{tester, constants.ActionAssignTesting},
				{tester, constants.ActionReject},
				{verifier, constants.ActionAssignTesting},
				{verifier, constants.ActionReject},
				{adder, constants.ActionAssignTesting},
				{adder, constants.ActionReject},
				{submitter, constants.ActionReject},
			},
		},
		{
			state: "assigned-testing",
			allowed: []allowedAction{
				{tester, constants.ActionUnassignTesting},
				{tester, constants.ActionApprove},
				{tester, constants.ActionRequestChanges},
				{tester, constants.ActionReject},
				{verifier, constants.ActionAssignTesting},
				{verifier, constants.ActionReject},
				{adder, constants.ActionAssignTesting},
				{adder, constants.ActionReject},
				{submitter, constants.ActionReject},
			},
		},
		{
			state: "approved",
			allowed: []allowedAction{
				{tester, constants.ActionRequestChanges},
				{tester, constants.ActionReject},
				{verifier, constants.ActionAssignTesting},
				{verifier, constants.ActionAssignVerification},
				{verifier, constants.ActionReject},
				{adder, constants.ActionAssignTesting},
				{adder, constants.ActionAssignVerification},
				{adder, constants.ActionReject},
				{submitter, constants.ActionReject},
			},
		},
		{
			state: "assigned-verification",
			allowed: []allowedAction{
				{tester, constants.ActionRequestChanges},
				{tester, constants.ActionReject},
				{verifier, constants.ActionUnassignVerification},
				{verifier, constants.ActionRequestChanges},
				{verifier, constants.ActionVerify},
				{verifier, constants.ActionReject},
				{adder, constants.ActionAssignTesting},
				{adder, constants.ActionReject},
				{submitter, constants.ActionReject},
			},
		},
		{
			state: "verified",
			allowed: []allowedAction{
				{tester, constants.ActionRequestChanges},
				{tester, constants.ActionReject},
				{verifier, constants.ActionRequestChanges},
				{verifier, constants.ActionReject},
				{adder, constants.ActionAssignTesting},
				{adder, constants.ActionAssignVerification},
				{adder, constants.ActionMarkAdded},
				{adder, constants.ActionReject},
				{submitter, constants.ActionReject},
			},
		},
	}

	for _, st := range tests {
		t.Run("State_"+st.state, func(t *testing.T) {
			for _, aa := range st.allowed {
				t.Run(fmt.Sprintf("%s_%s", aa.user.Name, aa.action), func(t *testing.T) {
					sid := advanceToState(t, l, app, st.state, submitter.Cookie, tester.Cookie, verifier.Cookie)
					rr := addComment(t, l, app, aa.user.Cookie, sid, aa.action,
						fmt.Sprintf("allowed action test: user=%s action=%s state=%s", aa.user.Name, aa.action, st.state))
					require.Equal(t, http.StatusOK, rr.Code,
						fmt.Sprintf("action %s should succeed for %s in state %s but got %d: %s",
							aa.action, aa.user.Name, st.state, rr.Code, rr.Body.String()))
				})
			}
		})
	}
}
