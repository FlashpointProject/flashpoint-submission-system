package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
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

func unfreezeSubmission(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/api/submission/%d/unfreeze", sid)
	req, err := http.NewRequest("POST", u, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func getFrozenAt(t *testing.T, maria *sql.DB, sid int64) *time.Time {
	var frozenAt *time.Time
	err := maria.QueryRow("SELECT frozen_at FROM submission WHERE id=?", sid).Scan(&frozenAt)
	require.NoError(t, err)
	return frozenAt
}

// TestFreezeUnfreeze tests freeze/unfreeze permissions and effects.
func TestFreezeUnfreeze(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const (
		roleCurator   = 442665038642413569
		roleTester    = 442988314480476170
		roleModerator = 442462642599231499
	)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000401), []int64{roleCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000402), []int64{roleTester}, "tester")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000403), []int64{roleModerator}, "moderator")

	t.Run("ModeratorCanFreeze", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		frozenAt := getFrozenAt(t, maria, sid)
		require.NotNil(t, frozenAt, "frozen_at should be set after freeze")
		require.WithinDuration(t, time.Now(), *frozenAt, 5*time.Second)
	})

	t.Run("ModeratorCanUnfreeze", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)

		rr = unfreezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		frozenAt := getFrozenAt(t, maria, sid)
		require.Nil(t, frozenAt, "frozen_at should be NULL after unfreeze")
	})

	t.Run("NonFreezerCannotFreeze", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := freezeSubmission(t, l, app, submitter.Cookie, sid)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "curator should not be able to freeze")

		rr = freezeSubmission(t, l, app, tester.Cookie, sid)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "tester should not be able to freeze")

		// Verify submission is still not frozen
		frozenAt := getFrozenAt(t, maria, sid)
		require.Nil(t, frozenAt)
	})

	t.Run("NonFreezerCannotUnfreeze", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)

		rr = unfreezeSubmission(t, l, app, submitter.Cookie, sid)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "curator should not be able to unfreeze")

		rr = unfreezeSubmission(t, l, app, tester.Cookie, sid)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "tester should not be able to unfreeze")

		// Verify submission is still frozen
		frozenAt := getFrozenAt(t, maria, sid)
		require.NotNil(t, frozenAt, "submission should still be frozen")
	})

	t.Run("FreezeBlocksComment", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)

		// Tester cannot comment on frozen submission
		rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionComment, "test comment on frozen")
		require.Equal(t, http.StatusUnauthorized, rr.Code, "tester should not be able to comment on frozen submission")

		// Moderator (freezer) can still comment
		rr = addComment(t, l, app, moderator.Cookie, sid, constants.ActionComment, "moderator comment on frozen")
		require.Equal(t, http.StatusOK, rr.Code, "moderator should be able to comment on frozen submission: "+rr.Body.String())
	})

	t.Run("FreezeIdempotent", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Freeze twice
		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)
		rr = freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)

		frozenAt := getFrozenAt(t, maria, sid)
		require.NotNil(t, frozenAt)

		// Unfreeze twice
		rr = unfreezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)
		rr = unfreezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)

		frozenAt = getFrozenAt(t, maria, sid)
		require.Nil(t, frozenAt)
	})

	t.Run("FreezeUnfreezeRoundtrip", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		// Initially not frozen
		frozenAt := getFrozenAt(t, maria, sid)
		require.Nil(t, frozenAt, "should not be frozen initially")

		// Freeze
		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)
		frozenAt = getFrozenAt(t, maria, sid)
		require.NotNil(t, frozenAt, "should be frozen after freeze")

		// Unfreeze
		rr = unfreezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)
		frozenAt = getFrozenAt(t, maria, sid)
		require.Nil(t, frozenAt, "should not be frozen after unfreeze")

		// Freeze again
		rr = freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code)
		frozenAt = getFrozenAt(t, maria, sid)
		require.NotNil(t, frozenAt, "should be frozen again after second freeze")
	})
}
