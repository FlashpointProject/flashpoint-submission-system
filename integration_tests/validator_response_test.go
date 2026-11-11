package integration_tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func validatorComment(t *testing.T, ctx context.Context, app *transport.App, uid, sid int64) *types.ExtendedComment {
	t.Helper()
	for _, c := range getComments(t, ctx, app, uid, sid) {
		if c.AuthorID == constants.ValidatorID {
			return c
		}
	}
	t.Fatalf("submission %d has no validator comment", sid)
	return nil
}

// Only the freezer warning may be dropped from the bot comment. Any other
// warning must keep the bot from approving.
// The subtests share one mock and must run in order: each upload takes the next
// entry of CurationWarningsSeq.
func TestValidatorCommentKeepsNonFreezerWarnings(t *testing.T) {
	unknownTag := "Tag `Foo` is not a known tag, please verify (did you write it correctly?)."
	freezer := "Release date 2025-01-01 is less than 3 years ago. Curation should be frozen."
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{
		CurationWarningsSeq: [][]string{{unknownTag}, {unknownTag, freezer}, {freezer}},
	})
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030101, []int64{roleIDCurator}, "submitter")

	for _, tc := range []struct {
		name       string
		action     string
		message    []string
		notMessage []string
		freeze     bool
	}{
		{"unknown tag only", constants.ActionRequestChanges, []string{unknownTag}, nil, false},
		{"unknown tag and freezer", constants.ActionRequestChanges, []string{unknownTag, "automatically frozen"}, []string{freezer}, true},
		{"freezer only", constants.ActionApprove, []string{"Looks good to me!", "automatically frozen"}, []string{freezer}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
			c := validatorComment(t, ctx, app, submitter.ID, sid)
			require.Equal(t, tc.action, c.Action)
			require.NotNil(t, c.Message)
			for _, s := range tc.message {
				require.Contains(t, *c.Message, s)
			}
			for _, s := range tc.notMessage {
				require.NotContains(t, *c.Message, s)
			}
			require.Equal(t, tc.freeze, searchSubmissionByID(t, ctx, app, sid).ShouldAutofreeze)
		})
	}
}

// Validator notes are shown in the bot comment but don't keep it from
// approving.
func TestValidatorCommentShowsNotes(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{
		CurationNotes: []string{"The archive is 44.3 GB.", "The curation is 103.6 GB uncompressed."},
	})
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030901, []int64{roleIDCurator}, "submitter")

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
	c := validatorComment(t, ctx, app, submitter.ID, sid)
	require.Equal(t, constants.ActionApprove, c.Action)
	require.Equal(t, "Looks good to me! 🤖\n\nℹ️ The archive is 44.3 GB.\nℹ️ The curation is 103.6 GB uncompressed.", *c.Message)
}

type addApp struct{ Name, ApplicationPath, LaunchCommand string }

func gameAddApps(t *testing.T, ctx context.Context, postgres *pgxpool.Pool, gameID string) []addApp {
	t.Helper()
	rows, err := postgres.Query(ctx, `SELECT name, application_path, launch_command FROM additional_app
		WHERE parent_game_id = $1 ORDER BY name`, gameID)
	require.NoError(t, err)
	defer rows.Close()
	var apps []addApp
	for rows.Next() {
		var a addApp
		require.NoError(t, rows.Scan(&a.Name, &a.ApplicationPath, &a.LaunchCommand))
		apps = append(apps, a)
	}
	require.NoError(t, rows.Err())
	return apps
}

// A content patch (a submission for a game that already exists) must keep
// Extras and Message, and must not wipe add apps it doesn't mention.
func TestMarkAddedContentPatchKeepsAdditionalApps(t *testing.T) {
	opts := &validatorMockOptions{}
	app, l, ctx, db, pgdb, _, postgres := setupIntegrationTestWithValidatorMockOptions(t, opts)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030201, []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030202, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030203, []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030204, []int64{roleIDModerator}, "adder")

	markAdded := func() {
		t.Helper()
		sid := advanceToState(t, l, app, "verified", submitter.Cookie, tester.Cookie, verifier.Cookie)
		rr := addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	}
	// Content patches are marked as added from the "apply content patch" page,
	// which sends the chosen metadata as a JSON body (at least "{}").
	markPatchAdded := func() {
		t.Helper()
		sid := advanceToState(t, l, app, "verified", submitter.Cookie, tester.Cookie, verifier.Cookie)
		target := fmt.Sprintf("/api/submission-batch/%d/comment?action=%s&message=%%22%%22&ignore-duplicate-actions=false",
			sid, constants.ActionMarkAdded)
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("{}"))
		req.AddCookie(adder.Cookie)
		rr := httptest.NewRecorder()
		logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	}

	// New game with Extras, Message and one alternate app.
	meta := defaultValidatorMockMeta()
	meta.Extras = utils.StrPtr("extras-folder")
	meta.Message = utils.StrPtr("Read me")
	opts.RepackMeta = &meta
	markAdded()
	var gameID string
	require.NoError(t, postgres.QueryRow(ctx, `SELECT id FROM game WHERE title = $1`, "Test Game").Scan(&gameID))
	original := []addApp{
		{"Extras", ":extras:", "extras-folder"},
		{"Extras Launcher", "extras/launcher.exe", "--launch-extras"},
		{"Message", ":message:", "Read me"},
	}
	require.Equal(t, original, gameAddApps(t, ctx, postgres, gameID))

	// Content patch whose curation has no add apps: the game keeps its own.
	// Marked as added with a plain comment form, which sends no JSON patch.
	patch := defaultValidatorMockMeta()
	patch.UUID = &gameID
	patch.AdditionalApps = []*types.CurationAdditionalApp{}
	opts.UploadMeta = &patch
	opts.RepackMeta = &patch
	markAdded()
	require.Equal(t, original, gameAddApps(t, ctx, postgres, gameID))

	// Content patch with add apps: they replace the game's, Extras included.
	withApps := patch
	withApps.Extras = utils.StrPtr("new-extras")
	withApps.AdditionalApps = []*types.CurationAdditionalApp{{
		Heading: utils.StrPtr("Other"), ApplicationPath: utils.StrPtr("other.exe"), LaunchCommand: utils.StrPtr("--other"),
	}}
	opts.RepackMeta = &withApps
	markPatchAdded()
	require.Equal(t, []addApp{
		{"Extras", ":extras:", "new-extras"},
		{"Other", "other.exe", "--other"},
	}, gameAddApps(t, ctx, postgres, gameID))
}

// A curation without a logo must be refused up front: no game is created and
// no data pack is written.
func TestMarkAddedWithoutLogoWritesNothing(t *testing.T) {
	app, l, ctx, db, pgdb, _, postgres := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{
		RepackImages: []types.ValidatorResponseImage{
			{Type: "screenshot", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="},
		},
	})
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030301, []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030302, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030303, []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030304, []int64{roleIDModerator}, "adder")

	sid := advanceToState(t, l, app, "verified", submitter.Cookie, tester.Cookie, verifier.Cookie)
	rr := addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added")
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "logo")

	var games int
	require.NoError(t, postgres.QueryRow(ctx, `SELECT COUNT(*) FROM game`).Scan(&games))
	require.Zero(t, games)
	packs, err := os.ReadDir(app.Conf.DataPacksDir)
	if !os.IsNotExist(err) {
		require.NoError(t, err)
		require.Empty(t, packs)
	}
	require.NotContains(t, searchSubmissionByID(t, ctx, app, sid).DistinctActions, constants.ActionMarkAdded)
}

// A content patch always shares its launch command with the game it patches.
// The validator flags that as "Identical launch command"; FPFSS must ignore it
// for content patches on upload and after a meta edit alike.
func TestContentPatchIgnoresIdenticalLaunchCommandAfterMetaEdit(t *testing.T) {
	opts := &validatorMockOptions{}
	app, l, ctx, db, pgdb, _, postgres := setupIntegrationTestWithValidatorMockOptions(t, opts)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030401, []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030402, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030403, []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100030404, []int64{roleIDModerator}, "adder")

	sid := advanceToState(t, l, app, "verified", submitter.Cookie, tester.Cookie, verifier.Cookie)
	rr := addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var gameID string
	require.NoError(t, postgres.QueryRow(ctx, `SELECT id FROM game WHERE title = $1`, "Test Game").Scan(&gameID))

	patch := defaultValidatorMockMeta()
	patch.UUID = &gameID
	opts.UploadMeta = &patch
	opts.CurationErrors = []string{"Identical launch command already present in the master database. Is your curation a duplicate?"}

	lastValidatorAction := func(sid int64) string {
		t.Helper()
		action := ""
		for _, c := range getComments(t, ctx, app, submitter.ID, sid) {
			if c.AuthorID == constants.ValidatorID {
				action = c.Action
			}
		}
		return action
	}

	patchSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
	require.Equal(t, constants.ActionApprove, lastValidatorAction(patchSID))

	rr = editFrozenTestMetadata(t, l, app, submitter, patchSID)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, constants.ActionApprove, lastValidatorAction(patchSID))
}
