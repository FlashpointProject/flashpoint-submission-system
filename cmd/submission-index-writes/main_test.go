package main

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDDLAllowlist(t *testing.T) {
	good := `-- experiment
CREATE INDEX sample ON submission_cache(fk_submission_id); CREATE STATISTICS sample_stats ON bot_action, distinct_actions FROM submission_cache; ANALYZE submission_cache;`
	statements, e := ddlStatements(good)
	require.NoError(t, e)
	require.Len(t, statements, 3)
	for _, bad := range []string{"", "COMMIT", "CREATE INDEX sample ON submission_cache(fk_submission_id); COMMIT;", "UPDATE submission_cache SET bot_action='x'", "DO $$ BEGIN COMMIT; END $$", "CREATE FUNCTION example() RETURNS int AS 'x' LANGUAGE SQL"} {
		_, e = ddlStatements(bad)
		require.Error(t, e, bad)
	}
}
