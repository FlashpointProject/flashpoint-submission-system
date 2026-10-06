package integration_tests

import (
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestSubmissionSearchLegacyTextAndNulls(t *testing.T) {
	f := newSQLFixture(t)
	text := fixtureMeta("Café O'Brien_100% \\ path,終")
	text.AlternateTitles = nil
	f.Legacy(t, 1, text, fixtureEpoch, fixtureEpoch)
	nullable := fixtureMeta("")
	nullable.Title = nil
	nullable.AlternateTitles = utils.StrPtr("Alternate only")
	nullable.Platform = nil
	f.Legacy(t, 2, nullable, fixtureEpoch, fixtureEpoch.Add(time.Microsecond))
	for _, tc := range []struct {
		name, query string
		titles      []*string
	}{
		{"case and accent insensitive baseline", "CAFE", []*string{text.Title}},
		{"apostrophe is data", "O'Brien", []*string{text.Title}},
		{"non ASCII", "終", []*string{text.Title}},
		{"comma is title data", "path,終", []*string{text.Title}},
		{"percent remains wildcard", "%", []*string{nil, text.Title}},
		{"underscore remains wildcard", "100_", []*string{text.Title}},
		{"backslash escapes LIKE wildcard", `100\%`, []*string{text.Title}},
		{"null title matches alternate", "Alternate only", []*string{nil}},
		{"no match", "absent", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, count := f.Search(t, 1, &types.SubmissionsFilter{TitlePartial: &tc.query})
			require.EqualValues(t, len(tc.titles), count)
			require.Len(t, rows, len(tc.titles))
			for i, row := range rows {
				require.Equal(t, tc.titles[i], row.CurationTitle)
			}
		})
	}
	rows, count := f.Search(t, 1, &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("!Unity")})
	require.EqualValues(t, 1, count, "SQL NOT LIKE excludes NULL platforms")
	require.Equal(t, text.Title, rows[0].CurationTitle)
	rows, count = f.Search(t, 1, &types.SubmissionsFilter{ExcludeLegacy: true})
	require.Empty(t, rows)
	require.Zero(t, count)
}

// UUID-distinct games remain separate even when all displayed metadata matches.
func TestSubmissionSearchLegacyIdentity(t *testing.T) {
	f := newSQLFixture(t)
	for _, id := range []int64{1, 2} {
		f.Legacy(t, id, fixtureMeta("Identical"), fixtureEpoch, fixtureEpoch)
	}
	rows, count := f.Search(t, 1, nil)
	require.EqualValues(t, 2, count)
	require.Len(t, rows, 2)
	require.EqualValues(t, -1, rows[0].SubmissionID)
	require.Equal(t, "Identical", *rows[0].CurationTitle)
	require.Equal(t, "Identical", *rows[1].CurationTitle)
	for i, id := range []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"} {
		require.Equal(t, &id, rows[i].GameUUID)
		pageRows, total := f.Search(t, 1, &types.SubmissionsFilter{ResultsPerPage: utils.Int64Ptr(1), Page: utils.Int64Ptr(int64(i + 1))})
		require.EqualValues(t, 2, total)
		require.Len(t, pageRows, 1)
		require.Equal(t, &id, pageRows[0].GameUUID)
	}
}
