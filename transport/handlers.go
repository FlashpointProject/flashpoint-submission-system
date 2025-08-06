package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/activityevents"

	"github.com/kofalt/go-memoize"

	"github.com/FlashpointProject/flashpoint-submission-system/clients"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/gorilla/mux"
)

var epoch = time.Unix(0, 0).Format(time.RFC1123)

var noCacheHeaders = map[string]string{
	"Expires":         epoch,
	"Cache-Control":   "no-cache, private, max-age=0",
	"Pragma":          "no-cache",
	"X-Accel-Expires": "0",
}

var etagHeaders = []string{
	"ETag",
	"If-Modified-Since",
	"If-Match",
	"If-None-Match",
	"If-Range",
	"If-Unmodified-Since",
}

func NoCache(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		// Delete any ETag headers that may have been set
		for _, v := range etagHeaders {
			if r.Header.Get(v) != "" {
				r.Header.Del(v)
			}
		}

		// Set our NoCache headers
		for k, v := range noCacheHeaders {
			w.Header().Set(k, v)
		}

		h.ServeHTTP(w, r)
	}

	return http.HandlerFunc(fn)
}

var pageDataCache = memoize.NewMemoizer(24*time.Hour, 48*time.Hour)

func (a *App) HandleCommentReceiverBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	params := mux.Vars(r)
	submissionIDs := strings.Split(params["submission-ids"], ",")
	sids := make([]int64, 0, len(submissionIDs))

	for _, submissionFileID := range submissionIDs {
		sid, err := strconv.ParseInt(submissionFileID, 10, 64)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
			return
		}
		sids = append(sids, sid)
	}

	if err := r.ParseForm(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
		return
	}

	// TODO use gorilla/schema
	formAction := r.FormValue("action")
	formMessage := r.FormValue("message")
	formIgnoreDupeActions := r.FormValue("ignore-duplicate-actions")

	if len([]rune(formMessage)) > 20000 {
		err := fmt.Errorf("message cannot be longer than 20000 characters")
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, constants.PublicError{Msg: err.Error(), Status: http.StatusBadRequest})
		return
	}

	if err := a.Service.ReceiveComments(ctx, uid, sids, formAction, formMessage, formIgnoreDupeActions,
		a.Conf.SubmissionsDirFullPath, a.Conf.DataPacksDir, a.Conf.FrozenPacksDir, a.Conf.ImagesDir, r); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp("success", http.StatusOK), http.StatusOK)
}

func (a *App) HandleSoftDeleteSubmissionFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionFileID := params[constants.ResourceKeyFileID]

	sfid, err := strconv.ParseInt(submissionFileID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission file id", http.StatusBadRequest))
		return
	}

	if err := r.ParseForm(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
		return
	}

	deleteReason := r.FormValue("reason")
	if len(deleteReason) < 3 {
		writeError(ctx, w, perr("reason must be at least 3 characters long", http.StatusBadRequest))
		return
	} else if len(deleteReason) > 255 {
		writeError(ctx, w, perr("reason cannot be longer than 255 characters", http.StatusBadRequest))
		return
	}

	if err := a.Service.SoftDeleteSubmissionFile(ctx, sfid, deleteReason); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleSoftDeleteSubmission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	deleteReason := r.FormValue("reason")
	if len(deleteReason) < 3 {
		writeError(ctx, w, perr("reason must be at least 3 characters long", http.StatusBadRequest))
		return
	} else if len(deleteReason) > 255 {
		writeError(ctx, w, perr("reason cannot be longer than 255 characters", http.StatusBadRequest))
		return
	}

	if err := a.Service.SoftDeleteSubmission(ctx, sid, deleteReason); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleSoftDeleteComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	commentID := params[constants.ResourceKeyCommentID]

	cid, err := strconv.ParseInt(commentID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid comment id", http.StatusBadRequest))
		return
	}

	deleteReason := r.FormValue("reason")
	if len(deleteReason) < 3 {
		writeError(ctx, w, perr("reason must be at least 3 characters long", http.StatusBadRequest))
		return
	} else if len(deleteReason) > 255 {
		writeError(ctx, w, perr("reason cannot be longer than 255 characters", http.StatusBadRequest))
		return
	}

	if err := a.Service.SoftDeleteComment(ctx, cid, deleteReason); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleOverrideBot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	if err := a.Service.OverrideBot(ctx, sid); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleSubmissionReceiverResumable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	// get submission ID
	var sid *int64
	if submissionID != "" {
		sidParsed, err := strconv.ParseInt(submissionID, 10, 64)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
			return
		}
		sid = &sidParsed
	}

	chunk, resumableParams, err := a.parseResumableRequest(ctx, r)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	// then a magic happens
	tn, err := a.Service.ReceiveSubmissionChunk(ctx, sid, resumableParams, chunk)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	resp := types.ReceiveFileTempNameResp{
		Message:  "success",
		TempName: tn,
	}

	writeResponse(ctx, w, resp, http.StatusOK)
}

func (a *App) HandleReceiverResumableTestChunk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// parse resumable params
	resumableParams := &types.ResumableParams{}

	if err := a.decoder.Decode(resumableParams, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode resumable query params", http.StatusInternalServerError))
		return
	}

	// then a magic happens
	alreadyReceived, err := a.Service.IsChunkReceived(ctx, resumableParams)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if !alreadyReceived {
		writeResponse(ctx, w, nil, http.StatusNotFound)
		return
	}

	writeResponse(ctx, w, nil, http.StatusOK)
}

func (a *App) HandleRootPage(w http.ResponseWriter, r *http.Request) {
	uid, err := a.GetUserIDFromCookie(r)
	ctx := r.Context()
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), utils.CtxKeys.UserID, uid))
	ctx = r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/root.gohtml")
}

func (a *App) HandleSessionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	sessions, err := a.Service.GetSessions(ctx, uid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"sessions": sessions}, http.StatusOK)
}

func (a *App) HandleSessionPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	sessionID := params[constants.ResourceKeySessionID]

	if sessionID == "" {
		writeError(ctx, w, perr("invalid session id format", http.StatusBadRequest))
		return
	}

	sessionIDInt, err := strconv.ParseInt(sessionID, 10, 64)
	if err != nil {
		writeError(ctx, w, perr("invalid session id format", http.StatusBadRequest))
		return
	}

	err = a.Service.RevokeSession(ctx, uid, sessionIDInt)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (a *App) HandleOwnedClientApplications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	ownedApps := make([]types.ClientApplication, 0)
	for _, clientApp := range clients.ClientApps {
		if clientApp.OwnerUID != 0 && clientApp.OwnerUID == uid {
			ownedApps = append(ownedApps, clientApp)
		}
	}

	writeResponse(ctx, w, map[string]interface{}{"apps": ownedApps}, http.StatusOK)
}

func (a *App) HandleOwnedClientApplication(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	clientID := params[constants.ResourceKeyClientAppID]

	// Make sure client exists and user owns it
	if clientID == "" {
		writeError(ctx, w, perr("invalid client id format", http.StatusBadRequest))
		return
	}
	var client *types.ClientApplication
	for _, clientApp := range clients.ClientApps {
		if clientApp.OwnerUID == uid && clientApp.ClientId == clientID {
			client = &clientApp
			break
		}
	}
	if client == nil {
		writeError(ctx, w, perr("client not found", http.StatusNotFound))
		return
	}

	// Regenerate client secret
	newSecret := make([]byte, 64)
	for i := range newSecret {
		newSecret[i] = deviceCodeCharset[rand.Intn(len(deviceCodeCharset))]
	}
	// Hash secret before storing
	hashClientSecret, err := hashClientSecret(string(newSecret))
	if err != nil {
		writeError(ctx, w, perr("failed to hash secret", http.StatusInternalServerError))
		return
	}

	err = a.Service.SetClientAppSecret(ctx, clientID, string(hashClientSecret))
	if err != nil {
		writeError(ctx, w, perr("failed to save secret", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"secret": string(newSecret)}, http.StatusOK)
}

func (a *App) HandleProfilePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	pageData, err := a.Service.GetProfilePageData(ctx, uid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/profile.gohtml")
}

func (a *App) HandleSubmitPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/submit.gohtml")
}

func (a *App) HandleFlashfreezeSubmitPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/flashfreeze-submit.gohtml")
}

func (a *App) HandleMinLauncherVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	writeResponse(ctx, w, map[string]interface{}{"min-version": a.Conf.MinLauncherVersion}, http.StatusOK)
}

func (a *App) HandleMetadataStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetMetadataStatsPageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/metadata-stats.gohtml")
}

func (a *App) HandleFetchGames(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	defer r.Body.Close()
	var requestBody types.FetchGamesRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		writeError(ctx, w, perr("failed to decode request body", http.StatusBadRequest))
		return
	}

	if requestBody.GameIDs == nil || len(requestBody.GameIDs) == 0 {
		writeError(ctx, w, perr("no game ids provided", http.StatusBadRequest))
		return
	}

	games, err := a.Service.FetchGames(ctx, requestBody.GameIDs)
	if err != nil {
		writeError(ctx, w, perr("failed to fetch games", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"games": games}, http.StatusOK)
}

func (a *App) HandleDeletedGames(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	modifiedAfterRaw, ok := r.URL.Query()["after"]
	var modifiedAfter string
	if ok {
		modifiedAfter = modifiedAfterRaw[0]
	} else {
		modifiedAfter = "1970-01-01"
	}

	games, err := a.Service.GetDeletedGamePageData(ctx, &modifiedAfter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	res := types.GamesDeletedSinceDateJSON{
		Games: games,
	}
	writeResponse(ctx, w, res, http.StatusOK)
}

func (a *App) HandleGameCountSinceDate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	modifiedAfterRaw, ok := r.URL.Query()["after"]
	var modifiedAfter string
	if ok {
		modifiedAfter = modifiedAfterRaw[0]
	} else {
		modifiedAfter = "1970-01-01"
	}

	result, err := a.Service.GetGameCountSinceDate(ctx, &modifiedAfter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	res := types.GameCountSinceDateJSON{
		Total: result,
	}
	writeResponse(ctx, w, res, http.StatusOK)
}

func (a *App) HandleGamesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	modifiedAfterRaw, ok := r.URL.Query()["after"]
	var modifiedAfter string
	if ok {
		modifiedAfter = modifiedAfterRaw[0]
	} else {
		modifiedAfter = "1970-01-01"
	}

	modifiedBeforeRaw, ok := r.URL.Query()["before"]
	var modifiedBefore string
	if ok {
		modifiedBefore = modifiedBeforeRaw[0]
	} else {
		modifiedBefore = "2999-01-01"
	}

	afterIdRaw, ok := r.URL.Query()["afterId"]
	var afterId string
	if ok {
		afterId = afterIdRaw[0]
	} else {
		afterId = ""
	}

	broadRaw, ok := r.URL.Query()["broad"]
	broad := false
	if ok && broadRaw[0] != "false" {
		broad = true
	}

	games, addApps, gameData, tagRelations, platformRelations, err := a.Service.GetGamesPageData(ctx, &modifiedAfter, &modifiedBefore, broad, &afterId)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	res := types.GamePageResJSON{
		Games:             games,
		AddApps:           addApps,
		GameData:          gameData,
		TagRelations:      tagRelations,
		PlatformRelations: platformRelations,
	}
	writeResponse(ctx, w, res, http.StatusOK)
}

// @Summary All Tags
// @Description Detailed list of all tags
// @Tags Tagged Fields
// @Produce json
// @Success 200 {array} types.Tag
// @Router /api/tags [get]
func (a *App) HandleTagsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	modifiedAfterRaw, ok := r.URL.Query()["after"]
	var modifiedAfter *string
	if ok {
		modifiedAfter = &modifiedAfterRaw[0]
	}

	pageData, err := a.Service.GetTagsPageData(ctx, modifiedAfter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		pageDataJson := types.TagsPageDataJSON{
			Tags:       pageData.Tags,
			Categories: pageData.Categories,
		}
		writeResponse(ctx, w, pageDataJson, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/tags-table.gohtml",
		"templates/tags.gohtml")
}

// @Summary All Platforms
// @Description Detailed list of all platforms
// @Tags Tagged Fields
// @Produce json
// @Success 200 {array} types.Platform
// @Router /api/platforms [get]
func (a *App) HandlePlatformsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	modifiedAfterRaw, ok := r.URL.Query()["after"]
	var modifiedAfter *string
	if ok {
		modifiedAfter = &modifiedAfterRaw[0]
	}

	pageData, err := a.Service.GetPlatformsPageData(ctx, modifiedAfter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData.Platforms, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/platforms-table.gohtml",
		"templates/platforms.gohtml")
}

func (a *App) HandlePostTag(w http.ResponseWriter, r *http.Request) {
	// Lock the database for sequential writes
	utils.MetadataMutex.Lock()
	defer utils.MetadataMutex.Unlock()

	ctx := r.Context()
	params := mux.Vars(r)
	tagIdStr := params[constants.ResourceKeyTagID]
	tagId, err := strconv.Atoi(tagIdStr)
	if err != nil {
		writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
		return
	}

	var tag types.Tag
	err = json.NewDecoder(r.Body).Decode(&tag)
	if err != nil {
		writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
		return
	}
	if tag.ID != int64(tagId) {
		writeResponse(ctx, w, "Tag ID does not match route", http.StatusBadRequest)
		return
	}

	err = a.Service.SaveTag(ctx, &tag)
	if err != nil {
		writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
		return
	}

	pageData, err := a.Service.GetTagPageData(ctx, tagIdStr)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, pageData.Tag, http.StatusOK)
}

// @Summary Tag Info
// @Description Find detailed info for a tag
// @Tags Tagged Fields
// @Param id_or_name path string true "Tag ID or Name"
// @Produce json
// @Success 200 {object} types.Tag
// @Failure 404 {object} constants.PublicError
// @Router /api/tag/{id_or_name} [get]
func (a *App) HandleTagPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	tagID := params[constants.ResourceKeyTagID]

	pageData, err := a.Service.GetTagPageData(ctx, tagID)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if pageData.Tag.Deleted && !constants.IsGodOrColin(pageData.UserRoles, pageData.UserID) {
		// Prevent non-God users viewing deleted resource
		writeResponse(ctx, w, map[string]interface{}{"error": "deleted resource"}, http.StatusNotFound)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData.Tag, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/tag.gohtml")
}

func (a *App) HandleTagEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	tagID := params[constants.ResourceKeyTagID]

	pageData, err := a.Service.GetTagPageData(ctx, tagID)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if pageData.Tag.Deleted && !constants.IsAdder(pageData.UserRoles) {
		// Prevent non-Admins from viewing deleted tags
		writeResponse(ctx, w, map[string]interface{}{"error": "deleted resource"}, http.StatusNotFound)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData.Tag, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/tag-edit.gohtml")
}

// @Summary Game Info
// @Description Find detailed info for a game
// @Tags Game
// @Param id path string true "Game ID"
// @Produce json
// @Success 200 {object} types.Game
// @Failure 404 {object} constants.PublicError
// @Router /api/game/{id} [get]
func (a *App) HandleGamePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	revisionDate := params[constants.ResourceKeyGameRevision]

	// Handle POST changes
	if utils.RequestType(ctx) != constants.RequestWeb && r.Method == "POST" {
		// Lock the database for sequential write
		utils.MetadataMutex.Lock()
		defer utils.MetadataMutex.Unlock()

		var game types.Game
		err := json.NewDecoder(r.Body).Decode(&game)
		if err != nil {
			writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
			return
		}
		err = a.Service.SaveGame(ctx, &game)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeResponse(ctx, w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	pageData, err := a.Service.GetGamePageData(ctx, gameId, a.Conf.ImagesCdn, a.Conf.ImagesCdnCompressed, revisionDate)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if pageData.Game.Deleted && !constants.IsDeleter(pageData.UserRoles) {
		// Prevent non-God users viewing deleted resource
		writeResponse(ctx, w, map[string]interface{}{"error": "deleted resource"}, http.StatusNotFound)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData.Game, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/game.gohtml")
}

func (a *App) HandleGameDataEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	dateStr := params[constants.ResourceKeyGameDataDate]
	date, err := strconv.ParseInt(dateStr, 10, 64)

	pageData, err := a.Service.GetGameDataPageData(ctx, gameId, date)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		// Save posted data
		var gameData types.GameData
		err = json.NewDecoder(r.Body).Decode(&gameData)
		if err != nil {
			utils.LogCtx(ctx).Error(fmt.Sprintf("decode error: %s", err.Error()))
			writeResponse(ctx, w, "Cannot decode body into game data", http.StatusBadRequest)
			return
		}

		// Apply editable fields
		err = a.Service.SaveGameData(ctx, gameId, date, &gameData)
		if err != nil {
			utils.LogCtx(ctx).Error(fmt.Sprintf("save error: %s", err.Error()))
			writeResponse(ctx, w, "Error saving game data", http.StatusInternalServerError)
			return
		}

		writeResponse(ctx, w, "OK", http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/game-data-edit.gohtml")
}

// @Summary Game Data Info
// @Description Find the indexed file list for a given game data entry
// @Tags Game
// @Param id path string true "Game ID"
// @Param date path number true "Game Data Creation Date (Unix Milliseconds)"
// @Produce json
// @Success 200 {object} types.GameDataIndex
// @Failure 404 {object} constants.PublicError
// @Router /api/game/{id}/data/{date}/index [get]
func (a *App) HandleGameDataIndexPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	dateStr := params[constants.ResourceKeyGameDataDate]
	date, err := strconv.ParseInt(dateStr, 10, 64)

	pageData, err := a.Service.GetGameDataIndexPageData(ctx, gameId, date)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData.Index, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/game-data-index.gohtml")
}

func (a *App) HandleDeleteGame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	query := r.URL.Query()
	reason := query.Get("reason")
	destId := query.Get("destId")
	validReasons := constants.GetValidDeleteReasons()

	if !isElementExist(validReasons, reason) {
		writeError(ctx, w,
			perr(fmt.Sprintf("reason query param must be of [%s], got %s", strings.Join(validReasons, ", "), reason), http.StatusBadRequest))
		return
	}

	if reason == "Duplicate" {
		if destId == "" {
			writeError(ctx, w, perr("Redirect ID must be provided when deleting a duplicate game", http.StatusBadRequest))
			return
		} else {
			reason += " - Redirect to " + destId
		}
	}

	err := a.Service.DeleteGame(ctx, gameId, reason, destId, a.Conf.ImagesDir, a.Conf.DataPacksDir, a.Conf.DeletedImagesDir,
		a.Conf.DeletedDataPacksDir, a.Conf.FrozenPacksDir)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("Error deleting game - "+err.Error(), http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"status": "success"}, http.StatusOK)
}

func (a *App) HandleRestoreGame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	query := r.URL.Query()
	reason := query.Get("reason")
	validReasons := constants.GetValidRestoreReasons()

	if !isElementExist(validReasons, reason) {
		writeError(ctx, w,
			perr(fmt.Sprintf("reason query param must be of [%s], got %s", strings.Join(validReasons, ", "), reason), http.StatusBadRequest))
		return
	}

	err := a.Service.RestoreGame(ctx, gameId, reason, a.Conf.ImagesDir, a.Conf.DataPacksDir, a.Conf.DeletedImagesDir,
		a.Conf.DeletedDataPacksDir, a.Conf.FrozenPacksDir)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("Error restoring game "+err.Error(), http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"status": "success"}, http.StatusOK)
}

// @Summary Index Hash Search
// @Description Find matching indexed files with a given hash
// @Tags Game Data Index
// @Param hash path string true "Hash to check"
// @Accept json
// @Produce json
// @Success 200 {object} types.IndexMatchResultData
// @Router /api/index/hash/{hash} [post]
func (a *App) HandleMatchingIndexHash(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	hashStr := params[constants.ResourceKeyHash]

	var hashType string
	if len(hashStr) == 8 {
		hashType = "crc32"
	} else if len(hashStr) == 32 {
		hashType = "md5"
	} else if len(hashStr) == 40 {
		hashType = "sha1"
	} else if len(hashStr) == 64 {
		hashType = "sha256"
	}
	if hashType == "" {
		writeError(ctx, w, perr("not a valid hash", http.StatusBadRequest))
		return
	}

	indexMatches, err := a.Service.GetIndexMatchesHash(ctx, hashType, hashStr)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("error checking index", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, indexMatches, http.StatusOK)
}

// @Summary Index Path Search
// @Description Find matching indexed files with a given path prefix
// @Tags Game Data Index
// @Param request body types.IndexPathRequest true "Request body"
// @Accept json
// @Produce json
// @Success 200 {object} types.IndexMatchPathResult
// @Failure 400 {object} constants.PublicError
// @Router /api/index/path [post]
func (a *App) HandleMatchingIndexPath(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var requestBody types.IndexPathRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		http.Error(w, "Invalid request, must have 'path' json parameter", http.StatusBadRequest)
		return
	}
	path := requestBody.Path
	path = strings.TrimPrefix(path, "content/")

	// Remove https:// and http:// prefixes
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}

	// Generate path variants
	paths := []string{fmt.Sprintf("content/%s", path)} // Start with the original path

	if strings.HasPrefix(path, "www.") {
		// Add the variant without "www."
		paths = append(paths, fmt.Sprintf("content/%s", strings.TrimPrefix(path, "www.")))
	} else {
		// Check if the first segment has only one period (e.g., "google.com")
		firstSegment := strings.SplitN(path, "/", 2)[0] // Get the first segment of the path
		if strings.Count(firstSegment, ".") == 1 {
			// Add the variant with "www."
			wwwVariant := "content/www." + path
			paths = append(paths, wwwVariant)
		}
	}

	// Ensure "www." variant is at the end of the list, if present
	sort.SliceStable(paths, func(i, j int) bool {
		return strings.HasPrefix(paths[j], "www.") && !strings.HasPrefix(paths[i], "www.")
	})

	indexMatches, err := a.Service.GetIndexMatchesPath(ctx, paths)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("error checking index", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, indexMatches, http.StatusOK)
}

func (a *App) HandleGameLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	revisionDate := ""

	game, err := a.Service.GetGamePageData(ctx, gameId, a.Conf.ImagesCdn, a.Conf.ImagesCdnCompressed, revisionDate)
	if err != nil {
		http.Error(w, "Game does not exist", http.StatusNotFound)
		return
	}
	if game.Game.Deleted {
		http.Error(w, "Game is deleted", http.StatusForbidden)
		return
	}

	// Parse the multipart form
	const maxUploadSize = 15 << 20 // 15 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "The uploaded file is too big. Please choose a file that is less than 15MB in size", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read the first 512 bytes to detect content type
	buffer := make([]byte, 512)
	if _, err := file.Read(buffer); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Check the file type
	contentType := http.DetectContentType(buffer)
	if contentType != "image/png" {
		http.Error(w, "The uploaded file is not a PNG image", http.StatusBadRequest)
		return
	}

	// Reset the file read pointer to the beginning
	if _, err := file.Seek(0, 0); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	logoPath := fmt.Sprintf("%s/Logos/%s/%s/%s.png", a.Conf.ImagesDir, gameId[:2], gameId[2:4], gameId)
	if err := os.MkdirAll(filepath.Dir(logoPath), os.ModePerm); err != nil {
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	// Create a new file
	dst, err := os.Create(logoPath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Reset the file read pointer to the beginning of the uploaded file
	if _, err := file.Seek(0, 0); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Copy the contents of the uploaded file to the new file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	if err := a.Service.EmitGameLogoUpdateEvent(ctx, uid, gameId); err != nil {
		utils.LogCtx(ctx).Error(err)
	}

	url := fmt.Sprintf("%s/Logos/%s/%s/%s.png",
		a.Conf.ImagesCdn, gameId[:2], gameId[2:4], gameId)
	// Clear the image microservice cached file
	client := &http.Client{}
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		http.Error(w, "Updated file, but failed to clear image cache", http.StatusInternalServerError)
	}
	req.Header.Set("Authorization", "Bearer "+a.Conf.ImagesCdnApiKey)
	_, err = client.Do(req)
	if err != nil {
		http.Error(w, "Updated file, but failed to clear image cache", http.StatusInternalServerError)
	}

	// File saved successfully
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File uploaded and saved successfully"))
}

func (a *App) HandleGameScreenshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]
	revisionDate := ""

	game, err := a.Service.GetGamePageData(ctx, gameId, a.Conf.ImagesCdn, a.Conf.ImagesCdnCompressed, revisionDate)
	if err != nil {
		http.Error(w, "Game does not exist", http.StatusNotFound)
		return
	}
	if game.Game.Deleted {
		http.Error(w, "Game is deleted", http.StatusForbidden)
		return
	}

	// Parse the multipart form
	const maxUploadSize = 15 << 20 // 15 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "The uploaded file is too big. Please choose a file that is less than 15MB in size", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read the first 512 bytes to detect content type
	buffer := make([]byte, 512)
	if _, err := file.Read(buffer); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Check the file type
	contentType := http.DetectContentType(buffer)
	if contentType != "image/png" {
		http.Error(w, "The uploaded file is not a PNG image", http.StatusBadRequest)
		return
	}

	// Reset the file read pointer to the beginning
	if _, err := file.Seek(0, 0); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	screenshotPath := fmt.Sprintf("%s/Screenshots/%s/%s/%s.png", a.Conf.ImagesDir, gameId[:2], gameId[2:4], gameId)
	if err := os.MkdirAll(filepath.Dir(screenshotPath), os.ModePerm); err != nil {
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	// Create a new file
	dst, err := os.Create(screenshotPath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Reset the file read pointer to the beginning of the uploaded file
	if _, err := file.Seek(0, 0); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Copy the contents of the uploaded file to the new file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	if err := a.Service.EmitGameScreenshotUpdateEvent(ctx, uid, gameId); err != nil {
		utils.LogCtx(ctx).Error(err)
	}

	url := fmt.Sprintf("%s/Screenshots/%s/%s/%s.png",
		a.Conf.ImagesCdn, gameId[:2], gameId[2:4], gameId)
	// Clear the image microservice cached file
	client := &http.Client{}
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		http.Error(w, "Updated file, but failed to clear image cache", http.StatusInternalServerError)
	}
	req.Header.Set("Authorization", "Bearer "+a.Conf.ImagesCdnApiKey)
	_, err = client.Do(req)
	if err != nil {
		http.Error(w, "Updated file, but failed to clear image cache", http.StatusInternalServerError)
	}

	// File saved successfully
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File uploaded and saved successfully"))

}

func (a *App) HandleFreezeGame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]

	dataPacksPath := a.Conf.DataPacksDir
	frozenPacksPath := a.Conf.FrozenPacksDir
	deletedPacksPath := a.Conf.DeletedDataPacksDir

	err := a.Service.FreezeGame(ctx, gameId, uid, dataPacksPath, frozenPacksPath, deletedPacksPath)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, gameId, http.StatusOK)
}

func (a *App) HandleUnfreezeGame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	gameId := params[constants.ResourceKeyGameID]

	dataPacksPath := a.Conf.DataPacksDir
	frozenPacksPath := a.Conf.FrozenPacksDir
	deletedPacksPath := a.Conf.DeletedDataPacksDir

	err := a.Service.UnfreezeGame(ctx, gameId, uid, dataPacksPath, frozenPacksPath, deletedPacksPath)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, gameId, http.StatusOK)
}

func (a *App) HandleGameRedirects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetGameRedirectsPageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) == constants.RequestJSON {
		if r.Method == http.MethodGet {
			writeResponse(ctx, w, pageData.GameRedirects, http.StatusOK)
			return
		} else {
			// POST
			var requestBody types.AddGameRedirectRequest
			err = json.NewDecoder(r.Body).Decode(&requestBody)
			if err != nil {
				writeResponse(ctx, w, err, http.StatusBadRequest)
				return
			}
			err := a.Service.AddNewGameRedirect(ctx, requestBody.SourceId, requestBody.DestId)
			if err != nil {
				writeError(ctx, w, err)
				return
			}
			writeResponse(ctx, w, "success", http.StatusOK)
			return
		}
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/game-redirects.gohtml")
}

func (a *App) HandleSubmissionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter := &types.SubmissionsFilter{}

	if err := a.decoder.Decode(filter, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	if err := filter.Validate(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
		return
	}

	pageData, err := a.Service.GetSubmissionsPageData(ctx, filter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	pageData.FilterLayout = r.FormValue("filter-layout")

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/submissions.gohtml",
		"templates/submission-filter.gohtml",
		"templates/submission-table.gohtml",
		"templates/submission-pagenav.gohtml",
		"templates/submission-filter-chunks.gohtml",
		"templates/comment-form.gohtml")
}

func (a *App) HandleMySubmissionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	filter := &types.SubmissionsFilter{}

	if err := a.decoder.Decode(filter, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	if err := filter.Validate(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}

	filter.SubmitterID = &uid

	pageData, err := a.Service.GetSubmissionsPageData(ctx, filter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	pageData.FilterLayout = r.FormValue("filter-layout")

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/my-submissions.gohtml",
		"templates/submission-filter.gohtml",
		"templates/submission-table.gohtml",
		"templates/submission-pagenav.gohtml",
		"templates/submission-filter-chunks.gohtml",
		"templates/comment-form.gohtml")
}

func (a *App) HandleApplyContentPatchPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	pageData, err := a.Service.GetApplyContentPatchPageData(ctx, sid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/submission-content-patch-apply.gohtml")
}

func (a *App) HandleApplySubmissionMetaEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	if utils.RequestType(ctx) == constants.RequestWeb && r.Method == "GET" {
		pageData, err := a.Service.GetEditSubmissionPageData(ctx, sid)
		if err != nil {
			writeError(ctx, w, err)
			return
		}

		a.RenderTemplates(ctx, w, r, pageData,
			"templates/edit-submission-meta.gohtml")
		return
	}

	if utils.RequestType(ctx) == constants.RequestJSON && r.Method == "POST" {
		err = r.ParseMultipartForm(52428800) // 50mb limit
		if err != nil {
			writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
			return
		}

		var logoPoint *multipart.File
		var screenshotPoint *multipart.File

		raw_meta := r.FormValue("metadata")
		logo, logoHeader, err := r.FormFile("logo")
		if err != nil && err != http.ErrMissingFile {
			writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
			return
		} else if err == nil {
			defer logo.Close()
			if !strings.HasSuffix(strings.ToLower(logoHeader.Filename), ".png") {
				writeError(ctx, w, perr("Invalid logo file type", http.StatusBadRequest))
				return
			}
			logoPoint = &logo
		}

		screenshot, screenshotHeader, err := r.FormFile("screenshot")
		if err != nil && err != http.ErrMissingFile {
			writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
			return
		} else if err == nil {
			defer screenshot.Close()
			if !strings.HasSuffix(strings.ToLower(screenshotHeader.Filename), ".png") {
				writeError(ctx, w, perr("Invalid screenshot file type", http.StatusBadRequest))
				return
			}
			screenshotPoint = &screenshot
		}

		var editCurationMeta *types.EditCurationMeta
		if raw_meta != "" {
			utils.LogCtx(ctx).Debug(raw_meta)
			editCurationMeta = &types.EditCurationMeta{}
			err = json.Unmarshal([]byte(raw_meta), editCurationMeta)
			if err != nil {
				writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		err = a.Service.ApplySubmissionMetaEdit(ctx, sid, editCurationMeta, logoPoint, screenshotPoint)
		if err != nil {
			writeResponse(ctx, w, err.Error(), http.StatusBadRequest)
			return
		}

		writeResponse(ctx, w, presp("Success", 200), http.StatusOK)
		return
	}
}

func (a *App) HandleViewSubmissionPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	pageData, err := a.Service.GetViewSubmissionPageData(ctx, uid, sid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/submission.gohtml",
		"templates/submission-table.gohtml",
		"templates/comment-form.gohtml",
		"templates/view-submission-nav.gohtml")
}

func (a *App) HandleViewSubmissionFilesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	pageData, err := a.Service.GetSubmissionsFilesPageData(ctx, sid)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/submission-files.gohtml", "templates/submission-files-table.gohtml")
}

func (a *App) HandleUpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	notificationSettings := &types.UpdateNotificationSettings{}

	if err := a.decoder.Decode(notificationSettings, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	if err := a.Service.UpdateNotificationSettings(ctx, uid, notificationSettings.NotificationActions); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp("success", http.StatusOK), http.StatusOK)
}

func (a *App) HandleUpdateSubscriptionSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	subscriptionSettings := &types.UpdateSubscriptionSettings{}

	if err := a.decoder.Decode(subscriptionSettings, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	if err := a.Service.UpdateSubscriptionSettings(ctx, uid, sid, subscriptionSettings.Subscribe); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp("success", http.StatusOK), http.StatusOK)
}

func (a *App) HandleInternalPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/internal.gohtml")
}

//var updateMasterDBGuard = make(chan struct{}, 1)
//
//func (a *App) HandleUpdateMasterDB(w http.ResponseWriter, r *http.Request) {
//	ctx := r.Context()
//
//	select {
//	case updateMasterDBGuard <- struct{}{}:
//		utils.LogCtx(ctx).Debug("starting update master db")
//	default:
//		writeResponse(ctx, w, presp("update master db already running", http.StatusForbidden), http.StatusForbidden)
//		return
//	}
//
//	go func() {
//		err := a.Service.UpdateMasterDB(context.WithValue(context.Background(), utils.CtxKeys.Log, utils.LogCtx(ctx)))
//		if err != nil {
//			utils.LogCtx(ctx).Error(err)
//		}
//		<-updateMasterDBGuard
//	}()
//
//	writeResponse(ctx, w, presp("starting update master db", http.StatusOK), http.StatusOK)
//}

func (a *App) HandleHelpPage(w http.ResponseWriter, r *http.Request) {
	// TODO all auth-free pages should use a middleware to remove all of this user ID handling from the handlers
	ctx := r.Context()
	uid, err := a.GetUserIDFromCookie(r)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), utils.CtxKeys.UserID, uid))
	ctx = r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/help.gohtml")
}

func (a *App) parseResumableRequest(ctx context.Context, r *http.Request) ([]byte, *types.ResumableParams, error) {
	// parse resumable params
	resumableParams := &types.ResumableParams{}

	if err := a.decoder.Decode(resumableParams, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, nil, perr("failed to decode resumable query params", http.StatusBadRequest)
	}

	// get chunk data
	if err := r.ParseMultipartForm(64 * 1000 * 1000); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, nil, perr("failed to parse form", http.StatusUnprocessableEntity)
	}

	fileHeaders := r.MultipartForm.File["file"]

	if len(fileHeaders) == 0 {
		err := fmt.Errorf("no files received")
		utils.LogCtx(ctx).Error(err)
		return nil, nil, perr(err.Error(), http.StatusBadRequest)
	}

	file, err := fileHeaders[0].Open()
	if err != nil {
		return nil, nil, perr("failed to open received file", http.StatusInternalServerError)
	}
	defer file.Close()

	utils.LogCtx(ctx).Debug("reading received chunk")

	chunk := make([]byte, resumableParams.ResumableCurrentChunkSize)
	n, err := file.Read(chunk)
	if err != nil || int64(n) != resumableParams.ResumableCurrentChunkSize {
		return nil, nil, perr("failed to read received file", http.StatusUnprocessableEntity)
	}

	return chunk, resumableParams, nil
}

func (a *App) HandleFlashfreezeReceiverResumable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chunk, resumableParams, err := a.parseResumableRequest(ctx, r)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	// then a magic happens
	fid, err := a.Service.ReceiveFlashfreezeChunk(ctx, resumableParams, chunk)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	var url *string
	if fid != nil {
		x := fmt.Sprintf("/flashfreeze/files?file-id=%d", *fid)
		url = &x
	}

	resp := types.ReceiveFileResp{
		Message: "success",
		URL:     url,
	}
	writeResponse(ctx, w, resp, http.StatusOK)
}

func (a *App) HandleSearchFlashfreezePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter := &types.FlashfreezeFilter{}

	if err := a.decoder.Decode(filter, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	if err := filter.Validate(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
		return
	}

	pageData, err := a.Service.GetSearchFlashfreezeData(ctx, filter)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/flashfreeze-files.gohtml",
		"templates/flashfreeze-table.gohtml",
		"templates/flashfreeze-filter.gohtml",
		"templates/flashfreeze-pagenav.gohtml")
}

var ingestGuard = make(chan struct{}, 1)

func (a *App) HandleIngestFlashfreeze(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	select {
	case ingestGuard <- struct{}{}:
		utils.LogCtx(ctx).Debug("starting flashfreeze ingestion")
	default:
		writeResponse(ctx, w, presp("ingestion already running", http.StatusForbidden), http.StatusForbidden)
		return
	}

	go func() {
		a.Service.IngestFlashfreezeItems(utils.LogCtx(context.WithValue(context.Background(), utils.CtxKeys.Log, utils.LogCtx(ctx))))
		<-ingestGuard
	}()

	writeResponse(ctx, w, presp("starting flashfreeze ingestion", http.StatusOK), http.StatusOK)
}

var recomputeSubmissionCacheAllGuard = make(chan struct{}, 1)

func (a *App) HandleRecomputeSubmissionCacheAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	select {
	case recomputeSubmissionCacheAllGuard <- struct{}{}:
		utils.LogCtx(ctx).Debug("starting recompute submission cache all")
	default:
		writeResponse(ctx, w, presp("recompute submission cache all already running", http.StatusForbidden), http.StatusForbidden)
		return
	}

	go func() {
		a.Service.RecomputeSubmissionCacheAll(context.WithValue(context.Background(), utils.CtxKeys.Log, utils.LogCtx(ctx)))
		<-recomputeSubmissionCacheAllGuard
	}()

	writeResponse(ctx, w, presp("starting recompute submission cache all", http.StatusOK), http.StatusOK)
}

var ingestUnknownGuard = make(chan struct{}, 1)

// HandleIngestUnknownFlashfreeze ingests flashfreeze files which are in the flashfreeze directory, but not in the database.
// This should not be needed and such files are a result of a bug or human error.
func (a *App) HandleIngestUnknownFlashfreeze(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	select {
	case ingestUnknownGuard <- struct{}{}:
		utils.LogCtx(ctx).Debug("starting flashfreeze ingestion of unknown files")
	default:
		writeResponse(ctx, w, presp("ingestion already running", http.StatusForbidden), http.StatusForbidden)
		return
	}

	go func() {
		a.Service.IngestUnknownFlashfreezeItems(utils.LogCtx(context.WithValue(context.Background(), utils.CtxKeys.Log, utils.LogCtx(ctx))))
		<-ingestUnknownGuard
	}()

	writeResponse(ctx, w, presp("starting flashfreeze ingestion of unknown files", http.StatusOK), http.StatusOK)
}

var indexUnindexedGuard = make(chan struct{}, 1)

func (a *App) HandleIndexUnindexedFlashfreeze(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	select {
	case indexUnindexedGuard <- struct{}{}:
		utils.LogCtx(ctx).Debug("starting flashfreeze indexing of unindexed files")
	default:
		writeResponse(ctx, w, presp("indexing already running", http.StatusForbidden), http.StatusForbidden)
		return
	}

	go func() {
		a.Service.IndexUnindexedFlashfreezeItems(utils.LogCtx(context.WithValue(context.Background(), utils.CtxKeys.Log, utils.LogCtx(ctx))))
		<-indexUnindexedGuard
	}()

	writeResponse(ctx, w, presp("starting flashfreeze indexing of unindexed files", http.StatusOK), http.StatusOK)
}

func (a *App) HandleDeleteUserSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
		return
	}

	req := &types.DeleteUserSessionsRequest{}

	if err := a.decoder.Decode(req, r.PostForm); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	count, err := a.Service.DeleteUserSessions(ctx, req.DiscordID)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp(fmt.Sprintf("deleted %d sessions", count), http.StatusOK), http.StatusOK)
}

func (a *App) HandleStatisticsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	f := func() (interface{}, error) {
		pageData, err := a.Service.GetStatisticsPageData(ctx)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, err)
			return nil, err
		}
		return pageData, nil
	}

	const key = "GetStatisticsPageData"

	pageDataI, err, cached := pageDataCache.Memoize(key, f)
	if err != nil {
		writeError(ctx, w, err)
		pageDataCache.Storage.Delete(key)
		return
	}

	pageData := pageDataI.(*types.StatisticsPageData)

	utils.LogCtx(ctx).WithField("cached", utils.BoolToString(cached)).Debug("getting statistics page data")

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/statistics.gohtml")
}

func (a *App) HandleUserStatisticsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	if utils.RequestType(ctx) != constants.RequestWeb {
		writeResponse(ctx, w, pageData, http.StatusOK)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/user-statistics.gohtml")
}

func (a *App) HandleSendRemindersAboutRequestedChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	count, err := a.Service.ProduceRemindersAboutRequestedChanges(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp(fmt.Sprintf("%d notifications added to the queue", count), http.StatusOK), http.StatusOK)
}

func (a *App) HandleGetUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	users, err := a.Service.GetUsers(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}

	data := struct {
		Users []*types.User `json:"users"`
	}{
		users,
	}

	writeResponse(ctx, w, data, http.StatusOK)
}

func (a *App) HandleGetUserStatistics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := mux.Vars(r)
	userID := params[constants.ResourceKeyUserID]

	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	f := func() (interface{}, error) {
		us, err := a.Service.GetUserStatistics(ctx, uid)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, err)
			return nil, err
		}
		return us, nil
	}

	key := fmt.Sprintf("GetUserStatistics-%d", uid)

	usI, err, cached := pageDataCache.Memoize(key, f)
	if err != nil {
		writeError(ctx, w, err)
		pageDataCache.Storage.Delete(key)
		return
	}

	us := usI.(*types.UserStatistics)

	utils.LogCtx(ctx).WithField("cached", utils.BoolToString(cached)).WithField("uid", uid).Debug("getting user statistics")

	writeResponse(ctx, w, us, http.StatusOK)
}

func (a *App) HandleUserBan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := mux.Vars(r)
	userIdStr := params[constants.ResourceKeyUserID]
	userId, err := strconv.Atoi(userIdStr)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	rows, err := a.Service.UserBan(ctx, int64(userId))
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, map[string]interface{}{"rows removed": rows}, http.StatusOK)
}

func (a *App) HandleGetUploadProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := mux.Vars(r)
	tempName := params[constants.ResourceKeyTempName]

	data := struct {
		Status *types.SubmissionStatus `json:"status"`
	}{
		a.Service.SSK.Get(tempName),
	}

	writeResponse(ctx, w, data, http.StatusOK)
}

func (a *App) HandleDeveloperPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/developer.gohtml")
}

func (a *App) HandleDeveloperTagDescFromValidator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.Service.DeveloperTagDescFromValidator(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to populate tags from validator", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleDeveloperDumpUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fmt.Println("handling dev dump")

	// get file from request body
	file, _, err := r.FormFile("file")
	if err != nil {
		fmt.Println("handling dev BAD")
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to get file from request", http.StatusBadRequest))
		return
	}
	defer file.Close()

	fmt.Println("got file")

	// decode JSON file
	var jsonData types.LauncherDump
	err = json.NewDecoder(file).Decode(&jsonData)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode JSON file", http.StatusBadRequest))
		return
	}

	fmt.Println("decoded")

	err = a.Service.DeveloperImportDatabaseJson(ctx, &jsonData)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to import", http.StatusInternalServerError))
		return
	}

	fmt.Println("dumped")

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func isElementExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

func (a *App) HandleFreezeSubmission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	if err := a.Service.FreezeSubmission(ctx, sid); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleUnfreezeSubmission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params[constants.ResourceKeySubmissionID]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid submission id", http.StatusBadRequest))
		return
	}

	if err := a.Service.UnfreezeSubmission(ctx, sid); err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, nil, http.StatusNoContent)
}

func (a *App) HandleNukeSessionTable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.Service.NukeSessionTable(ctx)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	writeResponse(ctx, w, presp("nuked the session table", http.StatusOK), http.StatusOK)
}

func (a *App) HandleStat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stat := a.Service.Stat()

	writeResponse(ctx, w, map[string]interface{}{"postgres": stat}, http.StatusOK)
}

func (a *App) HandleGetActivityEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter := &types.ActivityEventsFilter{}

	if err := a.decoder.Decode(filter, r.URL.Query()); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to decode query params", http.StatusInternalServerError))
		return
	}

	events, err := a.Service.GetActivityEvents(ctx, filter)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}

	data := struct {
		Events []*activityevents.ActivityEvent `json:"events"`
	}{
		events,
	}

	writeResponse(ctx, w, data, http.StatusOK)
}

func (a *App) HandleUserActivityPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/user-activity.gohtml")
}

// HandleRecommendationEngine recreates the POST request with the original body and sends back the response
func (a *App) HandleRecommendationEngine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	recommendationOperation := params[constants.ResourceKeyRecommendationOp] // similar-games or user-recommendations

	originalBody, err := io.ReadAll(r.Body)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}
	r.Body.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/%s", a.Conf.RecommendationEngineURL, recommendationOperation), bytes.NewBuffer(originalBody))
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}
	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	w.Write(result)
}

func (a *App) HandleRecommendationPlaygroundPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.UnsetCookie(w, utils.Cookies.Login)
		http.Redirect(w, r, "/web", http.StatusFound)
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/recommendation-playground.gohtml")
}
