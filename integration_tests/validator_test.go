package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func getWithCookie(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	req, err := http.NewRequest("GET", path, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func TestSubmissionStateMachine_ValidatorMetadataPropagation(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000601), []int64{roleIDCurator}, "submitter/curator")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000602), []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000603), []int64{roleIDTester}, "verifier")
	adder := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000604), []int64{roleIDModerator}, "adder")

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
	submission := searchSubmissionByID(t, ctx, app, sid)

	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	storedMeta, err := db.GetCurationMetaBySubmissionFileID(dbs, submission.FileID)
	require.NoError(t, err)
	require.NotNil(t, storedMeta.Title)
	require.Equal(t, "Test Game", *storedMeta.Title)
	require.NotNil(t, storedMeta.AlternateTitles)
	require.Equal(t, "Test Game Alt", *storedMeta.AlternateTitles)
	require.NotNil(t, storedMeta.CurationNotes)
	require.Equal(t, "Validator supplied curation notes", *storedMeta.CurationNotes)
	require.NotNil(t, storedMeta.MountParameters)
	require.Equal(t, "--proxy=localhost", *storedMeta.MountParameters)
	require.NotNil(t, storedMeta.RuffleSupport)
	require.Equal(t, "Standalone", *storedMeta.RuffleSupport)
	require.NotNil(t, storedMeta.Extreme)
	require.Equal(t, "No", *storedMeta.Extreme)
	require.Len(t, storedMeta.AdditionalApps, 1)
	require.NotNil(t, storedMeta.AdditionalApps[0].Heading)
	require.Equal(t, "Extras Launcher", *storedMeta.AdditionalApps[0].Heading)
	require.NotNil(t, storedMeta.AdditionalApps[0].ApplicationPath)
	require.Equal(t, "extras/launcher.exe", *storedMeta.AdditionalApps[0].ApplicationPath)
	require.NotNil(t, storedMeta.AdditionalApps[0].LaunchCommand)
	require.Equal(t, "--launch-extras", *storedMeta.AdditionalApps[0].LaunchCommand)

	submissionPage := getWithCookie(t, l, app, submitter.Cookie, fmt.Sprintf("/web/submission/%d", sid))
	require.Equal(t, http.StatusOK, submissionPage.Code, submissionPage.Body.String())
	for _, expected := range []string{
		"Test Game",
		"Test Game Alt",
		"Validator supplied game notes",
		"Validator supplied original description",
		"Validator supplied curation notes",
		"Flash",
		"Arcade",
		"Action",
		"Puzzle",
		"Extras Launcher",
		"extras/launcher.exe",
		"--launch-extras",
		"--proxy=localhost",
		"Standalone",
	} {
		require.Contains(t, submissionPage.Body.String(), expected)
	}

	rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "assign testing")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "approve")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionAssignVerification, "assign verification")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = addComment(t, l, app, adder.Cookie, sid, constants.ActionMarkAdded, "mark added")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	submission = searchSubmissionByID(t, ctx, app, sid)
	require.Contains(t, submission.DistinctActions, constants.ActionMarkAdded)

	var gameID string
	err = postgres.QueryRow(ctx, `SELECT id FROM game WHERE title = $1`, "Test Game").Scan(&gameID)
	require.NoError(t, err)

	var (
		title, alternateTitles, series, developer, publisher string
		playMode, status, notes, source                     string
		applicationPath, launchCommand                      string
		releaseDate, version, originalDescription           string
		language, library, primaryPlatform, ruffleSupport   string
	)
	err = postgres.QueryRow(ctx, `SELECT title, alternate_titles, series, developer, publisher, play_mode, status, notes,
			source, application_path, launch_command, release_date, version, original_description, language, library,
			platform_name, ruffle_support
		FROM game WHERE id = $1`, gameID).
		Scan(&title, &alternateTitles, &series, &developer, &publisher, &playMode, &status, &notes,
			&source, &applicationPath, &launchCommand, &releaseDate, &version, &originalDescription, &language, &library,
			&primaryPlatform, &ruffleSupport)
	require.NoError(t, err)
	require.Equal(t, "Test Game", title)
	require.Equal(t, "Test Game Alt", alternateTitles)
	require.Equal(t, "Test Series", series)
	require.Equal(t, "Test Developer", developer)
	require.Equal(t, "Test Publisher", publisher)
	require.Equal(t, "Single Player", playMode)
	require.Equal(t, "Playable", status)
	require.Equal(t, "Validator supplied game notes", notes)
	require.Equal(t, "https://example.com/test-game", source)
	require.Equal(t, "content/test-game.swf", applicationPath)
	require.Equal(t, "Flashpoint/start test-game", launchCommand)
	require.Equal(t, "2020-12-20", releaseDate)
	require.Equal(t, "v1.2.3", version)
	require.Equal(t, "Validator supplied original description", originalDescription)
	require.Equal(t, "en", language)
	require.Equal(t, "arcade", library)
	require.Equal(t, "Flash", primaryPlatform)
	require.Equal(t, "Standalone", ruffleSupport)

	var storedMountParams *string
	var storedDataAppPath, storedDataLaunch string
	err = postgres.QueryRow(ctx, `SELECT parameters, application_path, launch_command
		FROM game_data WHERE game_id = $1 ORDER BY date_added DESC LIMIT 1`, gameID).
		Scan(&storedMountParams, &storedDataAppPath, &storedDataLaunch)
	require.NoError(t, err)
	require.NotNil(t, storedMountParams)
	require.Equal(t, "--proxy=localhost", *storedMountParams)
	require.Equal(t, "content/test-game.swf", storedDataAppPath)
	require.Equal(t, "Flashpoint/start test-game", storedDataLaunch)

	rows, err := postgres.Query(ctx, `SELECT name, application_path, launch_command FROM additional_app WHERE parent_game_id = $1`, gameID)
	require.NoError(t, err)
	defer rows.Close()

	require.True(t, rows.Next())
	var addAppName, addAppPath, addAppLaunch string
	err = rows.Scan(&addAppName, &addAppPath, &addAppLaunch)
	require.NoError(t, err)
	require.Equal(t, "Extras Launcher", addAppName)
	require.Equal(t, "extras/launcher.exe", addAppPath)
	require.Equal(t, "--launch-extras", addAppLaunch)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())

	gameAPI := getWithCookie(t, l, app, adder.Cookie, fmt.Sprintf("/api/game/%s", gameID))
	require.Equal(t, http.StatusOK, gameAPI.Code, gameAPI.Body.String())

	var apiGame types.Game
	err = json.Unmarshal(gameAPI.Body.Bytes(), &apiGame)
	require.NoError(t, err)
	require.Equal(t, gameID, apiGame.ID)
	require.Equal(t, "Test Game", apiGame.Title)
	require.Equal(t, "Test Game Alt", apiGame.AlternateTitles)
	require.Equal(t, "Test Series", apiGame.Series)
	require.Equal(t, "Test Developer", apiGame.Developer)
	require.Equal(t, "Test Publisher", apiGame.Publisher)
	require.Equal(t, "Single Player", apiGame.PlayMode)
	require.Equal(t, "Playable", apiGame.Status)
	require.Equal(t, "Validator supplied game notes", apiGame.Notes)
	require.Equal(t, "https://example.com/test-game", apiGame.Source)
	require.Equal(t, "2020-12-20", apiGame.ReleaseDate)
	require.Equal(t, "v1.2.3", apiGame.Version)
	require.Equal(t, "Validator supplied original description", apiGame.OriginalDesc)
	require.Equal(t, "en", apiGame.Language)
	require.Equal(t, "arcade", apiGame.Library)
	require.Equal(t, "Flash", apiGame.PrimaryPlatform)
	require.Equal(t, "Standalone", apiGame.RuffleSupport)
	require.Len(t, apiGame.Data, 1)
	require.NotNil(t, apiGame.Data[0].Parameters)
	require.Equal(t, "--proxy=localhost", *apiGame.Data[0].Parameters)
	require.Equal(t, "content/test-game.swf", apiGame.Data[0].ApplicationPath)
	require.Equal(t, "Flashpoint/start test-game", apiGame.Data[0].LaunchCommand)
	require.Len(t, apiGame.AddApps, 1)
	require.Equal(t, "Extras Launcher", apiGame.AddApps[0].Name)
	require.Equal(t, "extras/launcher.exe", apiGame.AddApps[0].ApplicationPath)
	require.Equal(t, "--launch-extras", apiGame.AddApps[0].LaunchCommand)
	require.Len(t, apiGame.Tags, 2)
	require.Len(t, apiGame.Platforms, 2)

	tagNames := []string{apiGame.Tags[0].Name, apiGame.Tags[1].Name}
	platformNames := []string{apiGame.Platforms[0].Name, apiGame.Platforms[1].Name}
	require.ElementsMatch(t, []string{"Action", "Puzzle"}, tagNames)
	require.ElementsMatch(t, []string{"Arcade", "Flash"}, platformNames)

	gamePage := getWithCookie(t, l, app, adder.Cookie, fmt.Sprintf("/web/game/%s", gameID))
	require.Equal(t, http.StatusOK, gamePage.Code, gamePage.Body.String())
	for _, expected := range []string{
		"Test Game",
		"Test Game Alt",
		"Validator supplied game notes",
		"Validator supplied original description",
		"Single Player",
		"Playable",
		"https://example.com/test-game",
		"Arcade",
		"Flash",
		"Action",
		"Puzzle",
		"Standalone",
		"Extras Launcher",
		"extras/launcher.exe",
		"--launch-extras",
		"--proxy=localhost",
		"content/test-game.swf",
		"Flashpoint/start test-game",
	} {
		require.Contains(t, gamePage.Body.String(), expected)
	}
}
