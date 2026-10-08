package transport

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func renderSubmissionLayout(t *testing.T, roles []string, owner, frozen, added, patch, extreme bool, files uint64) string {
	t.Helper()
	t.Chdir("..")
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	sub := &types.ExtendedSubmission{SubmissionID: 42, FileID: 99, SubmitterID: 1, LastUploaderID: 2, UpdaterID: 3, OriginalFilename: `current <file>.7z`, Size: 1024, FileCount: files, IsFrozen: frozen, GameExists: patch}
	if added {
		sub.DistinctActions = []string{"mark-added"}
	}
	data := &types.ViewSubmissionPageData{SubmissionsPageData: types.SubmissionsPageData{BasePageData: types.BasePageData{UserID: 4, UserRoles: roles}, Submissions: []*types.ExtendedSubmission{sub}}, CurationMeta: &types.CurationMeta{GameExists: patch}, CurrentFileUploader: "Latest uploader", CurationImageIDs: []int64{11, 12, 13}}
	if owner {
		data.UserID = 1
	}
	if extreme {
		data.CurationMeta.Extreme = utils.StrPtr("Yes")
	}
	app := &App{Conf: &config.Config{IsDev: true}}
	w := httptest.NewRecorder()
	app.RenderTemplates(ctx, w, httptest.NewRequest("GET", "/web/submission/42", nil), data, "templates/submission.gohtml", "templates/submission-table.gohtml", "templates/comment-form.gohtml", "templates/view-submission-nav.gohtml")
	require.Equal(t, 200, w.Code, w.Body.String())
	return w.Body.String()
}

func TestSubmissionLayoutPermissionAndStateGates(t *testing.T) {
	for _, tc := range []struct {
		name                                              string
		roles                                             []string
		owner, frozen, added, patch                       bool
		update, admin, freeze, override, delete, download bool
	}{
		{name: "owner", owner: true, update: true, download: true},
		{name: "unprivileged other viewer", download: true},
		{name: "trial owner", roles: []string{constants.RoleTrialCurator}, owner: true, update: true, download: true},
		{name: "tester", roles: []string{constants.RoleTester}, update: true, admin: true, override: true, download: true},
		{name: "moderator", roles: []string{constants.RoleModerator}, update: true, admin: true, freeze: true, override: true, delete: true, download: true},
		{name: "frozen owner", owner: true, frozen: true},
		{name: "frozen tester", roles: []string{constants.RoleTester}, frozen: true},
		{name: "frozen moderator", roles: []string{constants.RoleModerator}, frozen: true, update: true, admin: true, freeze: true, override: true, delete: true, download: true},
		{name: "added moderator", roles: []string{constants.RoleModerator}, added: true, download: true},
		{name: "content patch moderator", roles: []string{constants.RoleModerator}, patch: true, update: true, admin: true, override: true, delete: true, download: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := renderSubmissionLayout(t, tc.roles, tc.owner, tc.frozen, tc.added, tc.patch, false, 2)
			for _, expect := range []struct {
				needle string
				want   bool
			}{
				{`id="submission-update-heading"`, tc.update}, {`id="submission-admin-heading"`, tc.admin},
				{`id="submission-upload-panel"`, tc.update}, {`id="submission-upload-toggle"`, tc.update},
				{`class="pure-button button-freeze"`, tc.freeze}, {`onclick="overrideBot(`, tc.override}, {`onclick="deleteSubmission(`, tc.delete},
				{`aria-label="Current submission file"`, tc.download},
			} {
				require.Equal(t, expect.want, strings.Contains(page, expect.needle), expect.needle)
			}
			if tc.freeze {
				if tc.frozen {
					require.Contains(t, page, "unfreezeSubmission(")
				} else {
					require.Contains(t, page, "freezeSubmission(")
				}
			}
			if tc.update {
				require.Contains(t, page, `aria-expanded="false"`)
				require.Contains(t, page, `class="submission-upload-panel" hidden`)
			}
		})
	}
}

func TestSubmissionLayoutPreservesActionTargetsAndImages(t *testing.T) {
	page := renderSubmissionLayout(t, []string{constants.RoleModerator}, false, false, false, false, false, 2)
	doc, err := html.Parse(strings.NewReader(page))
	require.NoError(t, err)
	links := map[string]string{}
	buttons := map[string]string{}
	var imageCount int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if n.Type == html.ElementNode {
			if n.Data == "a" {
				var label strings.Builder
				var text func(*html.Node)
				text = func(c *html.Node) {
					if c.Type == html.TextNode {
						textStr := strings.TrimSpace(c.Data)
						label.WriteString(textStr)
					}
					for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
						text(ch)
					}
				}
				text(n)
				links[label.String()] = attrs["href"]
			}
			if n.Data == "input" && attrs["type"] == "button" {
				buttons[attrs["value"]] = attrs["onclick"]
			}
			if n.Data == "img" && attrs["class"] == "curation-image" {
				imageCount++
				require.Equal(t, "div", n.Parent.Data, "images must not become links/buttons")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	require.Equal(t, "/data/submission/42/file/99", links["Download"])
	require.Equal(t, "flashpoint://fpfss/open_curation/data/submission/42/file/99", links["Open in Flashpoint Launcher"])
	require.Equal(t, "/web/submission/42/files", links["View older uploads"])
	require.Equal(t, "/web/submission/42/metaedit", links["Edit metadata"])
	require.Equal(t, map[string]string{"Start": "startUpload()", "Pause": "pauseUpload()", "Cancel": "cancelUpload()"}, buttons)
	require.Equal(t, 3, imageCount)
	require.Contains(t, page, `initResumableUploader("/api/submission-receiver-resumable/42", 1, [".7z", ".zip"], true)`)
	require.Contains(t, page, "current &lt;file&gt;.7z")
	require.Contains(t, page, "uploaded by Latest uploader")
	require.Less(t, strings.Index(page, `id="submission-upload-panel"`), strings.Index(page, `id="submission-images"`))
}

func TestSubmissionLayoutSingleUploadAndExtremeImages(t *testing.T) {
	page := renderSubmissionLayout(t, []string{constants.RoleModerator}, false, false, false, false, true, 1)
	require.NotContains(t, page, ">View older uploads</a>")
	require.Contains(t, page, ">Show Extreme Images</button>")
	require.NotContains(t, page, `class="curation-image" alt="curation image"`)
}
