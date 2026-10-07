package integration_tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/Masterminds/sprig"
	"github.com/stretchr/testify/require"
)

// Execute the production JavaScript on the rendered template rather than
// maintaining a second copy of each preset's query parameters in these tests.
func quickFilterQuery(t *testing.T, root, handler, layout string) string {
	t.Helper()
	// Start with stale selections to exercise reset as well as preset selection.
	query := submissionFilterFormQuery(t, root, handler, layout, &types.SubmissionsFilter{TitlePartial: utils.StrPtr("stale search"), CommentPartial: utils.StrPtr("stale comment"), Page: utils.Int64Ptr(9), SubscribedMe: utils.StrPtr("yes"), DeveloperPartial: utils.StrPtr("stale developer"), HasAdditionalApplications: utils.StrPtr("no"), LastUploaderNotMe: utils.StrPtr("yes"), SubmitterNotMe: utils.StrPtr("yes")})
	q, err := url.ParseQuery(query)
	require.NoError(t, err)
	require.Empty(t, q.Get("title-partial"))
	require.Empty(t, q.Get("comment-partial"))
	require.Empty(t, q.Get("page"))
	require.Empty(t, q.Get("developer-partial"))
	require.Empty(t, q.Get("has-additional-applications"))
	require.Empty(t, q.Get("subscribed-me"))
	require.Empty(t, q.Get("submitter-not-me"), "presets must not add an original-owner restriction")
	require.Equal(t, "advanced", q.Get("filter-layout"))
	return query
}

func submissionFilterFormQuery(t *testing.T, root, handler, layout string, filter *types.SubmissionsFilter) string {
	t.Helper()
	return runSubmissionFilterFormScript(t, root, "quick-filter.cjs", handler, layout, filter)
}

func runSubmissionFilterFormScript(t *testing.T, root, runner, handler, layout string, filter *types.SubmissionsFilter) string {
	t.Helper()
	tmpl, err := template.New("submission-filter").Funcs(sprig.FuncMap()).Funcs(template.FuncMap{
		"unpointify": utils.Unpointify,
	}).ParseFiles(filepath.Join(root, "templates/submission-filter.gohtml"), filepath.Join(root, "templates/submission-filter-chunks.gohtml"))
	require.NoError(t, err)
	var html bytes.Buffer
	err = tmpl.ExecuteTemplate(&html, "submission-filter", struct {
		Filter       *types.SubmissionsFilter
		FilterLayout string
	}{filter, layout})
	require.NoError(t, err)
	script, err := os.ReadFile(filepath.Join(root, "static/js.js"))
	require.NoError(t, err)
	input, err := json.Marshal(map[string]string{"html": html.String(), "script": string(script), "handler": handler, "layout": layout})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "integration_tests/browser", runner))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	require.NoError(t, err, stderr.String())
	return string(output)
}

func TestSubmissionQuickFiltersMultiUploader(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	testingQuery := quickFilterQuery(t, root, "filterReadyForTesting", "simple")
	verificationQuery := quickFilterQuery(t, root, "filterReadyForVerification", "advanced")
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	users := make([]*extendedTestUser, 4)
	for i, name := range []string{"original", "first fixer", "latest fixer", "independent reviewer"} {
		users[i] = createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100001001+i), []int64{roleIDCurator, roleIDTester}, name)
	}
	a, b, c, d := users[0], users[1], users[2], users[3]
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", a.Cookie, nil)
	search := func(t *testing.T, user *extendedTestUser, query string, match bool) {
		t.Helper()
		q, err := url.ParseQuery(query)
		require.NoError(t, err)
		q.Set("submission-id", fmt.Sprint(sid))
		rr := getWithCookie(t, l, app, user.Cookie, "/api/submissions?"+q.Encode())
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var page types.SubmissionsPageData
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page), rr.Body.String())
		if match {
			require.EqualValues(t, 1, page.TotalCount)
			require.Len(t, page.Submissions, 1, "%s: %s", user.Name, query)
		} else {
			require.Zero(t, page.TotalCount)
			require.Empty(t, page.Submissions, "%s: %s", user.Name, query)
		}
	}
	action := func(t *testing.T, user *extendedTestUser, kind string, allowed bool) {
		t.Helper()
		rr := addComment(t, l, app, user.Cookie, sid, kind, "quick filter eligibility regression")
		if allowed {
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		} else {
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			require.Contains(t, rr.Body.String(), "uploader of the newest version")
		}
	}
	t.Run("original upload", func(t *testing.T) {
		search(t, a, testingQuery, false)
		search(t, b, testingQuery, true)
		action(t, a, constants.ActionAssignTesting, false)
	})
	uploadFixedVersion(t, l, app, b, sid)
	t.Run("first fix", func(t *testing.T) {
		search(t, a, testingQuery, true) // Original ownership is not a review restriction.
		search(t, b, testingQuery, false)
		search(t, c, testingQuery, true)
		action(t, b, constants.ActionAssignTesting, false)
	})
	uploadFixedVersion(t, l, app, c, sid)
	t.Run("second fix permits earlier uploader to test", func(t *testing.T) {
		for _, user := range users {
			search(t, user, testingQuery, user != c)
		}
		action(t, c, constants.ActionAssignTesting, false)
		action(t, c, constants.ActionApprove, false)
		action(t, b, constants.ActionAssignTesting, true)
		action(t, b, constants.ActionApprove, true)
		for _, user := range users {
			search(t, user, verificationQuery, user != b && user != c)
		}
		action(t, c, constants.ActionAssignVerification, false)
		action(t, c, constants.ActionVerify, false)
	})
	// A new upload clears B's approval, so B can now verify after D tests.
	uploadFixedVersion(t, l, app, c, sid)
	action(t, d, constants.ActionAssignTesting, true)
	action(t, d, constants.ActionApprove, true)
	t.Run("earlier fixer can verify a later version", func(t *testing.T) {
		search(t, b, verificationQuery, true)
		action(t, b, constants.ActionAssignVerification, true)
		action(t, b, constants.ActionVerify, true)
		for _, user := range users {
			search(t, user, verificationQuery, false)
		}
	})
	t.Run("original and latest uploader exclusions are independent", func(t *testing.T) {
		for _, user := range users {
			search(t, user, "submitter-not-me=yes", user != a)
			search(t, user, "last-uploader-not-me=yes", user != c)
			search(t, user, "submitter-not-me=yes&last-uploader-not-me=yes", user != a && user != c)
		}
	})
}

func TestSubmissionQuickFiltersConflictingAssignment(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	query := quickFilterQuery(t, root, "filterReadyForVerification", "simple")
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	users := make([]*extendedTestUser, 3)
	for i := range users {
		users[i] = createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100002001+i), []int64{roleIDCurator, roleIDTester}, fmt.Sprint(i))
	}
	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", users[0].Cookie, nil)
	for _, u := range users[1:] {
		rr := addComment(t, l, app, u.Cookie, sid, constants.ActionAssignTesting, "")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	}
	rr := addComment(t, l, app, users[2].Cookie, sid, constants.ActionApprove, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = addComment(t, l, app, users[1].Cookie, sid, constants.ActionAssignVerification, "")
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "already assigned to test")
	rr = getWithCookie(t, l, app, users[1].Cookie, "/api/submissions?"+query)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var page types.SubmissionsPageData
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
	require.Empty(t, page.Submissions, "a user still assigned to test cannot take verification")
	require.Zero(t, page.TotalCount)
	rr = addComment(t, l, app, users[1].Cookie, sid, constants.ActionUnassignTesting, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = getWithCookie(t, l, app, users[1].Cookie, "/api/submissions?"+query)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
	require.Len(t, page.Submissions, 1, "unassigning testing makes verification available again")
	require.EqualValues(t, 1, page.TotalCount)
	rr = addComment(t, l, app, users[1].Cookie, sid, constants.ActionAssignVerification, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// Exercise every preset from both layouts, including reset of the two separate
// uploader options. All review presets exclude the latest uploader; the
// remaining queue criteria stay unchanged.
func TestSubmissionQuickFilterPresets(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	for _, layout := range []string{"simple", "advanced"} {
		for _, tc := range []struct {
			handler string
			want    url.Values
		}{
			{"filterReadyForTesting", url.Values{"bot-action": {"approve"}, "approvals-status": {"none"}, "verification-status": {"none"}, "requested-changes-status": {"none"}, "assigned-status-testing": {"unassigned"}, "assigned-status-verification": {"unassigned"}, "last-uploader-not-me": {"yes"}, "order-by": {"uploaded"}, "asc-desc": {"asc"}, "distinct-action-not": {"mark-added", "reject"}}},
			{"filterReadyForVerification", url.Values{"bot-action": {"approve"}, "approvals-status": {"approved"}, "verification-status": {"none"}, "requested-changes-status": {"none"}, "assigned-status-verification": {"unassigned"}, "assigned-status-testing-me": {"unassigned"}, "approvals-status-me": {"no"}, "last-uploader-not-me": {"yes"}, "order-by": {"uploaded"}, "asc-desc": {"asc"}, "distinct-action-not": {"mark-added", "reject"}}},
			{"filterReadyForFlashpoint", url.Values{"bot-action": {"approve"}, "verification-status": {"verified"}, "requested-changes-status": {"none"}, "order-by": {"uploaded"}, "asc-desc": {"asc"}, "distinct-action-not": {"mark-added", "reject"}}},
			{"filterAssignedToMeForTesting", url.Values{"assigned-status-testing-me": {"assigned"}, "last-uploader-not-me": {"yes"}}},
			{"filterAssignedToMeForVerification", url.Values{"assigned-status-verification-me": {"assigned"}, "last-uploader-not-me": {"yes"}}},
			{"filterIHaveRequestedChangesAfterTesting", url.Values{"assigned-status-testing-me": {"assigned"}, "requested-changes-status-me": {"ongoing"}, "last-uploader-not-me": {"yes"}}},
			{"filterIHaveRequestedChangesVerification", url.Values{"assigned-status-verification-me": {"assigned"}, "requested-changes-status-me": {"ongoing"}, "last-uploader-not-me": {"yes"}}},
		} {
			t.Run(layout+"/"+tc.handler, func(t *testing.T) {
				query := quickFilterQuery(t, root, tc.handler, layout)
				got, err := url.ParseQuery(query)
				require.NoError(t, err)
				for key, values := range got {
					if len(values) == 1 && values[0] == "" {
						got.Del(key)
					}
				}
				tc.want.Set("filter-layout", "advanced")
				require.Equal(t, tc.want, got)
			})
		}
	}
}

func TestSubmissionUploaderFilterControls(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	for _, tc := range []struct {
		name             string
		filter           types.SubmissionsFilter
		original, latest string
	}{
		{"neither", types.SubmissionsFilter{}, "", ""},
		{"original only", types.SubmissionsFilter{SubmitterNotMe: utils.StrPtr("yes")}, "yes", ""},
		{"latest only", types.SubmissionsFilter{LastUploaderNotMe: utils.StrPtr("yes")}, "", "yes"},
		{"both", types.SubmissionsFilter{SubmitterNotMe: utils.StrPtr("yes"), LastUploaderNotMe: utils.StrPtr("yes")}, "yes", "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := submissionFilterFormQuery(t, root, "", "advanced", &tc.filter)
			q, err := url.ParseQuery(query)
			require.NoError(t, err)
			require.Equal(t, tc.original, q.Get("submitter-not-me"))
			require.Equal(t, tc.latest, q.Get("last-uploader-not-me"))
		})
	}
}

func TestSubmissionQuickFiltersAssignedFixUploader(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	for _, tc := range []struct{ name, assigned, changes, assignment string }{
		{"testing", "filterAssignedToMeForTesting", "filterIHaveRequestedChangesAfterTesting", constants.ActionAssignTesting},
		{"verification", "filterAssignedToMeForVerification", "filterIHaveRequestedChangesVerification", constants.ActionAssignVerification},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assignedQuery := quickFilterQuery(t, root, tc.assigned, "simple")
			changesQuery := quickFilterQuery(t, root, tc.changes, "advanced")
			app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
			defer maria.Close()
			defer postgres.Close()
			original := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100003001, []int64{roleIDCurator, roleIDTester}, "original")
			fixer := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100003002, []int64{roleIDCurator, roleIDTester}, "assigned fixer")
			tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 100003003, []int64{roleIDCurator, roleIDTester}, "independent tester")
			sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", original.Cookie, nil)
			if tc.name == "verification" {
				rr := addComment(t, l, app, tester.Cookie, sid, constants.ActionAssignTesting, "")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				rr = addComment(t, l, app, tester.Cookie, sid, constants.ActionApprove, "")
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			}
			rr := addComment(t, l, app, fixer.Cookie, sid, tc.assignment, "")
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			rr = addComment(t, l, app, fixer.Cookie, sid, constants.ActionRequestChanges, "needs a fix")
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			check := func(query string, count int) {
				t.Helper()
				response := getWithCookie(t, l, app, fixer.Cookie, "/api/submissions?"+query)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var page types.SubmissionsPageData
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
				require.Len(t, page.Submissions, count)
				require.EqualValues(t, count, page.TotalCount)
			}
			check(assignedQuery, 1)
			check(changesQuery, 1)
			uploadFixedVersion(t, l, app, fixer, sid)
			// Upload does not clear assignment or RC, but the uploader can no longer
			// review the version. Manual state searches still expose that history.
			for _, query := range []string{assignedQuery, changesQuery} {
				manual, err := url.ParseQuery(query)
				require.NoError(t, err)
				manual.Del("last-uploader-not-me")
				check(manual.Encode(), 1)
				check(query, 0)
			}
			for _, action := range []string{constants.ActionApprove, constants.ActionVerify, constants.ActionRequestChanges} {
				rr = addComment(t, l, app, fixer.Cookie, sid, action, "cannot review own fix")
				require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
				require.Contains(t, rr.Body.String(), "uploader of the newest version")
			}
			uploadFixedVersion(t, l, app, original, sid)
			check(assignedQuery, 1)
			check(changesQuery, 1)
		})
	}
}

func TestSubmissionFilterLayoutSwitch(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	for _, layout := range []string{"simple", "advanced"} {
		t.Run(layout, func(t *testing.T) {
			result := runSubmissionFilterFormScript(t, root, "switch-filter.cjs", "", layout, &types.SubmissionsFilter{})
			require.Equal(t, "ok", result)
		})
	}
}
