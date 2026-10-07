package integration_tests

import (
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestSubmissionSearchPageTotalsForEmptyAndLaterPages(t *testing.T) {
	f, _ := seedSearchFixture(t)
	_, err := f.Maria.Exec(`UPDATE comment SET message='Page total needle' WHERE fk_submission_id IN (101,102,103)`)
	require.NoError(t, err)
	for _, filter := range []types.SubmissionsFilter{
		{CommentPartial: utils.StrPtr("total NEEDLE")},
		{CommentPartial: utils.StrPtr("total NEEDLE"), PlatformPartial: utils.StrPtr("Flash")},
		{PlatformPartial: utils.StrPtr("Flash"), ExcludeLegacy: true},
	} {
		all, total := f.Search(t, 1004, &filter)
		require.NotEmpty(t, all)
		filter.ResultsPerPage = utils.Int64Ptr(1)
		for page := int64(1); page <= total+2; page++ {
			filter.Page = &page
			rows, count := f.Search(t, 1004, &filter)
			require.Equal(t, total, count)
			if page <= total {
				require.Equal(t, all[page-1:page], rows)
			} else {
				require.Empty(t, rows, "a later empty page must retain its total")
			}
		}
	}
	_, err = f.Maria.Exec(`UPDATE submission SET deleted_at=?`, fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	for _, page := range []int64{1, 2} {
		rows, count := f.Search(t, 1004, &types.SubmissionsFilter{CommentPartial: utils.StrPtr("total needle"), Page: &page})
		require.Empty(t, rows)
		require.Zero(t, count)
	}
}

func TestSubmissionSearchLargePageMatchesOrdinaryPages(t *testing.T) {
	f, _ := seedSearchFixture(t)
	for _, base := range []types.SubmissionsFilter{
		{}, {ExcludeLegacy: true}, {TitlePartial: utils.StrPtr("Alpha")},
		{PlatformPartial: utils.StrPtr("Flash")}, {BotActions: []string{"approve"}},
		{CommentPartial: utils.StrPtr("cache-bulk-needle")},
	} {
		_, err := f.Maria.Exec(`UPDATE comment SET message='cache-bulk-needle' WHERE fk_submission_id=101`)
		require.NoError(t, err)
		for _, order := range []string{"uploaded", "updated", "size", "title", "platform", "library"} {
			for _, direction := range []string{"asc", "desc"} {
				filter := base
				filter.OrderBy, filter.AscDesc = &order, &direction
				want, total := f.Search(t, 1004, &filter)
				filter.ResultsPerPage = utils.Int64Ptr(100_000)
				got, count := f.Search(t, 1004, &filter)
				require.Equal(t, want, got, order+direction)
				require.Equal(t, total, count)
				filter.Page = utils.Int64Ptr(2)
				got, count = f.Search(t, 1004, &filter)
				require.Empty(t, got)
				require.Equal(t, total, count)
			}
		}
	}
}

// Filtering and pagination must choose the same cache/metadata row that is
// displayed, even on historical data without a unique cache or metadata key.
func TestSubmissionSearchPagePreservesMatchingJoinedRows(t *testing.T) {
	f, _ := seedSearchFixture(t)
	_, err := f.Maria.Exec(`INSERT INTO submission_cache SELECT * FROM submission_cache WHERE fk_submission_id=101`)
	require.NoError(t, err)
	_, err = f.Maria.Exec(`UPDATE submission_cache SET bot_action='reject', active_assigned_testing_ids='1003' WHERE fk_submission_id=101 LIMIT 1`)
	require.NoError(t, err)
	for _, action := range []string{"approve", "reject"} {
		rows, count := f.Search(t, 1004, &types.SubmissionsFilter{
			SubmissionIDs: []int64{101}, BotActions: []string{action}, ResultsPerPage: utils.Int64Ptr(1),
		})
		require.EqualValues(t, 1, count)
		require.Len(t, rows, 1)
		require.Equal(t, action, rows[0].BotAction)
		if action == "reject" {
			require.Equal(t, []int64{1003}, rows[0].AssignedTestingUserIDs)
		}
	}
	_, err = f.Maria.Exec(`INSERT INTO curation_meta (fk_submission_file_id,title,platform,library,extreme,launch_command,ruffle_support) VALUES (10002,'Matching duplicate metadata','HTML5','arcade','No','duplicate.html','')`)
	require.NoError(t, err)
	for _, order := range []string{"uploaded", "updated", "size", "title", "platform", "library"} {
		filter := &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Matching duplicate metadata"), OrderBy: &order, ResultsPerPage: utils.Int64Ptr(1)}
		rows, count := f.Search(t, 1004, filter)
		require.EqualValues(t, 1, count, order)
		require.Len(t, rows, 1, order)
		require.EqualValues(t, 101, rows[0].SubmissionID)
		require.Equal(t, utils.StrPtr("Matching duplicate metadata"), rows[0].CurationTitle)
		require.Equal(t, utils.StrPtr("HTML5"), rows[0].CurationPlatform)
		require.EqualValues(t, 2, rows[0].FileCount)
		filter.Page = utils.Int64Ptr(2)
		rows, count = f.Search(t, 1004, filter)
		require.Empty(t, rows)
		require.EqualValues(t, 1, count)
	}
}
