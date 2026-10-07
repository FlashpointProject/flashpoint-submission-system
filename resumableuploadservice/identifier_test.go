package resumableuploadservice

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCompleteIdentifiersHaveIndependentChunksAndCleanup(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)
	first, second := strings.Repeat("a", 64)+"first", strings.Repeat("a", 64)+"second"
	require.NoError(t, s.PutChunk(1, first, 1, []byte("one")))
	require.NoError(t, s.PutChunk(1, second, 1, []byte("longer")))
	for id, size := range map[string]int64{first: 3, second: 6} {
		ok, err := s.TestChunk(1, id, 1, size)
		require.NoError(t, err)
		require.True(t, ok)
	}
	// Missing earlier chunks must not prevent cleaning up later chunks.
	require.NoError(t, s.PutChunk(1, first, 3, []byte("tail")))
	require.NoError(t, s.DeleteFile(1, first, 3))
	require.NoError(t, s.DeleteFile(1, first, 3))
	ok, err := s.TestChunk(1, first, 3, 4)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = s.TestChunk(1, second, 1, 6)
	require.NoError(t, err)
	require.True(t, ok)
}
