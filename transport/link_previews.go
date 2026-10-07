package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/linkpreview"
	"github.com/gorilla/mux"
)

type previewSource interface {
	GetSubmissionPreview(context.Context, int64) (*linkpreview.Card, error)
	GetTagPreview(context.Context, string) (*linkpreview.Card, error)
}

var previewHTML = template.Must(template.New("preview").Parse(`<!doctype html>
<html><head><meta charset="utf-8">
<title>{{.Title}}</title>
<meta name="robots" content="noindex, noarchive">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:site_name" content="Flashpoint's Fantastic Submission System">
<meta property="og:url" content="{{.URL}}">
<meta property="og:image" content="{{.Image}}">
<meta property="og:image:type" content="image/png">
<meta property="og:image:width" content="{{.Width}}">
<meta property="og:image:height" content="{{.Height}}">
<meta property="og:image:alt" content="{{.Alt}}">
<meta name="twitter:card" content="photo">
</head><body></body></html>`))

// Register before every other route so even unknown URLs, auth redirects, static
// files and API routes return no preview to Discord. Image proxies do not always
// use Discordbot's UA, so the narrow PNG routes are public independently of UA.
func (a *App) setupPreviewRoutes(router *mux.Router) {
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "User-Agent")
			next.ServeHTTP(w, r)
		})
	})
	router.HandleFunc("/previews/{kind:submission|tag}/{id:[1-9][0-9]*}.png", a.handlePreviewImage).Methods(http.MethodGet, http.MethodHead)
	router.MatcherFunc(func(r *http.Request, _ *mux.RouteMatch) bool {
		// Both page fetches and linked JSON requests use this token. A spoofed token
		// only receives a public summary, never the authenticated page handler.
		for _, token := range strings.Fields(strings.NewReplacer(";", " ", "(", " ", ")", " ").Replace(r.UserAgent())) {
			if strings.HasPrefix(strings.ToLower(token), "discordbot/") {
				return true
			}
		}
		return false
	}).HandlerFunc(a.handleDiscordPreview)
}

func previewHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (a *App) loadPreview(r *http.Request, kind, key string) (*linkpreview.Card, error) {
	if a.Conf.FlashpointSourceOnlyMode || a.Conf.FlashpointSourceOnlyAdminMode {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var source previewSource = a.Service
	if a.previewSource != nil {
		source = a.previewSource
	}
	switch kind {
	case "submission":
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			return nil, nil
		}
		return source.GetSubmissionPreview(ctx, id)
	case "tag":
		if key == "" || len(key) > 256 {
			return nil, nil
		}
		return source.GetTagPreview(ctx, key)
	}
	return nil, nil
}

func (a *App) handleDiscordPreview(w http.ResponseWriter, r *http.Request) {
	previewHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 || parts[1] != "web" || (parts[2] != "submission" && parts[2] != "tag") || parts[3] == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	card, err := a.loadPreview(r, parts[2], parts[3])
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if card == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Never trust Host or forwarded headers when constructing public asset URLs.
	base, err := url.Parse(a.Conf.HostBaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	base.RawQuery = ""
	base.Fragment = ""
	origin := strings.TrimRight(base.String(), "/")
	imagePath := "/previews/" + strings.TrimPrefix(card.Path, "/web/") + ".png"
	// Refresh the image proxy URL when any displayed metadata or status changes.
	// The endpoint still checks current visibility rather than serving old bytes.
	encoded, _ := json.Marshal(card)
	imagePath += fmt.Sprintf("?v=%x", sha256.Sum256(encoded))
	width, height := linkpreview.Dimensions(card)
	data := struct {
		Title, Description, URL, Image, Alt string
		Width, Height                       int
	}{
		linkpreview.Text(card.Title, 200), card.Description(), origin + card.Path, origin + imagePath,
		linkpreview.Text(card.Title+". "+card.Description(), 1000), width, height,
	}
	var buf bytes.Buffer
	if err := previewHTML.Execute(&buf, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = w.Write(buf.Bytes())
	}
}

func (a *App) handlePreviewImage(w http.ResponseWriter, r *http.Request) {
	previewHeaders(w)
	vars := mux.Vars(r)
	card, err := a.loadPreview(r, vars["kind"], vars["id"])
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if card == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "image/png")
		return
	}
	png, err := linkpreview.PNG(card)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", fmt.Sprint(len(png)))
	_, _ = w.Write(png)
}
