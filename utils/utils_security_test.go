package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMoveFileBetweenRoots(t *testing.T) {
	t.Run("moves a confined file", func(t *testing.T) {
		base := t.TempDir()
		srcRoot := filepath.Join(base, "images")
		destRoot := filepath.Join(base, "deleted-images")
		require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "Logos", "ab"), 0o755))
		src := filepath.Join(srcRoot, "Logos", "ab", "game.png")
		require.NoError(t, os.WriteFile(src, []byte("image"), 0o644))

		require.NoError(t, MoveFileBetweenRoots(srcRoot, destRoot, "Logos/ab/game.png"))
		_, err := os.Stat(src)
		require.ErrorIs(t, err, os.ErrNotExist)
		contents, err := os.ReadFile(filepath.Join(destRoot, "Logos", "ab", "game.png"))
		require.NoError(t, err)
		require.Equal(t, []byte("image"), contents)
	})

	t.Run("rejects traversal and absolute paths", func(t *testing.T) {
		base := t.TempDir()
		srcRoot := filepath.Join(base, "images")
		destRoot := filepath.Join(base, "deleted-images")
		require.NoError(t, os.MkdirAll(srcRoot, 0o755))
		require.NoError(t, os.MkdirAll(destRoot, 0o755))
		outside := filepath.Join(base, "outside.png")
		require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o644))

		for _, name := range []string{"../outside.png", outside} {
			require.Error(t, MoveFileBetweenRoots(srcRoot, destRoot, name))
			contents, err := os.ReadFile(outside)
			require.NoError(t, err)
			require.Equal(t, []byte("keep"), contents)
		}
	})

	t.Run("rejects identical source and destination", func(t *testing.T) {
		root := t.TempDir()
		filePath := filepath.Join(root, "game.png")
		require.NoError(t, os.WriteFile(filePath, []byte("keep"), 0o644))

		require.Error(t, MoveFileBetweenRoots(root, root, "game.png"))
		contents, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, []byte("keep"), contents)
	})

	t.Run("rejects source and destination symlink escapes", func(t *testing.T) {
		base := t.TempDir()
		srcRoot := filepath.Join(base, "images")
		destRoot := filepath.Join(base, "deleted-images")
		outsideSrc := filepath.Join(base, "outside-source")
		outsideDest := filepath.Join(base, "outside-destination")
		for _, dir := range []string{srcRoot, destRoot, outsideSrc, outsideDest} {
			require.NoError(t, os.MkdirAll(dir, 0o755))
		}
		require.NoError(t, os.WriteFile(filepath.Join(outsideSrc, "outside.png"), []byte("keep"), 0o644))
		require.NoError(t, os.Symlink(outsideSrc, filepath.Join(srcRoot, "escaped")))

		require.Error(t, MoveFileBetweenRoots(srcRoot, destRoot, "escaped/outside.png"))
		contents, err := os.ReadFile(filepath.Join(outsideSrc, "outside.png"))
		require.NoError(t, err)
		require.Equal(t, []byte("keep"), contents)

		require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "Logos"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "Logos", "game.png"), []byte("image"), 0o644))
		require.NoError(t, os.Symlink(outsideDest, filepath.Join(destRoot, "Logos")))

		require.Error(t, MoveFileBetweenRoots(srcRoot, destRoot, "Logos/game.png"))
		_, err = os.Stat(filepath.Join(outsideDest, "game.png"))
		require.ErrorIs(t, err, os.ErrNotExist)
		contents, err = os.ReadFile(filepath.Join(srcRoot, "Logos", "game.png"))
		require.NoError(t, err)
		require.Equal(t, []byte("image"), contents)
	})
}
