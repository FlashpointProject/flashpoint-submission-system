package transport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
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

func TestSubmissionFilterBlankFieldsMatchOmittedFields(t *testing.T) {
	decoder := schema.NewDecoder()
	decoder.ZeroEmpty(false)
	decoder.IgnoreUnknownKeys(true)
	for _, query := range []string{"", "filter-layout=advanced&order-by=size&asc-desc=desc", "results-per-page=25&page=2&has-additional-applications=no&is-extreme=no"} {
		t.Run(query, func(t *testing.T) {
			short, err := url.ParseQuery(query)
			require.NoError(t, err)
			long, err := url.ParseQuery(query)
			require.NoError(t, err)
			// Blank text, number and select controls must have the same server
			// meaning as missing parameters; preserve explicitly selected values.
			fields := reflect.TypeOf(types.SubmissionsFilter{})
			for i := 0; i < fields.NumField(); i++ {
				field := fields.Field(i)
				name := field.Tag.Get("schema")
				if name != "" && field.Type.Kind() == reflect.Ptr && !long.Has(name) {
					long.Set(name, "")
				}
			}
			long.Set("submission-id", "")
			var shortFilter, longFilter types.SubmissionsFilter
			require.NoError(t, decoder.Decode(&shortFilter, short))
			require.NoError(t, decoder.Decode(&longFilter, long))
			require.NoError(t, shortFilter.Validate())
			require.NoError(t, longFilter.Validate())
			// The decoder produces an empty slice for submission-id= and nil
			// when omitted. Both disable the ID filter (which checks length).
			require.Empty(t, shortFilter.SubmissionIDs)
			require.Empty(t, longFilter.SubmissionIDs)
			longFilter.SubmissionIDs = nil
			require.Equal(t, shortFilter, longFilter)
		})
	}
}

func TestSubmissionFilterHTTPValidation(t *testing.T) {
	cases := []struct {
		query string
		valid bool
	}{
		{"results-per-page=", true},
		{"results-per-page=0", true},
		{"results-per-page=1", true},
		{"results-per-page=1000", true},
		{"results-per-page=1001", false},
		{"results-per-page=100000", false},
		{"results-per-page=-1", false},
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
		{"has-additional-applications=invalid", false},
		{"has-additional-applications=yes", true},
		{"has-additional-applications=no", true},
		{"has-additional-applications=", true},
		{"order-by=title", true},
		{"order-by=platform", true},
		{"order-by=library", true},
		{"order-by=invalid", false},
		{"submitter-not-me=invalid", false},
		{"submitter-not-me=no", false},
		{"submitter-not-me=yes", true},
		{"submitter-not-me=", true},
		{"submitter-not-me=yes&last-uploader-not-me=yes", true},
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
