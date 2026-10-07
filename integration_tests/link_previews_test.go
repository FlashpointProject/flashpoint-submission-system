package integration_tests

import (
	"bytes"
	"image/png"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestDiscordSubmissionPreviews(t *testing.T) {
	f, _ := seedSearchFixture(t)
	app := &transport.App{Conf: &config.Config{HostBaseURL: "https://fpfss.example"}, Service: rebuildFixtureService(f)}
	app.InitializeMux()
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil).WithContext(f.Ctx)
		r.Header.Set("User-Agent", "Discordbot/2.0")
		w := httptest.NewRecorder()
		app.Mux.ServeHTTP(w, r)
		return w
	}
	// Frozen visibility is checked independently on both endpoints, without login.
	require.Equal(t, 204, request("/web/submission/101").Code)
	require.Equal(t, 404, request("/previews/submission/101.png").Code)
	_, err := f.Maria.Exec(`UPDATE submission SET frozen_at=NULL WHERE id=101`)
	require.NoError(t, err)
	rr := request("/web/submission/101")
	require.Equal(t, 200, rr.Code)
	require.Contains(t, rr.Body.String(), "Aurora Café")
	card, err := app.Service.GetSubmissionPreview(f.Ctx, 101)
	require.NoError(t, err)
	require.Equal(t, "Alice Alpha", card.Rows[2][0].Value)
	require.Equal(t, "Casey Gamma", card.Rows[2][1].Value)
	require.Equal(t, "1", card.Statuses[3].Value)
	require.Equal(t, "1", card.Statuses[5].Value)
	imageResponse := request("/previews/submission/101.png")
	require.Equal(t, 200, imageResponse.Code)
	_, err = png.Decode(bytes.NewReader(imageResponse.Body.Bytes()))
	require.NoError(t, err)
	// A later upload or metadata edit is reflected immediately (no stale app cache).
	_, err = f.Maria.Exec(`UPDATE curation_meta SET title='Changed title' WHERE fk_submission_file_id=10002`)
	require.NoError(t, err)
	changed := request("/web/submission/101")
	require.Contains(t, changed.Body.String(), "Changed title")
	require.NotEqual(t, rr.Body.String(), changed.Body.String())
	// Even with a stale submission cache, a deleted current file is not exposed.
	_, err = f.Maria.Exec(`UPDATE submission_file SET deleted_at=NOW() WHERE id=10002`)
	require.NoError(t, err)
	require.Equal(t, 204, request("/web/submission/101").Code)
	require.Equal(t, 404, request("/previews/submission/101.png").Code)
	f.Rebuild(t, 101)
	require.Contains(t, request("/web/submission/101").Body.String(), "Old title")
	_, err = f.Maria.Exec(`UPDATE submission SET frozen_at=NOW() WHERE id=101`)
	require.NoError(t, err)
	require.Equal(t, 204, request("/web/submission/101").Code)
	require.Equal(t, 404, request("/previews/submission/101.png?v=old").Code)
	_, err = f.Maria.Exec(`UPDATE submission SET deleted_at=NOW() WHERE id=102`)
	require.NoError(t, err)
	require.Equal(t, 204, request("/web/submission/102").Code)
	require.Equal(t, 404, request("/previews/submission/102.png").Code)
	require.Equal(t, 204, request("/web/submission/99999").Code)
	// Browser authentication and API authorization remain in place.
	r := httptest.NewRequest("GET", "/web/submission/103", nil).WithContext(f.Ctx)
	w := httptest.NewRecorder()
	app.Mux.ServeHTTP(w, r)
	require.Equal(t, 302, w.Code)
	r = httptest.NewRequest("GET", "/api/submission/103", nil).WithContext(f.Ctx)
	w = httptest.NewRecorder()
	app.Mux.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
}

func TestDiscordTagPreviews(t *testing.T) {
	f := newSQLFixture(t)
	conf := config.GetConfig(nil)
	pg := database.OpenPostgresDB(utils.LogCtx(f.Ctx), conf)
	t.Cleanup(pg.Close)
	svc := service.NewWithMocks(utils.LogCtx(f.Ctx), f.Maria, pg, nil, nil, "", 0, "", "", true, nil, "", "")
	_, err := pg.Exec(f.Ctx, `INSERT INTO tag_category(id,name,color) VALUES(50001,'Genre','#ffffff');
 INSERT INTO tag_alias(name,tag_id) VALUES('Platformer',50001),('Platform Game',50001);
 INSERT INTO tag(id,primary_alias,category_id,description,action,reason,user_id) VALUES(50001,'Platformer',50001,'Jump between platforms','create','PRIVATE revision reason',123)`)
	require.NoError(t, err)
	app := &transport.App{Conf: &config.Config{HostBaseURL: "https://fpfss.example"}, Service: svc}
	app.InitializeMux()
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil).WithContext(f.Ctx)
		r.Header.Set("User-Agent", "Discordbot/2.0")
		w := httptest.NewRecorder()
		app.Mux.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/web/tag/50001", "/web/tag/Platform%20Game"} {
		rr := request(path)
		require.Equal(t, 200, rr.Code)
		require.Contains(t, rr.Body.String(), "Platformer")
		require.Contains(t, rr.Body.String(), "/previews/tag/50001.png")
		require.NotContains(t, rr.Body.String(), "PRIVATE")
	}
	require.Equal(t, 200, request("/previews/tag/50001.png").Code)
	require.Equal(t, 204, request("/web/tag/99999").Code)
	_, err = pg.Exec(f.Ctx, `UPDATE tag SET deleted=true WHERE id=50001`)
	require.NoError(t, err)
	require.Equal(t, 204, request("/web/tag/50001").Code)
	require.Equal(t, 404, request("/previews/tag/50001.png").Code)
}
