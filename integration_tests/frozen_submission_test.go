package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func frozenRequest(t *testing.T, l *logrus.Entry, app *transport.App, user *extendedTestUser, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if user != nil {
		req.AddCookie(user.Cookie)
	}
	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func editFrozenTestMetadata(t *testing.T, l *logrus.Entry, app *transport.App, user *extendedTestUser, sid int64) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("metadata", `{"Title":"Edited frozen-access fixture"}`))
	require.NoError(t, writer.Close())
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/submission/%d/metaedit", sid), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(user.Cookie)
	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func TestFrozenSubmissionReadAccessAndCacheIsolation(t *testing.T) {
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020001, []int64{roleIDTrialCurator}, "original uploader")
	updater := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020002, []int64{roleIDCurator}, "later uploader")
	editor := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020003, []int64{roleIDCurator}, "metadata editor")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020004, []int64{roleIDModerator}, "moderator")
	stranger := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020005, []int64{roleIDTester}, "commenter only")
	trial := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020006, []int64{roleIDTrialCurator}, "other trial")
	audit := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020007, nil, "other audit")
	admin := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020008, []int64{441043545735036929}, "administrator")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	original := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0]
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", updater.Cookie, &sid)
	rr := editFrozenTestMetadata(t, l, app, editor, sid)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	files := getSubmissionFilesBySubmissionID(t, ctx, db, sid)
	require.Len(t, files, 3)
	require.Equal(t, editor.ID, files[0].SubmitterID)
	require.Equal(t, "meta-edit.pack", files[0].OriginalFilename)
	rr = addComment(t, l, app, stranger.Cookie, sid, constants.ActionComment, "private frozen comment sentinel")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	// An uploader whose only version was removed must lose frozen read access.
	rr = softDeleteSubmissionFile(t, l, app, moderator.Cookie, sid, original.FileID, "superseded")
	require.Equal(t, 204, rr.Code, rr.Body.String())
	files = getSubmissionFilesBySubmissionID(t, ctx, db, sid)
	require.Len(t, files, 2)
	detail := func(user *extendedTestUser) types.ViewSubmissionPageData {
		t.Helper()
		rr := frozenRequest(t, l, app, user, "GET", fmt.Sprintf("/api/submission/%d", sid))
		require.Equal(t, 200, rr.Code, rr.Body.String())
		require.Equal(t, "private, no-store", rr.Header().Get("Cache-Control"))
		var data types.ViewSubmissionPageData
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &data))
		return data
	}
	// Populate the shared caches before freezing, including an unrelated viewer.
	for _, user := range []*extendedTestUser{stranger, moderator, owner, editor} {
		require.True(t, detail(user).CanReadDetails)
	}
	rr = freezeSubmission(t, l, app, moderator.Cookie, sid)
	require.Equal(t, 204, rr.Code, rr.Body.String())
	// Exercise both access orders repeatedly against the same cached submission.
	for _, tc := range []struct {
		user    *extendedTestUser
		allowed bool
	}{
		{stranger, false}, {admin, true}, {owner, false}, {trial, false}, {moderator, true}, {audit, false}, {updater, true}, {editor, true}, {stranger, false}, {owner, false},
	} {
		t.Run(fmt.Sprintf("viewer-%d-%t", tc.user.ID, tc.allowed), func(t *testing.T) {
			data := detail(tc.user)
			require.Equal(t, tc.user.ID, data.UserID)
			require.True(t, data.Submissions[0].IsFrozen)
			require.Equal(t, tc.allowed, data.CanReadDetails)
			page := frozenRequest(t, l, app, tc.user, "GET", fmt.Sprintf("/web/submission/%d", sid))
			require.Equal(t, 200, page.Code, page.Body.String())
			require.Equal(t, "private, no-store", page.Header().Get("Cache-Control"))
			html := page.Body.String()
			require.Contains(t, html, "This submission is frozen.")
			require.Contains(t, html, "Test Game")
			require.NotContains(t, html, `id="submission-update-heading"`)
			require.NotContains(t, html, `onclick="overrideBot(`)
			require.NotContains(t, html, `onclick="deleteSubmission(`)
			require.NotContains(t, html, `onclick="deleteComment(`)
			if tc.allowed {
				require.NotNil(t, data.CurationMeta)
				require.NotEmpty(t, data.Comments)
				require.NotEmpty(t, data.CurationImageIDs)
				require.Contains(t, html, "private frozen comment sentinel")
				require.Contains(t, html, "Validator supplied game notes")
				require.Contains(t, html, `aria-label="Current submission file"`)
			} else {
				require.Nil(t, data.CurationMeta)
				require.Empty(t, data.Comments)
				require.Empty(t, data.CurationImageIDs)
				require.Empty(t, data.TagList)
				require.NotContains(t, html, "private frozen comment sentinel")
				require.NotContains(t, html, "Validator supplied game notes")
				require.NotContains(t, html, "/curation-image/")
			}
			if tc.user != moderator && tc.user != admin {
				require.NotContains(t, html, `id="batch-comment"`)
			}
			want := http.StatusUnauthorized
			if tc.allowed {
				want = http.StatusOK
			}
			for _, prefix := range []string{"web", "api"} {
				history := frozenRequest(t, l, app, tc.user, "GET", fmt.Sprintf("/%s/submission/%d/files", prefix, sid))
				require.Equal(t, want, history.Code, history.Body.String())
				if tc.allowed {
					require.Contains(t, history.Body.String(), files[0].SHA256Sum)
					require.NotContains(t, history.Body.String(), "deleteSubmissionFile(")
				} else {
					require.NotContains(t, history.Body.String(), files[0].SHA256Sum)
				}
			}
			for _, file := range files {
				download := downloadSubmissionFile(t, l, app, tc.user.Cookie, sid, file.FileID)
				require.Equal(t, want, download.Code, download.Body.String())
				if tc.allowed {
					requireDownloadedFileMatchesStoredFile(t, app, download, file)
					require.Equal(t, "private, no-store", download.Header().Get("Cache-Control"))
				}
			}
			batch := downloadSubmissionBatch(t, l, app, tc.user.Cookie, []int64{files[0].FileID, files[1].FileID})
			require.Equal(t, want, batch.Code)
			images := getCurationImageIDsBySubmissionID(t, maria, sid)
			image := downloadCurationImage(t, l, app, tc.user.Cookie, sid, images[len(images)-1])
			require.Equal(t, want, image.Code, image.Body.String())
		})
	}
	// Summary/search stays visible; the API must not accidentally hide the row.
	search := frozenRequest(t, l, app, audit, "GET", "/api/submissions?is-frozen=yes")
	require.Equal(t, 200, search.Code, search.Body.String())
	var results types.SubmissionsPageData
	require.NoError(t, json.Unmarshal(search.Body.Bytes(), &results))
	require.Len(t, results.Submissions, 1)
	require.Equal(t, sid, results.Submissions[0].SubmissionID)
	require.Equal(t, 401, frozenRequest(t, l, app, nil, "GET", fmt.Sprintf("/api/submission/%d", sid)).Code)
	require.Equal(t, 401, downloadSubmissionFile(t, l, app, nil, sid, files[0].FileID).Code)
	require.Equal(t, 404, frozenRequest(t, l, app, nil, "GET", fmt.Sprintf("/previews/submission/%d.png", sid)).Code)
	// Concurrent requests cannot contaminate one another's decoded cache values.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			user := updater
			allowed := true
			if i%2 == 0 {
				user = stranger
				allowed = false
			}
			rr := frozenRequest(t, l, app, user, "GET", fmt.Sprintf("/api/submission/%d", sid))
			var data types.ViewSubmissionPageData
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &data))
			require.Equal(t, allowed, data.CanReadDetails)
			require.Equal(t, allowed, data.CurationMeta != nil)
		}(i)
	}
	wg.Wait()
	// Upload participation does not bypass an API token's scopes.
	for _, tc := range []struct {
		scope         string
		detail, files int
	}{
		{types.AuthScopeSubmissionRead, 200, 403},
		{types.AuthScopeSubmissionReadFiles, 403, 200},
		{types.AuthScopeIdentity, 403, 403},
	} {
		mapped, err := app.Service.GenAuthToken(addContextValues(ctx, l, updater.ID, "scope"), updater.ID, tc.scope, "frozen-scope", "127.0.0.1")
		require.NoError(t, err)
		token := *updater.AuthToken
		token.Secret = mapped["Secret"]
		scoped := *updater
		scoped.Cookie = createTestCookie(t, l, &token)
		require.Equal(t, tc.detail, frozenRequest(t, l, app, &scoped, "GET", fmt.Sprintf("/api/submission/%d", sid)).Code)
		require.Equal(t, tc.files, frozenRequest(t, l, app, &scoped, "GET", fmt.Sprintf("/api/submission/%d/files", sid)).Code)
		require.Equal(t, tc.files, downloadSubmissionFile(t, l, app, scoped.Cookie, sid, files[0].FileID).Code)
	}

	// Unfreeze invalidates redacted views; a new contributor then gains read access.
	actor := addContextValues(ctx, l, moderator.ID, "frozen-access")
	require.NoError(t, app.Service.UnfreezeSubmission(actor, sid))
	require.True(t, detail(stranger).CanReadDetails)
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", stranger.Cookie, &sid)
	require.NoError(t, app.Service.FreezeSubmission(actor, sid))
	require.True(t, detail(stranger).CanReadDetails)
	require.False(t, detail(trial).CanReadDetails)
	// Role revocation must take effect even if the full detail response was warm.
	_, err := maria.Exec("INSERT INTO discord_role (id,name,color) VALUES (1,'Frozen Reader',0)")
	require.NoError(t, err)
	_, err = app.Service.SaveUser(actor, &types.DiscordUser{ID: moderator.ID, Username: "former moderator"}, types.AuthScopeAll, "frozen-test", "127.0.0.1")
	require.NoError(t, err)
	require.False(t, detail(moderator).CanReadDetails)
	require.Equal(t, 401, downloadSubmissionFile(t, l, app, moderator.Cookie, sid, files[0].FileID).Code)
	require.False(t, detail(owner).CanReadDetails)
	require.True(t, detail(updater).CanReadDetails)
}

func TestFrozenSubmissionMutationGuardsAndMixedBatches(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTest(t)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020101, []int64{roleIDCurator}, "owner")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020102, []int64{roleIDModerator}, "moderator")
	other := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020103, []int64{roleIDCurator}, "other")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	foreign := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", other.Cookie, nil)
	file := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0]
	foreignFile := getSubmissionFilesBySubmissionID(t, ctx, db, foreign)[0]
	require.Equal(t, 204, freezeSubmission(t, l, app, moderator.Cookie, sid).Code)
	require.Equal(t, 204, freezeSubmission(t, l, app, moderator.Cookie, foreign).Code)
	// Knowing an accessible parent's ID cannot authorize a foreign frozen child.
	require.Equal(t, 401, downloadSubmissionFile(t, l, app, owner.Cookie, sid, foreignFile.FileID).Code)
	for _, ids := range [][]int64{{file.FileID, foreignFile.FileID}, {foreignFile.FileID, file.FileID}} {
		require.Equal(t, 401, downloadSubmissionBatch(t, l, app, owner.Cookie, ids).Code)
		require.Equal(t, 200, downloadSubmissionBatch(t, l, app, moderator.Cookie, ids).Code)
	}
	actor := addContextValues(ctx, l, moderator.ID, "frozen-mutation")
	for _, user := range []*extendedTestUser{owner, moderator, other} {
		for _, route := range []struct{ method, path string }{{"GET", fmt.Sprintf("/web/submission/%d/metaedit", sid)}, {"GET", fmt.Sprintf("/api/submission-receiver-resumable/%d", sid)}, {"POST", fmt.Sprintf("/api/submission/%d/override", sid)}} {
			rr := frozenRequest(t, l, app, user, route.method, route.path)
			require.Equal(t, 401, rr.Code, rr.Body.String())
		}
		require.Equal(t, 401, editFrozenTestMetadata(t, l, app, user, sid).Code)
		require.Equal(t, 401, quotaUpload(t, l, app, user.Cookie, &sid).Code)
	}
	require.Equal(t, 401, addComment(t, l, app, owner.Cookie, sid, constants.ActionComment, "must be read only").Code)
	require.Equal(t, 401, addBatchComment(t, l, app, owner.Cookie, []int64{sid, foreign}, constants.ActionComment, "must fail").Code)
	require.Equal(t, 200, addComment(t, l, app, moderator.Cookie, sid, constants.ActionComment, "moderator review remains possible").Code)
	// Check the authoritative service boundary, independently of middleware.
	require.ErrorContains(t, app.Service.ApplySubmissionMetaEdit(actor, sid, &types.EditCurationMeta{}, nil, nil), "frozen")
	require.ErrorContains(t, app.Service.OverrideBot(actor, sid), "frozen")
	require.ErrorContains(t, app.Service.SoftDeleteSubmission(actor, sid, "blocked"), "frozen")
	require.ErrorContains(t, app.Service.SoftDeleteSubmissionFile(actor, file.FileID, "blocked"), "frozen")
	require.ErrorContains(t, app.Service.ReceiveComments(addContextValues(ctx, l, owner.ID, "readonly"), owner.ID, []int64{sid}, constants.ActionComment, "blocked", "false", "", "", "", "", nil), "frozen")
	require.Len(t, getSubmissionFilesBySubmissionID(t, ctx, db, sid), 1)
	require.NoError(t, app.Service.UnfreezeSubmission(actor, sid))
	require.NoError(t, app.Service.OverrideBot(actor, sid))
	rr := editFrozenTestMetadata(t, l, app, owner, sid)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, &sid)
	require.Len(t, getSubmissionFilesBySubmissionID(t, ctx, db, sid), 3)
}

func TestFrozenSubmissionRejectsAlreadyProcessingUpload(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	defer once.Do(func() { close(release) })
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{BeforeValidate: func() {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
	}})
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020201, []int64{roleIDCurator}, "owner")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020202, []int64{roleIDModerator}, "moderator")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	original := getSubmissionFilesBySubmissionID(t, ctx, db, sid)
	name := quotaAccepted(t, quotaUpload(t, l, app, owner.Cookie, &sid))
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("replacement validation did not start")
	}
	rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
	require.Equal(t, 204, rr.Code, rr.Body.String())
	once.Do(func() { close(release) })
	status := quotaWait(t, app, name)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, original, getSubmissionFilesBySubmissionID(t, ctx, db, sid))
	data, err := app.Service.GetViewSubmissionPageData(addContextValues(ctx, l, owner.ID, "race"), owner.ID, sid)
	require.NoError(t, err)
	require.True(t, data.Submissions[0].IsFrozen)
	require.True(t, data.CanReadDetails)
}

func TestFrozenSubmissionAutomaticFreeze(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTestWithValidatorMockOptions(t, &validatorMockOptions{CurationWarnings: []string{"Release date is less than 3 years ago. Curation should be frozen."}})
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020301, nil, "audit uploader")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020302, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020303, []int64{roleIDTester}, "verifier")
	sid := advanceToState(t, l, app, "assigned-verification", owner.Cookie, tester.Cookie, verifier.Cookie)
	get := func(user *extendedTestUser) types.ViewSubmissionPageData {
		rr := frozenRequest(t, l, app, user, "GET", fmt.Sprintf("/api/submission/%d", sid))
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var data types.ViewSubmissionPageData
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &data))
		return data
	}
	before := get(verifier)
	require.True(t, before.Submissions[0].ShouldAutofreeze)
	require.False(t, before.Submissions[0].IsFrozen)
	require.True(t, before.CanReadDetails)
	rr := addComment(t, l, app, verifier.Cookie, sid, constants.ActionVerify, "verify and autofreeze")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	after := get(verifier)
	require.True(t, after.Submissions[0].IsFrozen)
	require.False(t, after.CanReadDetails)
	require.Nil(t, after.CurationMeta)
	require.Empty(t, after.Comments)
	ownerData := get(owner)
	require.True(t, ownerData.CanReadDetails)
	require.NotEmpty(t, ownerData.Comments)
	require.Equal(t, 200, downloadSubmissionFile(t, l, app, owner.Cookie, sid, ownerData.Submissions[0].FileID).Code)
	require.Equal(t, 401, downloadSubmissionFile(t, l, app, verifier.Cookie, sid, ownerData.Submissions[0].FileID).Code)
	require.Equal(t, 401, addComment(t, l, app, owner.Cookie, sid, constants.ActionComment, "read only").Code)
}

// Removing one contribution must preserve access while another active version
// remains; removing the last must revoke it, including a cached metadata editor.
func TestFrozenSubmissionUploadRemovalRevokesAccess(t *testing.T) {
	app, l, ctx, db, pgdb, _, _ := setupIntegrationTest(t)
	owner := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020401, []int64{roleIDCurator}, "two uploads")
	editor := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020402, []int64{roleIDCurator}, "metadata editor")
	keeper := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020403, []int64{roleIDCurator}, "remaining uploader")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100020404, []int64{roleIDModerator}, "moderator")
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, nil)
	first := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0].FileID
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", owner.Cookie, &sid)
	second := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0].FileID
	rr := editFrozenTestMetadata(t, l, app, editor, sid)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	edited := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0].FileID
	uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", keeper.Cookie, &sid)
	remaining := getSubmissionFilesBySubmissionID(t, ctx, db, sid)[0].FileID
	actor := addContextValues(ctx, l, moderator.ID, "remove-upload")
	require.NoError(t, app.Service.FreezeSubmission(actor, sid))
	check := func(user *extendedTestUser, allowed bool) {
		t.Helper()
		rr := frozenRequest(t, l, app, user, "GET", fmt.Sprintf("/api/submission/%d", sid))
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var data types.ViewSubmissionPageData
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &data))
		require.True(t, data.Submissions[0].IsFrozen)
		require.Equal(t, allowed, data.CanReadDetails)
		require.Equal(t, allowed, data.CurationMeta != nil)
		want := 401
		if allowed {
			want = 200
		} else {
			require.Empty(t, data.Comments)
		}
		require.Equal(t, want, frozenRequest(t, l, app, user, "GET", fmt.Sprintf("/api/submission/%d/files", sid)).Code)
		require.Equal(t, want, downloadSubmissionFile(t, l, app, user.Cookie, sid, remaining).Code)
	}
	// Warm all permission facts and both representations before any removal.
	check(owner, true)
	check(editor, true)
	for _, tc := range []struct {
		name    string
		fileID  int64
		user    *extendedTestUser
		allowed bool
	}{
		{"one of two uploads removed", first, owner, true},
		{"last upload removed", second, owner, false},
		{"metadata edit removed", edited, editor, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, app.Service.UnfreezeSubmission(actor, sid))
			rr := softDeleteSubmissionFile(t, l, app, moderator.Cookie, sid, tc.fileID, "removed version")
			require.Equal(t, 204, rr.Code, rr.Body.String())
			require.NoError(t, app.Service.FreezeSubmission(actor, sid))
			check(tc.user, tc.allowed)
			check(keeper, true)
			check(moderator, true)
		})
	}
}
