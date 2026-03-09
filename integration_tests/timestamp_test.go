package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// ensureTestUser inserts a minimal discord user directly via raw SQL (for FK constraints).
func ensureTestUser(t *testing.T, maria *sql.DB, uid int64) {
	_, err := maria.Exec(`INSERT IGNORE INTO discord_user (id, username, avatar, discriminator, public_flags, flags, locale, mfa_enabled) VALUES (?, ?, '', '', 0, 0, '', 0)`,
		uid, fmt.Sprintf("tsuser_%d", uid))
	require.NoError(t, err)
}

// ctxWithLogger returns a context with logger attached (needed by DAL methods that touch caching).
func ctxWithLogger(ctx context.Context) context.Context {
	l := logging.InitLogger().WithField("test", "timestamp")
	return context.WithValue(ctx, utils.CtxKeys.Log, l)
}

func TestSessionExpirationRoundtrip(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	const uid int64 = 900001
	ensureTestUser(t, maria, uid)

	// Store a session with 1 hour expiration
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	beforeStore := time.Now()
	err = db.StoreSession(dbs, "ts-test-secret-1", uid, 3600, "all", "test-client", "127.0.0.1")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Read back via GetSessionAuthInfo
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)

	info, ok, err := db.GetSessionAuthInfo(dbs, "ts-test-secret-1")
	require.NoError(t, err)
	require.True(t, ok, "session should be valid")
	require.Equal(t, uid, info.UID)
	require.Equal(t, "all", info.Scope)
	require.Equal(t, "test-client", info.Client)

	expectedExpiry := beforeStore.Add(time.Hour)
	require.WithinDuration(t, expectedExpiry, info.ExpiresAt, 5*time.Second, "ExpiresAt should be ~1 hour from now")

	// Read via GetSessions
	sessions, err := db.GetSessions(dbs, uid)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.WithinDuration(t, expectedExpiry, sessions[0].ExpiresAt, 5*time.Second)
	dbs.Rollback()

	// Test expired session (duration=0)
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	err = db.StoreSession(dbs, "ts-test-expired", uid, 0, "all", "test", "127.0.0.1")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)

	_, ok, err = db.GetSessionAuthInfo(dbs, "ts-test-expired")
	require.NoError(t, err)
	require.False(t, ok, "expired session should return ok=false")

	// Revoke session
	err = db.RevokeSession(dbs, uid, info.ID)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)

	_, _, err = db.GetSessionAuthInfo(dbs, "ts-test-secret-1")
	require.Error(t, err) // sql.ErrNoRows — session deleted

	// DeleteUserSessions
	count, err := db.DeleteUserSessions(dbs, uid)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "should delete the expired session")
	require.NoError(t, dbs.Commit())
}

func TestMicrosecondPrecision(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	const uid int64 = 900010
	ensureTestUser(t, maria, uid)

	// Create submission
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	sid, err := db.StoreSubmission(dbs, "audition")
	require.NoError(t, err)

	// Store two comments 500µs apart, same second
	now := time.Now().Truncate(time.Second).Add(500 * time.Millisecond) // mid-second
	c1Time := now
	c2Time := now.Add(500 * time.Microsecond)

	msg1 := "first comment"
	msg2 := "second comment"
	c1 := &types.Comment{AuthorID: uid, SubmissionID: sid, Action: "comment", Message: &msg1, CreatedAt: c1Time}
	c2 := &types.Comment{AuthorID: uid, SubmissionID: sid, Action: "comment", Message: &msg2, CreatedAt: c2Time}

	cid1, err := db.StoreComment(dbs, c1)
	require.NoError(t, err)
	cid2, err := db.StoreComment(dbs, c2)
	require.NoError(t, err)
	require.NotEqual(t, cid1, cid2)
	require.NoError(t, dbs.Commit())

	// Read back and verify ordering
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	comments, err := db.GetExtendedCommentsBySubmissionID(dbs, sid)
	require.NoError(t, err)
	require.Len(t, comments, 2, "should have 2 comments")

	// Comments are ordered by created_at ASC
	require.True(t, comments[0].CreatedAt.Before(comments[1].CreatedAt),
		"first comment should be before second: %v vs %v", comments[0].CreatedAt, comments[1].CreatedAt)

	diff := comments[1].CreatedAt.Sub(comments[0].CreatedAt)
	require.InDelta(t, 500, diff.Microseconds(), 100,
		"time difference should be ~500µs, got %v", diff)

	// Verify via GetCommentByID too
	readC1, err := db.GetCommentByID(dbs, cid1)
	require.NoError(t, err)
	require.WithinDuration(t, c1Time, readC1.CreatedAt, time.Millisecond,
		"comment 1 CreatedAt should roundtrip with µs precision")

	readC2, err := db.GetCommentByID(dbs, cid2)
	require.NoError(t, err)
	require.WithinDuration(t, c2Time, readC2.CreatedAt, time.Millisecond,
		"comment 2 CreatedAt should roundtrip with µs precision")
}

func TestSoftDeletionTimestamps(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()
	ctx = ctxWithLogger(ctx)

	const uid int64 = 900020
	ensureTestUser(t, maria, uid)

	// Create submission with 2 files and a comment
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)

	sid, err := db.StoreSubmission(dbs, "audition")
	require.NoError(t, err)

	now := time.Now()
	f1 := &types.SubmissionFile{
		SubmitterID: uid, SubmissionID: sid,
		OriginalFilename: "file1.zip", CurrentFilename: "ts_test_f1.zip",
		Size: 100, UploadedAt: now,
		MD5Sum: "aa000000000000000000000000000001", SHA256Sum: "bb00000000000000000000000000000000000000000000000000000000000001",
	}
	f2 := &types.SubmissionFile{
		SubmitterID: uid, SubmissionID: sid,
		OriginalFilename: "file2.zip", CurrentFilename: "ts_test_f2.zip",
		Size: 200, UploadedAt: now.Add(time.Microsecond),
		MD5Sum: "aa000000000000000000000000000002", SHA256Sum: "bb00000000000000000000000000000000000000000000000000000000000002",
	}
	fid1, err := db.StoreSubmissionFile(dbs, f1)
	require.NoError(t, err)
	fid2, err := db.StoreSubmissionFile(dbs, f2)
	require.NoError(t, err)

	msg := "test comment"
	comment := &types.Comment{AuthorID: uid, SubmissionID: sid, Action: "comment", Message: &msg, CreatedAt: now}
	cid, err := db.StoreComment(dbs, comment)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Soft delete the comment
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	beforeDelete := time.Now()
	err = db.SoftDeleteComment(dbs, cid, "test deletion")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Verify via raw SQL
	var commentDeletedAt time.Time
	err = maria.QueryRow("SELECT deleted_at FROM comment WHERE id=?", cid).Scan(&commentDeletedAt)
	require.NoError(t, err)
	require.WithinDuration(t, beforeDelete, commentDeletedAt, 5*time.Second, "comment deleted_at should be recent")

	// Soft delete a file (need at least 2 files to delete one)
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	beforeFileDelete := time.Now()
	err = db.SoftDeleteSubmissionFile(dbs, fid2, "test file deletion")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	var fileDeletedAt time.Time
	err = maria.QueryRow("SELECT deleted_at FROM submission_file WHERE id=?", fid2).Scan(&fileDeletedAt)
	require.NoError(t, err)
	require.WithinDuration(t, beforeFileDelete, fileDeletedAt, 5*time.Second, "file deleted_at should be recent")

	// Soft delete the whole submission
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	beforeSubDelete := time.Now()
	err = db.SoftDeleteSubmission(dbs, sid, "test sub deletion")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	var subDeletedAt time.Time
	err = maria.QueryRow("SELECT deleted_at FROM submission WHERE id=?", sid).Scan(&subDeletedAt)
	require.NoError(t, err)
	require.WithinDuration(t, beforeSubDelete, subDeletedAt, 5*time.Second, "submission deleted_at should be recent")

	// Remaining file should also be marked deleted
	var f1DeletedAt time.Time
	err = maria.QueryRow("SELECT deleted_at FROM submission_file WHERE id=?", fid1).Scan(&f1DeletedAt)
	require.NoError(t, err)
	require.WithinDuration(t, beforeSubDelete, f1DeletedAt, 5*time.Second)
}

func TestFreezeUnfreezeTimestamps(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	// Create submission
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	sid, err := db.StoreSubmission(dbs, "audition")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Freeze
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	beforeFreeze := time.Now()
	err = db.FreezeSubmission(dbs, sid)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Verify frozen_at via raw SQL
	var frozenAt *time.Time
	err = maria.QueryRow("SELECT frozen_at FROM submission WHERE id=?", sid).Scan(&frozenAt)
	require.NoError(t, err)
	require.NotNil(t, frozenAt, "frozen_at should be set")
	require.WithinDuration(t, beforeFreeze, *frozenAt, 5*time.Second, "frozen_at should be recent")

	// Unfreeze
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	err = db.UnfreezeSubmission(dbs, sid)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Verify frozen_at is NULL
	var frozenAt2 *time.Time
	err = maria.QueryRow("SELECT frozen_at FROM submission WHERE id=?", sid).Scan(&frozenAt2)
	require.NoError(t, err)
	require.Nil(t, frozenAt2, "frozen_at should be NULL after unfreeze")
}

func TestNotificationTimestamps(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	// Store notification
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	beforeStore := time.Now()
	err = db.StoreNotification(dbs, "timestamp test notification", "notification")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Read back
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)

	n, err := db.GetOldestUnsentNotification(dbs)
	require.NoError(t, err)
	require.WithinDuration(t, beforeStore, n.CreatedAt, 5*time.Second, "notification created_at should be recent")
	require.True(t, n.SentAt.IsZero(), "SentAt should be zero (unsent)")
	nid := n.ID

	// Mark as sent
	beforeSend := time.Now()
	err = db.MarkNotificationAsSent(dbs, nid)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Verify sent_at via raw SQL
	var sentAt *time.Time
	err = maria.QueryRow("SELECT sent_at FROM submission_notification WHERE id=?", nid).Scan(&sentAt)
	require.NoError(t, err)
	require.NotNil(t, sentAt, "sent_at should be set")
	require.WithinDuration(t, beforeSend, *sentAt, 5*time.Second, "sent_at should be recent")

	// No more unsent notifications
	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()
	_, err = db.GetOldestUnsentNotification(dbs)
	require.ErrorIs(t, err, sql.ErrNoRows, "should have no unsent notifications")
}

func TestNullTimestampHandling(t *testing.T) {
	_, _, ctx, db, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	const uid int64 = 900030
	ensureTestUser(t, maria, uid)

	// Create submission — deleted_at and frozen_at should be NULL
	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	sid, err := db.StoreSubmission(dbs, "audition")
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	// Verify NULLs via raw SQL with *time.Time
	var deletedAt, frozenAt *time.Time
	err = maria.QueryRow("SELECT deleted_at, frozen_at FROM submission WHERE id=?", sid).Scan(&deletedAt, &frozenAt)
	require.NoError(t, err)
	require.Nil(t, deletedAt, "deleted_at should be NULL for new submission")
	require.Nil(t, frozenAt, "frozen_at should be NULL for new submission")

	// Create a comment and read it back — verifies non-null created_at parses fine
	// even when the table has nullable deleted_at columns
	now := time.Now()
	msg := "null test"
	comment := &types.Comment{AuthorID: uid, SubmissionID: sid, Action: "comment", Message: &msg, CreatedAt: now}

	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	cid, err := db.StoreComment(dbs, comment)
	require.NoError(t, err)
	require.NoError(t, dbs.Commit())

	dbs, err = db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	c, err := db.GetCommentByID(dbs, cid)
	require.NoError(t, err)
	require.WithinDuration(t, now, c.CreatedAt, time.Millisecond, "comment CreatedAt should parse correctly")

	// Verify comment's deleted_at is NULL
	var commentDeletedAt *time.Time
	err = maria.QueryRow("SELECT deleted_at FROM comment WHERE id=?", cid).Scan(&commentDeletedAt)
	require.NoError(t, err)
	require.Nil(t, commentDeletedAt, "comment deleted_at should be NULL")
}
