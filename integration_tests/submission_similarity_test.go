package integration_tests

import (
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// The similarity candidate query uses UNION semantics for the whole tuple,
// under the source text collation. Identity alone must not remove different
// titles/commands, and matching content under different identities survives.
func TestSubmissionSimilarityCandidates(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "similarity-uploader")
	live := []struct {
		id             int64
		title, command *string
	}{
		{10, utils.StrPtr("Café"), utils.StrPtr("Run ")},
		{11, utils.StrPtr("Title"), utils.StrPtr("a")},
		{12, utils.StrPtr("Zulu"), utils.StrPtr("cmd")},
		{13, nil, nil},
		{14, nil, nil},
		{15, utils.StrPtr("Shared"), utils.StrPtr("same")},
		{18, utils.StrPtr("Résumé"), utils.StrPtr("Play ")},
		{17, utils.StrPtr("Deleted"), utils.StrPtr("deleted")},
	}
	for _, row := range live {
		f.Submission(t, row.id, "staff")
		meta := fixtureMeta("")
		meta.Title, meta.LaunchCommand = row.title, row.command
		f.File(t, fixtureFile{ID: row.id + 100, SubmissionID: row.id, UserID: 11, At: fixtureEpoch, Meta: meta})
		f.Rebuild(t, row.id)
	}
	// A missing file/cache pointer still produces a candidate with NULL fields.
	f.Submission(t, 16, "staff")
	_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission SET deleted_at=? WHERE id=?"), fixtureEpoch, 17)
	require.NoError(t, err)
	legacy := []struct {
		id             string
		title, command *string
	}{
		// Full-width identity digits match live ID 10 under the source collation.
		// Do not pad UUIDs: MariaDB CHAR storage trims trailing spaces on read.
		// Title and command exercise case, accent and PAD SPACE equality.
		{"１０", utils.StrPtr("CAFE"), utils.StrPtr("run")},
		{"11", utils.StrPtr("Title"), utils.StrPtr("b")},
		{"12", utils.StrPtr("Alpha"), utils.StrPtr("cmd")},
		{"13", nil, nil},
		{"14", utils.StrPtr(""), nil},
		{"17", utils.StrPtr("Surviving legacy"), utils.StrPtr("legacy")},
		{"18", utils.StrPtr("RESUME"), utils.StrPtr("play")},
		{"legacy-shared", utils.StrPtr("Shared"), utils.StrPtr("same")},
	}
	for _, row := range legacy {
		_, err := f.Maria.ExecContext(f.Ctx, testSQL("INSERT INTO masterdb_game (uuid,title,launch_command) VALUES (?,?,?)"), row.id, row.title, row.command)
		require.NoError(t, err)
	}
	want := []*types.SimilarityAttributes{
		{ID: "10", Title: utils.StrPtr("Café"), LaunchCommand: utils.StrPtr("Run ")},
		{ID: "11", Title: utils.StrPtr("Title"), LaunchCommand: utils.StrPtr("a")},
		{ID: "11", Title: utils.StrPtr("Title"), LaunchCommand: utils.StrPtr("b")},
		{ID: "12", Title: utils.StrPtr("Alpha"), LaunchCommand: utils.StrPtr("cmd")},
		{ID: "12", Title: utils.StrPtr("Zulu"), LaunchCommand: utils.StrPtr("cmd")},
		{ID: "13"},
		{ID: "14", Title: utils.StrPtr("")},
		{ID: "14"},
		{ID: "15", Title: utils.StrPtr("Shared"), LaunchCommand: utils.StrPtr("same")},
		{ID: "16"},
		{ID: "17", Title: utils.StrPtr("Surviving legacy"), LaunchCommand: utils.StrPtr("legacy")},
		{ID: "18", Title: utils.StrPtr("Résumé"), LaunchCommand: utils.StrPtr("Play ")},
		{ID: "legacy-shared", Title: utils.StrPtr("Shared"), LaunchCommand: utils.StrPtr("same")},
	}
	f.InTx(t, func(s database.DBSession) {
		got, err := f.DB.GetAllSimilarityAttributes(s)
		require.NoError(t, err)
		require.ElementsMatch(t, want, got, "full tuples and live-row representation must survive")
		if postgresSubmissionTests() {
			// PostgreSQL explicitly orders collation keys, including NULLS LAST.
			// MariaDB UNION has no ORDER BY, so its output order is not a contract.
			require.Equal(t, want, got)
		}
	})
}
