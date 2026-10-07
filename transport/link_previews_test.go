package transport

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/linkpreview"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

const discordUA = "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)"

type fakePreviewSource struct {
	card  *linkpreview.Card
	err   error
	calls int
	key   string
}

func (f *fakePreviewSource) GetSubmissionPreview(_ context.Context, id int64) (*linkpreview.Card, error) {
	f.calls++
	return f.card, f.err
}
func (f *fakePreviewSource) GetTagPreview(_ context.Context, key string) (*linkpreview.Card, error) {
	f.calls++
	f.key = key
	return f.card, f.err
}
func previewApp() (*App, *fakePreviewSource) {
	title := "Fusion Rocket"
	source := &fakePreviewSource{card: linkpreview.Submission(&types.ExtendedSubmission{SubmissionID: 1, FileID: 2, CurationTitle: &title})}
	app := &App{Conf: &config.Config{HostBaseURL: "https://fpfss.example"}, previewSource: source}
	app.InitializeMux()
	return app, source
}
func previewRequest(app *App, method, path, ua string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("User-Agent", ua)
	req.Host = "attacker.example"
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	rr := httptest.NewRecorder()
	app.Mux.ServeHTTP(rr, req)
	return rr
}
func TestDiscordPreviewRouting(t *testing.T) {
	app, source := previewApp()
	for _, path := range []string{"/", "/web", "/web/submissions?title-partial=test", "/web/tag/1/edit", "/web/submission/1/files", "/web/submission/1/metaedit", "/web/platform/1", "/api/submission/1", "/api/tags", "/auth", "/auth/callback", "/static/opal.png", "/swagger/", "/data/submission/1/file/2", "/missing", "/web/submission/0", "/web/submission/-1", "/web/submission/99999999999999999999999"} {
		t.Run(path, func(t *testing.T) {
			rr := previewRequest(app, "GET", path, discordUA)
			require.Equal(t, 204, rr.Code)
			require.Empty(t, rr.Body.String())
			require.Empty(t, rr.Header().Get("Location"))
			require.Equal(t, "private, no-store", rr.Header().Get("Cache-Control"))
		})
	}
	require.Zero(t, source.calls)
	rr := previewRequest(app, "POST", "/api/internal/recompute-submission-cache-all", discordUA)
	require.Equal(t, 204, rr.Code)
	require.Zero(t, source.calls)
	rr = previewRequest(app, "GET", "/web/submission/1?ignored=anything", discordUA)
	require.Equal(t, 200, rr.Code)
	require.Contains(t, rr.Body.String(), `content="https://fpfss.example/previews/submission/1.png`)
	require.Contains(t, rr.Body.String(), `name="twitter:card" content="photo"`)
	require.NotContains(t, rr.Body.String(), "attacker.example")
	require.Contains(t, rr.Header().Values("Vary"), "User-Agent")
	require.Empty(t, rr.Header().Get("Set-Cookie"))
	// Normal browsers still go through existing auth, including a lookalike bot UA.
	for _, ua := range []string{"Mozilla/5.0", "not-discordbot/2.0"} {
		rr = previewRequest(app, "GET", "/web/submission/1", ua)
		require.Equal(t, 302, rr.Code)
		require.Contains(t, rr.Header().Get("Location"), "/auth?dest=")
		require.Contains(t, rr.Header().Values("Vary"), "User-Agent")
	}
}
func TestDiscordPreviewImagesAndMissingData(t *testing.T) {
	app, source := previewApp()
	rr := previewRequest(app, "GET", "/previews/submission/1.png", "Discord image proxy")
	require.Equal(t, 200, rr.Code)
	require.Equal(t, "image/png", rr.Header().Get("Content-Type"))
	image, err := png.Decode(bytes.NewReader(rr.Body.Bytes()))
	require.NoError(t, err)
	require.Equal(t, 1120, image.Bounds().Dx())
	for _, path := range []string{"/web/submission/1", "/previews/submission/1.png"} {
		rr = previewRequest(app, "HEAD", path, discordUA)
		require.Equal(t, 200, rr.Code)
		require.Empty(t, rr.Body.String())
	}
	source.card = nil
	rr = previewRequest(app, "GET", "/web/submission/1", discordUA)
	require.Equal(t, 204, rr.Code)
	require.Empty(t, rr.Body.String())
	rr = previewRequest(app, "GET", "/previews/submission/1.png", "")
	require.Equal(t, 404, rr.Code)
	require.Empty(t, rr.Body.String())
	source.err = errors.New("database secret details")
	rr = previewRequest(app, "GET", "/web/submission/1", discordUA)
	require.Equal(t, 503, rr.Code)
	require.Empty(t, rr.Body.String())
}
func TestDiscordTagPreviewEscapesMetadata(t *testing.T) {
	app, source := previewApp()
	source.card = linkpreview.Tag(&types.Tag{ID: 7, Name: `bad"><script>alert(1)</script>`, Description: `</head><img src=x onerror="bad">`}, 3)
	rr := previewRequest(app, "GET", "/web/tag/Platform%20Game", "Discordbot/2.0")
	require.Equal(t, 200, rr.Code)
	require.Equal(t, "Platform Game", source.key)
	require.Contains(t, rr.Body.String(), "/previews/tag/7.png")
	require.NotContains(t, rr.Body.String(), "<script>")
	require.NotContains(t, rr.Body.String(), "<img")
	require.Contains(t, rr.Body.String(), "&lt;script&gt;")
}
func TestDiscordPreviewsDisabledInSourceModes(t *testing.T) {
	for _, admin := range []bool{false, true} {
		app, source := previewApp()
		app.Conf.FlashpointSourceOnlyMode = !admin
		app.Conf.FlashpointSourceOnlyAdminMode = admin
		for _, path := range []string{"/web/submission/1", "/web/tag/1", "/previews/submission/1.png", "/previews/tag/1.png"} {
			rr := previewRequest(app, "GET", path, discordUA)
			require.True(t, rr.Code == 204 || rr.Code == 404)
			require.Empty(t, rr.Body.String())
		}
		require.Zero(t, source.calls)
	}
}
func TestDiscordPreviewRejectsInvalidOrigin(t *testing.T) {
	for _, origin := range []string{"", "//untrusted", "javascript:alert(1)", "https://user:secret@example.com"} {
		app, _ := previewApp()
		app.Conf.HostBaseURL = origin
		rr := previewRequest(app, "GET", "/web/submission/1", discordUA)
		require.Equal(t, 503, rr.Code)
		require.Empty(t, strings.TrimSpace(rr.Body.String()))
	}
}
