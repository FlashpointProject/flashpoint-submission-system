package transport

import (
	"context"
	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTemplatesAreAppOwnedAndDevelopmentReloads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "templates"), 0700))
	t.Chdir(dir)
	write := func(name, value string) {
		require.NoError(t, os.WriteFile(filepath.Join("templates", name), []byte(value), 0600))
	}
	write("base.gohtml", `{{define "layout"}}{{template "content" .}}{{end}}`)
	write("navbar.gohtml", `{{define "navbar"}}nav{{end}}`)
	write("page.gohtml", `{{define "content"}}first{{end}}`)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	render := func(app *App) string {
		response := httptest.NewRecorder()
		app.RenderTemplates(ctx, response, httptest.NewRequest("GET", "/", nil), nil, "templates/page.gohtml")
		require.Equal(t, 200, response.Code, response.Body.String())
		return response.Body.String()
	}
	a, b := &App{Conf: &config.Config{}}, &App{Conf: &config.Config{}}
	require.Equal(t, "first", render(a))
	write("page.gohtml", `{{define "content"}}second{{end}}`)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); require.Equal(t, "first", render(a)) }()
	}
	wg.Wait()
	require.Equal(t, "second", render(b))
	a.Conf.IsDev = true
	require.Equal(t, "second", render(a))
	write("page.gohtml", `{{define "content"}}third{{end}}`)
	require.Equal(t, "third", render(a))
}
