package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

func TestSubmissionMetaEditRejectsAggregateBodyAboveLimit(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	metadata, err := writer.CreateFormField("metadata")
	require.NoError(t, err)
	_, err = metadata.Write(bytes.Repeat([]byte("x"), submissionMetaEditRequestLimit+1))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/submission/1/metaedit", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = mux.SetURLVars(req, map[string]string{constants.ResourceKeySubmissionID: "1"})
	ctx := context.WithValue(req.Context(), utils.CtxKeys.RequestType, constants.RequestJSON)
	req = req.WithContext(ctx)

	response := httptest.NewRecorder()
	(&App{}).HandleApplySubmissionMetaEditPage(response, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	require.Contains(t, response.Body.String(), errSubmissionMetaEditTooLarge.Error())
}

func TestValidateSubmissionMetaEditPNGAcceptsValidImageAndRewinds(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNiAAAABgADNjd8qAAAAABJRU5ErkJggg==")
	require.NoError(t, err)
	file := &memoryMultipartFile{Reader: bytes.NewReader(pngBytes)}
	header := &multipart.FileHeader{Filename: "logo.PNG", Size: int64(len(pngBytes))}

	require.NoError(t, validateSubmissionMetaEditPNG(file, header))
	position, err := file.Seek(0, 1)
	require.NoError(t, err)
	require.Zero(t, position)
}

func TestValidateSubmissionMetaEditPNGRejectsOversizedFile(t *testing.T) {
	file := &memoryMultipartFile{Reader: bytes.NewReader(nil)}
	header := &multipart.FileHeader{Filename: "logo.png", Size: submissionMetaEditFileLimit + 1}

	err := validateSubmissionMetaEditPNG(file, header)
	require.ErrorIs(t, err, errSubmissionMetaEditTooLarge)
}

type memoryMultipartFile struct {
	*bytes.Reader
}

func (f *memoryMultipartFile) Close() error { return nil }
