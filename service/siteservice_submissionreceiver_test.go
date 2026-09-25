package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/resumableuploadservice"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type testSubmissionReader struct{ *bytes.Reader }

func (testSubmissionReader) Close() error             { return nil }
func (testSubmissionReader) GetFractionRead() float32 { return 1 }

type testSubmissionReaderProvider struct{}

func (testSubmissionReaderProvider) GetReadCloserInformer() (resumableuploadservice.ReadCloserInformer, error) {
	return testSubmissionReader{bytes.NewReader([]byte("archive"))}, nil
}

type testSubmissionRandomStringer struct{}

func (testSubmissionRandomStringer) RandomString(int) string { return "submission" }

type failedSubmissionValidator struct {
	Validator
	pgOpened *bool
}

func (v failedSubmissionValidator) ProvideArchiveForValidation(string) (*types.ValidatorResponse, error) {
	if *v.pgOpened {
		return nil, errors.New("PostgreSQL opened before validator completed")
	}
	return nil, errors.New("validator unavailable")
}

func TestReceivedSubmissionDoesNotOpenPostgresWhileValidatorRuns(t *testing.T) {
	var pgOpened bool
	dir := t.TempDir()
	s := &SiteService{
		submissionsDir:       filepath.Join(dir, "submissions"),
		submissionImagesDir:  filepath.Join(dir, "images"),
		randomStringProvider: testSubmissionRandomStringer{},
		validator:            failedSubmissionValidator{pgOpened: &pgOpened},
		SSK:                  SubmissionStatusKeeper{m: make(map[string]*types.SubmissionStatus)},
	}
	s.SSK.SetReceived("test")
	ctx := context.WithValue(context.Background(), utils.CtxKeys.UserID, int64(123))
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, logrus.NewEntry(logrus.New()))
	openPGSession := func() (database.PGDBSession, error) {
		pgOpened = true
		return nil, errors.New("unexpected PostgreSQL transaction")
	}

	path, _, _, err := s.processReceivedSubmission(ctx, nil, openPGSession, testSubmissionReaderProvider{}, "test.zip", int64(len("archive")), nil, "trial", "test")
	require.ErrorContains(t, err, "validator unavailable")
	require.False(t, pgOpened)
	if path != nil {
		require.NoError(t, os.Remove(*path))
	}
}
