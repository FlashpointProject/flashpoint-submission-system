package integration_tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/stretchr/testify/require"
)

func TestReviewActionsImportedSubmissionBatch(t *testing.T) {
	app, l, ctx, db, _, maria, _ := setupIntegrationTest(t)
	ctx = addContextValues(ctx, l, 7101, "review-imported-batch")
	f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
	f.User(t, 7101, "reviewer")
	f.User(t, 7102, "uploader")
	for _, sid := range []int64{7001, 7002} {
		f.Submission(t, sid, "staff")
		f.File(t, fixtureFile{ID: sid, SubmissionID: sid, UserID: 7102, At: fixtureEpoch})
	}
	f.Comment(t, 7201, 7001, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(time.Second), nil)
	f.Comment(t, 7202, 7001, 7102, constants.ActionMarkAdded, fixtureEpoch.Add(2*time.Second), nil)
	// Sort the active submission first, exercising rollback of work already done
	// when the later imported submission rejects the batch.
	f.Comment(t, 7203, 7002, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(3*time.Second), nil)
	f.Rebuild(t, 7001, 7002)
	receive := func(ids []int64, action, ignore string) error {
		return app.Service.ReceiveComments(ctx, 7101, ids, action, "review test", ignore, "", "", "", "", nil)
	}
	count := func(sid int64) int {
		t.Helper()
		var count int
		require.NoError(t, maria.QueryRow(testSQL("SELECT COUNT(*) FROM comment WHERE fk_submission_id=?"), sid).Scan(&count))
		return count
	}
	for _, action := range []string{constants.ActionAssignTesting, constants.ActionUnassignTesting, constants.ActionAssignVerification, constants.ActionUnassignVerification, constants.ActionApprove, constants.ActionVerify, constants.ActionRequestChanges, constants.ActionReject} {
		t.Run(action, func(t *testing.T) {
			err := receive([]int64{7001}, action, "false")
			var public constants.PublicError
			require.ErrorAs(t, err, &public)
			require.Equal(t, http.StatusBadRequest, public.Status)
			require.Contains(t, public.Msg, "review actions are closed")
			require.Equal(t, 2, count(7001))
		})
	}
	err := receive([]int64{7002, 7001}, constants.ActionAssignTesting, "false")
	require.Error(t, err)
	require.Equal(t, 1, count(7002), "batch rejection rolls back the earlier valid action")
	require.Equal(t, 2, count(7001))
	require.NoError(t, receive([]int64{7002, 7001}, constants.ActionAssignTesting, "true"))
	require.Equal(t, 2, count(7002), "skip mode applies the active submission's action")
	require.Equal(t, 2, count(7001), "skip mode leaves imported submission unchanged")
	require.NoError(t, receive([]int64{7001}, constants.ActionComment, "false"))
	require.Equal(t, 3, count(7001), "ordinary comments remain available after import")
}
