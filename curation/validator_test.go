package curation

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempDirEntries lists what v left in its temp dir.
func tempDirEntries(t *testing.T, v *Validator) []string {
	t.Helper()
	entries, err := os.ReadDir(v.tempDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRunLimitsConcurrency(t *testing.T) {
	v := testValidator(t, pythonLookups())
	for range maxConcurrentOperations {
		v.slots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := v.Validate(ctx, fixture("test_curation_valid.zip"))
	require.ErrorIs(t, err, context.DeadlineExceeded)

	<-v.slots
	_, err = v.Validate(context.Background(), fixture("test_curation_valid.zip"))
	require.NoError(t, err)
}

func TestRunRecoversPanics(t *testing.T) {
	v := testValidator(t, pythonLookups())
	logger, logs := logtest.NewNullLogger()
	v.log = logger
	err := v.run(context.Background(), func() error { panic("boom") })
	// The error goes to the curator: no stack trace in it.
	require.EqualError(t, err, "panic while processing curation: boom")
	assert.Empty(t, v.slots, "the slot is released")
	// The log has the stack.
	entry := logs.LastEntry()
	require.NotNil(t, entry)
	assert.Equal(t, logrus.ErrorLevel, entry.Level)
	assert.Contains(t, entry.Message, "panic while processing curation: boom")
	assert.Contains(t, entry.Message, "TestRunRecoversPanics")
}

func TestWriteInNewDirRemovesTheDirOnFailure(t *testing.T) {
	v := testValidator(t, pythonLookups())
	failure := errors.New("write failed")
	_, err := v.writeInNewDir("pack-", "x.zip", func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return failure
	})
	require.ErrorIs(t, err, failure)
	assert.Empty(t, tempDirEntries(t, v))

	err = v.run(context.Background(), func() error {
		_, err := v.writeInNewDir("pack-", "x.zip", func(io.Writer) error { panic("boom") })
		return err
	})
	require.ErrorContains(t, err, "boom")
	assert.Empty(t, tempDirEntries(t, v))
}

func TestPack(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		t.Run(ext, func(t *testing.T) {
			v := testValidator(t, pythonLookups())
			r, err := v.Pack(context.Background(), fixture("test_curation_valid."+ext))
			require.NoError(t, err)

			require.NotNil(t, r.FilePath)
			assert.Nil(t, r.Error)
			assert.Equal(t, v.tempDir, filepath.Dir(filepath.Dir(*r.FilePath)))
			assert.True(t, strings.HasPrefix(filepath.Base(filepath.Dir(*r.FilePath)), "pack-"))
			assert.Equal(t, validRoot+".zip", filepath.Base(*r.FilePath))
			assert.Equal(t, strPtr("Sesame Street: Where Is Thumbkin?"), r.Meta.Title)
			assert.Equal(t, []string{"logo", "screenshot"}, []string{r.Images[0].Type, r.Images[1].Type})

			zr, err := zip.OpenReader(*r.FilePath)
			require.NoError(t, err)
			defer zr.Close()
			var names []string
			for _, f := range zr.File {
				names = append(names, f.Name)
			}
			// The 7z fixture stores the content files in another order.
			slices.Sort(names[1:])
			assert.Equal(t, []string{
				"content.json",
				"content/www.sesamestreet.org/sites/all/libraries/swfobject/swfobject.js",
				"content/www.sesamestreet.org/ssgames/flash/129.html",
			}, names)
		})
	}
}

// As the Python validator, Pack packs a curation despite its validation
// errors: staff can add a submission the bot complained about.
func TestPackIgnoresValidationErrors(t *testing.T) {
	for _, name := range []string{"missing_logo", "duplicate_launch_command", "unknown_tag", "invalid_extreme", "missing_title"} {
		t.Run(name, func(t *testing.T) {
			path := fixture("test_curation_" + name + ".zip")
			vr := validate(t, pythonLookups(), path)
			require.NotEmpty(t, append(vr.CurationErrors, vr.CurationWarnings...))

			v := testValidator(t, pythonLookups())
			r, err := v.Pack(context.Background(), path)
			require.NoError(t, err)
			assert.FileExists(t, *r.FilePath)
			assert.NotNil(t, r.Meta.LaunchCommand)
		})
	}
}

func TestPackFailsWhenTheCurationCannotBePacked(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  string
	}{
		{"invalid_archive", "There seems to a problem with your zip file."},
		{"missing_root_folder", msgNotStructured},
		{"missing_meta", msgMetaMissing},
		{"empty_meta", msgMetaEmpty},
		{"missing_content", msgContentMissing},
		{"empty_content", msgContentEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := testValidator(t, pythonLookups())
			r, err := v.Pack(context.Background(), fixture("test_curation_"+tc.name+".zip"))
			var ce *CurationError
			require.ErrorAs(t, err, &ce)
			assert.True(t, strings.HasPrefix(ce.Message, "The curation cannot be packed: "), ce.Message)
			assert.Contains(t, ce.Message, tc.err)
			assert.Nil(t, r)
			assert.Empty(t, tempDirEntries(t, v))
		})
	}
}

// The curator sees only the message of a curation error; its cause is
// logged, once per operation, for staff.
func TestCurationErrorCauseIsLogged(t *testing.T) {
	// A truncated zip: the zip library rejects it with its own error.
	data, err := os.ReadFile(fixture("test_curation_valid.zip"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "truncated.zip")
	require.NoError(t, os.WriteFile(path, data[:len(data)/2], 0o644))

	operations := map[string]func(v *Validator) error{
		"validate": func(v *Validator) error {
			_, err := v.Validate(context.Background(), path)
			return err
		},
		"pack": func(v *Validator) error {
			_, err := v.Pack(context.Background(), path)
			return err
		},
		"edit": func(v *Validator) error {
			_, err := v.EditMeta(context.Background(), path, nil, nil, nil)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			v := testValidator(t, pythonLookups())
			logger, logs := logtest.NewNullLogger()
			v.log = logger
			_ = operation(v)

			require.Len(t, logs.AllEntries(), 1)
			entry := logs.LastEntry()
			assert.Equal(t, logrus.WarnLevel, entry.Level)
			assert.Contains(t, entry.Message, path)
			assert.Contains(t, entry.Message, "There seems to a problem with your zip file.: zip: not a valid zip file")
		})
	}
}

func TestEditMeta(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		t.Run(ext, func(t *testing.T) {
			v := testValidator(t, pythonLookups())
			edit := &types.EditCurationMeta{Title: strPtr("New Title"), Version: strPtr("1.10")}
			path, err := v.EditMeta(context.Background(), fixture("test_curation_valid."+ext), edit, nil, nil)
			require.NoError(t, err)

			assert.Equal(t, v.tempDir, filepath.Dir(filepath.Dir(path)))
			assert.True(t, strings.HasPrefix(filepath.Base(filepath.Dir(path)), "edit-"))
			assert.Equal(t, "test_curation_valid.zip", filepath.Base(path))

			original := validate(t, pythonLookups(), fixture("test_curation_valid."+ext))
			edited := validate(t, pythonLookups(), path)
			assert.Equal(t, []string{}, edited.CurationErrors)
			want := original.Meta
			want.Title = strPtr("New Title")
			want.Version = strPtr("1.10")
			assert.Equal(t, want, edited.Meta)
			assert.Equal(t, original.Images, edited.Images)
		})
	}
}

func TestEditMetaImages(t *testing.T) {
	v := testValidator(t, pythonLookups())
	path, err := v.EditMeta(context.Background(), fixture("test_curation_valid.7z"), nil,
		strings.NewReader("new logo"), strings.NewReader("new screenshot"))
	require.NoError(t, err)

	r := validate(t, pythonLookups(), path)
	assert.Equal(t, []types.ValidatorResponseImage{
		{Type: "logo", Data: base64.StdEncoding.EncodeToString([]byte("new logo"))},
		{Type: "screenshot", Data: base64.StdEncoding.EncodeToString([]byte("new screenshot"))},
	}, r.Images)
	assert.Equal(t, strPtr("Sesame Street: Where Is Thumbkin?"), r.Meta.Title)
}

func TestEditMetaErrors(t *testing.T) {
	ctx := context.Background()
	edit := &types.EditCurationMeta{Title: strPtr("New Title")}
	brokenMeta := writeZip(t,
		zipEntry{name: validRoot + "/content/example.com/a.swf", content: "swf"},
		zipEntry{name: validRoot + "/meta.yaml", content: baseMeta + "Title: twice\n"},
	)
	for _, tc := range []struct {
		name       string
		path       string
		logo       io.Reader
		screenshot io.Reader
		err        string
	}{
		{name: "no meta", path: fixture("test_curation_missing_meta.zip"),
			err: "did not find a meta file to edit: " + msgMetaMissing},
		{name: "not an archive", path: fixture("test_curation_invalid_archive.zip"),
			err: "There seems to a problem with your zip file."},
		{name: "logo too large", path: fixture("test_curation_valid.zip"),
			logo: bytes.NewReader(make([]byte, MaxImageSize+1)),
			err:  "the new logo is larger than the 16MB limit"},
		{name: "screenshot too large", path: fixture("test_curation_valid.zip"),
			screenshot: bytes.NewReader(make([]byte, MaxImageSize+1)),
			err:        "the new screenshot is larger than the 16MB limit"},
		{name: "edited meta does not parse", path: brokenMeta,
			err: "the edited meta file is not valid: " + msgMetaYAML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := testValidator(t, pythonLookups())
			path, err := v.EditMeta(ctx, tc.path, edit, tc.logo, tc.screenshot)
			require.ErrorContains(t, err, tc.err)
			assert.Empty(t, path)
			assert.Empty(t, tempDirEntries(t, v))
		})
	}
}
