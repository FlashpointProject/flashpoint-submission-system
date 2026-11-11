package integration_tests

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/curation"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// The tests in this file run FPFSS with the real curation package on the
// fixture archives of curation/testdata, looking up tags and launch commands
// in the test Postgres database.

const (
	// e2eRoot is the root folder of test_curation_valid.{zip,7z}.
	e2eRoot  = "e647a839-c4d8-4c51-8f04-4cf142f1718c"
	e2eTitle = "Sesame Street: Where Is Thumbkin?"
)

// curationTestdata is the absolute path of curation/testdata. It is taken
// when the test binary starts in this directory: the tests change into a
// temporary working directory.
var curationTestdata = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(filepath.Dir(wd), "curation", "testdata")
}()

func curationFixturePath(name string) string {
	return filepath.Join(curationTestdata, name)
}

// e2eValidator is FPFSS's validator, except for GetTags, which would fetch
// the wiki (the test network has no internet access).
type e2eValidator struct{ service.Validator }

func (e2eValidator) GetTags(context.Context) ([]types.Tag, error) { return []types.Tag{}, nil }

func setupCurationE2ETest(t *testing.T) (*transport.App, *logrus.Entry, context.Context, database.DAL, database.PGDAL, *pgxpool.Pool) {
	app, l, ctx, db, pgdb, _, postgres := setupIntegrationTestWithValidator(t, func(conf *config.Config, postgres *pgxpool.Pool) service.Validator {
		return e2eValidator{service.NewValidator(curation.New(database.NewPostgresDAL(postgres), conf.RepackDir, logrus.New()), nil)}
	})
	return app, l, context.WithValue(ctx, utils.CtxKeys.Log, l), db, pgdb, postgres
}

// createTags adds tags to Postgres, as known tags for validation.
func createTags(t *testing.T, ctx context.Context, pgdb database.PGDAL, uid int64, names ...string) {
	t.Helper()
	dbs, err := pgdb.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()
	for _, name := range names {
		_, err := pgdb.GetOrCreateTag(dbs, name, "default", "test", uid)
		require.NoError(t, err)
	}
	require.NoError(t, dbs.Commit())
}

// requireRepackDirEmpty checks that FPFSS removed every pack and edit
// directory it got from the validator.
func requireRepackDirEmpty(t *testing.T, app *transport.App) {
	t.Helper()
	entries, err := os.ReadDir(app.Conf.RepackDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCurationE2EUploadIsCheckedAgainstPostgresTags(t *testing.T) {
	app, l, ctx, db, pgdb, _ := setupCurationE2ETest(t)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040101, []int64{roleIDCurator}, "submitter")

	// The fixture's tags are "A; B", unknown to an empty database.
	sid := uploadTestSubmission(t, l, app, curationFixturePath("test_curation_valid.zip"), submitter.Cookie, nil)
	c := validatorComment(t, ctx, app, submitter.ID, sid)
	require.Equal(t, constants.ActionRequestChanges, c.Action)
	require.Contains(t, *c.Message, "Tag `A` is not a known tag")
	require.Contains(t, *c.Message, "Tag `B` is not a known tag")

	// Tag names are matched case-insensitively.
	createTags(t, ctx, pgdb, submitter.ID, "a", "B")
	sid = uploadTestSubmission(t, l, app, curationFixturePath("test_curation_valid.zip"), submitter.Cookie, nil)
	c = validatorComment(t, ctx, app, submitter.ID, sid)
	require.Equal(t, constants.ActionApprove, c.Action, *c.Message)
	require.Contains(t, *c.Message, "Looks good to me!")
	require.Equal(t, e2eTitle, *searchSubmissionByID(t, ctx, app, sid).CurationTitle)
}

func TestCurationE2EMarkAddedWritesTheDataPack(t *testing.T) {
	app, l, ctx, db, pgdb, postgres := setupCurationE2ETest(t)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040201, []int64{roleIDCurator}, "submitter")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040202, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040203, []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040204, []int64{roleIDModerator}, "adder")
	createTags(t, ctx, pgdb, submitter.ID, "A", "B")

	sid := uploadTestSubmission(t, l, app, curationFixturePath("test_curation_valid.7z"), submitter.Cookie, nil)
	for _, step := range []struct {
		cookie *http.Cookie
		action string
	}{
		{tester.Cookie, constants.ActionAssignTesting},
		{tester.Cookie, constants.ActionApprove},
		{verifier.Cookie, constants.ActionAssignVerification},
		{verifier.Cookie, constants.ActionVerify},
		{adder.Cookie, constants.ActionMarkAdded},
	} {
		rr := addComment(t, l, app, step.cookie, sid, step.action, step.action)
		require.Equal(t, http.StatusOK, rr.Code, step.action+": "+rr.Body.String())
	}
	requireRepackDirEmpty(t, app)

	var gameID, launchCommand, sha string
	var size int64
	require.NoError(t, postgres.QueryRow(ctx, `SELECT game.id, game_data.launch_command, game_data.sha256, game_data.size
		FROM game JOIN game_data ON game_data.game_id = game.id WHERE game.title = $1`, e2eTitle).
		Scan(&gameID, &launchCommand, &sha, &size))
	require.Equal(t, "http://www.sesamestreet.org/ssgames/flash/129.html", launchCommand)

	packs, err := os.ReadDir(app.Conf.DataPacksDir)
	require.NoError(t, err)
	require.Len(t, packs, 1)
	require.True(t, strings.HasPrefix(packs[0].Name(), gameID+"-"), packs[0].Name())
	pack, err := os.ReadFile(filepath.Join(app.Conf.DataPacksDir, packs[0].Name()))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(pack)), sha)
	require.EqualValues(t, len(pack), size)

	zr, err := zip.NewReader(bytes.NewReader(pack), int64(len(pack)))
	require.NoError(t, err)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	require.ElementsMatch(t, []string{
		"content.json",
		"content/www.sesamestreet.org/sites/all/libraries/swfobject/swfobject.js",
		"content/www.sesamestreet.org/ssgames/flash/129.html",
	}, names)
	require.Equal(t, "content.json", names[0])
	r, err := zr.File[0].Open()
	require.NoError(t, err)
	contentJSON, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "{\r\n    \"version\": 1,\r\n    \"uniqueId\": \""+e2eRoot+"\"\r\n}", string(contentJSON))

	// Now that the game exists, its launch command is a duplicate.
	sid = uploadTestSubmission(t, l, app, curationFixturePath("test_curation_valid.zip"), submitter.Cookie, nil)
	c := validatorComment(t, ctx, app, submitter.ID, sid)
	require.Equal(t, constants.ActionRequestChanges, c.Action)
	require.Contains(t, *c.Message, "Identical launch command already present in the master database.")
}

func TestCurationE2EMetaEditWritesANewZip(t *testing.T) {
	app, l, ctx, db, pgdb, postgres := setupCurationE2ETest(t)
	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100040301, []int64{roleIDCurator}, "submitter")
	createTags(t, ctx, pgdb, submitter.ID, "A", "B")

	sid := uploadTestSubmission(t, l, app, curationFixturePath("test_curation_valid.7z"), submitter.Cookie, nil)
	original := searchSubmissionByID(t, ctx, app, sid).CurrentFilename

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("metadata", `{"Title":"Edited: Where Is Thumbkin?"}`))
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/submission/%d/metaedit", sid), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(submitter.Cookie)
	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	requireRepackDirEmpty(t, app)

	sub := searchSubmissionByID(t, ctx, app, sid)
	require.NotEqual(t, original, sub.CurrentFilename)
	require.Equal(t, ".zip", filepath.Ext(sub.CurrentFilename))
	require.Equal(t, "Edited: Where Is Thumbkin?", *sub.CurationTitle)
	require.Len(t, getSubmissionFilesBySubmissionID(t, ctx, db, sid), 2)

	edited := filepath.Join(app.Conf.SubmissionsDirFullPath, sub.CurrentFilename)
	vr, err := curation.New(database.NewPostgresDAL(postgres), t.TempDir(), l).Validate(ctx, edited)
	require.NoError(t, err)
	require.Empty(t, vr.CurationErrors)
	require.Equal(t, "Edited: Where Is Thumbkin?", *vr.Meta.Title)
	require.Equal(t, "Sesame Workshop", *vr.Meta.Developer)
}
