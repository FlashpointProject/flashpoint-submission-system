package integration_tests

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func readUserStatistics(t *testing.T, f *sqlFixture) map[string]*types.UserStatistics {
	t.Helper()
	var rows []*types.UserStatistics
	f.InTx(t, func(s database.DBSession) {
		var err error
		rows, err = f.DB.GetAllUserStatistics(s)
		require.NoError(t, err)
	})
	result := map[string]*types.UserStatistics{}
	for _, row := range rows {
		require.NotContains(t, result, row.UserID)
		result[row.UserID] = row
	}
	return result
}

func TestUserStatisticsBulkCountsAndHistory(t *testing.T) {
	f := newSQLFixture(t)
	for _, id := range []int64{101, 102, 103, 104} {
		f.User(t, id, "statistics user")
	}
	// Each submission has a different resulting state; all action counts are
	// historical, but submission columns count the effective current state.
	histories := [][]string{{}, {"request-changes"}, {"approve"}, {"approve", "verify"}, {"approve", "verify", "mark-added"}, {"approve", "verify", "mark-added", "reject"}}
	for i, actions := range histories {
		sid := int64(500 + i)
		f.Submission(t, sid, "staff")
		f.File(t, fixtureFile{ID: sid, SubmissionID: sid, UserID: 101, At: fixtureEpoch})
		f.Comment(t, sid*100, sid, 101, constants.ActionUpload, fixtureEpoch, nil)
		bot := constants.ActionApprove
		if i == 1 {
			bot = constants.ActionRequestChanges
		}
		f.Comment(t, sid*100+1, sid, constants.ValidatorID, bot, fixtureEpoch.Add(time.Second), nil)
		for j, action := range actions {
			f.Comment(t, sid*100+int64(j)+2, sid, 102, action, fixtureEpoch.Add(time.Duration(j+2)*time.Second), nil)
		}
		f.Rebuild(t, sid)
	}
	// Historical user actions remain visible even when another person is the
	// latest commenter. Assignment/unassignment still advance last activity.
	f.Comment(t, 90001, 500, 103, "comment", fixtureEpoch.Add(10*time.Second), utils.StrPtr("older comment"))
	f.Comment(t, 90002, 500, 103, "assign-testing", fixtureEpoch.Add(11*time.Second), nil)
	f.Comment(t, 90003, 500, 103, "unassign-testing", fixtureEpoch.Add(12*time.Second), nil)
	f.Comment(t, 90004, 500, 102, "comment", fixtureEpoch.Add(13*time.Second), nil)
	f.Comment(t, 90005, 500, 103, "comment", fixtureEpoch.Add(99*time.Second), nil)
	deleteFixtureComment(t, f, 90005)
	// A newer upload by another person does not move original ownership.
	f.File(t, fixtureFile{ID: 800, SubmissionID: 500, UserID: 102, At: fixtureEpoch.Add(20 * time.Second)})
	f.Comment(t, 90006, 500, 102, constants.ActionUpload, fixtureEpoch.Add(20*time.Second), nil)
	f.Rebuild(t, 500)
	// Deleted submissions disappear from submission totals, but live action
	// records on them still count, matching existing action-count semantics.
	f.Submission(t, 600, "staff")
	f.File(t, fixtureFile{ID: 600, SubmissionID: 600, UserID: 101, At: fixtureEpoch})
	f.Comment(t, 60001, 600, 103, "comment", fixtureEpoch.Add(15*time.Second), nil)
	f.Rebuild(t, 600)
	_, err := f.Maria.Exec("UPDATE submission SET deleted_at=? WHERE id=600", fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	// Exercise the historical duplicate-cache case explicitly.
	_, err = f.Maria.Exec("INSERT INTO submission_cache SELECT * FROM submission_cache WHERE fk_submission_id=503")
	require.NoError(t, err)
	// Metadata and legacy rows cannot multiply statistics.
	f.Legacy(t, 1, fixtureMeta("legacy"), fixtureEpoch, fixtureEpoch)
	rows := readUserStatistics(t, f)
	owner := rows["101"]
	require.EqualValues(t, 6, owner.SubmissionsCount)
	require.EqualValues(t, 5, owner.SubmissionsBotHappyCount) // latest bot action remains effective after a new upload
	require.EqualValues(t, 1, owner.SubmissionsBotUnhappyCount)
	require.EqualValues(t, 1, owner.SubmissionsRequestedChangesCount)
	require.EqualValues(t, 1, owner.SubmissionsApprovedCount)
	require.EqualValues(t, 1, owner.SubmissionsVerifiedCount)
	require.EqualValues(t, 1, owner.SubmissionsAddedToFlashpointCount)
	require.EqualValues(t, 1, owner.SubmissionsRejectedCount)
	reviewer := rows["102"]
	require.Zero(t, reviewer.SubmissionsCount)
	require.EqualValues(t, 1, reviewer.UserCommentedCount)
	require.EqualValues(t, 1, reviewer.UserRequestedChangesCount)
	require.EqualValues(t, 4, reviewer.UserApprovedCount)
	require.EqualValues(t, 3, reviewer.UserVerifiedCount)
	require.EqualValues(t, 2, reviewer.UserAddedToFlashpointCount)
	require.EqualValues(t, 1, reviewer.UserRejectedCount)
	require.Equal(t, fixtureEpoch.Add(20*time.Second), reviewer.LastUserActivity)
	require.EqualValues(t, 2, rows["103"].UserCommentedCount)
	require.Equal(t, fixtureEpoch.Add(15*time.Second), rows["103"].LastUserActivity)
	require.Equal(t, &types.UserStatistics{UserID: "104", Username: "statistics user", Role: "User"}, rows["104"])
	// Deleting the first upload transfers ownership to the oldest active file.
	_, err = f.Maria.Exec("UPDATE submission_file SET deleted_at=? WHERE id=500", fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	f.Rebuild(t, 500)
	rows = readUserStatistics(t, f)
	require.EqualValues(t, 5, rows["101"].SubmissionsCount)
	require.EqualValues(t, 1, rows["102"].SubmissionsCount)
	var columns string
	require.NoError(t, f.Maria.QueryRow(`SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='comment' AND INDEX_NAME='idx_comment_user_statistics'`).Scan(&columns))
	require.Equal(t, "deleted_at,fk_user_id,fk_action_id,created_at", columns)
	canceled, cancel := context.WithCancel(f.Ctx)
	cancel()
	_, err = f.DB.NewSession(canceled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestUserStatisticsBulkHTTPActivityAndRoles(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	f := &sqlFixture{DB: db, Maria: maria, Ctx: context.WithValue(ctx, utils.CtxKeys.Log, l)}
	const ownerID int64 = 9007199254741001
	token := createTestUser(t, ctx, l, app, db, pgdb, ownerID, []int64{roleIDCurator, roleIDTrialCurator})
	cookie := createTestCookie(t, l, token)
	trial := createTestUser(t, ctx, l, app, db, pgdb, 102, []int64{roleIDTrialCurator})
	audit := createTestUser(t, ctx, l, app, db, pgdb, 103, nil)
	createTestUser(t, ctx, l, app, db, pgdb, 1002, []int64{roleIDCurator})
	createTestUser(t, ctx, l, app, db, pgdb, 1003, nil)
	// Exercise exact role matching; default DB collation is case insensitive.
	f.User(t, 104, "case sensitive role")
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, db.StoreDiscordServerRoles(s, []types.DiscordRole{{ID: 99901, Name: "curator", Color: "#000000"}}))
		require.NoError(t, db.StoreDiscordUserRoles(s, 104, []int64{99901}))
	})
	f.Submission(t, 501, "staff")
	f.File(t, fixtureFile{ID: 501, SubmissionID: 501, UserID: ownerID, At: fixtureEpoch})
	f.Comment(t, 1, 501, ownerID, "upload-file", fixtureEpoch, nil)
	f.Comment(t, 2, 501, 102, "assign-testing", fixtureEpoch.Add(time.Second), nil)
	f.Comment(t, 3, 501, 103, "comment", fixtureEpoch.Add(5*time.Second), nil)
	f.Rebuild(t, 501)
	for _, event := range []struct {
		uid             int64
		area, operation string
		seconds         int
	}{
		{ownerID, "submission", "delete", 10}, {ownerID, "submission", "read", 12},
		{ownerID, "auth", "update", 100}, {102, "submission", "update", 9},
		{103, "submission", "update", 2},
	} {
		_, err := postgres.Exec(ctx, "INSERT INTO activity_events (uid,created_at,event_area,event_operation,event_data) VALUES ($1,$2,$3,$4,'{}')", event.uid, fixtureEpoch.Add(time.Duration(event.seconds)*time.Second), event.area, event.operation)
		require.NoError(t, err)
	}
	rr := getWithCookie(t, l, app, cookie, "/api/user-statistics/all")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var data types.UserStatisticsResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &data))
	require.False(t, data.GeneratedAt.IsZero())
	rows := map[string]*types.UserStatistics{}
	for _, row := range data.Users {
		rows[row.UserID] = row
	}
	require.Equal(t, "Staff", rows[strconv.FormatInt(ownerID, 10)].Role)
	require.Equal(t, fixtureEpoch.Add(12*time.Second), rows[strconv.FormatInt(ownerID, 10)].LastUserActivity)
	require.Equal(t, "Trial Curator", rows["102"].Role)
	require.Equal(t, fixtureEpoch.Add(9*time.Second), rows["102"].LastUserActivity)
	require.Equal(t, fixtureEpoch.Add(5*time.Second), rows["103"].LastUserActivity, "older event cannot erase newer comment")
	require.EqualValues(t, 1, rows["103"].UserCommentedCount)
	require.Equal(t, "User", rows["104"].Role)
	require.Equal(t, "User", rows["1002"].Role)
	require.Equal(t, "Staff", rows["1003"].Role)
	// Auth is checked even for a populated shared cache; page loading needs
	// only this endpoint and is accessible to the same roles as before.
	for _, auth := range []*http.Cookie{cookie, createTestCookie(t, l, trial), createTestCookie(t, l, audit)} {
		got := getWithCookie(t, l, app, auth, "/api/user-statistics/all")
		require.Equal(t, http.StatusOK, got.Code, got.Body.String())
		require.JSONEq(t, rr.Body.String(), got.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/user-statistics/all", nil)
	denied := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(denied, req)
	require.Equal(t, http.StatusUnauthorized, denied.Code, denied.Body.String())
	// A separate session with no users:read scope must also be rejected.
	restricted := createTestUser(t, ctx, l, app, db, pgdb, 105, nil)
	_, err := maria.Exec("UPDATE session SET scope=? WHERE secret=?", types.AuthScopeIdentity, restricted.Secret)
	require.NoError(t, err)
	denied = getWithCookie(t, l, app, createTestCookie(t, l, restricted), "/api/user-statistics/all")
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
}

func TestUserStatisticsBrowser(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	tmpl, err := template.ParseFiles(filepath.Join(root, "templates/user-statistics.gohtml"))
	require.NoError(t, err)
	var html bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&html, "main", nil))
	script, err := os.ReadFile(filepath.Join(root, "static/js.js"))
	require.NoError(t, err)
	styles, err := os.ReadFile(filepath.Join(root, "static/styles.css"))
	require.NoError(t, err)
	input, err := json.Marshal(map[string]string{"html": html.String(), "script": string(script), "styles": string(styles)})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "integration_tests/browser/user-statistics.cjs"))
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}
