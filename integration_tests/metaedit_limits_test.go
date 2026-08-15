package integration_tests

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/stretchr/testify/require"
)

func TestSubmissionMetaEditRejectsOversizedMultipartRequest(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	staff := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000801), []int64{roleIDModerator}, "staff")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	logo, err := writer.CreateFormFile("logo", "logo.png")
	require.NoError(t, err)
	_, err = logo.Write(bytes.Repeat([]byte("x"), 33<<20))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/submission/%d/metaedit", 1), body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(staff.Cookie)
	response := httptest.NewRecorder()

	logging.LogRequestHandler(l, app.Mux).ServeHTTP(response, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "submission meta-edit upload is too large")
}
