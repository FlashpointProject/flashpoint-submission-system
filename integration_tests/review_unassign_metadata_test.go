package integration_tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/stretchr/testify/require"
)

func TestReviewUnassignAfterMetadataEdit(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTest(t)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030001, []int64{roleIDCurator}, "uploader")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030002, []int64{roleIDCurator, roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030003, []int64{roleIDCurator, roleIDTester}, "verifier")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)

	for _, tc := range []struct {
		name, assign, unassign string
		user                   *extendedTestUser
	}{
		{"testing", constants.ActionAssignTesting, constants.ActionUnassignTesting, tester},
		{"verification", constants.ActionAssignVerification, constants.ActionUnassignVerification, verifier},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "verification" {
				// The original tester can still approve after another content uploader.
				uploadFixedVersion(t, l, app, owner, sid)
				for _, action := range []string{constants.ActionAssignTesting, constants.ActionApprove} {
					rr := addComment(t, l, app, tester.Cookie, sid, action, "prepare verification")
					require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				}
			}
			rr := addComment(t, l, app, tc.user.Cookie, sid, tc.assign, "")
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			rr = editFrozenTestMetadata(t, l, app, tc.user, sid)
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

			submission := searchSubmissionByID(t, ctx, app, sid)
			require.Equal(t, tc.user.ID, submission.LastUploaderID)
			require.Contains(t, submission.DistinctActions, constants.ActionEditMeta)
			if tc.name == "testing" {
				require.Contains(t, submission.AssignedTestingUserIDs, tc.user.ID)
			} else {
				require.Contains(t, submission.AssignedVerificationUserIDs, tc.user.ID)
				require.Contains(t, submission.ApprovedUserIDs, tester.ID, "metadata edits preserve approval")
			}
			for _, path := range []string{
				fmt.Sprintf("/web/submission/%d", sid),
				fmt.Sprintf("/web/submissions?submission-id=%d", sid),
			} {
				page := getWithCookie(t, l, app, tc.user.Cookie, path)
				require.Equal(t, http.StatusOK, page.Code, page.Body.String())
				require.Contains(t, page.Body.String(), "button-"+tc.unassign)
			}

			rr = addComment(t, l, app, tc.user.Cookie, sid, tc.unassign, "")
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			submission = searchSubmissionByID(t, ctx, app, sid)
			require.NotContains(t, submission.AssignedTestingUserIDs, tc.user.ID)
			require.NotContains(t, submission.AssignedVerificationUserIDs, tc.user.ID)
			rr = addComment(t, l, app, tc.user.Cookie, sid, tc.unassign, "")
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			require.Contains(t, rr.Body.String(), "you are not assigned")
		})
	}
}
