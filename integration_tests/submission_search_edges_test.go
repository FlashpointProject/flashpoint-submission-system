package integration_tests

import (
	"context"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func searchEdgeIDs(rows []*types.ExtendedSubmission) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SubmissionID)
	}
	return ids
}

func TestSubmissionSearchDeletedHistoricalFile(t *testing.T) {
	f, _ := seedSearchFixture(t)
	filters := []struct {
		name   string
		filter *types.SubmissionsFilter
	}{
		{"original filename", &types.SubmissionsFilter{OriginalFilenamePartialAny: utils.StrPtr("historic,first.7z")}},
		{"current filename", &types.SubmissionsFilter{CurrentFilenamePartialAny: utils.StrPtr("historic-current.7z")}},
		{"MD5", &types.SubmissionsFilter{MD5SumPartialAny: utils.StrPtr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}},
		{"SHA256", &types.SubmissionsFilter{SHA256SumPartialAny: utils.StrPtr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}},
	}
	for _, tc := range filters {
		rows, count := f.Search(t, 1004, tc.filter)
		require.Equal(t, []int64{101}, searchEdgeIDs(rows), tc.name)
		require.EqualValues(t, 1, count, tc.name)
	}
	_, err := f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE submission_file SET deleted_at=? WHERE id=10001`), fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	f.Rebuild(t, 101)
	for _, tc := range filters {
		t.Run(tc.name, func(t *testing.T) {
			rows, count := f.Search(t, 1004, tc.filter)
			require.Empty(t, rows, "deleted history must no longer match")
			require.Zero(t, count)
		})
	}
	rows, count := f.Search(t, 1004, &types.SubmissionsFilter{SubmissionIDs: []int64{101}})
	require.Len(t, rows, 1, "remaining current upload keeps submission searchable")
	require.EqualValues(t, 1, count)
	require.EqualValues(t, 1, rows[0].FileCount)
	require.EqualValues(t, 10002, rows[0].FileID)
}

func TestSubmissionSearchDeletedSubmission(t *testing.T) {
	f, want := seedSearchFixture(t)
	checkSearch(t, f, want, 1004, &types.SubmissionsFilter{SubmissionIDs: []int64{101}}, []string{"A"}, 1)
	_, err := f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE submission SET deleted_at=? WHERE id=101`), fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	// Keep files, comments and cache present: the submission tombstone itself
	// must remove the row from both ordinary search and its count.
	checkSearch(t, f, want, 1004, &types.SubmissionsFilter{SubmissionIDs: []int64{101}}, nil, 0)
	checkSearch(t, f, want, 1004, &types.SubmissionsFilter{ExcludeLegacy: true}, []string{"C", "B"}, 2)
	checkSearch(t, f, want, 1004, nil, []string{"C", "L1", "B", "L2"}, 4)
}

func TestSubmissionSearchCurrentBugCountUsesSeparateTransaction(t *testing.T) {
	f, _ := seedSearchFixture(t)
	ctx, cancel := context.WithTimeout(context.WithValue(f.Ctx, utils.CtxKeys.UserID, int64(1004)), 15*time.Second)
	defer cancel()
	session, err := f.DB.NewSession(ctx)
	require.NoError(t, err)
	defer session.Rollback()
	_, err = session.Tx().ExecContext(ctx, testSQL(`UPDATE submission SET deleted_at=? WHERE id=101`), fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	rows, count, err := f.DB.SearchSubmissions(session, &types.SubmissionsFilter{ExcludeLegacy: true})
	require.NoError(t, err)
	require.Equal(t, []int64{103, 102}, searchEdgeIDs(rows), "row query sees its transaction's uncommitted deletion")
	// Reproducer, not the migration contract: a separate connection counts the
	// still-committed row. No concurrent writer, sleep or timing race is needed.
	if postgresSubmissionTests() {
		require.EqualValues(t, 2, count, "PostgreSQL returns rows and count from the same statement snapshot")
		require.Equal(t, int64(len(rows)), count)
	} else {
		require.EqualValues(t, 3, count, "current separate-snapshot bug changed; require one snapshot when fixed")
		require.NotEqual(t, int64(len(rows)), count)
	}
	require.NoError(t, session.Rollback())
	rows, count = f.Search(t, 1004, &types.SubmissionsFilter{ExcludeLegacy: true})
	require.Equal(t, []int64{103, 102, 101}, searchEdgeIDs(rows))
	require.EqualValues(t, 3, count, "rollback restores consistent rows and count")
}

func TestSubmissionSearchEqualSortKeyCohort(t *testing.T) {
	f, _ := seedSearchFixture(t)
	uploaded := fixtureEpoch.Add(-10 * time.Second)
	updated := fixtureEpoch.Add(time.Hour)
	_, err := f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE submission_file SET created_at=? WHERE id IN (10001,20001,30001)`), uploaded)
	require.NoError(t, err)
	_, err = f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE comment SET created_at=? WHERE id IN (6,10,12)`), updated)
	require.NoError(t, err)
	_, err = f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE submission_file SET size=300 WHERE id IN (10002,20001,30001)`))
	require.NoError(t, err)
	f.Rebuild(t, 101, 102, 103)
	for _, order := range []string{"uploaded", "updated", "size"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(order+"/"+direction, func(t *testing.T) {
				filter := &types.SubmissionsFilter{ExcludeLegacy: true, OrderBy: utils.StrPtr(order), AscDesc: utils.StrPtr(direction)}
				rows, count := f.Search(t, 1004, filter)
				require.Equal(t, []int64{101, 102, 103}, searchEdgeIDs(rows))
				require.EqualValues(t, 3, count)
				for _, row := range rows {
					switch order {
					case "uploaded":
						require.True(t, row.UploadedAt.Equal(uploaded))
					case "updated":
						require.True(t, row.UpdatedAt.Equal(updated))
					case "size":
						require.EqualValues(t, 300, row.Size)
					}
				}
				// Identity ascending breaks ties in either primary sort direction.
				filter.ResultsPerPage = utils.Int64Ptr(2)
				rows, count = f.Search(t, 1004, filter)
				require.Len(t, rows, 2)
				require.EqualValues(t, 3, count)
				require.Equal(t, []int64{101, 102}, searchEdgeIDs(rows))
				filter.Page = utils.Int64Ptr(2)
				last, total := f.Search(t, 1004, filter)
				require.EqualValues(t, 3, total)
				require.Equal(t, []int64{103}, searchEdgeIDs(last))
				ids := searchEdgeIDs(rows)
				require.NotEqual(t, ids[0], ids[1])
				for _, id := range ids {
					require.Contains(t, []int64{101, 102, 103}, id)
				}
			})
		}
	}
}

// Branch-local limits must preserve the global ordering across live and legacy
// results, including peers spanning several pages in either date direction.
func TestSubmissionSearchMixedDateTiePages(t *testing.T) {
	f, _ := seedSearchFixture(t)
	for _, query := range []string{
		`UPDATE submission_file SET created_at=?`,
		`UPDATE comment SET created_at=?`,
		`UPDATE masterdb_game SET date_added=?, date_modified=?`,
	} {
		args := []any{fixtureEpoch}
		if query == `UPDATE masterdb_game SET date_added=?, date_modified=?` {
			args = append(args, fixtureEpoch)
		}
		_, err := f.Maria.ExecContext(f.Ctx, testSQL(query), args...)
		require.NoError(t, err)
	}
	for _, order := range []string{"uploaded", "updated"} {
		for _, direction := range []string{"asc", "desc"} {
			filter := &types.SubmissionsFilter{OrderBy: &order, AscDesc: &direction, ResultsPerPage: utils.Int64Ptr(2)}
			var all []*types.ExtendedSubmission
			for page := int64(1); page <= 4; page++ {
				filter.Page = &page
				rows, total := f.Search(t, 1004, filter)
				require.EqualValues(t, 5, total)
				all = append(all, rows...)
				if page == 4 {
					require.Empty(t, rows)
				}
			}
			require.Equal(t, []int64{-1, -1, 101, 102, 103}, searchEdgeIDs(all), order+direction)
			require.NotNil(t, all[0].GameUUID)
			require.NotNil(t, all[1].GameUUID)
			require.Less(t, *all[0].GameUUID, *all[1].GameUUID)
		}
	}
}
