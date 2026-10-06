package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// Different values in every column catch accidental cross-field wiring. Queries
// are deliberately partial and differently cased, including punctuation/Unicode.
var metadataTextCases = []struct{ meta, filter, query, value, needle string }{
	{"Series", "SeriesPartial", "series-partial", "The Aurora Chronicles", "AURORA"},
	{"Developer", "DeveloperPartial", "developer-partial", "O'Brien Studios", "O'BRIEN"},
	{"Publisher", "PublisherPartial", "publisher-partial", "Northwind Publishing", "WIND PUB"},
	{"PlayMode", "PlayModePartial", "play-mode-partial", "Single Player; Multiplayer", "MULTI"},
	{"Status", "StatusPartial", "status-partial", "Playable with warnings", "WITH WARN"},
	{"Version", "VersionPartial", "version-partial", "v2.5-beta", "2.5-B"},
	{"ReleaseDate", "ReleaseDatePartial", "release-date-partial", "2004-06-17", "2004-06"},
	{"Languages", "LanguagePartial", "language-partial", "en; ja; cs", "JA"},
	{"Source", "SourcePartial", "source-partial", "https://example.org/Archive/Game", "ORG/ARCHIVE"},
	{"GameNotes", "GameNotesPartial", "game-notes-partial", "Press Enter to start", "ENTER TO"},
	{"CurationNotes", "CurationNotesPartial", "curation-notes-partial", "Restored missing assets", "MISSING ASSET"},
	{"OriginalDescription", "OriginalDescriptionPartial", "original-description-partial", "Explore 終 and Café <worlds>", "CAFÉ <WORLD"},
}

func metadataSearchFilter(field, value string) *types.SubmissionsFilter {
	f := &types.SubmissionsFilter{}
	reflect.ValueOf(f).Elem().FieldByName(field).Set(reflect.ValueOf(&value))
	return f
}
func richSearchMetadata() *types.CurationMeta {
	m := fixtureMeta("Metadata example")
	m.AlternateTitles = utils.StrPtr("Secondary title")
	for _, tc := range metadataTextCases {
		reflect.ValueOf(m).Elem().FieldByName(tc.meta).Set(reflect.ValueOf(utils.StrPtr(tc.value)))
	}
	m.Tags = utils.StrPtr("Puzzle; Arcade")
	m.AdditionalApps = []*types.CurationAdditionalApp{{Heading: utils.StrPtr("Manual")}}
	return m
}

func seedMetadataSearch(t *testing.T, f *sqlFixture) {
	t.Helper()
	f.User(t, 1, "Metadata author")
	empty := fixtureMeta("Empty metadata")
	for _, tc := range metadataTextCases {
		reflect.ValueOf(empty).Elem().FieldByName(tc.meta).Set(reflect.ValueOf(utils.StrPtr("")))
	}
	for _, spec := range []struct {
		id   int64
		meta *types.CurationMeta
	}{
		{101, richSearchMetadata()}, {102, fixtureMeta("Null metadata")}, {103, empty},
		{104, richSearchMetadata()}, {105, empty},
	} {
		f.Submission(t, spec.id, "trial")
		f.Comment(t, spec.id, spec.id, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Second), nil)
		f.File(t, fixtureFile{ID: spec.id * 10, SubmissionID: spec.id, UserID: 1, At: fixtureEpoch, Meta: spec.meta})
	}
	// Historical metadata must not match. The deleted latest upload must not match either.
	f.File(t, fixtureFile{ID: 1041, SubmissionID: 104, UserID: 1, At: fixtureEpoch.Add(time.Second), Meta: empty})
	f.File(t, fixtureFile{ID: 1051, SubmissionID: 105, UserID: 1, At: fixtureEpoch.Add(time.Second), Meta: richSearchMetadata()})
	_, err := f.Maria.Exec(`UPDATE submission_file SET deleted_at=? WHERE id=1051`, fixtureEpoch.Add(time.Minute))
	require.NoError(t, err)
	f.Rebuild(t, 101, 102, 103, 104, 105)
	f.Legacy(t, 1, richSearchMetadata(), fixtureEpoch, fixtureEpoch)
	f.Legacy(t, 2, fixtureMeta("Null legacy metadata"), fixtureEpoch, fixtureEpoch)
}

func TestSubmissionSearchMetadataText(t *testing.T) {
	f := newSQLFixture(t)
	seedMetadataSearch(t, f)
	for _, tc := range metadataTextCases {
		t.Run(tc.query, func(t *testing.T) {
			filter := metadataSearchFilter(tc.filter, tc.needle)
			rows, count := f.Search(t, 1, filter)
			want := []int64{-1, 101}
			if tc.meta == "CurationNotes" {
				want = []int64{101}
			}
			require.ElementsMatch(t, want, searchEdgeIDs(rows))
			require.EqualValues(t, len(want), count)
			if len(want) == 2 {
				for _, row := range rows {
					if row.SubmissionID == -1 {
						require.Equal(t, "00000000-0000-0000-0000-000000000001", *row.GameUUID)
					}
				}
			}
			filter.ExcludeLegacy = true
			rows, count = f.Search(t, 1, filter)
			require.Equal(t, []int64{101}, searchEdgeIDs(rows))
			require.EqualValues(t, 1, count)
			// Empty values must disable a predicate, including for NULL/missing data.
			rows, count = f.Search(t, 1, metadataSearchFilter(tc.filter, ""))
			require.Len(t, rows, 7)
			require.EqualValues(t, 7, count)
			// Quotes/SQL syntax must be treated as search text.
			rows, count = f.Search(t, 1, metadataSearchFilter(tc.filter, "' OR 1=1 --"))
			require.Empty(t, rows)
			require.Zero(t, count)
			// Combined pagination counts include all matches, not just this page.
			filter = metadataSearchFilter(tc.filter, tc.needle)
			filter.ResultsPerPage = utils.Int64Ptr(1)
			filter.Page = utils.Int64Ptr(2)
			rows, count = f.Search(t, 1, filter)
			require.EqualValues(t, len(want), count)
			require.Len(t, rows, len(want)-1)
		})
	}
	t.Run("all filters combine with existing title alternate platform and library", func(t *testing.T) {
		filter := &types.SubmissionsFilter{TitlePartial: utils.StrPtr("SECONDARY"), PlatformPartial: utils.StrPtr("flash,!unity"), LibraryPartial: utils.StrPtr("arc"), TagsPartial: utils.StrPtr("puzzle,!adult"), HasAdditionalApplications: utils.StrPtr("yes")}
		for _, tc := range metadataTextCases {
			reflect.ValueOf(filter).Elem().FieldByName(tc.filter).Set(reflect.ValueOf(utils.StrPtr(tc.needle)))
		}
		rows, count := f.Search(t, 1, filter)
		require.Equal(t, []int64{101}, searchEdgeIDs(rows))
		require.EqualValues(t, 1, count)
		filter.CurationNotesPartial = nil
		filter.HasAdditionalApplications = nil
		filter.LaunchCommandFuzzy = utils.StrPtr("content/")
		rows, count = f.Search(t, 1, filter)
		require.ElementsMatch(t, []int64{-1, 101}, searchEdgeIDs(rows))
		require.EqualValues(t, 2, count)
		filter.PublisherPartial = utils.StrPtr("absent publisher")
		rows, count = f.Search(t, 1, filter)
		require.Empty(t, rows)
		require.Zero(t, count)
	})
	// Replacing the matching current version must invalidate every metadata match.
	f.File(t, fixtureFile{ID: 1011, SubmissionID: 101, UserID: 1, At: fixtureEpoch.Add(time.Second), Meta: fixtureMeta("Replacement")})
	f.Rebuild(t, 101)
	for _, tc := range metadataTextCases {
		filter := metadataSearchFilter(tc.filter, tc.needle)
		filter.ExcludeLegacy = true
		rows, count := f.Search(t, 1, filter)
		require.Empty(t, rows, tc.query)
		require.Zero(t, count, tc.query)
	}
}

func TestSubmissionSearchMetadataTags(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 1, "Tags author")
	for i, tags := range []*string{utils.StrPtr("Puzzle; Casual"), utils.StrPtr("Arcade; Action"), utils.StrPtr("Puzzle; Adult"), utils.StrPtr("Strategy"), utils.StrPtr(""), nil} {
		id := int64(i + 1)
		meta := fixtureMeta(fmt.Sprint(id))
		meta.Tags = tags
		f.Submission(t, id, "trial")
		f.Comment(t, id, id, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Second), nil)
		f.File(t, fixtureFile{ID: id, SubmissionID: id, UserID: 1, At: fixtureEpoch, Meta: meta})
		f.Rebuild(t, id)
		f.Legacy(t, id, meta, fixtureEpoch, fixtureEpoch)
	}
	for _, tc := range []struct {
		query string
		ids   []int64
	}{
		{"PUZZ", []int64{1, 3}}, {"puzzle, arcade, !adult", []int64{1, 2}},
		{"!adult,!casual", []int64{2, 4, 5}}, {" arcade, , PUZZ , !Adult ", []int64{1, 2}},
		{"puzz,!puzz", nil}, {"' OR 1=1 --", nil}, {", ,", []int64{1, 2, 3, 4, 5, 6}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, count := f.Search(t, 1, &types.SubmissionsFilter{TagsPartial: &tc.query})
			require.EqualValues(t, 2*len(tc.ids), count)
			var current []int64
			var legacy []string
			for _, row := range rows {
				if row.GameUUID == nil {
					current = append(current, row.SubmissionID)
				} else {
					legacy = append(legacy, *row.GameUUID)
				}
			}
			require.ElementsMatch(t, tc.ids, current)
			var wantLegacy []string
			for _, id := range tc.ids {
				wantLegacy = append(wantLegacy, fmt.Sprintf("00000000-0000-0000-0000-%012d", id))
			}
			require.ElementsMatch(t, wantLegacy, legacy)
		})
	}
}

func TestSubmissionSearchMetadataAdditionalApplications(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 1, "Apps author")
	for _, id := range []int64{1, 2, 3, 4, 5, 6} {
		meta := fixtureMeta(fmt.Sprint(id))
		if id == 1 {
			meta.AdditionalApps = []*types.CurationAdditionalApp{{Heading: utils.StrPtr("Manual")}, {Heading: utils.StrPtr("Extras")}}
		}
		if id == 3 {
			meta.AdditionalApps = []*types.CurationAdditionalApp{}
		}
		f.Submission(t, id, "trial")
		f.Comment(t, id, id, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Second), nil)
		f.File(t, fixtureFile{ID: id, SubmissionID: id, UserID: 1, At: fixtureEpoch, Meta: meta})
		f.Rebuild(t, id)
	}
	_, err := f.Maria.Exec(`UPDATE curation_meta SET additional_applications='null' WHERE fk_submission_file_id=4`)
	require.NoError(t, err)
	_, err = f.Maria.Exec(`DELETE FROM curation_meta WHERE fk_submission_file_id=5`)
	require.NoError(t, err)
	f.File(t, fixtureFile{ID: 60, SubmissionID: 6, UserID: 1, At: fixtureEpoch.Add(time.Second), Meta: richSearchMetadata()})
	f.Rebuild(t, 6)
	f.Legacy(t, 1, richSearchMetadata(), fixtureEpoch, fixtureEpoch)
	for _, tc := range []struct {
		value string
		ids   []int64
	}{
		{"yes", []int64{1, 6}}, {"no", []int64{2, 3, 4}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			rows, count := f.Search(t, 1, &types.SubmissionsFilter{HasAdditionalApplications: &tc.value})
			require.ElementsMatch(t, tc.ids, searchEdgeIDs(rows))
			require.EqualValues(t, len(tc.ids), count)
		})
	}
	_, err = f.Maria.Exec(`UPDATE submission_file SET deleted_at=? WHERE id=60`, fixtureEpoch.Add(time.Minute))
	require.NoError(t, err)
	f.Rebuild(t, 6)
	rows, count := f.Search(t, 1, &types.SubmissionsFilter{HasAdditionalApplications: utils.StrPtr("no")})
	require.ElementsMatch(t, []int64{2, 3, 4, 6}, searchEdgeIDs(rows))
	require.EqualValues(t, 4, count)
}

func TestSubmissionSearchMetadataHTTPForm(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	f := &sqlFixture{DB: db, Maria: maria, Ctx: context.WithValue(ctx, utils.CtxKeys.Log, l)}
	seedMetadataSearch(t, f)
	user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 1, []int64{roleIDCurator, roleIDTester}, "Metadata author")
	for _, tc := range append(metadataTextCases,
		struct{ meta, filter, query, value, needle string }{"Tags", "TagsPartial", "tags-partial", "Puzzle; Arcade", "PUZZ,!adult"},
		struct{ meta, filter, query, value, needle string }{"AdditionalApps", "HasAdditionalApplications", "has-additional-applications", "", "yes"},
		struct{ meta, filter, query, value, needle string }{"NoAdditionalApps", "HasAdditionalApplications", "has-additional-applications", "", "no"},
	) {
		t.Run(tc.query, func(t *testing.T) {
			filter := metadataSearchFilter(tc.filter, tc.needle)
			query := submissionFilterFormQuery(t, root, "", "advanced", filter)
			values, err := url.ParseQuery(query)
			require.NoError(t, err)
			require.Equal(t, tc.needle, values.Get(tc.query), "rendered form must retain selection")
			for _, path := range []string{"/api/submissions?", "/api/my-submissions?"} {
				rr := getWithCookie(t, l, app, user.Cookie, path+query)
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				var page types.SubmissionsPageData
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
				want := []int64{-1, 101}
				if tc.meta == "CurationNotes" || tc.meta == "AdditionalApps" || strings.Contains(path, "my-submissions") {
					want = []int64{101}
				}
				if tc.meta == "NoAdditionalApps" {
					want = []int64{102, 103, 104, 105}
				}
				require.ElementsMatch(t, want, searchEdgeIDs(page.Submissions))
				require.EqualValues(t, len(want), page.TotalCount)
				require.Equal(t, utils.StrPtr(tc.needle), reflect.ValueOf(page.Filter).FieldByName(tc.filter).Interface())
			}
		})
	}
}
