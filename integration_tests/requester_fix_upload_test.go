package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestSubmissionRequesterFixUpload(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	readyQuery := quickFilterQuery(t, root, "filterReadyForFlashpoint", "simple")
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009101, []int64{roleIDCurator, roleIDTester}, "original uploader")
	requester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009102, []int64{roleIDCurator, roleIDTester}, "requester and fixer")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009103, []int64{roleIDTester}, "independent verifier")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100009104, []int64{roleIDModerator}, "moderator")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	action := func(user *extendedTestUser, kind string, status int) {
		t.Helper()
		rr := addComment(t, l, app, user.Cookie, sid, kind, "requester fix upload regression")
		require.Equal(t, status, rr.Code, rr.Body.String())
	}
	checkRC := func(ids ...int64) {
		t.Helper()
		require.ElementsMatch(t, ids, searchSubmissionByID(t, ctx, app, sid).RequestedChangesUserIDs)
	}
	checkReady := func(want int) {
		t.Helper()
		query, err := url.ParseQuery(readyQuery)
		require.NoError(t, err)
		query.Set("submission-id", fmt.Sprint(sid))
		rr := getWithCookie(t, l, app, moderator.Cookie, "/api/submissions?"+query.Encode())
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var page types.SubmissionsPageData
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
		require.Len(t, page.Submissions, want)
		require.EqualValues(t, want, page.TotalCount)
	}
	action(requester, constants.ActionAssignTesting, http.StatusOK)
	action(requester, constants.ActionRequestChanges, http.StatusOK)
	action(verifier, constants.ActionRequestChanges, http.StatusOK)
	checkRC(requester.ID, verifier.ID)
	uploadFixedVersion(t, l, app, requester, sid)
	checkRC(verifier.ID)
	submission := searchSubmissionByID(t, ctx, app, sid)
	require.Equal(t, requester.ID, submission.LastUploaderID)
	require.Empty(t, submission.ApprovedUserIDs)
	require.Empty(t, submission.VerifiedUserIDs)
	for _, kind := range []string{constants.ActionApprove, constants.ActionVerify} {
		rr := addComment(t, l, app, requester.Cookie, sid, kind, "cannot self-review")
		require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
		require.Contains(t, rr.Body.String(), "uploader of the newest version")
	}
	action(moderator, constants.ActionMarkAdded, http.StatusBadRequest)
	checkReady(0)

	// Existing stuck cache rows must be repairable without editing source history.
	_, err = maria.Exec("UPDATE submission_cache SET active_requested_changes_ids=? WHERE fk_submission_id=?", fmt.Sprintf("%d,%d", requester.ID, verifier.ID), sid)
	require.NoError(t, err)
	for pass := 0; pass < 2; pass++ {
		result, err := app.Service.RecomputeSubmissionCacheAll(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, result.Recomputed)
		checkRC(verifier.ID)
	}

	// A different latest uploader must not resurrect the earlier request.
	uploadFixedVersion(t, l, app, owner, sid)
	checkRC(verifier.ID)
	// A genuinely new request after the withdrawal still counts.
	action(requester, constants.ActionRequestChanges, http.StatusOK)
	checkRC(requester.ID, verifier.ID)
	uploadFixedVersion(t, l, app, requester, sid)
	checkRC(verifier.ID)

	var requesterRCs int
	for _, comment := range getComments(t, ctx, app, moderator.ID, sid) {
		if comment.AuthorID == requester.ID && comment.Action == constants.ActionRequestChanges {
			requesterRCs++
			require.Equal(t, "requester fix upload regression", *comment.Message)
		}
	}
	require.Equal(t, 2, requesterRCs, "withdrawal preserves both request comments")
	action(owner, constants.ActionAssignTesting, http.StatusOK)
	action(owner, constants.ActionApprove, http.StatusOK)
	checkRC(verifier.ID)
	action(moderator, constants.ActionMarkAdded, http.StatusBadRequest)
	action(verifier, constants.ActionAssignVerification, http.StatusOK)
	action(verifier, constants.ActionVerify, http.StatusOK)
	checkRC()
	checkReady(1)
	action(moderator, constants.ActionMarkAdded, http.StatusOK)
	require.Contains(t, searchSubmissionByID(t, ctx, app, sid).DistinctActions, constants.ActionMarkAdded)
}
