package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"

	"github.com/FlashpointProject/flashpoint-submission-system/curation"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// curationValidator is the Validator FPFSS uses: the curation package for
// archives and the wiki for tag descriptions.
type curationValidator struct {
	validator *curation.Validator
	tags      *curation.WikiTags
}

func NewValidator(validator *curation.Validator, tags *curation.WikiTags) *curationValidator {
	return &curationValidator{validator: validator, tags: tags}
}

func (c *curationValidator) ProvideArchiveForValidation(ctx context.Context, filePath string) (*types.ValidatorResponse, error) {
	return c.validator.Validate(ctx, filePath)
}

// ProvideArchiveForRepacking packs the curation. A curation that cannot be
// packed is reported in the response's Error, as the Python validator did.
func (c *curationValidator) ProvideArchiveForRepacking(ctx context.Context, filePath string) (*types.ValidatorRepackResponse, error) {
	vr, err := c.validator.Pack(ctx, filePath)
	var ce *curation.CurationError
	if errors.As(err, &ce) {
		return &types.ValidatorRepackResponse{Error: &ce.Message}, nil
	}
	return vr, err
}

// ApplyEdit writes an edited copy of the curation and returns its path. The
// caller removes the copy's directory afterwards. For a problem with the
// curation, the error is only the curator-facing message.
func (c *curationValidator) ApplyEdit(ctx context.Context, filePath string, editCurationMeta *types.EditCurationMeta, logoFile *multipart.File, screenshotFile *multipart.File) (*string, error) {
	if editCurationMeta == nil && logoFile == nil && screenshotFile == nil {
		return nil, fmt.Errorf("at least one field must be provided")
	}
	var logo, screenshot io.Reader
	if logoFile != nil {
		logo = *logoFile
	}
	if screenshotFile != nil {
		screenshot = *screenshotFile
	}

	editedPath, err := c.validator.EditMeta(ctx, filePath, editCurationMeta, logo, screenshot)
	var ce *curation.CurationError
	if errors.As(err, &ce) {
		return nil, errors.New(ce.Message)
	}
	if err != nil {
		return nil, err
	}
	return &editedPath, nil
}

// GetTags returns the category and wiki tags. curation.WikiTags caches the
// wiki list and falls back to the last good one, so the error is always nil.
func (c *curationValidator) GetTags(ctx context.Context) ([]types.Tag, error) {
	return c.tags.GetTags(ctx)
}
