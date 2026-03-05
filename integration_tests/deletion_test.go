package integration_tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func softDeleteSubmission(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64, reason string) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/api/submission/%d?reason=%s", sid, url.QueryEscape(reason))
	req, err := http.NewRequest("DELETE", u, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func softDeleteSubmissionFile(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid, fid int64, reason string) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/api/submission/%d/file/%d?reason=%s", sid, fid, url.QueryEscape(reason))
	req, err := http.NewRequest("DELETE", u, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func softDeleteComment(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid, cid int64, reason string) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/api/submission/%d/comment/%d?reason=%s", sid, cid, url.QueryEscape(reason))
	req, err := http.NewRequest("DELETE", u, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func freezeSubmission(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/api/submission/%d/freeze", sid)
	req, err := http.NewRequest("POST", u, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func getComments(t *testing.T, ctx context.Context, app *transport.App, uid, sid int64) []*types.ExtendedComment {
	viewData, err := app.Service.GetViewSubmissionPageData(ctx, uid, sid)
	require.NoError(t, err)
	return viewData.Comments
}

// TestSubmissionDeletion tests soft deletion permissions for submissions, files, and comments.
func TestSubmissionDeletion(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const (
		roleCurator   = 442665038642413569
		roleTester    = 442988314480476170
		roleModerator = 442462642599231499
	)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000301), []int64{roleCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000302), []int64{roleTester}, "tester")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000303), []int64{roleModerator}, "moderator")

	t.Run("ModeratorCanDeleteSubmission", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := softDeleteSubmission(t, l, app, moderator.Cookie, sid, "test deletion reason")
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		// Verify submission no longer appears in search
		subs, _, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
		require.NoError(t, err)
		require.Empty(t, subs, "deleted submission should not appear in search results")
	})

	t.Run("ModeratorCanDeleteComment", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		comments := getComments(t, ctx, app, submitter.ID, sid)
		require.NotEmpty(t, comments)

		// Find the upload comment (authored by submitter)
		var userCommentID int64
		for _, c := range comments {
			if c.AuthorID == submitter.ID {
				userCommentID = c.CommentID
				break
			}
		}
		require.NotZero(t, userCommentID, "should find a user comment")

		rr := softDeleteComment(t, l, app, moderator.Cookie, sid, userCommentID, "deleting user comment")
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	})

	t.Run("CannotDeleteValidatorComment", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		comments := getComments(t, ctx, app, submitter.ID, sid)

		// Find the validator comment (bot auto-approve)
		var validatorCommentID int64
		for _, c := range comments {
			if c.AuthorID == constants.ValidatorID {
				validatorCommentID = c.CommentID
				break
			}
		}
		require.NotZero(t, validatorCommentID, "should find a validator comment")

		rr := softDeleteComment(t, l, app, moderator.Cookie, sid, validatorCommentID, "trying to delete validator comment")
		require.Equal(t, http.StatusForbidden, rr.Code, "should not be able to delete validator comment")
	})

	t.Run("CannotDeleteLastFile", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Get file IDs via DAL
		dbs, err := db.NewSession(ctx)
		require.NoError(t, err)
		files, err := db.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
		require.NoError(t, err)
		dbs.Rollback()
		require.Len(t, files, 1, "should have exactly 1 file")

		rr := softDeleteSubmissionFile(t, l, app, moderator.Cookie, sid, files[0].FileID, "trying to delete last file")
		require.Equal(t, http.StatusBadRequest, rr.Code, "should not be able to delete last file")
	})

	t.Run("CanDeleteFileWhenMultiple", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Upload a second version
		uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, &sid)

		// Get file IDs
		dbs, err := db.NewSession(ctx)
		require.NoError(t, err)
		files, err := db.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
		require.NoError(t, err)
		dbs.Rollback()
		require.Len(t, files, 2, "should have 2 files")

		rr := softDeleteSubmissionFile(t, l, app, moderator.Cookie, sid, files[0].FileID, "deleting one of two files")
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		// Verify only 1 file remains
		dbs, err = db.NewSession(ctx)
		require.NoError(t, err)
		files, err = db.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
		require.NoError(t, err)
		dbs.Rollback()
		require.Len(t, files, 1, "should have 1 file remaining")
	})

	t.Run("NonDeleterCannotDelete", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Submitter (Curator) cannot delete — middleware returns 401
		rr := softDeleteSubmission(t, l, app, submitter.Cookie, sid, "curator trying to delete")
		require.Equal(t, http.StatusUnauthorized, rr.Code, "curator should not be able to delete")

		// Tester cannot delete
		rr = softDeleteSubmission(t, l, app, tester.Cookie, sid, "tester trying to delete")
		require.Equal(t, http.StatusUnauthorized, rr.Code, "tester should not be able to delete")
	})

	t.Run("CannotDeleteFrozenSubmission", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Freeze submission
		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, "freeze should succeed: "+rr.Body.String())

		// Try to delete frozen submission — middleware returns 401
		rr = softDeleteSubmission(t, l, app, moderator.Cookie, sid, "trying to delete frozen")
		require.Equal(t, http.StatusUnauthorized, rr.Code, "should not be able to delete frozen submission")

		// Try to delete comment on frozen submission
		comments := getComments(t, ctx, app, submitter.ID, sid)
		var userCommentID int64
		for _, c := range comments {
			if c.AuthorID == submitter.ID {
				userCommentID = c.CommentID
				break
			}
		}
		require.NotZero(t, userCommentID)
		rr = softDeleteComment(t, l, app, moderator.Cookie, sid, userCommentID, "trying to delete comment on frozen")
		require.Equal(t, http.StatusUnauthorized, rr.Code, "should not be able to delete comment on frozen submission")
	})

	t.Run("ReasonValidation", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Empty reason
		rr := softDeleteSubmission(t, l, app, moderator.Cookie, sid, "")
		require.Equal(t, http.StatusBadRequest, rr.Code, "empty reason should be rejected")

		// Too short reason
		rr = softDeleteSubmission(t, l, app, moderator.Cookie, sid, "ab")
		require.Equal(t, http.StatusBadRequest, rr.Code, "too short reason should be rejected")

		// Valid reason
		rr = softDeleteSubmission(t, l, app, moderator.Cookie, sid, "valid reason for deletion")
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	})
}
