package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresSubmissionArgsUTC(t *testing.T) {
	original := time.Date(2024, 3, 4, 0, 15, 16, 123456000, time.FixedZone("offset", 5*3600+30*60))
	expected := time.Date(2024, 3, 3, 18, 45, 16, 123456000, time.UTC)
	var absent *time.Time
	args := []interface{}{original, &original, absent, nil, "unchanged", int64(123)}
	got := postgresSubmissionArgs(args)
	require.Equal(t, expected, got[0])
	require.Equal(t, expected, *got[1].(*time.Time))
	require.Nil(t, got[2])
	require.Nil(t, got[3])
	require.Equal(t, args[4:], got[4:])
	require.NotSame(t, &original, got[1].(*time.Time))
	require.Equal(t, original, args[0], "must not replace caller slice elements")
	require.Equal(t, "offset", original.Location().String(), "must not mutate caller pointer")
	require.Equal(t, 123456000, got[0].(time.Time).Nanosecond(), "microseconds preserved")
	require.Nil(t, postgresSubmissionArgs(nil))
}
