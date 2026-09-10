package integration_tests

import (
	"context"
	"fmt"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

		var pairs int
		require.NoError(t, maria.QueryRow(testSQL("SELECT COUNT(*) FROM submission_notification_subscription WHERE fk_user_id=? AND fk_submission_id=?"), tester.ID, sid).Scan(&pairs))
		if postgresSubmissionTests() {
			require.Equal(t, 1, pairs, "PostgreSQL repeated subscribe must not duplicate the pair")
		} else {
			require.Equal(t, 2, pairs, "MariaDB baseline reproduces repeated-subscribe duplicates")
		}
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

// Simultaneous explicit subscriptions must retain one PostgreSQL pair without
// changing recipient selection. MariaDB's duplicate source behavior is retained
// as a before-migration witness, rather than silently asserted as desired.
func TestConcurrentSubscriptionPair(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "subscriber")
	f.Submission(t, 101, "staff")
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.StoreNotificationSettings(s, 11, []string{constants.ActionApprove}))
	})
	ctx, cancel := context.WithTimeout(f.Ctx, 15*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			s, err := f.DB.NewSession(ctx)
			ready <- struct{}{}
			if err != nil {
				results <- err
				return
			}
			defer s.Rollback()
			select {
			case <-release:
			case <-ctx.Done():
				results <- ctx.Err()
				return
			}
			if err = f.DB.SubscribeUserToSubmission(s, 11, 101); err == nil {
				err = s.Commit()
			}
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	for range 2 {
		require.NoError(t, <-results)
	}
	var pairs int
	require.NoError(t, f.Maria.QueryRow(testSQL("SELECT COUNT(*) FROM submission_notification_subscription WHERE fk_user_id=? AND fk_submission_id=?"), 11, 101).Scan(&pairs))
	if postgresSubmissionTests() {
		require.Equal(t, 1, pairs)
	} else {
		require.Equal(t, 2, pairs)
	}
	f.InTx(t, func(s database.DBSession) {
		recipients, err := f.DB.GetUsersForNotification(s, 99, 101, constants.ActionApprove)
		require.NoError(t, err)
		require.Equal(t, []int64{11}, recipients)
		require.NoError(t, f.DB.UnsubscribeUserFromSubmission(s, 11, 101))
	})
	require.NoError(t, f.Maria.QueryRow("SELECT COUNT(*) FROM submission_notification_subscription").Scan(&pairs))
	require.Zero(t, pairs)
}
