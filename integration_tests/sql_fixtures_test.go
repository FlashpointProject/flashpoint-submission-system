package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

var fixtureEpoch = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

// sqlFixture seeds source records directly, never cached query answers. Each
// helper commits before reads because search counts use a separate connection.
// Keep backend SQL here so future PostgreSQL fixtures can share the assertions.
// It starts no app, validator, upload worker or external file operations.
type sqlFixture struct {
	DB    database.DAL
	Maria *sql.DB
	Ctx   context.Context
}

func newSQLFixture(t *testing.T) *sqlFixture {
	t.Helper()
	root := setupTestEnvironment(t)
	resetTestDatabases(t, root)
	maria, err := openSubmissionTestDB(config.GetConfig(nil))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, maria.Close()) })
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	return &sqlFixture{DB: database.NewSubmissionDAL(maria), Maria: maria, Ctx: ctx}
}

func (f *sqlFixture) InTx(t *testing.T, fn func(database.DBSession)) {
	t.Helper()
	session, err := f.DB.NewSession(f.Ctx)
	require.NoError(t, err)
	defer session.Rollback()
	fn(session)
	require.NoError(t, session.Commit())
}

func (f *sqlFixture) User(t *testing.T, id int64, name string) {
	t.Helper()
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.StoreDiscordUser(s, &types.DiscordUser{ID: id, Username: name}))
	})
}

func (f *sqlFixture) Submission(t *testing.T, id int64, level string) {
	t.Helper()
	f.InTx(t, func(s database.DBSession) {
		_, err := s.Tx().ExecContext(f.Ctx, testSQL(`INSERT INTO submission (id, fk_submission_level_id) VALUES (?, (SELECT id FROM submission_level WHERE name=?))`), id, level)
		require.NoError(t, err)
		_, err = s.Tx().ExecContext(f.Ctx, testSQL(`INSERT INTO submission_cache (fk_submission_id) VALUES (?)`), id)
		require.NoError(t, err)
		syncFixtureSequence(t, s.Tx(), "submission")
	})
}

type fixtureFile struct {
	ID, SubmissionID, UserID, Size int64
	At                             time.Time
	Original, Current, MD5, SHA256 string
	Meta                           *types.CurationMeta
}

func fixtureMeta(title string) *types.CurationMeta {
	return &types.CurationMeta{Title: utils.StrPtr(title), AlternateTitles: utils.StrPtr(""), Platform: utils.StrPtr("Flash"),
		LaunchCommand: utils.StrPtr("content/game.swf"), Library: utils.StrPtr("arcade"), Extreme: utils.StrPtr("No")}
}

func (f *sqlFixture) File(t *testing.T, file fixtureFile) {
	t.Helper()
	require.False(t, file.At.IsZero(), "file timestamps must be explicit")
	if file.Original == "" {
		file.Original = fmt.Sprintf("original-%d.7z", file.ID)
	}
	if file.Current == "" {
		file.Current = fmt.Sprintf("current-%d.7z", file.ID)
	}
	if file.MD5 == "" {
		file.MD5 = fmt.Sprintf("%032x", file.ID)
	}
	if file.SHA256 == "" {
		file.SHA256 = fmt.Sprintf("%064x", file.ID)
	}
	if file.Size == 0 {
		file.Size = 100
	}
	if file.Meta == nil {
		file.Meta = fixtureMeta(fmt.Sprintf("Submission %d file %d", file.SubmissionID, file.ID))
	}
	meta := *file.Meta
	meta.SubmissionFileID = file.ID
	f.InTx(t, func(s database.DBSession) {
		_, err := s.Tx().ExecContext(f.Ctx, testSQL(`INSERT INTO submission_file (id,fk_submission_id,fk_user_id,original_filename,current_filename,size,created_at,md5sum,sha256sum) VALUES (?,?,?,?,?,?,?,?,?)`), file.ID, file.SubmissionID, file.UserID, file.Original, file.Current, file.Size, file.At, file.MD5, file.SHA256)
		require.NoError(t, err)
		require.NoError(t, f.DB.StoreCurationMeta(s, &meta))
		syncFixtureSequence(t, s.Tx(), "submission_file")
	})
}

func (f *sqlFixture) Comment(t *testing.T, id, sid, uid int64, action string, at time.Time, message *string) {
	t.Helper()
	require.False(t, at.IsZero(), "comment timestamps must be explicit")
	_, err := f.Maria.ExecContext(f.Ctx, testSQL(`INSERT INTO comment (id,fk_submission_id,fk_user_id,fk_action_id,created_at,message) VALUES (?,?,?,(SELECT id FROM action WHERE name=?),?,?)`), id, sid, uid, action, at, message)
	require.NoError(t, err)
	syncFixtureSequence(t, f.Maria, "comment")
}

func (f *sqlFixture) Rebuild(t *testing.T, ids ...int64) {
	t.Helper()
	f.InTx(t, func(s database.DBSession) {
		for _, id := range ids {
			require.NoError(t, f.DB.UpdateSubmissionCacheTable(s, id))
		}
	})
}

func (f *sqlFixture) Search(t *testing.T, uid int64, filter *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64) {
	t.Helper()
	ctx := context.WithValue(f.Ctx, utils.CtxKeys.UserID, uid)
	session, err := f.DB.NewSession(ctx)
	require.NoError(t, err)
	defer session.Rollback()
	rows, count, err := f.DB.SearchSubmissions(session, filter)
	require.NoError(t, err)
	return rows, count
}

func (f *sqlFixture) Legacy(t *testing.T, id int64, meta *types.CurationMeta, created, updated time.Time) {
	t.Helper()
	_, err := f.Maria.ExecContext(f.Ctx, testSQL(`INSERT INTO masterdb_game (id,uuid,title,alternate_titles,platform,launch_command,library,extreme,date_added,date_modified) VALUES (?,?,?,?,?,?,?,?,?,?)`), id, fmt.Sprintf("00000000-0000-0000-0000-%012d", id), meta.Title, meta.AlternateTitles, meta.Platform, meta.LaunchCommand, meta.Library, meta.Extreme, created, updated)
	require.NoError(t, err)
	syncFixtureSequence(t, f.Maria, "masterdb_game")
}
