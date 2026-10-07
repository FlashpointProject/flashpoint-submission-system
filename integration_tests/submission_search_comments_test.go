package integration_tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func seedCommentSearch(t *testing.T, f *sqlFixture) {
	t.Helper()
	f.User(t, 1, "Comment author")
	f.User(t, 2, "Other uploader")
	for _, id := range []int64{101, 102, 103, 104, 105, 106, 107} {
		f.Submission(t, id, "trial")
		uid := int64(1)
		if id == 104 {
			uid = 2
		}
		f.File(t, fixtureFile{ID: id, SubmissionID: id, UserID: uid, At: fixtureEpoch})
		f.Comment(t, id, id, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(time.Second), nil)
	}
	for _, c := range []struct {
		id, sid, uid    int64
		action, message string
	}{
		{1001, 101, 1, constants.ActionComment, "Missing ASSETS: Café 終 100% file_name C:\\games a,b !literal ' OR 1=1 -- <script> & \"quoted\""},
		{1002, 101, 2, constants.ActionComment, "More missing assets"},
		{1003, 102, 1, constants.ActionComment, "missing assets deletedneedle"},
		{1004, 103, 1, constants.ActionComment, ""},
		{1005, 104, 1, constants.ActionRequestChanges, "Please fix missing assets"},
		{1006, 105, 1, constants.ActionComment, "missing"},
		{1007, 105, 1, constants.ActionComment, "assets"},
		{1008, 106, constants.ValidatorID, constants.ActionComment, "Bot found missing assets"},
		{1009, 107, 1, constants.ActionComment, "missing assets"},
	} {
		f.Comment(t, c.id, c.sid, c.uid, c.action, fixtureEpoch.Add(2*time.Second), utils.StrPtr(c.message))
	}
	_, err := f.Maria.Exec(`UPDATE comment SET deleted_at=? WHERE id=1003`, fixtureEpoch.Add(time.Minute))
	require.NoError(t, err)
	_, err = f.Maria.Exec(`UPDATE submission SET deleted_at=? WHERE id=107`, fixtureEpoch.Add(time.Minute))
	require.NoError(t, err)
	// A comment on an earlier upload remains searchable after a replacement.
	f.File(t, fixtureFile{ID: 1011, SubmissionID: 101, UserID: 1, At: fixtureEpoch.Add(3 * time.Second)})
	f.Rebuild(t, 101, 102, 103, 104, 105, 106, 107)
	f.Legacy(t, 1, fixtureMeta("missing assets"), fixtureEpoch, fixtureEpoch)
}

func TestSubmissionSearchComments(t *testing.T) {
	f := newSQLFixture(t)
	seedCommentSearch(t, f)
	for _, tc := range []struct {
		query string
		ids   []int64
	}{
		{"mIsSiNg AsSeTs", []int64{101, 104, 106}},
		{"ASSET", []int64{101, 104, 105, 106}},
		{"CAFÉ", []int64{101}}, {"cafe", nil}, {"終", []int64{101}},
		{"%", []int64{101}}, {"_", []int64{101}}, {`C:\games`, []int64{101}},
		{"a,b", []int64{101}}, {"!literal", []int64{101}},
		{"' OR 1=1 --", []int64{101}}, {"<script> & \"quoted\"", []int64{101}},
		{"deletedneedle", nil}, {"absent", nil},
		{"", []int64{-1, 101, 102, 103, 104, 105, 106}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, count := f.Search(t, 1, &types.SubmissionsFilter{CommentPartial: &tc.query})
			require.ElementsMatch(t, tc.ids, searchEdgeIDs(rows))
			require.EqualValues(t, len(tc.ids), count)
		})
	}
	filter := &types.SubmissionsFilter{CommentPartial: utils.StrPtr("missing assets"), ResultsPerPage: utils.Int64Ptr(1), OrderBy: utils.StrPtr("title"), AscDesc: utils.StrPtr("asc")}
	for i, id := range []int64{101, 104, 106} {
		filter.Page = utils.Int64Ptr(int64(i + 1))
		rows, count := f.Search(t, 1, filter)
		require.Equal(t, []int64{id}, searchEdgeIDs(rows))
		require.EqualValues(t, 3, count)
	}
	filter.ResultsPerPage = nil
	filter.Page = nil
	filter.TitlePartial = utils.StrPtr("104")
	filter.PlatformPartial = utils.StrPtr("flash")
	filter.IsFrozen = utils.StrPtr("no")
	rows, count := f.Search(t, 1, filter)
	require.Equal(t, []int64{104}, searchEdgeIDs(rows))
	require.EqualValues(t, 1, count)
	// State/action filters constrain the submission, not necessarily the matching comment.
	filter.DistinctActions = []string{constants.ActionApprove}
	rows, count = f.Search(t, 1, filter)
	require.Equal(t, []int64{104}, searchEdgeIDs(rows))
	require.EqualValues(t, 1, count)
	filter.IsFrozen = utils.StrPtr("yes")
	rows, count = f.Search(t, 1, filter)
	require.Empty(t, rows)
	require.Zero(t, count)
	// Source edits/deletions take effect without refreshing submission caches.
	_, err := f.Maria.Exec(`UPDATE comment SET message='replacementneedle' WHERE id=1001`)
	require.NoError(t, err)
	rows, count = f.Search(t, 1, &types.SubmissionsFilter{CommentPartial: utils.StrPtr("replacementneedle")})
	require.Equal(t, []int64{101}, searchEdgeIDs(rows))
	require.EqualValues(t, 1, count)
	_, err = f.Maria.Exec(`UPDATE comment SET deleted_at=? WHERE id=1001`, fixtureEpoch.Add(time.Minute))
	require.NoError(t, err)
	rows, count = f.Search(t, 1, &types.SubmissionsFilter{CommentPartial: utils.StrPtr("replacementneedle")})
	require.Empty(t, rows)
	require.Zero(t, count)
}

func TestSubmissionSearchCommentsHTTPForm(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	f := &sqlFixture{DB: db, Maria: maria, Ctx: context.WithValue(ctx, utils.CtxKeys.Log, l)}
	seedCommentSearch(t, f)
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 1, []int64{roleIDCurator, roleIDTester}, "Comment author")
	for _, needle := range []string{"MISSING assets", "<script> & \"quoted\"", ""} {
		query := submissionFilterFormQuery(t, root, "", "advanced", &types.SubmissionsFilter{CommentPartial: &needle})
		values, err := url.ParseQuery(query)
		require.NoError(t, err)
		require.Equal(t, needle, values.Get("comment-partial"))
		for _, path := range []string{"/api/submissions?", "/api/my-submissions?"} {
			rr := getWithCookie(t, l, app, user.Cookie, path+query)
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			var page types.SubmissionsPageData
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
			want := []int64{101}
			if needle == "MISSING assets" {
				want = []int64{101, 104, 106}
				if path == "/api/my-submissions?" {
					want = []int64{101, 106}
				}
			} else if needle == "" {
				want = []int64{-1, 101, 102, 103, 104, 105, 106}
				if path == "/api/my-submissions?" {
					want = []int64{101, 102, 103, 105, 106}
				}
			}
			require.ElementsMatch(t, want, searchEdgeIDs(page.Submissions))
			require.EqualValues(t, len(want), page.TotalCount)
			if needle == "" {
				require.Nil(t, page.Filter.CommentPartial)
			} else {
				require.Equal(t, &needle, page.Filter.CommentPartial)
			}
		}
	}
}
