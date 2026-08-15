package service

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

type gatedMultipartFile struct {
	*bytes.Reader
	release chan struct{}
	reads   int
}

func (f *gatedMultipartFile) Read(p []byte) (int, error) {
	if f.reads == 0 {
		f.reads++
		if len(p) > 4 {
			p = p[:4]
		}
		return f.Reader.Read(p)
	}
	<-f.release
	return f.Reader.Read(p)
}

func (f *gatedMultipartFile) Close() error { return nil }

func TestCurationValidatorApplyEditStreamsMultipartBody(t *testing.T) {
	receivedFirstBytes := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/edit-meta", r.URL.Path)
		require.NotEmpty(t, r.URL.Query().Get("path"))

		reader, err := r.MultipartReader()
		require.NoError(t, err)
		part, err := reader.NextPart()
		require.NoError(t, err)
		require.Equal(t, "logo", part.FormName())

		first := make([]byte, 1)
		_, err = io.ReadFull(part, first)
		require.NoError(t, err)
		close(receivedFirstBytes)

		contents, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, "streamed-image", string(first)+string(contents))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(types.ValidatorEditMetaResponse{Path: "/tmp/edited.7z"}))
	}))
	defer server.Close()

	release := make(chan struct{})
	var logo multipart.File = &gatedMultipartFile{
		Reader:  bytes.NewReader([]byte("streamed-image")),
		release: release,
	}

	result := make(chan error, 1)
	go func() {
		validator := NewValidator(server.URL)
		path, err := validator.ApplyEdit("submission.7z", nil, &logo, nil)
		if err == nil && (path == nil || *path != "/tmp/edited.7z") {
			err = io.ErrUnexpectedEOF
		}
		result <- err
	}()

	select {
	case <-receivedFirstBytes:
		// The request reached the server while the source file was still blocked.
	case <-time.After(5 * time.Second):
		t.Fatal("validator request did not begin before the source file reached EOF")
	}
	close(release)

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("validator request did not finish")
	}
}

func TestCurationValidatorApplyEditPreservesMetadataOnlyRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		require.True(t, strings.Contains(r.FormValue("metadata"), "Updated"))
		require.NoError(t, json.NewEncoder(w).Encode(types.ValidatorEditMetaResponse{Path: "/tmp/edited.7z"}))
	}))
	defer server.Close()

	title := "Updated"
	path, err := NewValidator(server.URL).ApplyEdit("submission.7z", &types.EditCurationMeta{Title: &title}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "/tmp/edited.7z", *path)
}
