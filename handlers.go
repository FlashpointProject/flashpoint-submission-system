package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strconv"
	"time"
)

func (a *App) handleRequests(l *logrus.Logger, srv *http.Server, router *mux.Router) {
	// oauth
	router.Handle("/auth", http.HandlerFunc(a.HandleDiscordAuth)).Methods("GET")
	router.Handle("/auth/callback", http.HandlerFunc(a.HandleDiscordCallback)).Methods("GET")

	// logout
	router.Handle("/logout", http.HandlerFunc(a.HandleLogout)).Methods("GET")

	// file server
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// pages
	router.Handle("/", http.HandlerFunc(a.HandleRootPage)).Methods("GET")
	router.Handle("/profile", http.HandlerFunc(a.UserAuthentication(a.HandleProfilePage))).Methods("GET")
	router.Handle("/submit", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleSubmitPage)))).Methods("GET")
	router.Handle("/submissions", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleSubmissionsPage)))).Methods("GET")
	router.Handle("/my-submissions", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleMySubmissionsPage)))).Methods("GET")
	router.Handle("/submission/{id}", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleViewSubmissionPage)))).Methods("GET")
	router.Handle("/submission/{id}/comment", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleCommentReceiver)))).Methods("POST")

	// file shenanigans
	router.Handle("/submission-receiver", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleSubmissionReceiver)))).Methods("POST")
	router.Handle("/download-submission/{id}", http.HandlerFunc(a.UserAuthentication(a.UserAuthorization(a.HandleDownloadSubmission)))).Methods("GET")
	err := srv.ListenAndServe()
	if err != nil {
		l.Fatal(err)
	}
}

func (a *App) HandleCommentReceiver(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, err := a.GetUserIDFromCookie(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "invalid cookie", http.StatusInternalServerError)
		return
	}

	params := mux.Vars(r)
	submissionID := params["id"]
	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "invalid submission id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	tx, err := a.db.conn.Begin()
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to begin transaction", http.StatusInternalServerError)
		return
	}

	action := r.FormValue("action")
	m := r.FormValue("message")
	var message *string
	if m != "" {
		message = &m
	}

	actions := []string{ActionComment, ActionApprove, ActionRequestChanges, ActionAccept, ActionMarkAdded, ActionReject}

	isActionValid := false
	for _, a := range actions {
		if action == a {
			isActionValid = true
			break
		}
	}

	if !isActionValid {
		err := fmt.Errorf("invalid comment action")
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	c := &Comment{
		AuthorID:     uid,
		SubmissionID: sid,
		Message:      message,
		Action:       action,
		CreatedAt:    time.Now(),
	}

	if err := a.db.StoreComment(tx, c); err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to store comment", http.StatusInternalServerError)
		a.LogIfErr(ctx, tx.Rollback())
		return
	}

	if err := tx.Commit(); err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to commit transaction", http.StatusInternalServerError)
		a.LogIfErr(ctx, tx.Rollback())
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/submission/%d", sid), http.StatusFound)
}

func (a *App) HandleDownloadSubmission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params["id"]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "invalid submission id", http.StatusBadRequest)
		return
	}

	const dir = "submissions"

	filter := &SubmissionsFilter{
		SubmissionID: &sid,
	}

	submissions, err := a.db.SearchSubmissions(filter)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to find submission", http.StatusInternalServerError) // TODO discern 404 here
		return
	}

	s := submissions[0]

	f, err := os.Open(fmt.Sprintf("%s/%s", dir, s.CurrentFilename))
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", s.CurrentFilename))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, s.CurrentFilename, s.UploadedAt, f)
}

func (a *App) HandleSubmissionReceiver(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tx, err := a.db.conn.Begin()
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to begin transaction", http.StatusInternalServerError)
		return
	}

	// limit RAM usage to 100MB
	if err := r.ParseMultipartForm(100 * 1000 * 1000); err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to parse form", http.StatusInternalServerError)
		return
	}

	fileHeaders := r.MultipartForm.File["files"]
	for _, fileHeader := range fileHeaders {
		err := a.ProcessReceivedSubmission(ctx, tx, fileHeader)
		if err != nil {
			LogCtx(ctx).Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			a.LogIfErr(ctx, tx.Rollback())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to commit transaction", http.StatusInternalServerError)
		a.LogIfErr(ctx, tx.Rollback())
	}

	http.Redirect(w, r, "/my-submissions", http.StatusFound)
}

func (a *App) HandleRootPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/root.gohtml")
}

func (a *App) HandleProfilePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/profile.gohtml")
}

func (a *App) HandleSubmitPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pageData, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/submit.gohtml")
}

type submissionsPageData struct {
	basePageData
	Submissions []*ExtendedSubmission
}

func (a *App) HandleSubmissionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bpd, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	submissions, err := a.db.SearchSubmissions(nil)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to load submissions", http.StatusInternalServerError)
		return
	}

	pageData := submissionsPageData{basePageData: *bpd, Submissions: submissions}

	a.RenderTemplates(ctx, w, r, pageData, "templates/submissions.gohtml", "templates/submission-table.gohtml")
}

func (a *App) HandleMySubmissionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := a.GetUserIDFromCookie(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "invalid cookie", http.StatusInternalServerError)
		return
	}

	bpd, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filter := &SubmissionsFilter{
		SubmitterID: &userID,
	}

	submissions, err := a.db.SearchSubmissions(filter)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to load user submissions", http.StatusInternalServerError)
		return
	}

	pageData := submissionsPageData{basePageData: *bpd, Submissions: submissions}

	a.RenderTemplates(ctx, w, r, pageData, "templates/my-submissions.gohtml", "templates/submission-table.gohtml")
}

type viewSubmissionPageData struct {
	basePageData
	Submissions  []*ExtendedSubmission
	CurationMeta CurationMeta
	Comments     []*ExtendedComment
}

func (a *App) HandleViewSubmissionPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	submissionID := params["id"]

	sid, err := strconv.ParseInt(submissionID, 10, 64)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "invalid submission id", http.StatusBadRequest)
		return
	}

	bpd, err := a.GetBasePageData(r)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filter := &SubmissionsFilter{
		SubmissionID: &sid,
	}

	submissions, err := a.db.SearchSubmissions(filter)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to load submission", http.StatusInternalServerError)
		return
	}

	submission := submissions[0]

	meta, err := a.db.GetCurationMetaBySubmissionFileID(submission.FileID)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to load curation meta", http.StatusInternalServerError)
		return
	}

	comments, err := a.db.GetExtendedCommentsBySubmissionID(sid)
	if err != nil {
		LogCtx(ctx).Error(err)
		http.Error(w, "failed to load curation comments", http.StatusInternalServerError)
		return
	}

	pageData := viewSubmissionPageData{
		basePageData: *bpd,
		Submissions:  submissions,
		CurationMeta: *meta,
		Comments:     comments,
	}

	a.RenderTemplates(ctx, w, r, pageData, "templates/submission.gohtml", "templates/submission-table.gohtml")
}
