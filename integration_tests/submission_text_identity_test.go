package integration_tests

import (
	"database/sql"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

// These exercise DAL lookups, not just standalone collation helpers. Identical
// assertions run against MariaDB and the PostgreSQL translation.
func TestSubmissionTextIdentityActions(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "reviewer")
	f.InTx(t, func(s database.DBSession) {
		sid, err := f.DB.StoreSubmission(s, "STÁFF  ")
		require.NoError(t, err)
		cid, err := f.DB.StoreComment(s, &types.Comment{AuthorID: 11, SubmissionID: sid, Action: "ÁPPROVE  ", CreatedAt: fixtureEpoch})
		require.NoError(t, err)
		comment, err := f.DB.GetCommentByID(s, cid)
		require.NoError(t, err)
		require.Equal(t, constants.ActionApprove, comment.Action)
		comments, err := f.DB.GetCommentsByUserIDAndAction(s, 11, "APPROVÉ ")
		require.NoError(t, err)
		require.Len(t, comments, 1)
		require.Equal(t, cid, comments[0].ID)
		comments, err = f.DB.GetCommentsByUserIDAndAction(s, 11, "missing-action")
		require.NoError(t, err)
		require.Empty(t, comments)
		require.NoError(t, f.DB.StoreNotificationSettings(s, 11, []string{"ÁPPROVE "}))
		recipients, err := f.DB.GetUsersForUniversalNotification(s, 12, "APPROVÉ  ")
		require.NoError(t, err)
		require.Equal(t, []int64{11}, recipients)
	})
}

func TestSubmissionTextIdentitySessions(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "session-owner")
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.StoreSession(s, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 11, 3600, "scope", "client", "127.0.0.1"))
		session, active, err := f.DB.GetSessionAuthInfo(s, "ÁAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA  ")
		require.NoError(t, err)
		require.True(t, active)
		require.Equal(t, int64(11), session.UID)
		require.NoError(t, f.DB.DeleteSession(s, "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA "))
		_, _, err = f.DB.GetSessionAuthInfo(s, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func TestSubmissionTextIdentityOAuthGeneralCollation(t *testing.T) {
	f := newSQLFixture(t)
	f.InTx(t, func(s database.DBSession) {
		// general_ci equates sharp-s with one s; unicode_ci instead expands it
		// to ss. OAuth is the source schema's general_ci exception.
		require.NoError(t, f.DB.SetClientSecret(s, "client-ß", "first"))
		require.NoError(t, f.DB.SetClientSecret(s, "client-ss", "second"))
		require.NoError(t, f.DB.SetClientSecret(s, "CLIENT-Ś  ", "updated"))
		single, err := f.DB.GetClientSecret(s, "client-s")
		require.NoError(t, err)
		require.Equal(t, "updated", single)
		doubled, err := f.DB.GetClientSecret(s, "CLIENT-SS ")
		require.NoError(t, err)
		require.Equal(t, "second", doubled)
		var count int
		require.NoError(t, s.Tx().QueryRowContext(f.Ctx, "SELECT COUNT(*) FROM oauth_client").Scan(&count))
		require.Equal(t, 2, count)
	})
}

func TestSubmissionTimestampOffsetRoundTrip(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "timestamp-author")
	// Non-integral-hour offset and a UTC date boundary catch both discarded zones
	// and conversions accidentally using the machine's current local timezone.
	at := time.Date(2024, 3, 4, 0, 15, 16, 123456000, time.FixedZone("offset", 5*3600+30*60))
	want := time.Date(2024, 3, 3, 18, 45, 16, 123456000, time.UTC)
	f.InTx(t, func(s database.DBSession) {
		sid, err := f.DB.StoreSubmission(s, "staff")
		require.NoError(t, err)
		file := &types.SubmissionFile{SubmitterID: 11, SubmissionID: sid, OriginalFilename: "offset.7z", CurrentFilename: "offset.7z", Size: 10, UploadedAt: at, MD5Sum: "11111111111111111111111111111111", SHA256Sum: "1111111111111111111111111111111111111111111111111111111111111111"}
		fid, err := f.DB.StoreSubmissionFile(s, file)
		require.NoError(t, err)
		cid, err := f.DB.StoreComment(s, &types.Comment{AuthorID: 11, SubmissionID: sid, Action: constants.ActionComment, CreatedAt: at})
		require.NoError(t, err)
		files, err := f.DB.GetSubmissionFiles(s, []int64{fid})
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.Equal(t, want, files[0].UploadedAt.UTC())
		comment, err := f.DB.GetCommentByID(s, cid)
		require.NoError(t, err)
		require.Equal(t, want, comment.CreatedAt.UTC())
		// This bulk insert uses ExecContext while the ID-returning inserts above use
		// QueryRowContext; both argument paths must preserve the instant.
		require.NoError(t, f.DB.StoreMasterDBGames(s, []*types.MasterDatabaseGame{{UUID: "offset-timestamp", DateAdded: at, DateModified: at}}))
		var added, modified time.Time
		require.NoError(t, s.Tx().QueryRowContext(f.Ctx, "SELECT date_added, date_modified FROM masterdb_game WHERE uuid='offset-timestamp'").Scan(&added, &modified))
		require.Equal(t, want, added.UTC())
		require.Equal(t, want, modified.UTC())
		require.Equal(t, "offset", file.UploadedAt.Location().String(), "DAL must not mutate caller timestamps")
	})
}
