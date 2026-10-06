package integration_tests

import (
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

// Exercise the real upload/comment/deletion services, including implicit action
// comments. Raw SQL is used only for independent persisted-state assertions and
// erasing derived cache rows; no history is rewritten to make assertions pass.
func TestSubmissionMutationCacheSearchEquivalence(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	ctx = addContextValues(ctx, l, 87001, "mutation-equivalence")
	f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
	uploader := createExtendedTestUser(t, ctx, l, app, db, pgdb, 87001, []int64{roleIDCurator}, "uploader")
	tester := createExtendedTestUser(t, ctx, l, app, db, pgdb, 87002, []int64{roleIDTester}, "tester")
	verifier := createExtendedTestUser(t, ctx, l, app, db, pgdb, 87003, []int64{roleIDTester}, "verifier")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, 87004, []int64{roleIDModerator}, "moderator")
	// A SQL-only unrelated submission avoids duplicate-archive detection affecting
	// the service scenario, while proving mutations and rebuilds stay scoped.
	const unrelatedID int64 = 87000
	f.Submission(t, unrelatedID, "staff")
	f.File(t, fixtureFile{ID: 87000, SubmissionID: unrelatedID, UserID: uploader.ID, At: fixtureEpoch})
	f.Comment(t, 87000, unrelatedID, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(time.Second), nil)
	f.Rebuild(t, unrelatedID)
	unrelatedCache := cacheSnapshot(t, f, unrelatedID)
	unrelatedSearch := canonicalSearchSubmission(searchSubmissionByID(t, ctx, app, unrelatedID))

	sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", uploader.Cookie, nil)
	var firstFileID int64
	require.NoError(t, maria.QueryRow(testSQL("SELECT id FROM submission_file WHERE fk_submission_id=?"), sid).Scan(&firstFileID))
	// Every checkpoint asserts an independent expected state before checking
	// incremental maintenance against a completely discarded/rebuilt cache.
	checkpoint := func(name string, expected actionCounters, fileCount uint64, newestFileID int64, deleted bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			rows, count, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
			require.NoError(t, err)
			if deleted {
				require.Empty(t, rows)
				require.Zero(t, count)
			} else {
				require.Len(t, rows, 1)
				require.EqualValues(t, 1, count)
				sub := rows[0]
				assertActionCounters(t, sub, &expected)
				require.Equal(t, fileCount, sub.FileCount)
				require.Equal(t, newestFileID, sub.FileID)
				require.Equal(t, uploader.ID, sub.SubmitterID)
				require.Equal(t, uploader.ID, sub.LastUploaderID)
				require.Equal(t, constants.ActionApprove, sub.BotAction)
				var first, latest, commented time.Time
				var original, current string
				var size int64
				require.NoError(t, maria.QueryRow(testSQL("SELECT created_at FROM submission_file WHERE id=?"), firstFileID).Scan(&first))
				require.NoError(t, maria.QueryRow(testSQL("SELECT created_at,original_filename,current_filename,size FROM submission_file WHERE id=?"), newestFileID).Scan(&latest, &original, &current, &size))
				require.True(t, first.Equal(sub.UploadedAt), "oldest upload timestamp must roundtrip exactly")
				require.NoError(t, maria.QueryRow(testSQL("SELECT created_at FROM comment WHERE fk_submission_id=? AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1"), sid).Scan(&commented))
				require.True(t, commented.Equal(sub.UpdatedAt), "newest comment timestamp must roundtrip exactly")
				require.False(t, latest.Before(first))
				meta := defaultValidatorMockMeta()
				require.Equal(t, meta.Title, sub.CurationTitle)
				require.Equal(t, meta.AlternateTitles, sub.CurationAlternateTitles)
				require.Equal(t, meta.Platform, sub.CurationPlatform)
				require.Equal(t, meta.LaunchCommand, sub.CurationLaunchCommand)
				require.Equal(t, original, sub.OriginalFilename)
				require.Equal(t, current, sub.CurrentFilename)
				require.Equal(t, size, sub.Size)
			}
			// Direct source reads prove API comments preserve IDs, authors, action,
			// nullable message and exact microsecond timestamp, not just their count.
			var comments []*types.ExtendedComment
			if !deleted {
				comments = getComments(t, ctx, app, uploader.ID, sid)
			}
			raw, err := maria.Query(testSQL(`SELECT c.id,c.fk_user_id,a.name,c.message,c.created_at FROM comment c JOIN action a ON a.id=c.fk_action_id WHERE c.fk_submission_id=? AND c.deleted_at IS NULL ORDER BY c.created_at,c.id`), sid)
			require.NoError(t, err)
			i := 0
			for raw.Next() {
				var id, author int64
				var action string
				var message sql.NullString
				var at time.Time
				require.NoError(t, raw.Scan(&id, &author, &action, &message, &at))
				require.Less(t, i, len(comments))
				c := comments[i]
				require.Equal(t, id, c.CommentID)
				require.Equal(t, author, c.AuthorID)
				require.Equal(t, action, c.Action)
				if message.Valid {
					require.NotNil(t, c.Message)
					require.Equal(t, message.String, *c.Message)
				} else {
					require.Nil(t, c.Message)
				}
				require.True(t, at.Equal(c.CreatedAt), "comment timestamp must roundtrip exactly")
				require.Zero(t, at.Nanosecond()%1000)
				i++
			}
			require.NoError(t, raw.Err())
			require.NoError(t, raw.Close())
			require.Len(t, comments, i)
			before := cacheSnapshot(t, f, sid)
			if len(comments) > 0 {
				require.Equal(t, strconv.FormatInt(comments[len(comments)-1].CommentID, 10), before["fk_newest_comment_id"])
			}
			_, err = maria.Exec(testSQL("DELETE FROM submission_cache WHERE fk_submission_id=?"), sid)
			require.NoError(t, err)
			_, err = maria.Exec(testSQL("INSERT INTO submission_cache(fk_submission_id) VALUES (?)"), sid)
			require.NoError(t, err)
			f.Rebuild(t, sid)
			require.Equal(t, before, cacheSnapshot(t, f, sid), "fresh rebuild must reproduce incremental cache")
			rebuilt, rebuiltCount, err := app.Service.SearchSubmissions(ctx, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
			require.NoError(t, err)
			require.Equal(t, count, rebuiltCount)
			require.Len(t, rebuilt, len(rows))
			if len(rows) > 0 {
				require.Equal(t, canonicalSearchSubmission(rows[0]), canonicalSearchSubmission(rebuilt[0]))
				require.Equal(t, comments, getComments(t, ctx, app, uploader.ID, sid))
			}
			require.Equal(t, unrelatedCache, cacheSnapshot(t, f, unrelatedID))
			require.Equal(t, unrelatedSearch, canonicalSearchSubmission(searchSubmissionByID(t, ctx, app, unrelatedID)))
		})
	}
	action := func(user *extendedTestUser, action string, implicit string) int64 {
		t.Helper()
		before := getComments(t, ctx, app, uploader.ID, sid)
		message := "mutation equivalence: " + action
		started := time.Now().UTC().Truncate(time.Microsecond)
		rr := addComment(t, l, app, user.Cookie, sid, action, message)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		after := getComments(t, ctx, app, uploader.ID, sid)
		n := 1
		if implicit != "" {
			n++
		}
		require.Len(t, after, len(before)+n)
		require.Equal(t, before, after[:len(before)], "existing comments must be immutable")
		c := after[len(before)]
		require.Equal(t, user.ID, c.AuthorID)
		require.Equal(t, action, c.Action)
		if action == constants.ActionAssignTesting || action == constants.ActionAssignVerification {
			require.Nil(t, c.Message, "assignment messages are deliberately discarded")
		} else {
			require.Equal(t, &message, c.Message)
		}
		require.False(t, c.CreatedAt.Before(started))
		require.False(t, c.CreatedAt.After(time.Now().UTC().Add(time.Millisecond)))
		if implicit != "" {
			next := after[len(before)+1]
			require.Equal(t, user.ID, next.AuthorID)
			require.Equal(t, implicit, next.Action)
			require.Nil(t, next.Message)
			require.True(t, next.CreatedAt.Sub(c.CreatedAt) >= time.Microsecond, "implicit unassignment must be at least one microsecond after the action")
		}
		return c.CommentID
	}
	initialComments := getComments(t, ctx, app, uploader.ID, sid)
	require.Len(t, initialComments, 2)
	require.Equal(t, uploader.ID, initialComments[0].AuthorID)
	require.Equal(t, constants.ActionUpload, initialComments[0].Action)
	require.EqualValues(t, constants.ValidatorID, initialComments[1].AuthorID)
	require.Equal(t, constants.ActionApprove, initialComments[1].Action)
	checkpoint("upload", actionCounters{}, 1, firstFileID, false)
	action(tester, constants.ActionAssignTesting, "")
	checkpoint("assigned-testing", actionCounters{AssignedTestingUserIDs: []int64{tester.ID}}, 1, firstFileID, false)
	action(tester, constants.ActionRequestChanges, "")
	checkpoint("requested-changes", actionCounters{AssignedTestingUserIDs: []int64{tester.ID}, RequestedChangesUserIDs: []int64{tester.ID}}, 1, firstFileID, false)
	action(tester, constants.ActionApprove, constants.ActionUnassignTesting)
	checkpoint("approved-clears-request-and-assignment", actionCounters{ApprovedUserIDs: []int64{tester.ID}}, 1, firstFileID, false)
	action(verifier, constants.ActionAssignVerification, "")
	checkpoint("assigned-verification", actionCounters{ApprovedUserIDs: []int64{tester.ID}, AssignedVerificationUserIDs: []int64{verifier.ID}}, 1, firstFileID, false)
	verifyID := action(verifier, constants.ActionVerify, constants.ActionUnassignVerification)
	reviewed := actionCounters{ApprovedUserIDs: []int64{tester.ID}, VerifiedUserIDs: []int64{verifier.ID}}
	checkpoint("verified", reviewed, 1, firstFileID, false)
	require.Equal(t, sid, uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", uploader.Cookie, &sid))
	var newestFileID int64
	require.NoError(t, maria.QueryRow(testSQL("SELECT id FROM submission_file WHERE fk_submission_id=? ORDER BY created_at DESC,id DESC LIMIT 1"), sid).Scan(&newestFileID))
	require.NotEqual(t, firstFileID, newestFileID)
	checkpoint("new-upload-invalidates-review", actionCounters{}, 2, newestFileID, false)
	deletedFileStart := time.Now().UTC().Truncate(time.Microsecond)
	rr := softDeleteSubmissionFile(t, l, app, moderator.Cookie, sid, newestFileID, "remove superseding upload")
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	var deletedAt time.Time
	var reason string
	require.NoError(t, maria.QueryRow(testSQL("SELECT deleted_at,deleted_reason FROM submission_file WHERE id=?"), newestFileID).Scan(&deletedAt, &reason))
	require.Equal(t, "remove superseding upload", reason)
	require.False(t, deletedAt.Before(deletedFileStart))
	require.Zero(t, deletedAt.Nanosecond()%1000)
	checkpoint("delete-newest-file-restores-old-review", reviewed, 1, firstFileID, false)
	rr = softDeleteComment(t, l, app, moderator.Cookie, sid, verifyID, "remove verification action")
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	require.NoError(t, maria.QueryRow(testSQL("SELECT deleted_at,deleted_reason FROM comment WHERE id=?"), verifyID).Scan(&deletedAt, &reason))
	require.Equal(t, "remove verification action", reason)
	require.Zero(t, deletedAt.Nanosecond()%1000)
	checkpoint("delete-verify-comment", actionCounters{ApprovedUserIDs: []int64{tester.ID}}, 1, firstFileID, false)
	rr = softDeleteSubmission(t, l, app, moderator.Cookie, sid, "delete complete submission")
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	checkpoint("delete-submission", actionCounters{}, 0, 0, true)
}
