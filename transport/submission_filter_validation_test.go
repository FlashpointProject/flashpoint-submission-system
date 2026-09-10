package transport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/gorilla/schema"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// Reject connection attempts deliberately: reaching this boundary proves valid
// input passed HTTP decoding/validation without requiring a database server.
type submissionFilterBoundaryConnector struct{ attempts int }

func (c *submissionFilterBoundaryConnector) Connect(context.Context) (driver.Conn, error) {
	c.attempts++
	return nil, errors.New("submission filter test stopped at database boundary")
}
func (*submissionFilterBoundaryConnector) Driver() driver.Driver { return nil }

func TestSubmissionFilterHTTPValidation(t *testing.T) {
	cases := []struct {
		query string
		valid bool
	}{
		{"assigned-status-testing-user=assigned", false},
		{"assigned-status-verification-user=unassigned", false},
		{"requested-changes-status-user=none", false},
		{"approvals-status-user=yes", false},
		{"verification-status-user=no", false},
		{"approvals-status-user=yes&assigned-status-user-id=0", false},
		{"approvals-status-user=yes&assigned-status-user-id=-1", false},
		{"is-extreme=invalid", false},
		{"is-content-change=invalid", false},
		{"is-frozen=invalid", false},
		{"is-extreme=yes", true},
		{"is-extreme=Yes", true},
		{"is-extreme=no", true},
		{"is-extreme=No", true},
		{"assigned-status-user-id=12", true},
		{"approvals-status-user=yes&assigned-status-user-id=12", true},
		{"is-extreme=&is-content-change=&is-frozen=&approvals-status-user=&assigned-status-user-id=0", true},
	}
	for _, mySubmissions := range []bool{false, true} {
		page := "submissions"
		if mySubmissions {
			page = "my-submissions"
		}
		for _, tc := range cases {
			t.Run(page+"/"+tc.query, func(t *testing.T) {
				connector := &submissionFilterBoundaryConnector{}
				db := sql.OpenDB(connector)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				logger := logrus.New()
				logger.SetOutput(io.Discard)
				entry := logrus.NewEntry(logger)
				decoder := schema.NewDecoder()
				decoder.ZeroEmpty(false)
				decoder.IgnoreUnknownKeys(true)
				app := &App{
					decoder: decoder,
					Service: service.NewWithMocks(entry, db, nil, nil, nil, "", 0, "", "", true, nil, "", ""),
				}
				req := httptest.NewRequest(http.MethodGet, "/api/"+page+"?"+tc.query, nil)
				ctx := context.WithValue(req.Context(), utils.CtxKeys.RequestType, constants.RequestJSON)
				ctx = context.WithValue(ctx, utils.CtxKeys.Log, entry)
				ctx = context.WithValue(ctx, utils.CtxKeys.UserID, int64(12))
				req = req.WithContext(ctx)
				response := httptest.NewRecorder()
				if mySubmissions {
					app.HandleMySubmissionsPage(response, req)
				} else {
					app.HandleSubmissionsPage(response, req)
				}
				if tc.valid {
					require.Equal(t, 1, connector.attempts, "valid input must reach the service database boundary")
					require.Equal(t, http.StatusInternalServerError, response.Code, "intentional database failure")
				} else {
					require.Zero(t, connector.attempts, "invalid input must fail before any database access")
					require.Equal(t, http.StatusBadRequest, response.Code)
					require.NotContains(t, response.Body.String(), "Internal Server Error")
				}
			})
		}
	}
}
