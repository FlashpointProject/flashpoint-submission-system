package integration_tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestSubmissionStateMachine_FixUploadsFlow(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const (
		roleCurator   = 442665038642413569
		roleTester    = 442988314480476170
		roleModerator = 442462642599231499
	)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100001101), []int64{roleCurator}, "submitter")
	approver := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100001102), []int64{roleTester}, "approver")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100001103), []int64{roleTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100001104), []int64{roleModerator}, "adder")

	getSubmission := func(t *testing.T, sid int64) *types.ExtendedSubmission {
		t.Helper()

		viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, sid)
		require.NoError(t, err)
		require.NotEmpty(t, viewData.Submissions)
		return viewData.Submissions[0]
	}

	assertBotApproved := func(t *testing.T, sid int64, stage string) {
		t.Helper()

		submission := getSubmission(t, sid)
		require.Equal(t, constants.ActionApprove, submission.BotAction, "%s: bot should approve upload", stage)
	}

	addAction := func(t *testing.T, user *extendedTestUser, sid int64, action string, msg string) {
		t.Helper()

		rr := addComment(t, l, app, user.Cookie, sid, action, msg)
		require.Equal(t, http.StatusOK, rr.Code, "action %s by user %d should succeed: %s", action, user.ID, rr.Body.String())
	}

	submissionID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
	assertBotApproved(t, submissionID, "initial upload")

	// first approver says not OK
	addAction(t, approver, submissionID, constants.ActionAssignTesting, "assign for testing before first review")
	addAction(t, approver, submissionID, constants.ActionRequestChanges, "first review not OK")

	// same-file upload is rejected; submitter must upload a different file for fixes
	failed := uploadTestSubmissionExpectFailed(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, submissionID)
	require.NotNil(t, failed.Message)
	require.Contains(t, *failed.Message, "already present in the DB")

	fixUpload1 := createUniqueSubmissionVariant(t, "fix-upload-1")
	uploadedSID := uploadTestSubmission(t, l, app, fixUpload1, submitter.Cookie, &submissionID)
	require.Equal(t, submissionID, uploadedSID)
	assertBotApproved(t, submissionID, "first fixed upload")

	// approver says OK
	addAction(t, approver, submissionID, constants.ActionAssignTesting, "assign for testing after first fix")
	addAction(t, approver, submissionID, constants.ActionApprove, "first fixed version looks good")

	// verifier says not OK
	addAction(t, verifier, submissionID, constants.ActionAssignVerification, "assign for verification")
	addAction(t, verifier, submissionID, constants.ActionRequestChanges, "verification not OK")

	fixUpload2 := createUniqueSubmissionVariant(t, "fix-upload-2")
	uploadedSID = uploadTestSubmission(t, l, app, fixUpload2, submitter.Cookie, &submissionID)
	require.Equal(t, submissionID, uploadedSID)
	assertBotApproved(t, submissionID, "second fixed upload")

	// New file versions require a fresh approve before verify can proceed.
	addAction(t, approver, submissionID, constants.ActionAssignTesting, "assign for testing after second fix")
	addAction(t, approver, submissionID, constants.ActionApprove, "second fixed version looks good")

	// verifier says OK
	addAction(t, verifier, submissionID, constants.ActionVerify, "verification OK")

	// submission added
	addAction(t, adder, submissionID, constants.ActionMarkAdded, "submission added")

	submission := getSubmission(t, submissionID)
	require.Equal(t, uint64(3), submission.FileCount)
	require.Contains(t, submission.DistinctActions, constants.ActionMarkAdded)
	require.Len(t, submission.ApprovedUserIDs, 1)
	require.Contains(t, submission.ApprovedUserIDs, approver.ID)
	require.Len(t, submission.VerifiedUserIDs, 1)
	require.Contains(t, submission.VerifiedUserIDs, verifier.ID)

	viewData, err := app.Service.GetViewSubmissionPageData(ctx, submitter.ID, submissionID)
	require.NoError(t, err)

	uploadCount := 0
	for _, comment := range viewData.Comments {
		if comment.Action == constants.ActionUpload && comment.AuthorID == submitter.ID {
			uploadCount++
		}
	}
	require.Equal(t, 3, uploadCount, "submission should have exactly three successful uploads")

	verifyTestSubmissionExists(t, ctx, l, app, submitter.ID, submissionID)
}

func createUniqueSubmissionVariant(t *testing.T, tag string) string {
	t.Helper()

	originalData, err := os.ReadFile("./test_files/Warpstar4K.7z")
	require.NoError(t, err)

	variantData := append([]byte{}, originalData...)
	variantData = append(variantData, []byte("\n"+tag+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))...)

	variantPath := fmt.Sprintf("./test_data/%s-%d.7z", tag, time.Now().UnixNano())
	err = os.WriteFile(variantPath, variantData, 0o644)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = os.Remove(variantPath)
	})

	return variantPath
}

func uploadTestSubmissionExpectFailed(t *testing.T, l *logrus.Entry, app *transport.App, filename string, cookie *http.Cookie, sid int64) *types.SubmissionStatus {
	t.Helper()

	fileContent, err := os.ReadFile(filename)
	require.NoError(t, err)
	fileSize := int64(len(fileContent))

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	url := fmt.Sprintf("/api/submission-receiver-resumable/%d", sid)
	req, err := http.NewRequest("POST", url, body)
	require.NoError(t, err)

	identifier := fmt.Sprintf("%d-duplicate-attempt-%d", fileSize, time.Now().UnixNano())
	q := req.URL.Query()
	q.Add("resumableChunkNumber", "1")
	q.Add("resumableChunkSize", strconv.FormatInt(16*1024*1024, 10))
	q.Add("resumableCurrentChunkSize", strconv.FormatInt(fileSize, 10))
	q.Add("resumableTotalSize", strconv.FormatInt(fileSize, 10))
	q.Add("resumableType", "application/x-7z-compressed")
	q.Add("resumableIdentifier", identifier)
	q.Add("resumableFilename", filename)
	q.Add("resumableRelativePath", filename)
	q.Add("resumableTotalChunks", "1")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp types.ReceiveFileTempNameResp
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.NotNil(t, resp.TempName)

	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			require.FailNow(t, "timeout waiting for duplicate upload to fail")
		case <-ticker.C:
			status := app.Service.SSK.Get(*resp.TempName)
			if status == nil {
				continue
			}
			if status.Status == constants.SubmissionStatusFailed {
				return status
			}
			if status.Status == constants.SubmissionStatusSuccess {
				require.FailNow(t, "duplicate upload unexpectedly succeeded")
			}
		}
	}
}
