package integration_tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func updateSubscription(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64, subscribe bool) *httptest.ResponseRecorder {
	url := fmt.Sprintf("/api/submission/%d/subscription-settings?subscribe=%v", sid, subscribe)
	req, err := http.NewRequest("PUT", url, nil)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func isUserSubscribed(t *testing.T, ctx context.Context, app *transport.App, uid, sid int64) bool {
	viewData, err := app.Service.GetViewSubmissionPageData(ctx, uid, sid)
	require.NoError(t, err)
	return viewData.IsUserSubscribed
}

// TestSubscribeUnsubscribe tests manual and automatic subscription behavior.
func TestSubscribeUnsubscribe(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000201), []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000202), []int64{roleIDTester}, "tester")
	_ = createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000203), []int64{roleIDTester}, "verifier")
	trialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000204), []int64{roleIDTrialCurator}, "trial-curator")
	_ = createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000205), []int64{roleIDModerator}, "adder")

	t.Run("AutoSubscribeOnUpload", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		require.True(t, isUserSubscribed(t, ctx, app, submitter.ID, sid),
			"submitter should be auto-subscribed after upload")
	})

	t.Run("ManualSubscribeUnsubscribe", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Tester subscribes
		rr := updateSubscription(t, l, app, tester.Cookie, sid, true)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.True(t, isUserSubscribed(t, ctx, app, tester.ID, sid), "tester should be subscribed")

		// Tester unsubscribes
		rr = updateSubscription(t, l, app, tester.Cookie, sid, false)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.False(t, isUserSubscribed(t, ctx, app, tester.ID, sid), "tester should be unsubscribed")
	})

	t.Run("AutoSubscribeOnAction", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Tester assigns (should auto-subscribe)
		rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.True(t, isUserSubscribed(t, ctx, app, tester.ID, sid), "tester should be auto-subscribed after action")

		// Tester manually unsubscribes
		rr = updateSubscription(t, l, app, tester.Cookie, sid, false)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.False(t, isUserSubscribed(t, ctx, app, tester.ID, sid))

		// Tester approves (should auto-subscribe again)
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.True(t, isUserSubscribed(t, ctx, app, tester.ID, sid), "tester should be re-subscribed after approve")
	})

	t.Run("UnsubscribeAndResubscribe", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		require.True(t, isUserSubscribed(t, ctx, app, submitter.ID, sid))

		// Unsubscribe
		rr := updateSubscription(t, l, app, submitter.Cookie, sid, false)
		require.Equal(t, http.StatusOK, rr.Code)
		require.False(t, isUserSubscribed(t, ctx, app, submitter.ID, sid))

		// Re-subscribe
		rr = updateSubscription(t, l, app, submitter.Cookie, sid, true)
		require.Equal(t, http.StatusOK, rr.Code)
		require.True(t, isUserSubscribed(t, ctx, app, submitter.ID, sid))
	})

	t.Run("IdempotentSubscribe", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Subscribe tester twice
		rr := updateSubscription(t, l, app, tester.Cookie, sid, true)
		require.Equal(t, http.StatusOK, rr.Code)
		rr = updateSubscription(t, l, app, tester.Cookie, sid, true)
		require.Equal(t, http.StatusOK, rr.Code)
		require.True(t, isUserSubscribed(t, ctx, app, tester.ID, sid))

		// Unsubscribe twice
		rr = updateSubscription(t, l, app, tester.Cookie, sid, false)
		require.Equal(t, http.StatusOK, rr.Code)
		rr = updateSubscription(t, l, app, tester.Cookie, sid, false)
		require.Equal(t, http.StatusOK, rr.Code)
		require.False(t, isUserSubscribed(t, ctx, app, tester.ID, sid))
	})

	t.Run("TrialCuratorCanSubscribe", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := updateSubscription(t, l, app, trialCurator.Cookie, sid, true)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.True(t, isUserSubscribed(t, ctx, app, trialCurator.ID, sid), "trial curator should be able to subscribe")
	})
}
