package integration_tests

import (
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestSubmissionSearchPlatformDatePages(t *testing.T) {
	f, want := seedSearchFixture(t)
	_, err := f.Maria.ExecContext(f.Ctx, `UPDATE curation_meta SET platform='HTML5' WHERE fk_submission_file_id=30001`)
	require.NoError(t, err)
	want["C"].CurationPlatform = utils.StrPtr("HTML5")
	for _, query := range []string{`UPDATE submission_file SET created_at=?`, `UPDATE comment SET created_at=?`, `UPDATE masterdb_game SET date_added=?, date_modified=?`} {
		args := []any{fixtureEpoch}
		if query == `UPDATE masterdb_game SET date_added=?, date_modified=?` {
			args = append(args, fixtureEpoch)
		}
		_, err = f.Maria.ExecContext(f.Ctx, testSQL(query), args...)
		require.NoError(t, err)
	}
	for _, row := range want {
		row.UploadedAt, row.UpdatedAt = fixtureEpoch, fixtureEpoch
	}
	for _, tc := range []struct {
		platform string
		keys     []string
	}{
		{"Flash", []string{"L1", "A"}},
		{"Unity", []string{"L2", "B"}},
		{"HTML5", []string{"C"}},
		{"!Unity", []string{"L1", "A", "C"}},
		{"Flash,Unity", []string{"L1", "L2", "A", "B"}},
		{"Flash,Unity,!Arcade", []string{"L1", "L2", "B"}},
		{"absent-platform", nil},
	} {
		for _, order := range []string{"uploaded", "updated"} {
			for _, direction := range []string{"asc", "desc"} {
				t.Run(tc.platform+"/"+order+"/"+direction, func(t *testing.T) {
					filter := &types.SubmissionsFilter{PlatformPartial: &tc.platform, OrderBy: &order, AscDesc: &direction, ResultsPerPage: utils.Int64Ptr(1)}
					for page := 1; page <= len(tc.keys)+1; page++ {
						filter.Page = utils.Int64Ptr(int64(page))
						rows, count := f.Search(t, 1004, filter)
						require.EqualValues(t, len(tc.keys), count)
						if page > len(tc.keys) {
							require.Empty(t, rows)
							continue
						}
						require.Len(t, rows, 1)
						require.Equal(t, canonicalSearchSubmission(want[tc.keys[page-1]]), canonicalSearchSubmission(rows[0]))
					}
				})
			}
		}
	}
}
