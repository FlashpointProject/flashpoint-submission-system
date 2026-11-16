package service

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/curation"
	"github.com/sirupsen/logrus"
)

// repackLeftoverAge is how old a directory left by the curation package
// must be to be removed at startup.
const repackLeftoverAge = 24 * time.Hour

// PrepareRepackDir creates dir, where data packs and edited curations are
// written, if it is missing, and removes the curation package's output
// directories (pack-*, edit-*) older than a day: output that a crash or
// restart left behind (a pack can be tens of GB). Nothing else in dir is
// touched. Call it at startup, before anything writes into dir.
func PrepareRepackDir(l logrus.FieldLogger, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, curation.PackDirPrefix) && !strings.HasPrefix(name, curation.EditDirPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if time.Since(info.ModTime()) < repackLeftoverAge {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.RemoveAll(path); err != nil {
			l.Errorf("failed to remove repack leftover %s: %v", path, err)
			continue
		}
		l.Infof("removed repack leftover %s", path)
	}
	return nil
}
