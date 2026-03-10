package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func downloadCurationImage(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid, ciid int64) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequest("GET", fmt.Sprintf("/data/submission/%d/curation-image/%d.png", sid, ciid), nil)
	require.NoError(t, err)
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func getCurationImageIDsBySubmissionID(t *testing.T, maria *sql.DB, sid int64) []int64 {
	t.Helper()

	rows, err := maria.Query(`
		SELECT curation_image.id
		FROM curation_image
		JOIN submission_file ON submission_file.id = curation_image.fk_submission_file_id
		WHERE submission_file.fk_submission_id = ?
		ORDER BY curation_image.id`, sid)
	require.NoError(t, err)
	defer rows.Close()

	var imageIDs []int64
	for rows.Next() {
		var ciid int64
		require.NoError(t, rows.Scan(&ciid))
		imageIDs = append(imageIDs, ciid)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, imageIDs, "test submission should have validator-provided curation images")
	return imageIDs
}

func hasCommentID(comments []*types.ExtendedComment, cid int64) bool {
	for _, comment := range comments {
		if comment.CommentID == cid {
			return true
		}
	}
	return false
}

func TestSubmissionResourceBinding(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const (
		roleModerator    = 442462642599231499
		roleCurator      = 442665038642413569
		roleTrialCurator = 569328799318016018
	)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000501), []int64{roleTrialCurator}, "submitter")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000502), []int64{roleModerator}, "moderator")
	curator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000503), []int64{roleCurator}, "curator")

	t.Run("MismatchedSubmissionIDCannotDeleteFile", func(t *testing.T) {
		frozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, &frozenSID)
		frozenFiles := getSubmissionFilesBySubmissionID(t, ctx, db, frozenSID)
		require.Len(t, frozenFiles, 2)

		rr := freezeSubmission(t, l, app, moderator.Cookie, frozenSID)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		unfrozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr = softDeleteSubmissionFile(t, l, app, moderator.Cookie, unfrozenSID, frozenFiles[0].FileID, "mismatched route should not delete frozen file")
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		frozenFiles = getSubmissionFilesBySubmissionID(t, ctx, db, frozenSID)
		require.Len(t, frozenFiles, 2, "frozen submission files should remain intact")
	})

	t.Run("MismatchedSubmissionIDCannotDeleteComment", func(t *testing.T) {
		frozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		comments := getComments(t, ctx, app, submitter.ID, frozenSID)
		require.NotEmpty(t, comments)

		var commentID int64
		for _, comment := range comments {
			if comment.AuthorID == submitter.ID {
				commentID = comment.CommentID
				break
			}
		}
		require.NotZero(t, commentID, "submitter upload comment should exist")

		rr := freezeSubmission(t, l, app, moderator.Cookie, frozenSID)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		unfrozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr = softDeleteComment(t, l, app, moderator.Cookie, unfrozenSID, commentID, "mismatched route should not delete frozen comment")
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		comments = getComments(t, ctx, app, submitter.ID, frozenSID)
		require.True(t, hasCommentID(comments, commentID), "frozen submission comment should remain visible")
	})

	t.Run("MismatchedSubmissionIDCannotDownloadFrozenCurationImage", func(t *testing.T) {
		frozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		imageIDs := getCurationImageIDsBySubmissionID(t, maria, frozenSID)

		rr := freezeSubmission(t, l, app, moderator.Cookie, frozenSID)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		unfrozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr = downloadCurationImage(t, l, app, curator.Cookie, unfrozenSID, imageIDs[0])
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})

	t.Run("MismatchedSubmissionIDCannotDownloadDeletedCurationImage", func(t *testing.T) {
		deletedSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		imageIDs := getCurationImageIDsBySubmissionID(t, maria, deletedSID)

		rr := softDeleteSubmission(t, l, app, moderator.Cookie, deletedSID, "delete submission before probing image access")
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		activeSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr = downloadCurationImage(t, l, app, curator.Cookie, activeSID, imageIDs[0])
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})
}
