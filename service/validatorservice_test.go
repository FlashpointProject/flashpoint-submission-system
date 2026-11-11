package service

import (
	"context"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/curation"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noLookups knows no tags and no launch commands.
type noLookups struct{}

func (noLookups) TagExists(context.Context, string) (bool, error)           { return false, nil }
func (noLookups) LaunchCommandExists(context.Context, string) (bool, error) { return false, nil }

func testCurationValidator(t *testing.T) *curationValidator {
	return NewValidator(curation.New(noLookups{}, t.TempDir(), logrus.New()), curation.NewWikiTags(logrus.New()))
}

func curationFixture(name string) string {
	return filepath.Join("..", "curation", "testdata", name)
}

func TestCurationValidatorApplyEdit(t *testing.T) {
	ctx := context.Background()
	v := testCurationValidator(t)
	logoFile, err := os.Open(curationFixture("test_curation_valid.zip")) // any bytes do
	require.NoError(t, err)
	defer logoFile.Close()
	var logo multipart.File = logoFile

	title := "Updated"
	path, err := v.ApplyEdit(ctx, curationFixture("test_curation_valid.7z"), &types.EditCurationMeta{Title: &title}, &logo, nil)
	require.NoError(t, err)
	defer RemoveRepackFolder(ctx, *path)

	vr, err := v.ProvideArchiveForValidation(ctx, *path)
	require.NoError(t, err)
	assert.Equal(t, "Updated", *vr.Meta.Title)
	require.Len(t, vr.Images, 2)
	assert.Equal(t, "logo", vr.Images[0].Type)
}

// A problem with the curation is returned as the curator-facing message only.
func TestCurationValidatorApplyEditReportsCurationProblems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.zip")
	require.NoError(t, os.WriteFile(path, []byte("not an archive"), 0o644))
	title := "Updated"
	_, err := testCurationValidator(t).ApplyEdit(context.Background(), path, &types.EditCurationMeta{Title: &title}, nil, nil)
	require.EqualError(t, err, "There seems to a problem with your zip file.")
}

func TestCurationValidatorApplyEditNeedsAnEdit(t *testing.T) {
	_, err := testCurationValidator(t).ApplyEdit(context.Background(), curationFixture("test_curation_valid.zip"), nil, nil, nil)
	require.EqualError(t, err, "at least one field must be provided")
}

func TestCurationValidatorRepack(t *testing.T) {
	ctx := context.Background()
	v := testCurationValidator(t)
	vr, err := v.ProvideArchiveForRepacking(ctx, curationFixture("test_curation_valid.zip"))
	require.NoError(t, err)
	defer RemoveRepackFolder(ctx, *vr.FilePath)
	assert.Nil(t, vr.Error)
	assert.FileExists(t, *vr.FilePath)
	assert.NotNil(t, vr.Meta.LaunchCommand)
}

// A curation that cannot be packed is an Error in the response, which FPFSS
// shows as a repack error (as with the Python validator).
func TestCurationValidatorRepackReportsCurationProblems(t *testing.T) {
	vr, err := testCurationValidator(t).ProvideArchiveForRepacking(context.Background(), curationFixture("test_curation_missing_meta.zip"))
	require.NoError(t, err)
	require.NotNil(t, vr.Error)
	assert.Contains(t, *vr.Error, "The curation cannot be packed: ")
	assert.Nil(t, vr.FilePath)
}
