package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// testPNG is a 1x1 PNG, the fake's default logo and screenshot.
const testPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg=="

// fakeValidator is the service.Validator of most integration tests. It does
// not read the archives: the meta, errors and warnings come from
// validatorMockOptions. Responses go through JSON, as they did when the
// validator was an HTTP service, so FPFSS never shares memory with the
// options.
type fakeValidator struct {
	options   *validatorMockOptions
	repackDir string
	responses atomic.Int64
}

func newFakeValidator(options *validatorMockOptions, repackDir string) *fakeValidator {
	if options == nil {
		options = &validatorMockOptions{}
	}
	return &fakeValidator{options: options, repackDir: repackDir}
}

func (f *fakeValidator) ProvideArchiveForValidation(_ context.Context, path string) (*types.ValidatorResponse, error) {
	if f.options.BeforeValidate != nil {
		f.options.BeforeValidate()
	}

	responseIndex := int(f.responses.Add(1) - 1)
	curationErrors := f.options.CurationErrors
	curationWarnings := f.options.CurationWarnings
	if seq := f.options.CurationErrorsSeq; len(seq) > 0 {
		curationErrors = seq[min(responseIndex, len(seq)-1)]
	}
	if seq := f.options.CurationWarningsSeq; len(seq) > 0 {
		curationWarnings = seq[min(responseIndex, len(seq)-1)]
	}
	meta := defaultValidatorMockMeta()
	if f.options.UploadMeta != nil {
		meta = *f.options.UploadMeta
	}
	return viaJSON(&types.ValidatorResponse{
		Path:             path,
		Meta:             meta,
		CurationErrors:   curationErrors,
		CurationWarnings: curationWarnings,
		CurationNotes:    f.options.CurationNotes,
		Images:           defaultTestImages(),
	})
}

// ProvideArchiveForRepacking "packs" the curation by copying it into a
// directory of its own, which FPFSS removes afterwards.
func (f *fakeValidator) ProvideArchiveForRepacking(_ context.Context, path string) (*types.ValidatorRepackResponse, error) {
	meta := defaultValidatorMockMeta()
	if f.options.RepackMeta != nil {
		meta = *f.options.RepackMeta
	}
	images := defaultTestImages()
	if f.options.RepackImages != nil {
		images = f.options.RepackImages
	}
	packPath, err := f.writeInNewDir("pack-", "pack.zip", path, nil)
	if err != nil {
		return nil, err
	}
	return viaJSON(&types.ValidatorRepackResponse{FilePath: &packPath, Meta: meta, Images: images})
}

// ApplyEdit writes a distinct "edited" archive into a directory of its own:
// the original bytes plus the edit as JSON. The meta of the edited archive
// still comes from the options when it is validated.
func (f *fakeValidator) ApplyEdit(_ context.Context, path string, edit *types.EditCurationMeta, logo, screenshot *multipart.File) (*string, error) {
	if edit == nil && logo == nil && screenshot == nil {
		return nil, fmt.Errorf("at least one field must be provided")
	}
	var suffix []byte
	if edit != nil {
		metadata, err := json.Marshal(edit)
		if err != nil {
			return nil, err
		}
		suffix = append([]byte("\n"), metadata...)
	}
	editedPath, err := f.writeInNewDir("edit-", "edited.7z", path, suffix)
	if err != nil {
		return nil, err
	}
	return &editedPath, nil
}

func (f *fakeValidator) GetTags(context.Context) ([]types.Tag, error) {
	return []types.Tag{}, nil
}

// writeInNewDir writes the content of the file at source, followed by
// suffix, to a file name in a new directory under the repack directory.
func (f *fakeValidator) writeInNewDir(pattern, name, source string, suffix []byte) (string, error) {
	content, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(f.repackDir, pattern)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, append(content, suffix...), 0o644)
}

func defaultTestImages() []types.ValidatorResponseImage {
	return []types.ValidatorResponseImage{{Type: "logo", Data: testPNG}, {Type: "screenshot", Data: testPNG}}
}

// viaJSON returns a copy of v made by encoding and decoding it as JSON.
func viaJSON[T any](v *T) (*T, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out T
	return &out, json.Unmarshal(data, &out)
}
