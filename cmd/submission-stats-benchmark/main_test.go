package main

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStatisticsWorkloads(t *testing.T) {
	cases := workloads(123)
	require.Len(t, cases, 15)
	seen := map[string]bool{}
	for i, w := range cases {
		require.False(t, seen[w.Name])
		seen[w.Name] = true
		if w.Filter != nil {
			require.NoError(t, w.Filter.Validate())
		}
		if i >= 7 {
			require.EqualValues(t, 123, *w.Filter.SubmitterID)
		}
	}
	require.Nil(t, cases[0].Filter)
	require.Equal(t, "none", *cases[11].Filter.VerificationStatus)
	require.Equal(t, []string{"reject", "mark-added"}, cases[11].Filter.DistinctActionsNot)
	require.Equal(t, []string{"reject"}, cases[13].Filter.DistinctActionsNot)
}
func TestMedian(t *testing.T) {
	require.Equal(t, 2.0, median([]float64{3, 1, 2}))
	require.Equal(t, 2.5, median([]float64{4, 1, 3, 2}))
}
