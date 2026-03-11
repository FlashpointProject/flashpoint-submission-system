package integration_tests

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func downloadSubmissionFile(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, sid, fid int64) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequest("GET", fmt.Sprintf("/data/submission/%d/file/%d", sid, fid), nil)
	require.NoError(t, err)
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func downloadSubmissionBatch(t *testing.T, l *logrus.Entry, app *transport.App, cookie *http.Cookie, fids []int64) *httptest.ResponseRecorder {
	t.Helper()

	rawIDs := make([]string, 0, len(fids))
	for _, fid := range fids {
		rawIDs = append(rawIDs, strconv.FormatInt(fid, 10))
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("/data/submission-file-batch/%s", strings.Join(rawIDs, ",")), nil)
	require.NoError(t, err)
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	logging.LogRequestHandler(l, app.Mux).ServeHTTP(rr, req)
	return rr
}

func getSubmissionFilesBySubmissionID(t *testing.T, ctx context.Context, db database.DAL, sid int64) []*types.ExtendedSubmissionFile {
	t.Helper()

	dbs, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer dbs.Rollback()

	files, err := db.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
	require.NoError(t, err)
	return files
}

func requireDownloadedFileMatchesStoredFile(t *testing.T, app *transport.App, rr *httptest.ResponseRecorder, file *types.ExtendedSubmissionFile) {
	t.Helper()

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
	require.Equal(t, fmt.Sprintf("attachment; filename=%s", file.CurrentFilename), rr.Header().Get("Content-Disposition"))

	expectedPath := filepath.Join(app.Conf.SubmissionsDirFullPath, file.CurrentFilename)
	expectedContent, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	require.Equal(t, expectedContent, rr.Body.Bytes())
}

func readTarballEntries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()

	entries := make(map[string][]byte)
	reader := tar.NewReader(bytes.NewReader(archive))

	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		require.NoError(t, err)

		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		entries[header.Name] = content
	}
}

func TestSubmissionFileAccess(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	submitter := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000401), []int64{roleIDTrialCurator}, "submitter")
	moderator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000402), []int64{roleIDModerator}, "moderator")
	curator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000403), []int64{roleIDCurator}, "curator")
	otherTrialCurator := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000404), []int64{roleIDTrialCurator}, "other-trial-curator")
	inAudit := createExtendedTestUser(t, ctx, l, app, db, pgdb, int64(100000405), nil, "in-audit")

	t.Run("UnfrozenRoleAccess", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		files := getSubmissionFilesBySubmissionID(t, ctx, db, sid)
		require.Len(t, files, 1)

		rr := downloadSubmissionFile(t, l, app, moderator.Cookie, sid, files[0].FileID)
		requireDownloadedFileMatchesStoredFile(t, app, rr, files[0])

		rr = downloadSubmissionFile(t, l, app, curator.Cookie, sid, files[0].FileID)
		requireDownloadedFileMatchesStoredFile(t, app, rr, files[0])

		rr = downloadSubmissionFile(t, l, app, otherTrialCurator.Cookie, sid, files[0].FileID)
		requireDownloadedFileMatchesStoredFile(t, app, rr, files[0])

		rr = downloadSubmissionFile(t, l, app, inAudit.Cookie, sid, files[0].FileID)
		requireDownloadedFileMatchesStoredFile(t, app, rr, files[0])

		rr = downloadSubmissionFile(t, l, app, nil, sid, files[0].FileID)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})

	t.Run("AllVersionsAccessibleIndividuallyAndInBatchAcrossUploaders", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", curator.Cookie, &sid)

		files := getSubmissionFilesBySubmissionID(t, ctx, db, sid)
		require.Len(t, files, 2)
		require.Equal(t, curator.ID, files[0].SubmitterID, "newest file should belong to the updating user")
		require.Equal(t, submitter.ID, files[1].SubmitterID, "oldest file should belong to the original submitter")

		submission := searchSubmissionByID(t, ctx, app, sid)
		require.Equal(t, submitter.ID, submission.SubmitterID, "submission submitter should remain the original uploader")
		require.Equal(t, curator.ID, submission.LastUploaderID, "last uploader should reflect the latest uploader")

		downloadedBodies := make(map[string]struct{}, len(files))
		fileIDs := make([]int64, 0, len(files))

		for _, file := range files {
			rr := downloadSubmissionFile(t, l, app, moderator.Cookie, sid, file.FileID)
			requireDownloadedFileMatchesStoredFile(t, app, rr, file)
			downloadedBodies[string(rr.Body.Bytes())] = struct{}{}
			fileIDs = append(fileIDs, file.FileID)
		}
		require.Len(t, downloadedBodies, len(files), "each submission version should remain individually downloadable")

		rr := downloadSubmissionBatch(t, l, app, moderator.Cookie, fileIDs)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))

		entries := readTarballEntries(t, rr.Body.Bytes())
		require.Len(t, entries, len(files))

		for _, file := range files {
			expectedPath := filepath.Join(app.Conf.SubmissionsDirFullPath, file.CurrentFilename)
			expectedContent, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			require.Equal(t, expectedContent, entries[expectedPath])
		}
	})

	t.Run("FrozenSubmissionRequiresFreezerRole", func(t *testing.T) {
		sid := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		files := getSubmissionFilesBySubmissionID(t, ctx, db, sid)
		require.Len(t, files, 1)

		rr := freezeSubmission(t, l, app, moderator.Cookie, sid)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		rr = downloadSubmissionFile(t, l, app, moderator.Cookie, sid, files[0].FileID)
		requireDownloadedFileMatchesStoredFile(t, app, rr, files[0])

		rr = downloadSubmissionFile(t, l, app, curator.Cookie, sid, files[0].FileID)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		rr = downloadSubmissionFile(t, l, app, otherTrialCurator.Cookie, sid, files[0].FileID)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		rr = downloadSubmissionFile(t, l, app, inAudit.Cookie, sid, files[0].FileID)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})

	t.Run("MismatchedSubmissionIDIsUnauthorized", func(t *testing.T) {
		frozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)
		frozenFiles := getSubmissionFilesBySubmissionID(t, ctx, db, frozenSID)
		require.Len(t, frozenFiles, 1)

		rr := freezeSubmission(t, l, app, moderator.Cookie, frozenSID)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

		unfrozenSID := uploadTestSubmission(t, l, app, "./test_files/Warpstar4K.7z", submitter.Cookie, nil)

		rr = downloadSubmissionFile(t, l, app, curator.Cookie, unfrozenSID, frozenFiles[0].FileID)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})
}
