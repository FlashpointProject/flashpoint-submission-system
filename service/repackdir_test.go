package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareRepackDirCreatesTheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repack")
	require.NoError(t, PrepareRepackDir(logrus.New(), dir))
	assert.DirExists(t, dir)
}

func TestPrepareRepackDirRemovesOldLeftovers(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-25 * time.Hour)
	for _, name := range []string{"pack-old", "edit-old", "pack-new", "unrelated-old"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "x.zip"), []byte("zip"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old-file"), []byte("x"), 0o644))
	for _, name := range []string{"pack-old", "edit-old", "unrelated-old", "old-file"} {
		require.NoError(t, os.Chtimes(filepath.Join(dir, name), old, old))
	}

	require.NoError(t, PrepareRepackDir(logrus.New(), dir))

	assert.NoDirExists(t, filepath.Join(dir, "pack-old"))
	assert.NoDirExists(t, filepath.Join(dir, "edit-old"))
	assert.FileExists(t, filepath.Join(dir, "pack-new", "x.zip"))
	// Only the curation package's directories are removed.
	assert.FileExists(t, filepath.Join(dir, "unrelated-old", "x.zip"))
	assert.FileExists(t, filepath.Join(dir, "old-file"))
}
