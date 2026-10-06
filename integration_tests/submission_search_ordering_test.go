package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
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

// Each column has a different order. NULL, empty strings, and case-insensitive
// ties span both sources; identity tie-breakers must stay ascending in both directions.
var metadataOrderCases = []struct {
	order     string
	asc, desc []string
}{
	{"title", []string{"103", "L2", "L1", "102", "101"}, []string{"101", "L1", "102", "L2", "103"}},
	{"platform", []string{"102", "L2", "103", "L1", "101"}, []string{"L1", "101", "103", "L2", "102"}},
	{"library", []string{"L2", "101", "103", "L1", "102"}, []string{"L1", "102", "103", "101", "L2"}},
}

func seedMetadataOrdering(t *testing.T, f *sqlFixture) {
	t.Helper()
	f.User(t, 1, "Ordering author")
	for _, spec := range []struct {
		id                       int64
		title, platform, library *string
	}{
		{101, utils.StrPtr("beta"), utils.StrPtr("Zulu"), utils.StrPtr("")},
		{102, utils.StrPtr("ALPHA"), nil, utils.StrPtr("Theatre")},
		{103, nil, utils.StrPtr("alpha"), utils.StrPtr("Arcade")},
		{1, utils.StrPtr("alpha"), utils.StrPtr("zulu"), utils.StrPtr("theatre")},
		{2, utils.StrPtr(""), utils.StrPtr(""), nil},
	} {
		meta := fixtureMeta("")
		meta.Title, meta.Platform, meta.Library = spec.title, spec.platform, spec.library
		if spec.id < 100 {
			f.Legacy(t, spec.id, meta, fixtureEpoch, fixtureEpoch)
			continue
		}
		f.Submission(t, spec.id, "trial")
		if spec.id == 101 {
			// Sorting must use the current upload, not this older title/platform/library.
			old := fixtureMeta("ZZZ historical title")
			old.Platform = utils.StrPtr("AAA historical platform")
			old.Library = utils.StrPtr("ZZZ historical library")
			f.File(t, fixtureFile{ID: 1009, SubmissionID: 101, UserID: 1, At: fixtureEpoch, Meta: old})
		}
		f.File(t, fixtureFile{ID: spec.id * 10, SubmissionID: spec.id, UserID: 1, At: fixtureEpoch.Add(time.Second), Meta: meta})
		f.Comment(t, spec.id, spec.id, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Second), nil)
		f.Rebuild(t, spec.id)
	}
}

func metadataOrderKeys(rows []*types.ExtendedSubmission) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.GameUUID != nil {
			keys = append(keys, "L"+(*row.GameUUID)[len(*row.GameUUID)-1:])
		} else {
			keys = append(keys, fmt.Sprint(row.SubmissionID))
		}
	}
	return keys
}

func TestSubmissionSearchMetadataOrdering(t *testing.T) {
	f := newSQLFixture(t)
	seedMetadataOrdering(t, f)
	for _, tc := range metadataOrderCases {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(tc.order+"/"+direction, func(t *testing.T) {
				want := tc.asc
				if direction == "desc" {
					want = tc.desc
				}
				filter := &types.SubmissionsFilter{OrderBy: &tc.order, AscDesc: &direction}
				rows, count := f.Search(t, 1, filter)
				require.Equal(t, want, metadataOrderKeys(rows))
				require.EqualValues(t, 5, count)
				filter.ResultsPerPage = utils.Int64Ptr(2)
				var all []*types.ExtendedSubmission
				for page := int64(1); page <= 4; page++ {
					filter.Page = &page
					rows, count = f.Search(t, 1, filter)
					require.EqualValues(t, 5, count)
					all = append(all, rows...)
					if page == 4 {
						require.Empty(t, rows)
					}
				}
				require.Equal(t, want, metadataOrderKeys(all), "pagination must not duplicate or skip tied results")
			})
		}
		rows, _ := f.Search(t, 1, &types.SubmissionsFilter{OrderBy: &tc.order})
		require.Equal(t, tc.desc, metadataOrderKeys(rows), "existing default direction remains descending")
	}
	// All primary keys equal, including two legacy UUIDs: retain deterministic pages.
	for _, query := range []string{`UPDATE curation_meta SET title='same', platform='same', library='same'`, `UPDATE masterdb_game SET title='same', platform='same', library='same'`} {
		_, err := f.Maria.Exec(query)
		require.NoError(t, err)
	}
	for _, tc := range metadataOrderCases {
		for _, direction := range []string{"asc", "desc"} {
			filter := &types.SubmissionsFilter{OrderBy: &tc.order, AscDesc: &direction, ResultsPerPage: utils.Int64Ptr(2)}
			var all []*types.ExtendedSubmission
			for page := int64(1); page <= 3; page++ {
				filter.Page = &page
				rows, count := f.Search(t, 1, filter)
				require.EqualValues(t, 5, count)
				all = append(all, rows...)
			}
			require.Equal(t, []string{"L1", "L2", "101", "102", "103"}, metadataOrderKeys(all), tc.order+direction)
		}
	}
}

func TestSubmissionSearchMetadataOrderingHTTPForm(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	f := &sqlFixture{DB: db, Maria: maria, Ctx: context.WithValue(ctx, utils.CtxKeys.Log, l)}
	seedMetadataOrdering(t, f)
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 1, []int64{roleIDCurator, roleIDTester}, "Ordering author")
	for _, tc := range metadataOrderCases {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(tc.order+"/"+direction, func(t *testing.T) {
				filter := &types.SubmissionsFilter{OrderBy: &tc.order, AscDesc: &direction}
				query := submissionFilterFormQuery(t, root, "", "advanced", filter)
				values, err := url.ParseQuery(query)
				require.NoError(t, err)
				require.Equal(t, []string{tc.order}, values["order-by"])
				require.Equal(t, direction, values.Get("asc-desc"))
				rr := getWithCookie(t, l, app, user.Cookie, "/api/submissions?"+query)
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				var page types.SubmissionsPageData
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
				want := tc.asc
				if direction == "desc" {
					want = tc.desc
				}
				require.Equal(t, want, metadataOrderKeys(page.Submissions))
				require.EqualValues(t, 5, page.TotalCount)
				require.Equal(t, filter.OrderBy, page.Filter.OrderBy)
			})
		}
	}
}
