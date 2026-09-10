package integration_tests

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Cache sequences represent sets: the production GROUP_CONCAT expressions do not
// specify ordering. Compare membership without making an accidental order contract.
// Companion <column>.valid entries preserve SQL NULL versus a present empty string
// during whole-snapshot comparisons; individual value assertions stay readable.
func cacheSnapshot(t *testing.T, f *sqlFixture, sid int64) map[string]string {
	t.Helper()
	columns := []string{"fk_oldest_file_id", "fk_newest_file_id", "fk_newest_comment_id", "active_assigned_testing_ids", "active_assigned_verification_ids", "active_requested_changes_ids", "active_approved_ids", "active_verified_ids", "original_filename_sequence", "current_filename_sequence", "md5sum_sequence", "sha256sum_sequence", "bot_action", "distinct_actions"}
	values := make([]sql.NullString, len(columns))
	args := make([]interface{}, len(columns))
	for i := range values {
		args[i] = &values[i]
	}
	require.NoError(t, f.Maria.QueryRowContext(f.Ctx, testSQL("SELECT "+strings.Join(columns, ",")+" FROM submission_cache WHERE fk_submission_id = ?"), sid).Scan(args...))
	result := make(map[string]string, 2*len(columns))
	for i, key := range columns {
		result[key+".valid"] = strconv.FormatBool(values[i].Valid)
		if values[i].Valid {
			parts := strings.Split(values[i].String, ",")
			sort.Strings(parts)
			result[key] = strings.Join(parts, ",")
		} else {
			result[key] = ""
		}
	}
	return result
}

func deleteFixtureComment(t *testing.T, f *sqlFixture, id int64) {
	t.Helper()
	_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE comment SET deleted_at = ? WHERE id = ?"), fixtureEpoch.Add(100*time.Hour), id)
	require.NoError(t, err)
}

func TestSubmissionCacheActionHistories(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 101, "reviewer one")
	f.User(t, 102, "reviewer two")
	pairs := []struct{ name, column, enabler, disabler string }{
		{"testing", "active_assigned_testing_ids", "assign-testing", "unassign-testing"},
		{"verification assignment", "active_assigned_verification_ids", "assign-verification", "unassign-verification"},
		{"requested changes cleared by approve", "active_requested_changes_ids", "request-changes", "approve"},
		{"requested changes cleared by verify", "active_requested_changes_ids", "request-changes", "verify"},
		{"approval", "active_approved_ids", "approve", "request-changes"},
		{"verification", "active_verified_ids", "verify", "request-changes"},
	}
	var id int64 = 1000
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			id += 100
			sid := id
			f.Submission(t, sid, "staff")
			f.File(t, fixtureFile{ID: sid, SubmissionID: sid, UserID: 101, At: fixtureEpoch})
			add := func(cid, uid int64, action string, seconds int) {
				f.Comment(t, cid, sid, uid, action, fixtureEpoch.Add(time.Duration(seconds)*time.Second), nil)
			}
			check := func(want string) {
				t.Helper()
				f.Rebuild(t, sid)
				snapshot := cacheSnapshot(t, f, sid)
				require.Equal(t, want, snapshot[pair.column])
				require.Equal(t, strconv.FormatBool(want != ""), snapshot[pair.column+".valid"], "empty reviewer sets are SQL NULL")
			}
			add(sid+1, 101, pair.enabler, 1)
			add(sid+2, 102, pair.enabler, 2)
			check("101,102")
			add(sid+3, 101, pair.enabler, 3)
			check("101,102") // duplicate enabler does not duplicate membership
			add(sid+4, 101, pair.disabler, 4)
			check("102") // disablers affect their author only
			deleteFixtureComment(t, f, sid+4)
			check("101,102") // deleting the disabler restores earlier enabler
			add(sid+5, 101, pair.disabler, 5)
			add(sid+6, 101, pair.enabler, 6)
			check("101,102")
			deleteFixtureComment(t, f, sid+6)
			check("102") // deleted latest enabler reveals live disabler
			add(sid+7, 102, pair.disabler, 2)
			check("") // later comment ID disables an equal-timestamp enabler
			add(sid+8, 102, pair.enabler, 2)
			check("102") // re-enabling later at the same timestamp restores membership
			add(sid+9, 102, pair.disabler, 2)
			check("") // the inverse order leaves membership disabled
			deleteFixtureComment(t, f, sid+9)
			check("102") // deleting the tied disabler reveals the latest live action
		})
	}
}

func TestSubmissionCacheUploadVersionBoundaries(t *testing.T) {
	f := newSQLFixture(t)
	for _, uid := range []int64{101, 102, 103, 104} {
		f.User(t, uid, "reviewer "+strconv.FormatInt(uid, 10))
	}
	for n, action := range []string{"approve", "verify"} {
		t.Run(action, func(t *testing.T) {
			sid := int64(2000 + n)
			f.Submission(t, sid, "staff")
			f.File(t, fixtureFile{ID: sid, SubmissionID: sid, UserID: 101, At: fixtureEpoch.Add(-time.Hour)})
			f.File(t, fixtureFile{ID: sid + 10, SubmissionID: sid, UserID: 101, At: fixtureEpoch})
			for i, uid := range []int64{101, 102, 103} {
				f.Comment(t, sid*10+int64(i), sid, uid, action, fixtureEpoch.Add(time.Duration(i-1)*time.Microsecond), nil)
			}
			// Assignment and requested changes deliberately survive upload version boundaries.
			f.Comment(t, sid*10+3, sid, 101, "assign-testing", fixtureEpoch.Add(-2*time.Second), nil)
			f.Comment(t, sid*10+4, sid, 102, "assign-verification", fixtureEpoch.Add(-2*time.Second), nil)
			f.Comment(t, sid*10+5, sid, 103, "request-changes", fixtureEpoch.Add(-2*time.Second), nil)
			f.Comment(t, sid*10+6, sid, 104, "request-changes", fixtureEpoch.Add(-2*time.Second), nil)
			f.Rebuild(t, sid)
			got := cacheSnapshot(t, f, sid)
			column := "active_approved_ids"
			if action == "verify" {
				column = "active_verified_ids"
			}
			require.Equal(t, "103", got[column], "only actions strictly after newest upload count")
			require.Equal(t, "101", got["active_assigned_testing_ids"])
			require.Equal(t, "102", got["active_assigned_verification_ids"])
			require.Equal(t, "104", got["active_requested_changes_ids"], "upload preserves requests; later approval/verification clears only its author request")
			_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission_file SET deleted_at = ? WHERE id = ?"), fixtureEpoch.Add(time.Hour), sid+10)
			require.NoError(t, err)
			f.Rebuild(t, sid)
			require.Equal(t, "101,102,103", cacheSnapshot(t, f, sid)[column], "deleting newest version restores prior version boundary")
		})
	}
}

func TestSubmissionCacheDeletedFilesBotRejectAndRebuild(t *testing.T) {
	f := newSQLFixture(t)
	const bot int64 = 810112564787675166
	f.User(t, 101, "reviewer")
	f.User(t, 102, "changes reviewer")
	f.User(t, bot, "validator")
	const sid int64 = 3000
	f.Submission(t, sid, "staff")
	for i := int64(1); i <= 3; i++ {
		f.File(t, fixtureFile{ID: i, SubmissionID: sid, UserID: 101, At: fixtureEpoch.Add(time.Duration(i) * time.Second), Original: "original" + strconv.FormatInt(i, 10), Current: "current" + strconv.FormatInt(i, 10), MD5: "md5" + strconv.FormatInt(i, 10), SHA256: "sha" + strconv.FormatInt(i, 10)})
	}
	f.Comment(t, 1, sid, bot, "approve", fixtureEpoch.Add(4*time.Second), nil)
	f.Comment(t, 2, sid, 101, "approve", fixtureEpoch.Add(5*time.Second), nil)
	f.Comment(t, 3, sid, bot, "request-changes", fixtureEpoch.Add(6*time.Second), nil)
	f.Rebuild(t, sid)
	got := cacheSnapshot(t, f, sid)
	require.Equal(t, "101", got["active_approved_ids"], "bot excluded from reviewer sets")
	require.Empty(t, got["active_requested_changes_ids"])
	require.Equal(t, "request-changes", got["bot_action"])
	deleteFixtureComment(t, f, 3)
	f.Rebuild(t, sid)
	got = cacheSnapshot(t, f, sid)
	require.Equal(t, "approve", got["bot_action"])
	require.Equal(t, "2", got["fk_newest_comment_id"])
	f.Comment(t, 5, sid, 101, "assign-testing", fixtureEpoch.Add(6*time.Second), nil)
	f.Comment(t, 6, sid, 101, "assign-verification", fixtureEpoch.Add(6*time.Second), nil)
	f.Comment(t, 7, sid, 101, "verify", fixtureEpoch.Add(6*time.Second), nil)
	f.Comment(t, 8, sid, 102, "request-changes", fixtureEpoch.Add(6500*time.Millisecond), nil)
	f.Rebuild(t, sid)
	beforeReject := cacheSnapshot(t, f, sid)
	for _, column := range []string{"active_approved_ids", "active_verified_ids", "active_assigned_testing_ids", "active_assigned_verification_ids", "active_requested_changes_ids"} {
		require.NotEmpty(t, beforeReject[column], "reject suppression must start with an active set")
	}
	f.Comment(t, 4, sid, 101, "reject", fixtureEpoch.Add(7*time.Second), nil)
	f.Rebuild(t, sid)
	got = cacheSnapshot(t, f, sid)
	require.Equal(t, "reject", got["distinct_actions"])
	for _, column := range []string{"active_approved_ids", "active_verified_ids", "active_assigned_testing_ids", "active_assigned_verification_ids", "active_requested_changes_ids"} {
		require.Empty(t, got[column])
	}
	deleteFixtureComment(t, f, 4)
	_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission_file SET deleted_at = ? WHERE id IN (1,3)"), fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	f.Rebuild(t, sid)
	got = cacheSnapshot(t, f, sid)
	require.Equal(t, "101", got["active_approved_ids"])
	require.Equal(t, "2", got["fk_oldest_file_id"])
	require.Equal(t, "2", got["fk_newest_file_id"])
	require.Equal(t, "original2", got["original_filename_sequence"])
	require.Equal(t, "current2", got["current_filename_sequence"])
	require.Equal(t, "md52", got["md5sum_sequence"])
	require.Equal(t, "sha2", got["sha256sum_sequence"])
	// Rebuild from an empty cache row must recover the same derived content as
	// repeated incremental updates. This tests the DAL rebuild, not >10k batch paging.
	_, err = f.Maria.ExecContext(f.Ctx, testSQL("DELETE FROM submission_cache WHERE fk_submission_id = ?"), sid)
	require.NoError(t, err)
	_, err = f.Maria.ExecContext(f.Ctx, testSQL("INSERT INTO submission_cache (fk_submission_id) VALUES (?)"), sid)
	require.NoError(t, err)
	f.Rebuild(t, sid)
	require.Equal(t, got, cacheSnapshot(t, f, sid))
	f.Rebuild(t, sid)
	require.Equal(t, got, cacheSnapshot(t, f, sid))
	_, err = f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission_file SET deleted_at = ? WHERE id = 2"), fixtureEpoch.Add(time.Hour))
	require.NoError(t, err)
	f.Rebuild(t, sid)
	got = cacheSnapshot(t, f, sid)
	require.Empty(t, got["fk_oldest_file_id"])
	require.Empty(t, got["fk_newest_file_id"])
	require.Empty(t, got["original_filename_sequence"])
	require.Empty(t, got["active_approved_ids"])
	for _, column := range []string{"fk_oldest_file_id", "fk_newest_file_id", "original_filename_sequence", "current_filename_sequence", "md5sum_sequence", "sha256sum_sequence", "active_approved_ids", "active_verified_ids"} {
		require.Equal(t, "false", got[column+".valid"], "no active file produces SQL NULL for %s", column)
	}
}
