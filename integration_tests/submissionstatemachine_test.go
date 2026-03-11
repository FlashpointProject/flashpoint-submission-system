package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
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

func searchSubmissionByID(t *testing.T, ctx context.Context, app *transport.App, sid int64) *types.ExtendedSubmission {
	submissions, _, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
	require.NoError(t, err)
	require.Len(t, submissions, 1)
	return submissions[0]
}

func renderSubmissionBody(t *testing.T, ctx context.Context, l *logrus.Entry, app *transport.App, user *extendedTestUser, sid int64) string {
	rctx := addContextValues(ctx, l, user.ID, fmt.Sprintf("req_RenderSubmission_u%d_s%d", user.ID, sid))
	viewData, err := app.Service.GetViewSubmissionPageData(rctx, user.ID, sid)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	app.RenderTemplates(rctx, recorder, nil, viewData,
		"templates/submission.gohtml",
		"templates/submission-table.gohtml",
		"templates/comment-form.gohtml",
		"templates/view-submission-nav.gohtml")

	return recorder.Body.String()
}

func overrideBot(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64) *httptest.ResponseRecorder {
	req, err := http.NewRequest("POST", fmt.Sprintf("/api/submission/%d/override", sid), nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func TestSubmissionStateMachine_BotOverride(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{
		CurationWarnings: []string{"Validator requested a manual review", "Validator requested a second manual review"},
	})
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000301), []int64{roleIDCurator}, "submitter/curator")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000302), []int64{roleIDTester}, "tester")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000303), []int64{roleIDModerator}, "moderator")
	secondModerator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000304), []int64{roleIDModerator}, "second-moderator")
	trialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000305), []int64{roleIDTrialCurator}, "trial-curator")

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

	t.Run("BeforeOverride", func(t *testing.T) {
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

		staffBody := renderSubmissionBody(t, ctx, l, app, moderator, sid)
		require.Contains(t, staffBody, "button-override")

		trialBody := renderSubmissionBody(t, ctx, l, app, trialCurator, sid)
		require.NotContains(t, trialBody, "button-override")

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, moderator.ID, sid)
		require.NoError(t, err)
		require.NotEmpty(t, viewData.Comments)
		lastComment := viewData.Comments[len(viewData.Comments)-1]
		require.EqualValues(t, constants.ValidatorID, lastComment.AuthorID)
		require.Equal(t, constants.ActionRequestChanges, lastComment.Action)
		require.NotNil(t, lastComment.Message)
		require.Contains(t, *lastComment.Message, "Validator requested a manual review")
	})

	t.Run("Authorization", func(t *testing.T) {
		rr := overrideBot(t, l, app, trialCurator.Cookie, sid)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		rr = overrideBot(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	})

	t.Run("AfterOverride", func(t *testing.T) {
		submission := searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertDistinctActions(t, submission, []string{constants.ActionUpload, constants.ActionRequestChanges, constants.ActionApprove})
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})

		approveResults, _, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{
			SubmissionIDs: []int64{sid},
			BotActions:    []string{constants.ActionApprove},
		})
		require.NoError(t, err)
		require.Len(t, approveResults, 1)

		requestChangesResults, _, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{
			SubmissionIDs: []int64{sid},
			BotActions:    []string{constants.ActionRequestChanges},
		})
		require.NoError(t, err)
		require.Len(t, requestChangesResults, 0)

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, moderator.ID, sid)
		require.NoError(t, err)
		require.NotEmpty(t, viewData.Comments)
		lastComment := viewData.Comments[len(viewData.Comments)-1]
		require.EqualValues(t, constants.ValidatorID, lastComment.AuthorID)
		require.Equal(t, constants.ActionApprove, lastComment.Action)
		require.NotNil(t, lastComment.Message)
		require.Contains(t, *lastComment.Message, fmt.Sprintf("Approval override by user user_%d (%d)", moderator.ID, moderator.ID))

		rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignVerification, "assign verification after bot override")
		require.NotEqual(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), "not approved")
	})

	t.Run("RepeatedOverride", func(t *testing.T) {
		rr := overrideBot(t, l, app, secondModerator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		submission := searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction)
		assertActionCounters(t, submission, &actionCounters{
			AssignedTestingUserIDs:      []int64{},
			RequestedChangesUserIDs:     []int64{},
			ApprovedUserIDs:             []int64{},
			AssignedVerificationUserIDs: []int64{},
			VerifiedUserIDs:             []int64{},
		})

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, secondModerator.ID, sid)
		require.NoError(t, err)
		require.NotEmpty(t, viewData.Comments)
		lastComment := viewData.Comments[len(viewData.Comments)-1]
		require.Equal(t, constants.ActionApprove, lastComment.Action)
		require.NotNil(t, lastComment.Message)
		require.Contains(t, *lastComment.Message, fmt.Sprintf("Approval override by user user_%d (%d)", secondModerator.ID, secondModerator.ID))
	})
}

// TODO tests after mark added? to cover postgres as well
// TODO add test for metadata edit ^ ?
// TODO tests for search
// TODO test that validator propagates metadata to fpfss correctly, and that they are displayed on the frontend correctly

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

	// Create users
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000101), []int64{roleIDCurator}, "submitter/curator") // Submitter is staff, TODO submitter should also be trial
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000102), []int64{roleIDTester}, "tester/tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000103), []int64{roleIDTester}, "verifier/tester") // Verifiers are testers
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000104), []int64{roleIDModerator}, "adder/moderator")
	trialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000105), []int64{roleIDTrialCurator}, "trial curator")
	trialEditor := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000106), []int64{roleIDTrialEditor}, "trial editor")

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

	// Create users
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000101), []int64{roleIDCurator}, "submitter/curator")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000102), []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000103), []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000104), []int64{roleIDModerator}, "adder")

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
