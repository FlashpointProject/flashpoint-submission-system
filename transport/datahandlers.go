package transport

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/gorilla/mux"
)

func (a *App) HandleGameImageFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	longFilePath := params[constants.ResourceKeyLongFile]

	diskFilePath := filepath.Clean(fmt.Sprintf(`%s/%s`, a.Conf.ImagesDir, longFilePath))

	if !strings.HasPrefix(diskFilePath, filepath.Clean(a.Conf.ImagesDir)) {
		writeError(ctx, w, perr("invalid file path", http.StatusBadRequest))
		return
	}

	fileInfo, err := os.Stat(diskFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Error accessing file", http.StatusInternalServerError)
		return
	}

	if fileInfo.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	f, err := os.Open(diskFilePath)
	defer f.Close()
	http.ServeContent(w, r, fileInfo.Name(), fileInfo.ModTime(), f)
}

func (a *App) HandleDownloadSubmissionFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	submissionFileID := params[constants.ResourceKeyFileID]

	sfid, err := strconv.ParseInt(submissionFileID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission file id", http.StatusBadRequest))
		return
	}

	sfs, err := a.Service.GetSubmissionFiles(ctx, []int64{sfid})
	if err != nil {
		writeError(ctx, w, err)
		return
	}
	sf := sfs[0]

	err = a.Service.EmitSubmissionDownloadEvent(ctx, uid, sf.SubmissionID, sfid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	f, err := os.Open(fmt.Sprintf("%s/%s", a.Conf.SubmissionsDirFullPath, sf.CurrentFilename))

	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to read file", http.StatusInternalServerError))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", sf.CurrentFilename))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, sf.CurrentFilename, sf.UploadedAt, f)
}

func (a *App) HandleDownloadSubmissionBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	submissionFileIDs := strings.Split(params[constants.ResourceKeyFileIDs], ",")
	sfids := make([]int64, 0, len(submissionFileIDs))

	for _, submissionFileID := range submissionFileIDs {
		sfid, err := strconv.ParseInt(submissionFileID, 10, 64)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("invalid submission file id", http.StatusBadRequest))
			return
		}
		sfids = append(sfids, sfid)
	}

	sfs, err := a.Service.GetSubmissionFiles(ctx, sfids)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	filePaths := make([]string, 0, len(sfs))

	for _, sf := range sfs {
		filePaths = append(filePaths, fmt.Sprintf("%s/%s", a.Conf.SubmissionsDirFullPath, sf.CurrentFilename))

		err = a.Service.EmitSubmissionDownloadEvent(ctx, uid, sf.SubmissionID, sf.ID)
		if err != nil {
			writeError(ctx, w, err)
			return
		}
	}

	filename := fmt.Sprintf("fpfss-batch-%dfiles-%s.tar", len(sfs), utils.NewRealRandomStringProvider().RandomString(16))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := utils.WriteTarball(w, filePaths); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to create tarball", http.StatusInternalServerError))
		return
	}
}

func (a *App) HandleDownloadCurationImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	curationImageID := params[constants.ResourceKeyCurationImageID]

	ciid, err := strconv.ParseInt(curationImageID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid curation image id", http.StatusBadRequest))
		return
	}

	ci, err := a.Service.GetCurationImage(ctx, ciid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	f, err := os.Open(fmt.Sprintf("%s/%s", a.Conf.SubmissionImagesDirFullPath, ci.Filename))
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to read file", http.StatusInternalServerError))
		return
	}

	fi, err := f.Stat()
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to read file", http.StatusInternalServerError))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image")
	http.ServeContent(w, r, ci.Filename, fi.ModTime(), f)
}
