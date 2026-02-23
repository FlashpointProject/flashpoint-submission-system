package integration_tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/resumableuploadservice"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/securecookie"
	"github.com/stretchr/testify/require"
)

var DiscordServerRoles = []types.DiscordRole{
	{
		ID:    432708847304704010,
		Name:  "@everyone",
		Color: "#000000",
	},
	{
		ID:    441043545735036929,
		Name:  "Administrator",
		Color: "#3498db",
	},
	{
		ID:    442462642599231499,
		Name:  "Moderator",
		Color: "#9b59b6",
	},
	{
		ID:    442665038642413569,
		Name:  "Curator",
		Color: "#f1c40f",
	},
	{
		ID:    442987546046103562,
		Name:  "Hacker",
		Color: "#b11515",
	},
	{
		ID:    442988314480476170,
		Name:  "Tester",
		Color: "#e67e22",
	},
	{
		ID:    451454116393254912,
		Name:  "The Moe",
		Color: "#e0a4f1",
	},
	{
		ID:    453048638646648833,
		Name:  "VIP",
		Color: "#d6047f",
	},
	{
		ID:    454640479317917696,
		Name:  "The Blue",
		Color: "#385f84",
	},
	{
		ID:    475413811394904074,
		Name:  "Archivist",
		Color: "#fa8072",
	},
	{
		ID:    477773789724409861,
		Name:  "Mechanic",
		Color: "#748e9a",
	},
	{
		ID:    478369603622273024,
		Name:  "Notification Squad",
		Color: "#000000",
	},
	{
		ID:    546894295962222622,
		Name:  "Hunter",
		Color: "#1f8b4c",
	},
	{
		ID:    569328799318016018,
		Name:  "Trial Curator",
		Color: "#000000",
	},
	{
		ID:    581828606502764565,
		Name:  "Nitro Booster",
		Color: "#f47fff",
	},
	{
		ID:    602334900623900672,
		Name:  "Virus Destroyer",
		Color: "#71368a",
	},
	{
		ID:    636078914493480960,
		Name:  "MOTAS Finder",
		Color: "#3498db",
	},
	{
		ID:    643230868768423976,
		Name:  "Archiver",
		Color: "#000000",
	},
	{
		ID:    666444964120494080,
		Name:  "Former Staff",
		Color: "#607d8b",
	},
	{
		ID:    703740452385325136,
		Name:  "Translator",
		Color: "#01e8fc",
	},
	{
		ID:    810221698179792897,
		Name:  "BlueBot",
		Color: "#000000",
	},
	{
		ID:    830110904561041428,
		Name:  "Bunnyposter",
		Color: "#2ecc71",
	},
	{
		ID:    852181478044598273,
		Name:  "PF Scrape",
		Color: "#000000",
	},
	{
		ID:    855487139029319681,
		Name:  "FPFSS Notification Service",
		Color: "#ff00ec",
	},
	{
		ID:    855488531802750996,
		Name:  "The D",
		Color: "#008080",
	},
	{
		ID:    871819872408055828,
		Name:  "Developer",
		Color: "#7477ce",
	},
	{
		ID:    880181541089738844,
		Name:  "Guardian",
		Color: "#000000",
	},
	{
		ID:    880203664822775829,
		Name:  "Bot",
		Color: "#ff0000",
	},
	{
		ID:    914324576434028574,
		Name:  "Helper",
		Color: "#00ff8d",
	},
	{
		ID:    921852025904435251,
		Name:  "Donator",
		Color: "#ffe4a8",
	},
	{
		ID:    1007648354437693581,
		Name:  "Forbidden knowledge",
		Color: "#000000",
	},
	{
		ID:    1008934569547927693,
		Name:  "Linux",
		Color: "#000000",
	},
	{
		ID:    1008934759679922267,
		Name:  "Mac (Intel)",
		Color: "#000000",
	},
	{
		ID:    1008934935492558869,
		Name:  "Mac (Apple Silicon)",
		Color: "#000000",
	},
	{
		ID:    1021367134623907844,
		Name:  "Flashpoint Mail",
		Color: "#000000",
	},
	{
		ID:    1058518177140711526,
		Name:  "CredBot",
		Color: "#000000",
	},
	{
		ID:    1058757300967448680,
		Name:  "Flashpoint GOTD",
		Color: "#000000",
	},
	{
		ID:    1084270358321963078,
		Name:  "The Mentioner",
		Color: "#000000",
	},
	{
		ID:    1101806666380496926,
		Name:  "Trial Editor",
		Color: "#1abc9c",
	},
	{
		ID:    1126109274418987048,
		Name:  "International Manager",
		Color: "#e74c3c",
	},
	{
		ID:    1128307753459392513,
		Name:  "Editor",
		Color: "#1abc9c",
	},
	{
		ID:    1133654438729494532,
		Name:  "Doomposter",
		Color: "#000001",
	},
	{
		ID:    1133654698600181770,
		Name:  "Hopeposter",
		Color: "#c27c0e",
	},
	{
		ID:    1133852834991972444,
		Name:  "Flashpoint Ultimate",
		Color: "#000000",
	},
	{
		ID:    1136910341603856427,
		Name:  "deleted-role",
		Color: "#858585",
	},
	{
		ID:    1136941292031594587,
		Name:  "Donator",
		Color: "#ffe4a8",
	},
	{
		ID:    1148134067137695777,
		Name:  "CredBot",
		Color: "#000000",
	},
	{
		ID:    1153356678843093244,
		Name:  "9o3o",
		Color: "#000000",
	},
	{
		ID:    1158439542420951153,
		Name:  "DJ Kakabus",
		Color: "#000000",
	},
	{
		ID:    1159895585336328245,
		Name:  "Craig",
		Color: "#000000",
	},
	{
		ID:    1172153393544962100,
		Name:  "NGAU",
		Color: "#000000",
	},
	{
		ID:    1203384090733056000,
		Name:  "Interviewer",
		Color: "#000000",
	},
	{
		ID:    1203384239341572186,
		Name:  "new role",
		Color: "#000000",
	},
	{
		ID:    1403105517194313859,
		Name:  "Media Poster",
		Color: "#000000",
	},
	{
		ID:    1410724183876571157,
		Name:  "new role",
		Color: "#000000",
	},
	{
		ID:    1410724266164359259,
		Name:  "Raven",
		Color: "#e8acc7",
	},
}

func setupTestEnvironment(t *testing.T) {
	// replace relative paths with absolute paths in env
	cwd, err := os.Getwd()
	require.NoError(t, err)
	data, err := os.ReadFile("./testenv.env")
	require.NoError(t, err)
	data = bytes.ReplaceAll(data, []byte("=./"), []byte("="+cwd+"/"))
	err = os.WriteFile("./absolute.env", data, 0644)
	require.NoError(t, err)

	err = godotenv.Overload("./absolute.env")
	require.NoError(t, err)

	// Create directories
	dirs := []string{
		"./test_data/resumable",
		"./test_data/ingest",
		"./test_data/submissions",
		"./test_data/images",
		"./test_data/datapacks",
		"./test_data/frozen",
		"./test_data/images_path",
		"./test_data/deleted_datapacks",
		"./test_data/deleted_images",
		"./test_data/repack_temp",
	}

	for _, d := range dirs {
		err := os.MkdirAll(d, 0777)
		require.NoError(t, err)
	}

	// Create symlinks for templates
	if _, err := os.Lstat("templates"); os.IsNotExist(err) {
		err = os.Symlink("../templates", "templates")
		require.NoError(t, err)
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func recreateTestDatabases(t *testing.T) {
	fmt.Println("setting up databases...")

	// Start DBs
	fmt.Println("rebuilding...")
	err := runCommand("make", "rebuild-db")
	require.NoError(t, err, "failed to rebuild mysql")

	// Wait for DBs to be ready
	fmt.Println("waiting for databases to come online")
	conf := config.GetConfig(nil)
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	mysqlReady := false
	postgresReady := false

	for !mysqlReady || !postgresReady {
		select {
		case <-timeout:
			require.Fail(t, "timed out waiting for databases to be ready")
		case <-ticker.C:
			if !mysqlReady {
				db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?multiStatements=true", conf.DBUser, conf.DBPassword, conf.DBIP, conf.DBPort, conf.DBName))
				if err == nil {
					if err := db.Ping(); err == nil {
						mysqlReady = true
						fmt.Println("mysql ready")
					}
					db.Close()
				}
			}
			if !postgresReady {
				// postgres://user:password@host:port/dbname?sslmode=disable
				// In postgresdal.go, dbname is set to user.
				connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", conf.PostgresUser, conf.PostgresPassword, conf.PostgresHost, conf.PostgresPort, conf.PostgresUser)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				pool, err := pgxpool.New(ctx, connStr)
				cancel()
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := pool.Ping(ctx); err == nil {
						postgresReady = true
						fmt.Println("postgres ready")
					}
					cancel()
					pool.Close()
				}
			}
		}
	}

	// Run migrations
	fmt.Println("running migrations")
	err = runCommand("make", "migrate")
	require.NoError(t, err, "failed to run migrations")

	fmt.Println("databases OK")
}

func createTestUser(t *testing.T, ctx context.Context, l *logrus.Entry, app *transport.App, db database.DAL, pgdb database.PGDAL, uid int64, roles []int64) *service.AuthToken {
	l.Infof("creating test user %v", uid)
	// start session
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)

	pgdbs, err := pgdb.NewSession(ctx)
	require.NoError(t, err)

	discordUser := &types.DiscordUser{
		ID:       uid,
		Username: fmt.Sprintf("user_%d", uid),
		Avatar:   "no_avatar",
	}

	// save discord user data
	err = db.StoreDiscordUser(dbs, discordUser)
	require.NoError(t, err)

	// enable all notifications for a new user
	err = db.StoreNotificationSettings(dbs, discordUser.ID, constants.GetActionsWithNotification())
	require.NoError(t, err)

	// save discord roles
	err = db.StoreDiscordServerRoles(dbs, DiscordServerRoles)
	require.NoError(t, err)

	err = db.StoreDiscordUserRoles(dbs, discordUser.ID, roles)
	require.NoError(t, err)

	// create cookie and save session
	authTokenProvider := service.NewAuthTokenProvider()
	authToken, err := authTokenProvider.CreateAuthToken(discordUser.ID)
	require.NoError(t, err)

	err = db.StoreSession(dbs, authToken.Secret, discordUser.ID, 86400, types.AuthScopeAll, "FPFSS", "192.168.0.1")
	require.NoError(t, err)

	err = app.Service.EmitAuthLoginEvent(pgdbs, discordUser.ID)
	require.NoError(t, err)

	err = pgdbs.Commit()
	require.NoError(t, err)

	err = dbs.Commit()
	require.NoError(t, err)

	return authToken
}

func createTestCookie(t *testing.T, l *logrus.Entry, authToken *service.AuthToken) *http.Cookie {
	l.Infof("creating cookie for user %v", authToken.UserID)
	codec := securecookie.New([]byte(os.Getenv("SECURECOOKIE_HASH_KEY_CURRENT")), []byte(os.Getenv("SECURECOOKIE_BLOCK_KEY_CURRENT")))
	encoded, err := securecookie.EncodeMulti(utils.Cookies.Login, service.MapAuthToken(authToken), codec)
	require.NoError(t, err)

	cookie := &http.Cookie{
		Name:     utils.Cookies.Login,
		Value:    encoded,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		MaxAge:   86400,
	}
	return cookie
}

func uploadTestSubmission(t *testing.T, l *logrus.Entry, app *transport.App, filename string, cookie *http.Cookie, sid *int64) int64 {
	if sid == nil {
		l.Infof("uploading submission")
	} else {
		l.Infof("uploading submission %d", *sid)
	}

	// Prepare file upload
	filePath := filename
	fileContent, err := os.ReadFile(filePath)
	require.NoError(t, err)
	fileSize := int64(len(fileContent))

	// We need to simulate chunk upload. Since it's small enough, one chunk.
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	part.Write(fileContent)
	writer.Close()

	// If submission ID is provided, we're updating a submission, not submitting a new one.
	url := "/api/submission-receiver-resumable"
	if sid != nil {
		url = fmt.Sprintf("/api/submission-receiver-resumable/%d", *sid)
	}

	req, err := http.NewRequest("POST", url, body)
	require.NoError(t, err)

	q := req.URL.Query()
	q.Add("resumableChunkNumber", "1")
	q.Add("resumableChunkSize", strconv.FormatInt(16*1024*1024, 10)) // Size of full chunk
	q.Add("resumableCurrentChunkSize", strconv.FormatInt(fileSize, 10))
	q.Add("resumableTotalSize", strconv.FormatInt(fileSize, 10))
	q.Add("resumableType", "application/x-7z-compressed")
	q.Add("resumableIdentifier", fmt.Sprintf("%d-%s", fileSize, "test-file-id"))
	q.Add("resumableFilename", filename)
	q.Add("resumableRelativePath", filename)
	q.Add("resumableTotalChunks", "1")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	// Verify response
	var resp map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "success", resp["message"])
	tempName := resp["temp_name"].(string)
	require.NotEmpty(t, tempName)

	l.Infof("upload successful, tempName: %s", tempName)

	// Wait for processing (it runs in goroutine)
	// We can poll the database or the status endpoint.
	// Since we have the `ss` (SiteService), we can check the status keeper `ss.SSK`.

	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var submissionID int64

	done := false
	for !done {
		select {
		case <-timeout:
			require.Fail(t, "timeout waiting for submission processing")
		case <-ticker.C:
			status := app.Service.SSK.Get(tempName)
			if status != nil {
				msg := "<nil>"
				if status.Message != nil {
					msg = *status.Message
				}
				l.Infof("status: %s, message: %s", status.Status, msg)
				if status.Status == "success" {
					if status.SubmissionID != nil {
						submissionID = *status.SubmissionID
						done = true
					}
				} else if status.Status == "failed" {
					require.Failf(t, "submission failed: %s", msg)
				}
			}
		}
	}

	require.NotZero(t, submissionID)
	l.Infof("submission ID: %v", submissionID)

	return submissionID
}

func initTestApp(t *testing.T, l *logrus.Entry, conf *config.Config, maria *sql.DB, postgres *pgxpool.Pool) *transport.App {
	authBotMock := &MockDiscordRoleReader{}
	notifBotMock := &MockDiscordNotificationSender{}

	// Mock Validator
	validatorMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.Infof("Mock Request: %s %s", r.Method, r.URL.String())

		if r.URL.Path == "/tags" {
			l.Infof("Mock: Handling /tags")
			json.NewEncoder(w).Encode(types.ValidatorTagResponse{Tags: []types.Tag{}})
			return
		}

		path := r.URL.Query().Get("path")
		if path == "" {
			path = "/tmp/dummy_path"
		}
		l.Infof("Mock: path param=%s", path)

		if r.URL.Path == "/pack-path" {
			l.Infof("Mock: Handling /pack-path")
			resp := types.ValidatorRepackResponse{
				FilePath: &path,
				Meta: types.CurationMeta{
					Title:           utils.StrPtr("Test Game"),
					LaunchCommand:   utils.StrPtr("http://test.com"),
					ApplicationPath: utils.StrPtr("fp"),
					ReleaseDate:     utils.StrPtr("2020-12-20"),
					Tags:            utils.StrPtr("a;b;c"),
					Platform:        utils.StrPtr("a"),
					// TODO also fill the rest of metadata
				},
				Images: []types.ValidatorResponseImage{
					{Type: "logo", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="},
					{Type: "screenshot", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="},
				},
			}
			b, _ := json.Marshal(resp)
			l.Infof("Mock JSON: %s", string(b))
			w.Write(b)
			return
		}

		l.Infof("Mock: Handling default")
		// Respond with a valid ValidatorResponse
		resp := types.ValidatorResponse{
			Path: path,
			Meta: types.CurationMeta{
				Title:           utils.StrPtr("Test Game"),
				LaunchCommand:   utils.StrPtr("http://test.com"),
				ApplicationPath: utils.StrPtr("fp"),
				ReleaseDate:     utils.StrPtr("2020-12-20"), // TODO a test where this is not set
				Tags:            utils.StrPtr("a;b;c"),
				Platform:        utils.StrPtr("a"),
				// TODO also fill the rest of metadata
			},
			CurationErrors:   []string{},
			CurationWarnings: []string{},
			Images: []types.ValidatorResponseImage{
				{Type: "logo", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="},
				{Type: "screenshot", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	// We don't defer Close() here because app lives longer than this function scope,
	// but for tests it's fine as it will die with the process. Ideally we return a cleanup func.
	conf.ValidatorServerURL = validatorMock.URL

	// Create ResumableUploadService
	rsu, err := resumableuploadservice.New(conf.ResumableUploadDirFullPath)
	require.NoError(t, err)
	defer rsu.Close()

	ss := service.NewWithMocks(l, maria, postgres, authBotMock, notifBotMock, conf.ValidatorServerURL, conf.SessionExpirationSeconds, conf.SubmissionsDirFullPath, conf.SubmissionImagesDirFullPath, conf.IsDev, rsu, conf.ArchiveIndexerServerURL, conf.DataPacksDir)

	app := &transport.App{
		Conf:    conf,
		Service: ss,
		CC: utils.CookieCutter{
			Previous: securecookie.New([]byte(conf.SecurecookieHashKeyPrevious), []byte(conf.SecurecookieBlockKeyPrevious)),
			Current:  securecookie.New([]byte(conf.SecurecookieHashKeyCurrent), []byte(conf.SecurecookieBlockKeyCurrent)),
		},
	}
	app.InitializeMux()

	return app
}

func verifyTestSubmissionExists(t *testing.T, ctx context.Context, l *logrus.Entry, app *transport.App, uid, sid int64) {
	l.Infof("verifying that submission %d exists", sid)

	// Check submission exists
	submissions, _, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
	require.NoError(t, err)
	require.Len(t, submissions, 1)
	sub := submissions[0]
	require.Equal(t, uid, sub.SubmitterID)

	// Verify file on disk
	expectedPath := filepath.Join(app.Conf.SubmissionsDirFullPath, sub.CurrentFilename)
	_, err = os.Stat(expectedPath)
	require.NoError(t, err, "submission file should exist on disk")
}

func setupIntegrationTest(t *testing.T) (*transport.App, *logrus.Entry, context.Context, database.DAL, database.PGDAL, *sql.DB, *pgxpool.Pool) {
	ctx := context.Background()

	fmt.Println("setting up environment...")
	setupTestEnvironment(t)
	recreateTestDatabases(t)

	fmt.Println("init logger...")
	log := logging.InitLogger()
	l := log.WithField("test", "integration")
	conf := config.GetConfig(l)

	// Connect to DB
	maria := database.OpenDB(l, conf)
	postgres := database.OpenPostgresDB(l, conf)

	db := database.NewMysqlDAL(maria)
	pgdb := database.NewPostgresDAL(postgres)

	app := initTestApp(t, l, conf, maria, postgres)

	return app, l, ctx, db, pgdb, maria, postgres
}

func addContextValues(ctx context.Context, l *logrus.Entry, uid int64, rid string) context.Context {
	rctx := context.WithValue(ctx, utils.CtxKeys.Log, l)
	rctx = context.WithValue(rctx, utils.CtxKeys.RequestID, rid)
	rctx = context.WithValue(rctx, utils.CtxKeys.UserID, uid)
	return rctx
}

func addComment(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid int64, action, message string) *httptest.ResponseRecorder {
	// 1. Construct the form values using url.Values
	data := url.Values{}
	data.Set("action", action)
	data.Set("message", message)
	data.Set("ignore-duplicate-actions", "false")

	// 2. Encode the data into the request body
	url := fmt.Sprintf("/api/submission-batch/%d/comment", sid)
	req, err := http.NewRequest("POST", url, strings.NewReader(data.Encode()))
	require.NoError(t, err)

	// 3. Set the correct Content-Type so the server knows how to parse the body
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}
