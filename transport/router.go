package transport

import (
	"fmt"
	"github.com/Dri0m/flashpoint-submission-system/constants"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"net/http"
)

func (a *App) handleRequests(l *logrus.Logger, srv *http.Server, router *mux.Router) {
	// TODO refactor these helpers away from here?
	any := func(authorizers ...func(*http.Request, int64) (bool, error)) func(*http.Request, int64) (bool, error) {
		return func(r *http.Request, uid int64) (bool, error) {
			for _, authorizer := range authorizers {
				ok, err := authorizer(r, uid)
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
			return false, nil
		}
	}

	all := func(authorizers ...func(*http.Request, int64) (bool, error)) func(*http.Request, int64) (bool, error) {
		return func(r *http.Request, uid int64) (bool, error) {
			isAuthorized := true
			for _, authorizer := range authorizers {
				ok, err := authorizer(r, uid)
				if err != nil {
					return false, err
				}
				if !ok {
					isAuthorized = false
					break
				}
			}

			if !isAuthorized {
				return false, nil
			}
			return true, nil
		}
	}

	isStaff := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.StaffRoles())
	}
	isTrialCurator := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.TrialCuratorRoles())
	}
	isDeletor := func(r *http.Request, uid int64) (bool, error) {
		return a.UserHasAnyRole(r, uid, constants.DeletorRoles())
	}
	userOwnsSubmission := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeySubmissionID)
	}
	userOwnsFile := func(r *http.Request, uid int64) (bool, error) {
		return a.UserOwnsResource(r, uid, constants.ResourceKeyFileID)
	}

	// oauth
	router.Handle("/auth", http.HandlerFunc(a.HandleDiscordAuth)).Methods("GET")
	router.Handle("/auth/callback", http.HandlerFunc(a.HandleDiscordCallback)).Methods("GET")

	// logout
	router.Handle("/logout", http.HandlerFunc(a.HandleLogout)).Methods("GET")

	// file server
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// pages
	router.Handle("/",
		http.HandlerFunc(a.HandleRootPage)).Methods("GET")

	router.Handle("/profile",
		http.HandlerFunc(a.UserAuthMux(a.HandleProfilePage))).Methods("GET")

	router.Handle("/submit",
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSubmitPage, any(isStaff, isTrialCurator)))).Methods("GET")

	router.Handle("/submissions",
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSubmissionsPage, any(isStaff)))).Methods("GET")

	router.Handle("/my-submissions",
		http.HandlerFunc(a.UserAuthMux(
			a.HandleMySubmissionsPage, any(isStaff, isTrialCurator)))).Methods("GET")

	router.Handle(fmt.Sprintf("/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleViewSubmissionPage, any(isStaff, all(isTrialCurator, userOwnsSubmission))))).Methods("GET")

	router.Handle(fmt.Sprintf("/submission/{%s}/files", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleViewSubmissionFilesPage, any(isStaff, all(isTrialCurator, userOwnsSubmission))))).Methods("GET")

	// receivers
	router.Handle("/submission-receiver",
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSubmissionReceiver, any(isStaff, isTrialCurator)))).Methods("POST")

	router.Handle(fmt.Sprintf("/submission-receiver/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSubmissionReceiver, any(isStaff, all(isTrialCurator, userOwnsSubmission))))).Methods("POST")

	router.Handle(fmt.Sprintf("/submission/{%s}/comment", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleCommentReceiver, any(
				all(isStaff, a.UserCanCommentAction),
				all(isTrialCurator, userOwnsSubmission, a.UserCanCommentAction))))).Methods("POST")

	router.Handle(fmt.Sprintf("/submission-batch/{%s}/comment", constants.ResourceKeySubmissionIDs), // TODO trial curator should be able to use this
		http.HandlerFunc(a.UserAuthMux(
			a.HandleCommentReceiverBatch, all(isStaff, a.UserCanCommentAction)))).Methods("POST")

	// providers
	router.Handle(fmt.Sprintf("/submission-file/{%s}", constants.ResourceKeyFileID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleDownloadSubmissionFile, any(isStaff, all(isTrialCurator, userOwnsFile))))).Methods("GET")

	router.Handle(fmt.Sprintf("/submission-file-batch/{%s}", constants.ResourceKeyFileIDs), // TODO trial curator should be able to use this
		http.HandlerFunc(a.UserAuthMux(
			a.HandleDownloadSubmissionBatch, any(isStaff)))).Methods("GET")

	// soft delete
	router.Handle(fmt.Sprintf("/submission-file/{%s}", constants.ResourceKeyFileID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSoftDeleteSubmissionFile, all(isDeletor)))).Methods("DELETE")

	router.Handle(fmt.Sprintf("/submission/{%s}", constants.ResourceKeySubmissionID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSoftDeleteSubmission, all(isDeletor)))).Methods("DELETE")

	router.Handle(fmt.Sprintf(
		"/submission/{%s}/comment/{%s}", constants.ResourceKeySubmissionID, constants.ResourceKeyCommentID),
		http.HandlerFunc(a.UserAuthMux(
			a.HandleSoftDeleteComment, all(isDeletor)))).Methods("DELETE")

	err := srv.ListenAndServe()
	if err != nil {
		l.Fatal(err)
	}
}
