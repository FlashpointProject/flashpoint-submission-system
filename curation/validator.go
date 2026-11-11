package curation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
)

// maxConcurrentOperations is how many archive operations (validate, pack,
// edit) may run at the same time.
const maxConcurrentOperations = 2

// Prefixes of the directories that Pack and EditMeta create under the
// temp directory, one per output file.
const (
	PackDirPrefix = "pack-"
	EditDirPrefix = "edit-"
)

// Validator validates, packs and edits curation archives. It is safe for
// concurrent use; at most two operations run at a time.
type Validator struct {
	lookups    Lookups
	tempDir    string
	log        logrus.FieldLogger
	now        func() time.Time
	maxEntries int
	noteSize   int64 // NoteSize, smaller in tests
	slots      chan struct{}
}

// New returns a Validator that looks up tags and launch commands with
// lookups and writes data packs and edited archives into new directories
// under tempDir. Panics while processing a curation are logged to log.
func New(lookups Lookups, tempDir string, log logrus.FieldLogger) *Validator {
	return &Validator{
		lookups:    lookups,
		tempDir:    tempDir,
		log:        log,
		now:        time.Now,
		maxEntries: MaxEntries,
		noteSize:   NoteSize,
		slots:      make(chan struct{}, maxConcurrentOperations),
	}
}

// NoteSize is the archive size, and separately the total uncompressed size,
// from which validation adds a note about it. Notes are information for
// reviewers, not problems with the curation.
const NoteSize = 10 * 1000 * 1000 * 1000

// Validate checks the curation archive at path. Problems with the curation
// are reported as curation errors and warnings in the response, information
// for reviewers as notes; an error means the archive could not be processed
// at all.
func (v *Validator) Validate(ctx context.Context, path string) (*types.ValidatorResponse, error) {
	var r *types.ValidatorResponse
	err := v.run(ctx, func() error {
		var err error
		r, _, err = v.validate(ctx, path)
		return err
	})
	return r, err
}

// Pack validates the curation archive at path and writes its data pack to
// <tempDir>/pack-*/<root folder>.zip. The caller removes the directory of
// the returned path.
//
// As the Python validator, Pack does not refuse a curation for its
// validation errors (staff may add a curation despite them). It fails only
// when the curation cannot be packed: the archive or its layout is broken,
// the meta file is missing or does not parse, or there is no content. Those
// problems are a *CurationError.
func (v *Validator) Pack(ctx context.Context, path string) (*types.ValidatorRepackResponse, error) {
	var r *types.ValidatorRepackResponse
	err := v.run(ctx, func() error {
		vr, metaParsed, err := v.validate(ctx, path)
		if err != nil {
			return err
		}
		if !metaParsed {
			return &CurationError{Message: "The curation cannot be packed: " + strings.Join(vr.CurationErrors, " ")}
		}

		a, l, err := v.openCuration(path)
		if err != nil {
			return err
		}
		defer a.Close()
		if l.ContentError != "" {
			return &CurationError{Message: "The curation cannot be packed: " + l.ContentError}
		}
		packPath, err := v.writeInNewDir(PackDirPrefix, l.Root+".zip", func(w io.Writer) error {
			return writeDataPack(w, a, l)
		})
		if err != nil {
			return err
		}
		r = &types.ValidatorRepackResponse{FilePath: &packPath, Meta: vr.Meta, Images: vr.Images}
		return nil
	})
	v.logCause(path, err)
	return r, err
}

// EditMeta writes a copy of the curation archive at path as a zip, with the
// non-nil values of edit changed in its meta file and the logo and
// screenshot replaced by the given images (nil means unchanged). The copy
// is <tempDir>/edit-*/<base name of path>.zip; the caller removes it.
func (v *Validator) EditMeta(ctx context.Context, path string, edit *types.EditCurationMeta, logo, screenshot io.Reader) (string, error) {
	logoData, err := readImage("logo", logo)
	if err != nil {
		return "", err
	}
	screenshotData, err := readImage("screenshot", screenshot)
	if err != nil {
		return "", err
	}

	var editedPath string
	err = v.run(ctx, func() error {
		a, l, err := v.openCuration(path)
		if err != nil {
			return err
		}
		defer a.Close()
		if l.Meta == nil {
			return fmt.Errorf("did not find a meta file to edit: %s", l.MetaError)
		}
		if l.Meta.Size > MaxMetaSize {
			return fmt.Errorf(msgMetaTooLarge, l.Meta.Name)
		}
		data, err := a.ReadFiles(map[string]int64{l.Meta.Name: MaxMetaSize})
		if err != nil {
			return err
		}
		meta, err := editMetaYAML(data[l.Meta.Name], edit)
		if err != nil {
			return fmt.Errorf("editing the meta file: %w", err)
		}
		if _, errs := parseMeta(meta); len(errs) > 0 {
			return fmt.Errorf("the edited meta file is not valid: %s", strings.Join(errs, "; "))
		}

		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".zip"
		editedPath, err = v.writeInNewDir(EditDirPrefix, name, func(w io.Writer) error {
			return writeEditedArchive(w, a, l, meta, logoData, screenshotData)
		})
		return err
	})
	v.logCause(path, err)
	return editedPath, err
}

// run runs fn once one of the operation slots is free, or returns the
// context's error if ctx ends first. A panic in fn is logged with its stack
// and returned as an error (without the stack: FPFSS shows the error to the
// curator).
func (v *Validator) run(ctx context.Context, fn func() error) (err error) {
	select {
	case v.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-v.slots }()
	defer func() {
		if p := recover(); p != nil {
			v.log.Errorf("panic while processing curation: %v\n%s", p, debug.Stack())
			err = fmt.Errorf("panic while processing curation: %v", p)
		}
	}()
	return fn()
}

// logCause logs the cause of err if it is a curation error with one. The
// curator sees only the message; staff need the cause to diagnose real
// curations that the archive libraries reject.
func (v *Validator) logCause(path string, err error) {
	var ce *CurationError
	if errors.As(err, &ce) && ce.Err != nil {
		v.log.Warnf("curation error in %s: %s: %v", path, ce.Message, ce.Err)
	}
}

// openCuration opens the archive at path and finds its layout. Problems with
// the curation are returned as a *CurationError.
func (v *Validator) openCuration(path string) (*Archive, *Layout, error) {
	a, err := Open(path, v.maxEntries)
	if err != nil {
		return nil, nil, err
	}
	l, err := FindLayout(a)
	if err != nil {
		a.Close()
		return nil, nil, err
	}
	return a, l, nil
}

// writeInNewDir creates a new directory under v.tempDir (named after
// pattern, as os.MkdirTemp) and writes the file name in it with write. It
// returns the path of the file. If anything fails, the directory is removed.
func (v *Validator) writeInNewDir(pattern, name string, write func(w io.Writer) error) (string, error) {
	dir, err := os.MkdirTemp(v.tempDir, pattern)
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(dir)
		}
	}()

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := write(f); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	done = true
	return path, nil
}

// readImage reads a new logo or screenshot. A nil reader gives nil.
func readImage(what string, r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxImageSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading the new %s: %w", what, err)
	}
	if len(data) > MaxImageSize {
		return nil, errors.New("the new " + what + " is larger than the 16MB limit")
	}
	return data, nil
}
