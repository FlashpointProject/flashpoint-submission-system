package integration_tests

import (
	"strconv"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

func TestCommentOrderingChronologyDeletionAndTies(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 101, "first author")
	f.User(t, 102, "second author")
	f.Submission(t, 4000, "staff")
	f.Submission(t, 4001, "staff")
	msg := "untouched message"
	emptyMessage := ""
	// IDs and insertion order intentionally disagree with chronological order.
	f.Comment(t, 11, 4000, 101, "approve", fixtureEpoch.Add(3*time.Second), &msg)
	f.Comment(t, 15, 4000, 102, "comment", fixtureEpoch.Add(time.Second), nil)
	f.Comment(t, 12, 4000, 101, "verify", fixtureEpoch.Add(2*time.Second), nil)
	f.Comment(t, 13, 4000, 102, "comment", fixtureEpoch.Add(2*time.Second), &emptyMessage)
	f.Comment(t, 14, 4000, 101, "reject", fixtureEpoch.Add(4*time.Second), nil)
	f.Comment(t, 16, 4001, 101, "comment", fixtureEpoch, nil)
	deleteFixtureComment(t, f, 14)
	var comments []*types.ExtendedComment
	f.InTx(t, func(dbs database.DBSession) {
		var err error
		comments, err = f.DB.GetExtendedCommentsBySubmissionID(dbs, 4000)
		require.NoError(t, err)
	})
	require.Len(t, comments, 4)
	require.EqualValues(t, 15, comments[0].CommentID)
	require.EqualValues(t, 11, comments[3].CommentID)
	require.Equal(t, []int64{12, 13}, []int64{comments[1].CommentID, comments[2].CommentID}, "equal timestamps are ordered by comment ID")
	require.EqualValues(t, 102, comments[0].AuthorID)
	require.Equal(t, "second author", comments[0].Username)
	require.Nil(t, comments[0].Message)
	require.Equal(t, &msg, comments[3].Message)
	for _, comment := range comments[1:3] {
		if comment.CommentID == 13 {
			require.Equal(t, &emptyMessage, comment.Message)
		} else {
			require.Nil(t, comment.Message)
		}
	}
	f.InTx(t, func(dbs database.DBSession) {
		empty, err := f.DB.GetExtendedCommentsBySubmissionID(dbs, 99999)
		require.NoError(t, err)
		require.Empty(t, empty)
	})
}

func TestSubmissionCacheTimestampTiesUseIDs(t *testing.T) {
	f := newSQLFixture(t)
	const bot int64 = 810112564787675166
	f.User(t, 101, "reviewer")
	f.User(t, bot, "validator")
	f.Submission(t, 4100, "staff")
	for _, id := range []int64{21, 20} {
		f.File(t, fixtureFile{ID: id, SubmissionID: 4100, UserID: 101, At: fixtureEpoch})
	}
	f.Comment(t, 23, 4100, bot, "request-changes", fixtureEpoch.Add(time.Second), nil)
	f.Comment(t, 22, 4100, bot, "approve", fixtureEpoch.Add(time.Second), nil)
	f.Rebuild(t, 4100)
	got := cacheSnapshot(t, f, 4100)
	require.Equal(t, "20", got["fk_oldest_file_id"])
	require.Equal(t, "21", got["fk_newest_file_id"])
	require.Equal(t, "23", got["fk_newest_comment_id"])
	require.Equal(t, "request-changes", got["bot_action"])
	f.Rebuild(t, 4100)
	require.Equal(t, got, cacheSnapshot(t, f, 4100), "rebuilding preserves tie choices")
	for _, id := range []int64{20, 21} {
		require.Contains(t, got["current_filename_sequence"], strconv.FormatInt(id, 10))
	}
	deleteFixtureComment(t, f, 23)
	_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission_file SET deleted_at = ? WHERE id = ?"), fixtureEpoch.Add(time.Hour), 21)
	require.NoError(t, err)
	f.Rebuild(t, 4100)
	got = cacheSnapshot(t, f, 4100)
	require.Equal(t, "20", got["fk_oldest_file_id"])
	require.Equal(t, "20", got["fk_newest_file_id"])
	require.Equal(t, "22", got["fk_newest_comment_id"])
	require.Equal(t, "approve", got["bot_action"], "deleted tie winner reveals the live comment")
}
