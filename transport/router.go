package transport

import (
	"fmt"
	"github.com/Dri0m/flashpoint-submission-system/constants"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"net/http"
)

func (a *App) handleRequests(l *logrus.Logger, srv *http.Server, router *mux.Router) {
	isStaff := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.StaffRoles())
	}
	isTrialCurator := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.TrialCuratorRoles())
	}
	isDeleter := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.DeleterRoles())
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
	userOwnsSubmission := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeySubmissionID)
	}
	userOwnsAllSubmissions := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeySubmissionIDs)
	}
	userHasNoSubmissions := func(r *http.Request, uid int64) (bool, error) {
		return a.IsUserWithinResourceLimit(r, uid, constants.ResourceKeySubmissionID, 1)
	}

	// static file server
	router.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// auth
	router.Handle(
		"/web/auth",
		http.HandlerFunc(a.RequestWeb(a.HandleDiscordAuth))).
		Methods("GET")
	router.Handle(
		"/web/auth/callback",
		http.HandlerFunc(a.RequestWeb(a.HandleDiscordCallback))).
		Methods("GET")
	router.Handle(
		"/logout",
		http.HandlerFunc(a.RequestWeb(a.HandleLogout))).
		Methods("GET")

	// pages
	router.Handle(
		"/",
		http.HandlerFunc(a.RequestWeb(a.HandleRootPage))).
		Methods("GET")

	router.Handle(
		"/web",
		http.HandlerFunc(a.RequestWeb(a.HandleRootPage))).
		Methods("GET")

	router.Handle(
		"/web/profile",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(a.HandleProfilePage)))).
		Methods("GET")

	router.Handle(
		"/web/submit",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.HandleSubmitPage, muxAny(isStaff, isTrialCurator, isInAudit))))).
		Methods("GET")

	router.Handle(
		"/web/submissions",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.HandleSubmissionsPage, muxAny(isStaff))))).
		Methods("GET")

	router.Handle(
		"/web/my-submissions",
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.HandleMySubmissionsPage, muxAny(isStaff, isTrialCurator, isInAudit))))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.HandleViewSubmissionPage,
			muxAny(isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission)))))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/web/submission/{%s}/files", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestWeb(a.UserAuthMux(
			a.HandleViewSubmissionFilesPage,
			muxAny(isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission)))))).
		Methods("GET")

	// receivers
	router.Handle(
		"/submission-receiver",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleSubmissionReceiver, muxAny(
				isStaff,
				isTrialCurator,
				muxAll(isInAudit, userHasNoSubmissions)))))).
		Methods("POST")

	router.Handle(
		fmt.Sprintf("/submission-receiver/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleSubmissionReceiver,
			muxAny(isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission)))))).
		Methods("POST")

	router.Handle(
		fmt.Sprintf("/submission-batch/{%s}/comment", constants.ResourceKeySubmissionIDs),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleCommentReceiverBatch, muxAny(
				muxAll(isStaff, a.UserCanCommentAction),
				muxAll(isTrialCurator, userOwnsAllSubmissions),
				muxAll(isInAudit, userOwnsAllSubmissions)))))).
		Methods("POST")

	router.Handle("/api/notification-settings",
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleUpdateNotificationSettings, muxAny(isStaff, isTrialCurator, isInAudit))))).
		Methods("PUT")

	router.Handle(
		fmt.Sprintf("/submission/{%s}/subscription-settings", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleUpdateSubscriptionSettings, muxAny(isStaff, isTrialCurator, isInAudit))))).
		Methods("PUT")

	// providers
	router.Handle(
		fmt.Sprintf("/data/submission/{%s}/file/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyFileID),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.HandleDownloadSubmissionFile,
			muxAny(isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission)))))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/submission-file-batch/{%s}", constants.ResourceKeyFileIDs),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.HandleDownloadSubmissionBatch, muxAny(
				isStaff,
				muxAll(isTrialCurator, userOwnsAllSubmissions)))))).
		Methods("GET")

	router.Handle(
		fmt.Sprintf("/data/submission/{%s}/curation-image/{%s}.png", constants.ResourceKeySubmissionID, constants.ResourceKeyCurationImageID),
		http.HandlerFunc(a.RequestData(a.UserAuthMux(
			a.HandleDownloadCurationImage,
			muxAny(isStaff,
				muxAll(isTrialCurator, userOwnsSubmission),
				muxAll(isInAudit, userOwnsSubmission)))))).
		Methods("GET")

	// soft delete
	router.Handle(
		fmt.Sprintf("/submission/{%s}/file/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyFileID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleSoftDeleteSubmissionFile, muxAll(isDeleter))))).
		Methods("DELETE")

	router.Handle(
		fmt.Sprintf("/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleSoftDeleteSubmission, muxAll(isDeleter))))).
		Methods("DELETE")

	router.Handle(
		fmt.Sprintf("/submission/{%s}/comment/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyCommentID),
		http.HandlerFunc(a.RequestJSON(a.UserAuthMux(
			a.HandleSoftDeleteComment, muxAll(isDeleter))))).
		Methods("DELETE")

	err := srv.ListenAndServe()
	if err != nil {
		l.Fatal(err)
	}
}
