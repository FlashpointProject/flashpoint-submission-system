package transport

import (
	"fmt"
	"net/http"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	_ "github.com/FlashpointProject/flashpoint-submission-system/docs"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	httpSwagger "github.com/swaggo/http-swagger"
)

func (a *App) handleRequests(l *logrus.Entry, srv *http.Server, router *mux.Router) {
	isStaff := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.StaffRoles())
	}
	isTrialEditor := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.TrialEditorRoles())
	}
	isTrialCurator := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.TrialCuratorRoles())
	}
	isDeleter := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.DeleterRoles())
	}
	isFreezer := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.FreezerRoles())
	}
	isInAudit := func(r *http.Request, uid int64) (bool, error) {
		s, err := a.UserHasAnyRole(r, uid, constants.StaffRoles())
		if err != nil {
			return false, err
		}
		t, err := a.UserHasAnyRole(r, uid, constants.TrialCuratorRoles())
		if err != nil {
			return false, err
		}
		return !(s || t), nil
	}
	isColin := func(r *http.Request, uid int64) (bool, error) {
		return uid == 689080719460663414, nil
	}
	isGod := func(r *http.Request, uid int64) (bool, error) {
		s, err := isColin(r, uid)
		if err != nil || s {
			return s, err
		} else {
			return a.UserHasAnyRole(r, uid, constants.GodRoles())
		}
	}
	userOwnsSubmission := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeySubmissionID)
	}
	userOwnsAllSubmissions := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeySubmissionIDs)
	}
	userHasNoSubmissions := func(r *http.Request, uid int64) (bool, error) {
		return a.IsUserWithinResourceLimit(r, uid, constants.ResourceKeySubmissionID, 1)
	}
	isSubmissionFrozen := func(r *http.Request, uid int64) (bool, error) {
		return a.IsResourceFrozen(r, constants.ResourceKeySubmissionID)
	}
	isAnySubmissionFrozen := func(r *http.Request, uid int64) (bool, error) {
		return a.IsResourceFrozen(r, constants.ResourceKeySubmissionIDs)
	}
	isAnySubmissionFileFrozen := func(r *http.Request, uid int64) (bool, error) {
		return a.IsResourceFrozen(r, constants.ResourceKeyFileIDs)
	}

	// SLIM SERVICES

	// static file server
	router.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", NoCache(http.FileServer(http.Dir("./static/")))))

	router.HandleFunc("/swagger/docs.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./docs/swagger.json")
	})

	swaggerHandler := httpSwagger.Handler(
		httpSwagger.URL("/swagger/docs.json"),
	)
	router.PathPrefix("/swagger/").Handler(swaggerHandler)

	// auth
	router.Handle(
		"/auth",
		http.HandlerFunc(a.RequestWeb(a.HandleDiscordAuth, false))).
		Methods("GET")
	router.Handle(
		"/auth/callback",
		http.HandlerFunc(a.RequestWeb(a.HandleDiscordCallback, false))).
		Methods("GET")
	router.Handle(
		"/api/logout",
		http.HandlerFunc(a.RequestJSON(a.HandleLogout, false))).
		Methods("GET")

	// authorization code grant
	router.Handle(
		"/auth/authorize",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.HandleOauthAuthorize), false))).
		Methods("GET", "POST")

	// device authorization grant
	router.Handle(
		"/auth/token",
		http.HandlerFunc(a.RequestJSON(a.HandleOauthToken, false))).
		Methods("POST")
	router.Handle(
		"/auth/device",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleOauthDevice, types.AuthScopeAll),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")
	router.Handle(
		"/auth/device",
		http.HandlerFunc(a.RequestJSON(a.HandleOauthDevice, false))).
		Methods("POST")
	router.Handle(
		"/auth/device/respond",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.HandleOauthDeviceResponse), false))).
		Methods("POST")

	router.Handle(
		fmt.Sprintf("/api/server-user/{%s}", constants.ResourceKeyUserID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(a.GetServerUser), false))).
		Methods("GET")

	router.Handle(
		"/.well-known/openid-configuration",
		http.HandlerFunc(a.RequestJSON(a.GetOpenIdConfiguration, false))).
		Methods("GET")

	// pages
	if !a.Conf.FlashpointSourceOnlyMode {
		router.Handle(
			"/",
			http.HandlerFunc(a.RequestWeb(a.HandleRootPage, false))).
			Methods("GET")
	} else {
		router.Handle(
			"/",
			http.HandlerFunc(a.RequestWeb(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("Flashpoint Submission System Source Only Mode"))
				w.WriteHeader(http.StatusOK)
			}, true))).Methods("GET")
	}

	router.Handle(
		"/web",
		http.HandlerFunc(a.RequestWeb(a.HandleRootPage, false))).
		Methods("GET")

	router.Handle(
		"/web/submit",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.RequestScope(a.HandleSubmitPage, types.AuthScopeSubmissionUpload),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	router.Handle(
		"/web/help",
		http.HandlerFunc(a.RequestWeb(a.HandleHelpPage, false))).
		Methods("GET")

	router.Handle(
		"/web/flashfreeze/submit",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.RequestScope(a.HandleFlashfreezeSubmitPage, types.AuthScopeFlashfreezeUpload),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	////////////////////////

	f := a.UserAuthMux(a.RequestScope(a.HandleProfilePage, types.AuthScopeIdentity))

	router.Handle(
		"/web/profile",
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		"/api/profile",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(a.RequestScope(a.HandleSessionsPage, types.AuthScopeAll))

	router.Handle(
		"/api/profile/sessions",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(a.RequestScope(a.HandleSessionPage, types.AuthScopeAll))

	router.Handle(
		fmt.Sprintf("/api/profile/session/{%s}", constants.ResourceKeySessionID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("DELETE")

	f = a.UserAuthMux(a.RequestScope(a.HandleOwnedClientApplications, types.AuthScopeProfileAppsRead))

	router.Handle(
		"/api/profile/apps",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(a.RequestScope(a.HandleOwnedClientApplication, types.AuthScopeAll))

	router.Handle(
		fmt.Sprintf("/api/profile/app/{%s}/generate-secret", constants.ResourceKeyClientAppID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleSubmissionsPage, types.AuthScopeSubmissionRead),
		muxAny(isStaff, isTrialCurator, isInAudit))

	router.Handle(
		"/web/submissions",
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		"/api/submissions",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	/////////////////////////

	f = a.HandleTagsPage

	router.Handle(
		"/web/tags",
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	router.Handle(
		"/api/tags",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandlePostTag, types.AuthScopeTagEdit),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/api/tag/{%s}", constants.ResourceKeyTagID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.HandleTagPage, muxAny(isStaff, isTrialCurator, isInAudit))

	router.Handle(
		fmt.Sprintf("/web/tag/{%s}", constants.ResourceKeyTagID),
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/tag/{%s}", constants.ResourceKeyTagID),
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleTagEditPage, types.AuthScopeTagEdit),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/web/tag/{%s}/edit", constants.ResourceKeyTagID),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	////////////////////////

	f = a.HandleMetadataStats

	router.Handle(
		"/web/metadata-stats",
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	////////////////////////

	f = a.HandleMinLauncherVersion

	router.Handle(
		"/api/min-launcher",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	////////////////////////

	f = a.HandleGameCountSinceDate

	router.Handle(
		"/api/games/updates",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.HandleGamesPage

	router.Handle(
		"/api/games",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.HandleDeletedGames

	router.Handle(
		"/api/games/deleted",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.HandleFetchGames

	router.Handle(
		"/api/games/fetch",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("POST")

	////////////////////////

	f = a.HandlePlatformsPage

	router.Handle(
		"/web/platforms",
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	router.Handle(
		"/api/platforms",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGamePage, types.AuthScopeGameRead),
		muxAny(isStaff, isTrialEditor))

	router.Handle(
		fmt.Sprintf("/web/game/{%s}", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/web/game/{%s}/revision/{%s}", constants.ResourceKeyGameID, constants.ResourceKeyGameRevision),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/game/{%s}", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGamePage, types.AuthScopeGameEdit),
		muxAny(isStaff, isTrialEditor))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGameDataIndexPage, types.AuthScopeGameDataRead),
		muxAny(isTrialCurator, isStaff))

	router.Handle(
		fmt.Sprintf("/web/game/{%s}/data/{%s}/index", constants.ResourceKeyGameID, constants.ResourceKeyGameDataDate),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/data/{%s}/index", constants.ResourceKeyGameID, constants.ResourceKeyGameDataDate),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGameDataEditPage, types.AuthScopeGameDataEdit),
		muxAny(isStaff))

	router.Handle(
		fmt.Sprintf("/web/game/{%s}/data/{%s}/edit", constants.ResourceKeyGameID, constants.ResourceKeyGameDataDate),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/web/game/{%s}/data/{%s}/edit", constants.ResourceKeyGameID, constants.ResourceKeyGameDataDate),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleDeleteGame, types.AuthScopeAll),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("DELETE")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleRestoreGame, types.AuthScopeAll),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/restore", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGameLogo, types.AuthScopeGameEdit),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/logo", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGameScreenshot, types.AuthScopeGameDataEdit),
		muxAny(isDeleter))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/screenshot", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleFreezeGame, types.AuthScopeAll),
		muxAny(isFreezer))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/freeze", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleUnfreezeGame, types.AuthScopeAll),
		muxAny(isFreezer))

	router.Handle(
		fmt.Sprintf("/api/game/{%s}/unfreeze", constants.ResourceKeyGameID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	////////////////////////

	f = a.OpenMux(a.HandleGameRedirects)

	router.Handle(
		"/web/game-redirects",
		http.HandlerFunc(a.RequestWeb(f, true))).
		Methods("GET")

	router.Handle(
		"/api/game-redirects",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleGameRedirects, types.AuthScopeRedirectEdit),
		muxAny(isFreezer))

	router.Handle(
		"/api/game-redirects",
		http.HandlerFunc(a.RequestJSON(f, true))).
		Methods("POST")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleMatchingIndexHash, types.AuthScopeIndexRead),
		muxAny(isStaff, isTrialCurator))

	router.Handle(
		fmt.Sprintf("/api/index/hash/{%s}", constants.ResourceKeyHash),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleMatchingIndexPath, types.AuthScopeIndexRead),
		muxAny(isStaff, isTrialCurator))

	router.Handle(
		"/api/index/path",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleMySubmissionsPage, types.AuthScopeSubmissionRead),
		muxAny(isStaff, isTrialCurator, isInAudit))

	router.Handle(
		"/web/my-submissions",
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		"/api/my-submissions",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleViewSubmissionPage, types.AuthScopeSubmissionRead),
		muxAny(isStaff, isTrialCurator, isInAudit))

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleApplyContentPatchPage, types.AuthScopeAll),
		isDeleter)

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}/apply", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	f = a.UserAuthMux(
		a.RequestScope(a.HandleApplySubmissionMetaEditPage, types.AuthScopeSubmissionEdit),
		muxAny(isStaff,
			muxAll(isTrialCurator, userOwnsSubmission),
			muxAll(isInAudit, userOwnsSubmission)))

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}/metaedit", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/metaedit", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("POST")

	////////////////////////

	f = a.UserAuthMux(
		a.RequestScope(a.HandleViewSubmissionFilesPage, types.AuthScopeSubmissionReadFiles),
		muxAny(isStaff, isTrialCurator, isInAudit))

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}/files", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/files", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	////////////////////////

	//f = a.UserAuthMux(
	//	a.RequestScope(a.HandleSearchFlashfreezePage, types.AuthScopeFlashfreezeReadFiles),
	//	muxAny(isStaff, isTrialCurator, isInAudit))
	//
	//router.Handle(
	//	"/web/flashfreeze/files",
	//	http.HandlerFunc(a.RequestWeb(f, false))).
	//	Methods("GET")
	//
	//router.Handle(
	//	"/api/flashfreeze/files",
	//	http.HandlerFunc(a.RequestJSON(f, false))).
	//	Methods("GET")

	////////////////////////

	f = a.UserAuthMux(a.HandleStatisticsPage)

	router.Handle(
		"/web/statistics",
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		"/api/statistics",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	////////////////////////

	f = a.UserAuthMux(a.HandleUserStatisticsPage)

	router.Handle(
		"/web/user-statistics",
		http.HandlerFunc(a.RequestWeb(f, false))).
		Methods("GET")

	router.Handle(
		"/api/user-statistics",
		http.HandlerFunc(a.RequestJSON(f, false))).
		Methods("GET")

	////////////////////////

	// receivers

	////////////////////////

	router.Handle(
		"/api/submission-receiver-resumable",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleSubmissionReceiverResumable, types.AuthScopeSubmissionUpload),
			muxAny(
				isStaff,
				isTrialCurator,
				muxAll(isInAudit, userHasNoSubmissions))), false))).
		Methods("POST")

	router.Handle(
		fmt.Sprintf("/api/submission-receiver-resumable/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleSubmissionReceiverResumable, types.AuthScopeSubmissionUpload),
			muxAny(
				isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission))), false))).
		Methods("POST")

	router.Handle(
		"/api/submission-receiver-resumable",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleReceiverResumableTestChunk, types.AuthScopeSubmissionUpload),
			muxAny(
				isStaff,
				isTrialCurator,
				muxAll(isInAudit, userHasNoSubmissions))), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/submission-receiver-resumable/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleReceiverResumableTestChunk, types.AuthScopeSubmissionUpload),
			muxAny(
				isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission))), false))).
		Methods("GET")

	////////////////////////

	// flashfreeze disabled for now

	//router.Handle(
	//	"/api/flashfreeze-receiver-resumable",
	//	http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
	//		a.HandleFlashfreezeReceiverResumable,
	//		muxAny(isStaff, isTrialCurator, isInAudit))))).
	//	Methods("POST")
	//
	//router.Handle(
	//	"/api/flashfreeze-receiver-resumable",
	//	http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
	//		a.HandleReceiverResumableTestChunk,
	//		muxAny(isStaff, isTrialCurator, isInAudit))))).
	//	Methods("GET")

	////////////////////////

	router.Handle(
		fmt.Sprintf("/api/submission-batch/{%s}/comment", constants.ResourceKeySubmissionIDs),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleCommentReceiverBatch, types.AuthScopeAll),
			muxAll(
				muxAny(
					muxAll(muxNot(isAnySubmissionFrozen), isStaff, a.UserCanCommentAction),
					muxAll(muxNot(isAnySubmissionFrozen), isTrialCurator, userOwnsAllSubmissions, a.UserCanCommentAction),
					muxAll(muxNot(isAnySubmissionFrozen), isInAudit, userOwnsAllSubmissions, a.UserCanCommentAction),
					muxAll(isAnySubmissionFrozen, isFreezer, a.UserCanCommentAction),
				))), false))).
		Methods("POST")

	router.Handle("/api/notification-settings",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleUpdateNotificationSettings, types.AuthScopeProfileEdit),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("PUT")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/subscription-settings", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleUpdateSubscriptionSettings, types.AuthScopeProfileEdit),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("PUT")

	////////////////////////

	router.Handle(
		"/web/developer",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.RequestScope(a.HandleDeveloperPage, types.AuthScopeAll), muxAny(isGod, isColin)), false))).
		Methods("GET")

	if a.Conf.FlashpointSourceOnlyAdminMode {
		router.Handle(
			"/api/developer/submit_dump",
			http.HandlerFunc(a.RequestWeb(a.AdminPassAuth(a.RequestScope(a.HandleDeveloperDumpUpload, types.AuthScopeAll)), true))).
			Methods("POST")
	} else {
		router.Handle(
			"/api/developer/submit_dump",
			http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
				a.RequestScope(a.HandleDeveloperDumpUpload, types.AuthScopeAll), muxAny(isGod, isColin)), false))).
			Methods("POST")
	}

	router.Handle(
		"/api/developer/tag_desc_from_validator",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.RequestScope(a.HandleDeveloperTagDescFromValidator, types.AuthScopeAll), muxAny(isGod, isColin)), false))).
		Methods("GET")

	////////////////////////

	// providers

	////////////////////////

	router.Handle(
		fmt.Sprintf("/data/game/image/{%s:.*}", constants.ResourceKeyLongFile),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.RequestScope(a.HandleGameImageFile, types.AuthScopeGameRead),
		), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/submission/{%s}/file/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyFileID),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.RequestScope(a.HandleDownloadSubmissionFile, types.AuthScopeSubmissionReadFiles),
			muxAny(
				muxAll(muxNot(isSubmissionFrozen), isStaff),
				muxAll(muxNot(isSubmissionFrozen), isTrialCurator),
				muxAll(muxNot(isSubmissionFrozen), isInAudit),
				muxAll(isSubmissionFrozen, isFreezer, muxAny(isStaff, isTrialCurator, isInAudit)),
			)), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/submission-file-batch/{%s}", constants.ResourceKeyFileIDs),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.RequestScope(a.HandleDownloadSubmissionBatch, types.AuthScopeSubmissionReadFiles),
			muxAny(
				muxAll(muxNot(isAnySubmissionFileFrozen), isStaff),
				muxAll(muxNot(isAnySubmissionFileFrozen), isTrialCurator),
				muxAll(muxNot(isAnySubmissionFileFrozen), isInAudit),
				muxAll(isAnySubmissionFileFrozen, isFreezer, muxAny(isStaff, isTrialCurator, isInAudit)),
			)), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/submission/{%s}/curation-image/{%s}.png", constants.ResourceKeySubmissionID, constants.ResourceKeyCurationImageID),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.RequestScope(a.HandleDownloadCurationImage, types.AuthScopeSubmissionRead),
			muxAny(
				muxAll(muxNot(isSubmissionFrozen), isStaff),
				muxAll(muxNot(isSubmissionFrozen), isTrialCurator),
				muxAll(muxNot(isSubmissionFrozen), isInAudit),
				muxAll(isSubmissionFrozen, isFreezer, muxAny(isStaff, isTrialCurator, isInAudit)),
			)), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/flashfreeze/file/{%s}", constants.ResourceKeyFlashfreezeRootFileID),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.RequestScope(a.HandleDownloadFlashfreezeRootFile, types.AuthScopeSubmissionReadFiles),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	// soft delete
	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/file/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyFileID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleSoftDeleteSubmissionFile, types.AuthScopeAll),
			muxAll(muxNot(isSubmissionFrozen), isDeleter)), false))).
		Methods("DELETE")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleSoftDeleteSubmission, types.AuthScopeAll),
			muxAll(muxNot(isSubmissionFrozen), isDeleter)), false))).
		Methods("DELETE")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/comment/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyCommentID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleSoftDeleteComment, types.AuthScopeAll),
			muxAll(muxNot(isSubmissionFrozen), isDeleter)), false))).
		Methods("DELETE")

	// bot override

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/override", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleOverrideBot, types.AuthScopeAll),
			muxAny(isDeleter, isStaff)), false))).
		Methods("POST")

	// freeze

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/freeze", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleFreezeSubmission, types.AuthScopeAll),
			muxAll(isFreezer)), false))).
		Methods("POST")

	router.Handle(
		fmt.Sprintf("/api/submission/{%s}/unfreeze", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleUnfreezeSubmission, types.AuthScopeAll),
			muxAll(isFreezer)), false))).
		Methods("POST")

	// activity events

	router.Handle(
		"/api/activity-events",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleGetActivityEvents, types.AuthScopeAll),
			muxAny(isStaff)), false))).
		Methods("GET")

	router.Handle(
		"/web/user-activity",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleUserActivityPage, types.AuthScopeAll),
			muxAny(isStaff)), false))).
		Methods("GET")

	// user statistics

	router.Handle(
		"/api/users",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleGetUsers, types.AuthScopeUsersRead),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/api/user-statistics/{%s}", constants.ResourceKeyUserID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleGetUserStatistics, types.AuthScopeUsersRead),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	// user routes

	router.Handle(
		fmt.Sprintf("/api/user/{%s}/ban", constants.ResourceKeyUserID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleUserBan, types.AuthScopeAll),
			muxAny(isDeleter)), false))).
		Methods("GET")

	// upload status

	router.Handle(
		fmt.Sprintf("/api/upload-status/{%s}", constants.ResourceKeyTempName),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleGetUploadProgress, types.AuthScopeSubmissionUpload),
			muxAny(isStaff, isTrialCurator, isInAudit)), false))).
		Methods("GET")

	// recommendation engine

	router.Handle(
		fmt.Sprintf("/api/recommendation/{%s}", constants.ResourceKeyRecommendationOp),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleRecommendationEngine, types.AuthScopeAll), // TODO might want to add auth scope for recommendation engine
			muxAll(isStaff)), false))).
		Methods("POST")

	router.Handle(
		"/web/recommendation-playground",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.RequestScope(a.HandleRecommendationPlaygroundPage, types.AuthScopeAll),
			muxAny(isStaff)), false))).
		Methods("GET")

	////////////////////////

	// god tools

	router.Handle("/web/internal",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleInternalPage, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	//router.Handle("/api/internal/update-master-db",
	//	http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleUpdateMasterDB, types.AuthScopeAll), isGod), false))).
	//	Methods("GET")

	router.Handle("/api/internal/flashfreeze/ingest",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleIngestFlashfreeze, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/internal/recompute-submission-cache-all",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleRecomputeSubmissionCacheAll, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/internal/flashfreeze/ingest-unknown-files",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleIngestUnknownFlashfreeze, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/internal/flashfreeze/index-unindexed-files",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleIndexUnindexedFlashfreeze, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/internal/delete-user-sessions",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleDeleteUserSessions, types.AuthScopeAll), isGod), false))).
		Methods("POST")

	router.Handle("/api/internal/approve-submission",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleForceApproveSubmission, types.AuthScopeAll), isGod), false))).
		Methods("POST")

	router.Handle("/api/internal/verify-submission",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleForceVerifySubmission, types.AuthScopeAll), isGod), false))).
		Methods("POST")

	router.Handle("/api/internal/send-reminders-about-requested-changes",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleSendRemindersAboutRequestedChanges, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/internal/nuke-session-table",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.RequestScope(a.HandleNukeSessionTable, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	router.Handle("/api/stat",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(a.RequestScope(a.HandleStat, types.AuthScopeAll), isGod), false))).
		Methods("GET")

	err := srv.ListenAndServe()
	if err != nil {
		l.Fatal(err)
	}
}
