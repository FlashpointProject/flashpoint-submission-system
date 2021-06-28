package service

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Dri0m/flashpoint-submission-system/authbot"
	"github.com/Dri0m/flashpoint-submission-system/constants"
	"github.com/Dri0m/flashpoint-submission-system/database"
	"github.com/Dri0m/flashpoint-submission-system/notificationbot"
	"github.com/Dri0m/flashpoint-submission-system/types"
	"github.com/Dri0m/flashpoint-submission-system/utils"
	"github.com/bwmarrin/discordgo"
	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"mime/multipart"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type MultipartFileWrapper struct {
	fileHeader *multipart.FileHeader
}

func NewMutlipartFileWrapper(fileHeader *multipart.FileHeader) *MultipartFileWrapper {
	return &MultipartFileWrapper{
		fileHeader: fileHeader,
	}
}

func (m *MultipartFileWrapper) Filename() string {
	return m.fileHeader.Filename
}

func (m *MultipartFileWrapper) Size() int64 {
	return m.fileHeader.Size
}

func (m *MultipartFileWrapper) Open() (multipart.File, error) {
	return m.fileHeader.Open()
}

type RealClock struct {
}

func (r *RealClock) Now() time.Time {
	return time.Now()
}

func (r *RealClock) Unix(sec int64, nsec int64) time.Time {
	return time.Unix(sec, nsec)
}

// authToken is authToken
type authToken struct {
	Secret string
	UserID string
}

type authTokenProvider struct {
}

func NewAuthTokenProvider() *authTokenProvider {
	return &authTokenProvider{}
}

type AuthTokenizer interface {
	CreateAuthToken(userID int64) (*authToken, error)
}

func (a *authTokenProvider) CreateAuthToken(userID int64) (*authToken, error) {
	s, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	return &authToken{
		Secret: s.String(),
		UserID: fmt.Sprint(userID),
	}, nil
}

// ParseAuthToken parses map into token
func ParseAuthToken(value map[string]string) (*authToken, error) {
	secret, ok := value["Secret"]
	if !ok {
		return nil, fmt.Errorf("missing Secret")
	}
	userID, ok := value["userID"]
	if !ok {
		return nil, fmt.Errorf("missing userid")
	}
	return &authToken{
		Secret: secret,
		UserID: userID,
	}, nil
}

func MapAuthToken(token *authToken) map[string]string {
	return map[string]string{"Secret": token.Secret, "userID": token.UserID}
}

type SiteService struct {
	authBot                   authbot.DiscordRoleReader
	notificationBot           notificationbot.DiscordNotificationSender
	dal                       database.DAL
	validator                 Validator
	clock                     Clock
	randomStringProvider      utils.RandomStringer
	authTokenProvider         AuthTokenizer
	sessionExpirationSeconds  int64
	submissionsDir            string
	submissionImagesDir       string
	notificationQueueNotEmpty chan bool
	isDev                     bool
	submissionReceiverMutex   sync.Mutex
}

func NewSiteService(l *logrus.Logger, db *sql.DB, authBotSession, notificationBotSession *discordgo.Session,
	flashpointServerID, notificationChannelID, curationFeedChannelID, validatorServerURL string,
	sessionExpirationSeconds int64, submissionsDir, submissionImagesDir string, isDev bool) *SiteService {
	return &SiteService{
		authBot:                   authbot.NewBot(authBotSession, flashpointServerID, l.WithField("botName", "authBot"), isDev),
		notificationBot:           notificationbot.NewBot(notificationBotSession, flashpointServerID, notificationChannelID, curationFeedChannelID, l.WithField("botName", "notificationBot"), isDev),
		dal:                       database.NewMysqlDAL(db),
		validator:                 NewValidator(validatorServerURL),
		clock:                     &RealClock{},
		randomStringProvider:      utils.NewRealRandomStringProvider(),
		authTokenProvider:         NewAuthTokenProvider(),
		sessionExpirationSeconds:  sessionExpirationSeconds,
		submissionsDir:            submissionsDir,
		submissionImagesDir:       submissionImagesDir,
		notificationQueueNotEmpty: make(chan bool, 1),
		isDev:                     isDev,
	}
}

// GetBasePageData loads base user data, does not return error if user is not logged in
func (s *SiteService) GetBasePageData(ctx context.Context) (*types.BasePageData, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	uid := utils.UserID(ctx)
	if uid == 0 {
		return &types.BasePageData{}, nil
	}

	discordUser, err := s.dal.GetDiscordUser(dbs, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	userRoles, err := s.dal.GetDiscordUserRoles(dbs, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	bpd := &types.BasePageData{
		Username:  discordUser.Username,
		UserID:    discordUser.ID,
		AvatarURL: utils.FormatAvatarURL(discordUser.ID, discordUser.Avatar),
		UserRoles: userRoles,
	}

	return bpd, nil
}

func (s *SiteService) GetViewSubmissionPageData(ctx context.Context, uid, sid int64) (*types.ViewSubmissionPageData, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	bpd, err := s.GetBasePageData(ctx)
	if err != nil {
		return nil, err
	}

	filter := &types.SubmissionsFilter{
		SubmissionIDs: []int64{sid},
	}

	submissions, err := s.dal.SearchSubmissions(dbs, filter)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	if len(submissions) == 0 {
		return nil, perr("submission not found", http.StatusNotFound)
	}

	submission := submissions[0]

	meta, err := s.dal.GetCurationMetaBySubmissionFileID(dbs, submission.FileID)
	if err != nil && err != sql.ErrNoRows {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	comments, err := s.dal.GetExtendedCommentsBySubmissionID(dbs, sid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	isUserSubscribed, err := s.dal.IsUserSubscribedToSubmission(dbs, uid, sid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	curationImages, err := s.dal.GetCurationImagesBySubmissionFileID(dbs, submission.FileID)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	ciids := make([]int64, 0, len(curationImages))

	for _, curationImage := range curationImages {
		ciids = append(ciids, curationImage.ID)
	}

	var nextSID *int64
	var prevSID *int64

	nsid, err := s.dal.GetNextSubmission(dbs, sid)
	if err != nil {
		if err != sql.ErrNoRows {
			utils.LogCtx(ctx).Error(err)
			return nil, dberr(err)
		}
	} else {
		nextSID = &nsid
	}

	psid, err := s.dal.GetPreviousSubmission(dbs, sid)
	if err != nil {
		if err != sql.ErrNoRows {
			utils.LogCtx(ctx).Error(err)
			return nil, dberr(err)
		}
	} else {
		prevSID = &psid
	}

	pageData := &types.ViewSubmissionPageData{
		SubmissionsPageData: types.SubmissionsPageData{
			BasePageData: *bpd,
			Submissions:  submissions,
		},
		CurationMeta:         meta,
		Comments:             comments,
		IsUserSubscribed:     isUserSubscribed,
		CurationImageIDs:     ciids,
		NextSubmissionID:     nextSID,
		PreviousSubmissionID: prevSID,
	}

	return pageData, nil
}

func (s *SiteService) GetSubmissionsFilesPageData(ctx context.Context, sid int64) (*types.SubmissionsFilesPageData, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	bpd, err := s.GetBasePageData(ctx)
	if err != nil {
		return nil, err
	}

	sf, err := s.dal.GetExtendedSubmissionFilesBySubmissionID(dbs, sid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	pageData := &types.SubmissionsFilesPageData{
		BasePageData:    *bpd,
		SubmissionFiles: sf,
	}

	return pageData, nil
}

func (s *SiteService) GetSubmissionsPageData(ctx context.Context, filter *types.SubmissionsFilter) (*types.SubmissionsPageData, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	bpd, err := s.GetBasePageData(ctx)
	if err != nil {
		return nil, err
	}

	submissions, err := s.dal.SearchSubmissions(dbs, filter)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	pageData := &types.SubmissionsPageData{
		BasePageData: *bpd,
		Submissions:  submissions,
		Filter:       *filter,
	}

	return pageData, nil
}

func (s *SiteService) SearchSubmissions(ctx context.Context, filter *types.SubmissionsFilter) ([]*types.ExtendedSubmission, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	submissions, err := s.dal.SearchSubmissions(dbs, filter)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	return submissions, nil
}

func (s *SiteService) GetSubmissionFiles(ctx context.Context, sfids []int64) ([]*types.SubmissionFile, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	sfs, err := s.dal.GetSubmissionFiles(dbs, sfids)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	return sfs, nil
}

func (s *SiteService) GetUIDFromSession(ctx context.Context, key string) (int64, bool, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return 0, false, dberr(err)
	}
	defer dbs.Rollback()

	uid, ok, err := s.dal.GetUIDFromSession(dbs, key)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return 0, false, dberr(err)
	}

	return uid, ok, nil
}

func (s *SiteService) SoftDeleteSubmissionFile(ctx context.Context, sfid int64, deleteReason string) error {
	uid := utils.UserID(ctx)

	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	sfs, err := s.dal.GetSubmissionFiles(dbs, []int64{sfid})
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	authorID := sfs[0].SubmitterID
	sid := sfs[0].SubmissionID

	if err := s.dal.SoftDeleteSubmissionFile(dbs, sfid, deleteReason); err != nil {
		if err.Error() == constants.ErrorCannotDeleteLastSubmissionFile {
			return perr(err.Error(), http.StatusBadRequest)
		}
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := s.createDeletionNotification(dbs, authorID, uid, &sid, nil, &sfid, deleteReason); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	s.announceNotification()

	return nil
}

func (s *SiteService) SoftDeleteSubmission(ctx context.Context, sid int64, deleteReason string) error {
	uid := utils.UserID(ctx)

	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	submissions, err := s.dal.SearchSubmissions(dbs, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	authorID := submissions[0].SubmitterID

	if err := s.dal.SoftDeleteSubmission(dbs, sid, deleteReason); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := s.createDeletionNotification(dbs, authorID, uid, &sid, nil, nil, deleteReason); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	s.announceNotification()

	return nil
}

func (s *SiteService) SoftDeleteComment(ctx context.Context, cid int64, deleteReason string) error {
	uid := utils.UserID(ctx)

	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	c, err := s.dal.GetCommentByID(dbs, cid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := s.dal.SoftDeleteComment(dbs, cid, deleteReason); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := s.createDeletionNotification(dbs, c.AuthorID, uid, &c.SubmissionID, &cid, nil, deleteReason); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	s.announceNotification()

	return nil
}

func (s *SiteService) SaveUser(ctx context.Context, discordUser *types.DiscordUser) (*authToken, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	userExists := true
	_, err = s.dal.GetDiscordUser(dbs, discordUser.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			userExists = false
		} else {
			utils.LogCtx(ctx).Error(err)
			return nil, dberr(err)
		}
	}

	// save discord user data
	if err := s.dal.StoreDiscordUser(dbs, discordUser); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	// enable all notifications for a new user
	if !userExists {
		if err := s.dal.StoreNotificationSettings(dbs, discordUser.ID, constants.GetActionsWithNotification()); err != nil {
			utils.LogCtx(ctx).Error(err)
			return nil, dberr(err)
		}
	}

	// get discord roles
	serverRoles, err := s.authBot.GetFlashpointRoles() // TODO changes in roles need to be refreshed sometimes
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, err
	}
	userRoleIDs, err := s.authBot.GetFlashpointRoleIDsForUser(discordUser.ID)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, err
	}

	userRolesIDsNumeric := make([]int64, 0, len(userRoleIDs))
	for _, userRoleID := range userRoleIDs {
		id, err := strconv.ParseInt(userRoleID, 10, 64)
		if err != nil {
			return nil, err
		}
		userRolesIDsNumeric = append(userRolesIDsNumeric, id)
	}

	// save discord roles
	if err := s.dal.StoreDiscordServerRoles(dbs, serverRoles); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	if err := s.dal.StoreDiscordUserRoles(dbs, discordUser.ID, userRolesIDsNumeric); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	// create cookie and save session
	authToken, err := s.authTokenProvider.CreateAuthToken(discordUser.ID)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, err
	}

	if err = s.dal.StoreSession(dbs, authToken.Secret, discordUser.ID, s.sessionExpirationSeconds); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	return authToken, nil
}

func (s *SiteService) Logout(ctx context.Context, secret string) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if err := s.dal.DeleteSession(dbs, secret); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) GetUserRoles(ctx context.Context, uid int64) ([]string, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	roles, err := s.dal.GetDiscordUserRoles(dbs, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	return roles, nil
}

func (s *SiteService) GetProfilePageData(ctx context.Context, uid int64) (*types.ProfilePageData, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	bpd, err := s.GetBasePageData(ctx)
	if err != nil {
		return nil, err
	}

	notificationActions, err := s.dal.GetNotificationSettingsByUserID(dbs, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}

	pageData := &types.ProfilePageData{
		BasePageData:        *bpd,
		NotificationActions: notificationActions,
	}

	return pageData, nil
}

func (s *SiteService) UpdateNotificationSettings(ctx context.Context, uid int64, notificationActions []string) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if err := s.dal.StoreNotificationSettings(dbs, uid, notificationActions); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) UpdateSubscriptionSettings(ctx context.Context, uid, sid int64, subscribe bool) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if subscribe {
		if err := s.dal.SubscribeUserToSubmission(dbs, uid, sid); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}
	} else {
		if err := s.dal.UnsubscribeUserFromSubmission(dbs, uid, sid); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) GetCurationImage(ctx context.Context, ciid int64) (*types.CurationImage, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	ci, err := s.dal.GetCurationImage(dbs, ciid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	return ci, nil
}

func (s *SiteService) GetNextSubmission(ctx context.Context, sid int64) (*int64, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	nsid, err := s.dal.GetNextSubmission(dbs, sid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	return &nsid, nil
}

func (s *SiteService) GetPreviousSubmission(ctx context.Context, sid int64) (*int64, error) {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	defer dbs.Rollback()

	psid, err := s.dal.GetPreviousSubmission(dbs, sid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		utils.LogCtx(ctx).Error(err)
		return nil, dberr(err)
	}
	return &psid, nil
}
