package integration_tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// Unlike uploadTestSubmission, admission and asynchronous completion are separate:
// two requests must pass middleware before either is permitted to commit.
func quotaUpload(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid *int64) *httptest.ResponseRecorder {
	t.Helper()
	content, err := os.ReadFile("./test_files/Warpstar4K.7z")
	require.NoError(t, err)
	n := uploadCounter.Add(1)
	content = append(content, []byte(fmt.Sprintf("\nquota-%d", n))...)
	return uploadSubmissionContent(t, l, app, cookie, sid, content)
}

// Keep caller-provided bytes intact while using a fresh resumable identifier.
// Duplicate-file tests require identical payloads to reach database uniqueness.
func uploadSubmissionContent(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid *int64, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	n := uploadCounter.Add(1)
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "quota.7z")
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	path := "/api/submission-receiver-resumable"
	if sid != nil {
		path += fmt.Sprintf("/%d", *sid)
	}
	req := httptest.NewRequest(http.MethodPost, path, body)
	q := req.URL.Query()
	for key, value := range map[string]string{"resumableChunkNumber": "1", "resumableChunkSize": "16777216", "resumableCurrentChunkSize": strconv.Itoa(len(content)), "resumableTotalSize": strconv.Itoa(len(content)), "resumableType": "application/x-7z-compressed", "resumableIdentifier": fmt.Sprintf("quota-%d", n), "resumableFilename": "quota.7z", "resumableRelativePath": "quota.7z", "resumableTotalChunks": "1"} {
		q.Set(key, value)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}
func quotaAccepted(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var response struct {
		TempName string `json:"temp_name"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	require.NotEmpty(t, response.TempName)
	return response.TempName
}
func quotaWait(t *testing.T, app *transport.App, name string) types.SubmissionStatus {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if status := app.Service.SSK.Get(name); status != nil && (status.Status == "success" || status.Status == "failed") {
			return *status
		}
		select {
		case <-deadline.C:
			t.Fatal("upload worker did not finish")
		case <-ticker.C:
		}
	}
}
func quotaRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	return count
}

func TestSubmissionQuotaConcurrentAdmission(t *testing.T) {
	for _, tc := range []struct {
		name            string
		roles           []int64
		successes       int
		separateService bool
	}{
		{"audition", nil, 1, false}, {"audition-two-services", nil, 1, true}, {"trial-curator", []int64{roleIDTrialCurator}, 2, false}, {"staff", []int64{roleIDCurator}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var once, releaseOnce sync.Once
			options := &validatorMockOptions{BeforeValidate: func() { once.Do(func() { close(entered); <-release }) }}
			app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTestWithValidatorMockOptions(t, options)
			secondApp := app
			if tc.separateService {
				conf := *config.GetConfig(l)
				secondApp = initTestAppWithValidatorMockOptions(t, l, &conf, maria, postgres, options)
			}
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
			user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98001, tc.roles, "quota uploader")
			first := quotaAccepted(t, quotaUpload(t, l, app, user.Cookie, nil))
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first upload never reached validator barrier")
			}
			second := quotaAccepted(t, quotaUpload(t, l, secondApp, user.Cookie, nil))
			require.Zero(t, quotaRowCount(t, maria, "submission"), "both requests admitted before first commit")
			releaseOnce.Do(func() { close(release) })
			outcomes := []types.SubmissionStatus{quotaWait(t, app, first), quotaWait(t, secondApp, second)}
			successes := 0
			for _, status := range outcomes {
				if status.Status == "success" {
					successes++
					require.NotNil(t, status.SubmissionID)
				} else {
					require.NotNil(t, status.Message)
					require.Contains(t, *status.Message, "only one non-rejected submission")
					require.Nil(t, status.SubmissionID)
				}
			}
			require.Equal(t, tc.successes, successes)
			for _, table := range []string{"submission", "submission_file", "curation_meta", "submission_cache", "submission_notification_subscription"} {
				require.Equal(t, tc.successes, quotaRowCount(t, maria, table), table)
			}
			require.Equal(t, 2*tc.successes, quotaRowCount(t, maria, "comment"), "upload and validator comments only for committed uploads")
		})
	}
}

func TestSubmissionQuotaExistingHistory(t *testing.T) {
	for _, kind := range []string{"live", "rejected", "deleted", "deleted-rejection", "latest-other-user", "deleted-oldest", "update"} {
		t.Run(kind, func(t *testing.T) {
			app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
			ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
			user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98001, nil, "quota uploader")
			f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
			f.Submission(t, 98001, constants.SubmissionLevelAudition)
			f.File(t, fixtureFile{ID: 98001, SubmissionID: 98001, UserID: user.ID, At: fixtureEpoch})
			f.Comment(t, 97999, 98001, user.ID, constants.ActionUpload, fixtureEpoch.Add(time.Microsecond), nil)
			if kind == "latest-other-user" || kind == "deleted-oldest" {
				f.User(t, 98002, "later uploader")
				f.File(t, fixtureFile{ID: 98002, SubmissionID: 98001, UserID: 98002, At: fixtureEpoch.Add(time.Minute)})
				if kind == "deleted-oldest" {
					_, err := maria.Exec(testSQL(`UPDATE submission_file SET deleted_at=? WHERE id=98001`), fixtureEpoch.Add(time.Hour))
					require.NoError(t, err)
				}
			}
			if kind == "rejected" || kind == "deleted-rejection" {
				f.Comment(t, 98001, 98001, user.ID, constants.ActionReject, fixtureEpoch.Add(time.Minute), nil)
				if kind == "deleted-rejection" {
					_, err := maria.Exec(testSQL(`UPDATE comment SET deleted_at=? WHERE id=98001`), fixtureEpoch.Add(time.Hour))
					require.NoError(t, err)
				}
			}
			if kind == "deleted" {
				_, err := maria.Exec(testSQL(`UPDATE submission SET deleted_at=? WHERE id=98001`), fixtureEpoch.Add(time.Hour))
				require.NoError(t, err)
			}
			f.Comment(t, 97998, 98001, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Minute), nil)
			f.Rebuild(t, 98001)
			var sid *int64
			if kind == "update" {
				id := int64(98001)
				sid = &id
			}
			rr := quotaUpload(t, l, app, user.Cookie, sid)
			if kind == "live" || kind == "deleted-rejection" || kind == "latest-other-user" {
				require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
				require.Equal(t, 1, quotaRowCount(t, maria, "submission"))
				return
			}
			status := quotaWait(t, app, quotaAccepted(t, rr))
			require.Equal(t, "success", status.Status, status.Message)
			expected := 2
			if kind == "update" {
				expected = 1
				require.EqualValues(t, 98001, *status.SubmissionID)
			}
			require.Equal(t, expected, quotaRowCount(t, maria, "submission"))
			files := 2
			if kind == "deleted-oldest" {
				files = 3
			}
			require.Equal(t, files, quotaRowCount(t, maria, "submission_file"))
		})
	}
}

func TestSubmissionQuotaFailureRollsBackAndAllowsRetry(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98001, nil, "quota uploader")
	var baselineEvents int
	require.NoError(t, postgres.QueryRow(ctx, `SELECT COUNT(*) FROM activity_events`).Scan(&baselineEvents))
	// This failure happens after submission/file/comment/subscription writes.
	_, err := maria.Exec(testTriggerSQL(`CREATE TRIGGER quota_fail_cache BEFORE UPDATE ON submission_cache FOR EACH ROW BEGIN IF NEW.fk_newest_file_id IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='quota rollback injection'; END IF; END`))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = maria.Exec(testDropTriggerSQL("quota_fail_cache")) })
	status := quotaWait(t, app, quotaAccepted(t, quotaUpload(t, l, app, user.Cookie, nil)))
	require.Equal(t, "failed", status.Status)
	for _, table := range []string{"submission", "submission_file", "curation_meta", "comment", "submission_cache", "submission_notification_subscription", "submission_notification", "submission_creation_lock"} {
		require.Zero(t, quotaRowCount(t, maria, table), table)
	}
	var afterEvents int
	require.NoError(t, postgres.QueryRow(ctx, `SELECT COUNT(*) FROM activity_events`).Scan(&afterEvents))
	require.Equal(t, baselineEvents, afterEvents, "failed upload rolls back PostgreSQL events too")
	_, err = maria.Exec(testDropTriggerSQL("quota_fail_cache"))
	require.NoError(t, err)
	status = quotaWait(t, app, quotaAccepted(t, quotaUpload(t, l, app, user.Cookie, nil)))
	require.Equal(t, "success", status.Status, status.Message)
	require.Equal(t, 1, quotaRowCount(t, maria, "submission"))
	require.Equal(t, 1, quotaRowCount(t, maria, "submission_cache"))
}

func TestSubmissionQuotaDifferentUsersProgressIndependently(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{BeforeValidate: func() {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
	}})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	firstUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98001, nil, "blocked audition")
	otherUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98002, nil, "independent audition")
	first := quotaAccepted(t, quotaUpload(t, l, app, firstUser.Cookie, nil))
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first upload did not reach validator")
	}
	other := quotaAccepted(t, quotaUpload(t, l, app, otherUser.Cookie, nil))
	status := quotaWait(t, app, other)
	require.Equal(t, "success", status.Status, status.Message)
	require.Equal(t, 1, quotaRowCount(t, maria, "submission"), "another user's upload commits while first user remains blocked")
	releaseOnce.Do(func() { close(release) })
	status = quotaWait(t, app, first)
	require.Equal(t, "success", status.Status, status.Message)
	require.Equal(t, 2, quotaRowCount(t, maria, "submission"))
}

// Observe the database waiter before releasing the first transaction. This
// rules out an implementation that merely rechecks quota without serializing it,
// and uses distinct services so a process-local mutex cannot satisfy the test.
func TestSubmissionQuotaWaitsForOtherServiceCommit(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98001, nil, "quota uploader")
	conf := *config.GetConfig(l)
	secondApp := initTestApp(t, l, &conf, maria, postgres)
	f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
	barrier := newCommentWriteBarrier(t, f, user.ID)
	first := quotaAccepted(t, quotaUpload(t, l, app, user.Cookie, nil))
	barrier.wait(t, 1)
	second := quotaAccepted(t, quotaUpload(t, l, secondApp, user.Cookie, nil))
	waitQuotaLockStatement(t, ctx)
	require.Zero(t, quotaRowCount(t, maria, "submission"), "first submission remains uncommitted")
	barrier.release(t, user.ID)
	firstStatus := quotaWait(t, app, first)
	secondStatus := quotaWait(t, secondApp, second)
	require.Equal(t, "success", firstStatus.Status, firstStatus.Message)
	require.Equal(t, "failed", secondStatus.Status, secondStatus.Message)
	require.NotNil(t, secondStatus.Message)
	require.Contains(t, *secondStatus.Message, "only one non-rejected submission")
	for _, table := range []string{"submission", "submission_file", "curation_meta", "submission_cache", "submission_notification_subscription", "submission_creation_lock"} {
		require.Equal(t, 1, quotaRowCount(t, maria, table), table)
	}
	require.Equal(t, 2, quotaRowCount(t, maria, "comment"))
}
