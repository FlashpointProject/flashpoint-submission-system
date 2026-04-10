package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	profileAuditionUploadLabel    = "Get notified about every new audition upload"
	profileAuditionSubscribeLabel = "Automatically subscribe to every new audition upload"
)

type queuedNotification struct {
	ID      int64
	Type    string
	Message string
}

func updateNotificationSettingsRequest(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, actions []string) *httptest.ResponseRecorder {
	params := url.Values{}
	for _, action := range actions {
		params.Add("notification-action", action)
	}

	reqURL := "/api/notification-settings"
	if encoded := params.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	req, err := http.NewRequest("PUT", reqURL, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func notificationQueueCountByType(t *testing.T, maria *sql.DB, notificationType string) int {
	var count int
	err := maria.QueryRow(`
		SELECT COUNT(*)
		FROM submission_notification sn
		JOIN submission_notification_type snt ON snt.id = sn.fk_submission_notification_type_id
		WHERE snt.name = ?`, notificationType).Scan(&count)
	require.NoError(t, err)
	return count
}

func latestQueuedNotificationByType(t *testing.T, maria *sql.DB, notificationType string) queuedNotification {
	var notification queuedNotification
	err := maria.QueryRow(`
		SELECT sn.id, snt.name, sn.message
		FROM submission_notification sn
		JOIN submission_notification_type snt ON snt.id = sn.fk_submission_notification_type_id
		WHERE snt.name = ?
		ORDER BY sn.id DESC
		LIMIT 1`, notificationType).Scan(&notification.ID, &notification.Type, &notification.Message)
	require.NoError(t, err)
	return notification
}

func getStoredNotificationActions(t *testing.T, ctx context.Context, db database.DAL, uid int64) []string {
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	actions, err := db.GetNotificationSettingsByUserID(dbs, uid)
	require.NoError(t, err)
	return actions
}

func requireProfileLabels(t *testing.T, rr *httptest.ResponseRecorder, labels []string, shouldExist bool) {
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	for _, label := range labels {
		if shouldExist {
			require.Contains(t, rr.Body.String(), label)
		} else {
			require.NotContains(t, rr.Body.String(), label)
		}
	}
}

func TestNotificationPreferences_ProfileVisibilityAndAuth(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	staffUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000701), []int64{roleIDTester}, "staff-user")
	trialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000702), []int64{roleIDTrialCurator}, "trial-curator")
	inAudit := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000703), nil, "in-audit")

	labels := []string{profileAuditionUploadLabel, profileAuditionSubscribeLabel}

	requireProfileLabels(t, getWithCookie(t, l, app, staffUser.Cookie, "/web/profile"), labels, true)
	requireProfileLabels(t, getWithCookie(t, l, app, trialCurator.Cookie, "/web/profile"), labels, false)
	requireProfileLabels(t, getWithCookie(t, l, app, inAudit.Cookie, "/web/profile"), labels, false)

	rr := updateNotificationSettingsRequest(t, l, app, staffUser.Cookie, []string{
		constants.ActionAuditionUpload,
		constants.ActionAuditionSubscribe,
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.ElementsMatch(t, []string{
		constants.ActionAuditionUpload,
		constants.ActionAuditionSubscribe,
	}, getStoredNotificationActions(t, ctx, db, staffUser.ID))

	rr = updateNotificationSettingsRequest(t, l, app, trialCurator.Cookie, []string{constants.ActionComment})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.ElementsMatch(t, []string{constants.ActionComment}, getStoredNotificationActions(t, ctx, db, trialCurator.ID))

	rr = updateNotificationSettingsRequest(t, l, app, trialCurator.Cookie, []string{
		constants.ActionComment,
		constants.ActionAuditionUpload,
	})
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	require.ElementsMatch(t, []string{constants.ActionComment}, getStoredNotificationActions(t, ctx, db, trialCurator.ID))

	rr = updateNotificationSettingsRequest(t, l, app, inAudit.Cookie, []string{constants.ActionComment})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.ElementsMatch(t, []string{constants.ActionComment}, getStoredNotificationActions(t, ctx, db, inAudit.ID))

	rr = updateNotificationSettingsRequest(t, l, app, inAudit.Cookie, []string{
		constants.ActionComment,
		constants.ActionAuditionSubscribe,
	})
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	require.ElementsMatch(t, []string{constants.ActionComment}, getStoredNotificationActions(t, ctx, db, inAudit.ID))
}

func TestNotificationPreferences_QueueRows(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000711), []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000712), []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000713), []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000714), []int64{roleIDModerator}, "adder")
	watcher := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000715), []int64{roleIDTester}, "watcher")

	testCases := []struct {
		name                string
		notificationAction  string
		expectedMessage     string
		prepareSubmission   func(sid int64)
		triggerNotification func(sid int64)
	}{
		{
			name:               "Comment",
			notificationAction: constants.ActionComment,
			expectedMessage:    "There is a new comment on the submission.",
			prepareSubmission:  func(sid int64) {},
			triggerNotification: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionComment, "notification comment")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
		},
		{
			name:               "Approve",
			notificationAction: constants.ActionApprove,
			expectedMessage:    "The submission has been approved.",
			prepareSubmission: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign for approve")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
			triggerNotification: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve for notification")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
		},
		{
			name:               "RequestChanges",
			notificationAction: constants.ActionRequestChanges,
			expectedMessage:    "User has requested changes on the submission.",
			prepareSubmission: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign for request changes")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
			triggerNotification: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionRequestChanges, "request changes for notification")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
		},
		{
			name:               "MarkAdded",
			notificationAction: constants.ActionMarkAdded,
			expectedMessage:    "The submission has been marked as added to Flashpoint.",
			prepareSubmission: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign for mark added")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve for mark added")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, "assign verification for mark added")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify for mark added")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
			triggerNotification: func(sid int64) {
				rr := addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added for notification")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
		},
		{
			name:               "UploadFile",
			notificationAction: constants.ActionUpload,
			expectedMessage:    "A new version has been uploaded by",
			prepareSubmission:  func(sid int64) {},
			triggerNotification: func(sid int64) {
				updatedSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, &sid)
				require.Equal(t, sid, updatedSID)
			},
		},
		{
			name:               "Reject",
			notificationAction: constants.ActionReject,
			expectedMessage:    "The submission has been rejected.",
			prepareSubmission:  func(sid int64) {},
			triggerNotification: func(sid int64) {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionReject, "reject for notification")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

			rr := updateSubscription(t, l, app, watcher.Cookie, sid, true)
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

			rr = updateNotificationSettingsRequest(t, l, app, watcher.Cookie, []string{tc.notificationAction})
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

			tc.prepareSubmission(sid)
			baselineCount := notificationQueueCountByType(t, maria, constants.NotificationDefault)

			tc.triggerNotification(sid)

			require.Equal(t, baselineCount+1, notificationQueueCountByType(t, maria, constants.NotificationDefault))

			notification := latestQueuedNotificationByType(t, maria, constants.NotificationDefault)
			require.Equal(t, constants.NotificationDefault, notification.Type)
			require.Contains(t, notification.Message, tc.expectedMessage)
			require.Contains(t, notification.Message, fmt.Sprintf("<@%d>", watcher.ID))
		})
	}
}

func TestNotificationPreferences_AuditionActions(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	staffWatcher := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000721), []int64{roleIDTester}, "staff-watcher")

	t.Run("AuditionUploadNotification", func(t *testing.T) {
		inAuditUploader := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000722), nil, "in-audit-uploader-notify")

		rr := updateNotificationSettingsRequest(t, l, app, staffWatcher.Cookie, []string{constants.ActionAuditionUpload})
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		baselineCount := notificationQueueCountByType(t, maria, constants.NotificationCurationFeed)
		_ = uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", inAuditUploader.Cookie, nil)

		require.Equal(t, baselineCount+1, notificationQueueCountByType(t, maria, constants.NotificationCurationFeed))
		notification := latestQueuedNotificationByType(t, maria, constants.NotificationCurationFeed)
		require.Equal(t, constants.NotificationCurationFeed, notification.Type)
		require.Contains(t, notification.Message, fmt.Sprintf("<@%d>", staffWatcher.ID))
	})

	t.Run("AuditionAutoSubscribe", func(t *testing.T) {
		inAuditUploader := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000723), nil, "in-audit-uploader-subscribe")

		rr := updateNotificationSettingsRequest(t, l, app, staffWatcher.Cookie, []string{constants.ActionAuditionSubscribe})
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", inAuditUploader.Cookie, nil)
		require.True(t, isUserSubscribed(t, ctx, app, staffWatcher.ID, sid), "staff watcher should auto-subscribe to new audition uploads")
	})
}

func TestNotificationPreferences_InvalidActionRejected(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	staffUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000731), []int64{roleIDTester}, "staff-user")

	rr := updateNotificationSettingsRequest(t, l, app, staffUser.Cookie, []string{"not-a-real-action"})
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	actions := getStoredNotificationActions(t, ctx, db, staffUser.ID)
	require.NotContains(t, strings.Join(actions, ","), "not-a-real-action")
}
