package service

import (
	"context"
	"io"
	"mime/multipart"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

type Validator interface {
	Validate(ctx context.Context, file io.Reader, filename string) (*types.ValidatorResponse, error)
	ApplyEdit(filePath string, editCurationMeta *types.EditCurationMeta, logoFile *multipart.File, screenshotFile *multipart.File) (*string, error)
	GetTags(ctx context.Context) ([]types.Tag, error)
	ProvideArchiveForValidation(filePath string) (*types.ValidatorResponse, error)
	ProvideArchiveForRepacking(filePath string) (*types.ValidatorRepackResponse, error)
}

type MultipartFileProvider interface {
	Filename() string
	Size() int64
	Open() (multipart.File, error)
}

type Clock interface {
	Now() time.Time
	Unix(sec int64, nsec int64) time.Time
}
