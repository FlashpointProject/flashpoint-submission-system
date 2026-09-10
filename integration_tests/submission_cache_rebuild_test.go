package integration_tests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// The traversal must work without files or caches, including beyond the old
// 10,000 limit. Test the real SQL cursor separately from expensive recomputation.
func TestSubmissionCacheRebuildSourceTraversal(t *testing.T) {
	f := newSQLFixture(t)
	const total = 10004
	f.InTx(t, func(s database.DBSession) {
		var level int64
		require.NoError(t, s.Tx().QueryRow(testSQL("SELECT id FROM submission_level WHERE name='trial'")).Scan(&level))
		stmt, err := s.Tx().Prepare(testSQL("INSERT INTO submission(id,fk_submission_level_id) VALUES (?,?)"))
		require.NoError(t, err)
		defer stmt.Close()
		for id := int64(1); id <= total; id++ {
			_, err = stmt.Exec(id, level)
			require.NoError(t, err)
		}
		_, err = s.Tx().Exec(testSQL("UPDATE submission SET deleted_at=? WHERE id IN(2,5001,10004)"), fixtureEpoch)
		require.NoError(t, err)
	})
	var all []int64
	var after int64
	var pageSizes []int
	for {
		var ids []int64
		f.InTx(t, func(s database.DBSession) {
			var err error
			ids, err = f.DB.ListSubmissionIDsForCacheRebuild(s, after, 10000)
			require.NoError(t, err)
		})
		if len(ids) == 0 {
			break
		}
		pageSizes = append(pageSizes, len(ids))
		all = append(all, ids...)
		after = ids[len(ids)-1]
	}
	require.Equal(t, []int{10000, 1}, pageSizes)
	want := make([]int64, 0, total-3)
	for id := int64(1); id <= total; id++ {
		if id != 2 && id != 5001 && id != 10004 {
			want = append(want, id)
		}
	}
	require.Equal(t, want, all, "every nondeleted source ID exactly once despite absent files/cache")
}

func rebuildFixtureService(f *sqlFixture) *service.SiteService {
	return service.NewWithMocks(utils.LogCtx(f.Ctx), f.Maria, nil, nil, nil, "", 0, "", "", true, nil, "", "")
}

func TestSubmissionCacheRebuildRepairsAndRepeats(t *testing.T) {
	f, want := seedSearchFixture(t)
	expectedA, expectedB := cacheSnapshot(t, f, 101), cacheSnapshot(t, f, 102)
	_, err := f.Maria.Exec(testSQL("DELETE FROM submission_cache WHERE fk_submission_id=101"))
	require.NoError(t, err)
	_, err = f.Maria.Exec(testSQL("UPDATE submission_cache SET active_approved_ids='999',fk_newest_file_id=30001,bot_action='reject' WHERE fk_submission_id=102"))
	require.NoError(t, err)
	_, err = f.Maria.Exec(testSQL("UPDATE submission SET deleted_at=? WHERE id=103"), fixtureEpoch)
	require.NoError(t, err)
	deletedCache := cacheSnapshot(t, f, 103)
	f.Submission(t, 104, "trial")
	_, err = f.Maria.Exec(testSQL("DELETE FROM submission_cache WHERE fk_submission_id=104"))
	require.NoError(t, err)
	svc := rebuildFixtureService(f)
	fileFilters := []*types.SubmissionsFilter{
		{OriginalFilenamePartialAny: utils.StrPtr("original-10002.7z")},
		{CurrentFilenamePartialAny: utils.StrPtr("current-10002.7z")},
		{MD5SumPartialAny: utils.StrPtr(fmt.Sprintf("%032x", 10002))},
		{SHA256SumPartialAny: utils.StrPtr(fmt.Sprintf("%064x", 10002))},
	}
	for pass := 0; pass < 3; pass++ {
		if pass == 1 {
			// The production snapshot also contained caches with a correct newest
			// pointer but collections containing only the older live file.
			corruptCollections := `UPDATE submission_cache c JOIN submission_file f ON f.id=10001
				SET c.original_filename_sequence=f.original_filename,
				c.current_filename_sequence=f.current_filename,
				c.md5sum_sequence=f.md5sum,c.sha256sum_sequence=f.sha256sum
				WHERE c.fk_submission_id=101`
			if postgresSubmissionTests() {
				corruptCollections = `UPDATE submission_cache c
				SET original_filename_sequence=f.original_filename,
				current_filename_sequence=f.current_filename,
				md5sum_sequence=f.md5sum,sha256sum_sequence=f.sha256sum
				FROM submission_file f WHERE f.id=10001 AND c.fk_submission_id=101`
			}
			_, err = f.Maria.Exec(corruptCollections)
			require.NoError(t, err)
			stale := cacheSnapshot(t, f, 101)
			require.Equal(t, expectedA["fk_newest_file_id"], stale["fk_newest_file_id"])
			for _, column := range []string{"original_filename_sequence", "current_filename_sequence", "md5sum_sequence", "sha256sum_sequence"} {
				require.NotEqual(t, expectedA[column], stale[column], column)
			}
			for _, filter := range fileFilters {
				checkSearch(t, f, want, 1004, filter, nil, 0)
			}
		}
		result, err := svc.RecomputeSubmissionCacheAll(f.Ctx)
		require.NoError(t, err)
		require.EqualValues(t, 3, result.Recomputed)
		require.EqualValues(t, 104, result.LastSubmissionID)
		require.Equal(t, expectedA, cacheSnapshot(t, f, 101))
		require.Equal(t, expectedB, cacheSnapshot(t, f, 102))
		require.Equal(t, deletedCache, cacheSnapshot(t, f, 103), "deleted submission cache untouched")
		for key, value := range cacheSnapshot(t, f, 104) {
			if strings.HasSuffix(key, ".valid") {
				require.Equal(t, "false", value, key)
			}
		}
		checkSearch(t, f, want, 1004, &types.SubmissionsFilter{SubmissionIDs: []int64{101, 102}}, []string{"B", "A"}, 2)
		for _, filter := range fileFilters {
			checkSearch(t, f, want, 1004, filter, []string{"A"}, 1)
		}
		var count int
		require.NoError(t, f.Maria.QueryRow(testSQL("SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id IN(101,102,104)")).Scan(&count))
		require.Equal(t, 3, count)
	}
}

func TestSubmissionCacheRebuildFailureProgressAndRetry(t *testing.T) {
	f := newSQLFixture(t)
	for _, id := range []int64{10, 20, 30} {
		f.Submission(t, id, "trial")
	}
	_, err := f.Maria.Exec(testSQL("DELETE FROM submission_cache"))
	require.NoError(t, err)
	_, err = f.Maria.Exec(testTriggerSQL(`CREATE TRIGGER fail_rebuild_update BEFORE UPDATE ON submission_cache FOR EACH ROW BEGIN IF NEW.fk_submission_id=20 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='rebuild injected failure'; END IF; END`))
	require.NoError(t, err)
	t.Cleanup(func() { _, err := f.Maria.Exec(testDropTriggerSQL("fail_rebuild_update")); require.NoError(t, err) })
	svc := rebuildFixtureService(f)
	result, err := svc.RecomputeSubmissionCacheAll(f.Ctx)
	require.ErrorContains(t, err, "rebuild injected failure")
	require.ErrorContains(t, err, "20")
	require.EqualValues(t, 1, result.Recomputed)
	require.EqualValues(t, 10, result.LastSubmissionID)
	var ids string
	idsQuery := "SELECT GROUP_CONCAT(fk_submission_id ORDER BY fk_submission_id) FROM submission_cache"
	if postgresSubmissionTests() {
		idsQuery = "SELECT string_agg(fk_submission_id::text, ',' ORDER BY fk_submission_id) FROM submission_cache"
	}
	require.NoError(t, f.Maria.QueryRow(idsQuery).Scan(&ids))
	require.Equal(t, "10", ids, "failed item's inserted cache row rolled back; later item untouched")
	_, err = f.Maria.Exec(testDropTriggerSQL("fail_rebuild_update"))
	require.NoError(t, err)
	result, err = svc.RecomputeSubmissionCacheAll(f.Ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, result.Recomputed)
	require.EqualValues(t, 30, result.LastSubmissionID)
	require.NoError(t, f.Maria.QueryRow(idsQuery).Scan(&ids))
	require.Equal(t, "10,20,30", ids)
}

func TestSubmissionCacheRebuildRejectsDuplicateCache(t *testing.T) {
	f := newSQLFixture(t)
	f.Submission(t, 1, "trial")
	before := cacheSnapshot(t, f, 1)
	_, err := f.Maria.Exec(testSQL("INSERT INTO submission_cache(fk_submission_id,bot_action) VALUES(1,'duplicate sentinel')"))
	if postgresSubmissionTests() {
		require.ErrorContains(t, err, "23505", "target uniqueness rejects corrupt cache before it can persist")
		require.Equal(t, before, cacheSnapshot(t, f, 1))
		result, rebuildErr := rebuildFixtureService(f).RecomputeSubmissionCacheAll(f.Ctx)
		require.NoError(t, rebuildErr)
		require.EqualValues(t, 1, result.Recomputed)
		var count int
		require.NoError(t, f.Maria.QueryRow("SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id=1").Scan(&count))
		require.Equal(t, 1, count)
		return
	}
	require.NoError(t, err)
	result, err := rebuildFixtureService(f).RecomputeSubmissionCacheAll(f.Ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "submission 1 has 2 cache rows")
	require.Zero(t, result.Recomputed)
	var count int
	require.NoError(t, f.Maria.QueryRow(testSQL("SELECT COUNT(*) FROM submission_cache WHERE fk_submission_id=1")).Scan(&count))
	require.Equal(t, 2, count, "invalid duplicate source cache must not be silently rewritten")
	require.NoError(t, f.Maria.QueryRow(testSQL("SELECT COUNT(*) FROM submission_cache WHERE bot_action='duplicate sentinel'")).Scan(&count))
	require.Equal(t, 1, count)
}
