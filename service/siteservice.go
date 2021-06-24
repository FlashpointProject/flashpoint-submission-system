package service

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/Dri0m/flashpoint-submission-system/authbot"
	"github.com/Dri0m/flashpoint-submission-system/constants"
	"github.com/Dri0m/flashpoint-submission-system/database"
	"github.com/Dri0m/flashpoint-submission-system/notificationbot"
	"github.com/Dri0m/flashpoint-submission-system/types"
	"github.com/Dri0m/flashpoint-submission-system/utils"
	"github.com/bwmarrin/discordgo"
	"github.com/go-sql-driver/mysql"
	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	uid := utils.UserIDFromContext(ctx)
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

func (s *SiteService) ReceiveSubmissions(ctx context.Context, sid *int64, fileProviders []MultipartFileProvider) error {
	uid := utils.UserIDFromContext(ctx)
	if uid == 0 {
		utils.LogCtx(ctx).Panic("no user associated with request")
	}

	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	userRoles, err := s.dal.GetDiscordUserRoles(dbs, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if constants.IsInAudit(userRoles) && len(fileProviders) > 1 {
		return perr("cannot upload more than one submission at once when user is in audit", http.StatusForbidden)
	}

	if constants.IsInAudit(userRoles) && fileProviders[0].Size() > constants.UserInAuditSumbissionMaxFilesize {
		return perr("submission filesize limited to 200MB for users in audit", http.StatusForbidden)
	}

	var submissionLevel string

	if constants.IsInAudit(userRoles) {
		submissionLevel = constants.SubmissionLevelAudition
	} else if constants.IsTrialCurator(userRoles) {
		submissionLevel = constants.SubmissionLevelTrial
	} else if constants.IsStaff(userRoles) {
		submissionLevel = constants.SubmissionLevelStaff
	}

	destinationFilenames := make([]string, 0)
	imageFilePaths := make([]string, 0)

	cleanup := func() {
		for _, fp := range destinationFilenames {
			utils.LogCtx(ctx).Debugf("cleaning up file '%s'...", fp)
			if err := os.Remove(fp); err != nil {
				utils.LogCtx(ctx).Error(err)
			}
		}
		for _, fp := range imageFilePaths {
			utils.LogCtx(ctx).Debugf("cleaning up image file '%s'...", fp)
			if err := os.Remove(fp); err != nil {
				utils.LogCtx(ctx).Error(err)
			}
		}
	}

	for _, fileProvider := range fileProviders {
		destinationFilename, ifp, err := s.processReceivedSubmission(ctx, dbs, fileProvider, sid, submissionLevel)

		if destinationFilename != nil {
			destinationFilenames = append(destinationFilenames, *destinationFilename)
		}
		for _, imageFilePath := range ifp {
			imageFilePaths = append(imageFilePaths, imageFilePath)
		}

		if err != nil {
			cleanup()
			return err
		}
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		cleanup()
		return dberr(err)
	}

	s.announceNotification()

	return nil
}

func (s *SiteService) processReceivedSubmission(ctx context.Context, dbs database.DBSession, fileHeader MultipartFileProvider, sid *int64, submissionLevel string) (*string, []string, error) {
	uid := utils.UserIDFromContext(ctx)
	if uid == 0 {
		utils.LogCtx(ctx).Panic("no user associated with request")
	}

	file, err := fileHeader.Open()
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	utils.LogCtx(ctx).Debugf("received a file '%s' - %d bytes", fileHeader.Filename(), fileHeader.Size())

	if err := os.MkdirAll(s.submissionsDir, os.ModeDir); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(s.submissionImagesDir, os.ModeDir); err != nil {
		return nil, nil, err
	}

	ext := filepath.Ext(fileHeader.Filename())

	if ext != ".7z" && ext != ".zip" {
		return nil, nil, perr("unsupported file extension", http.StatusBadRequest)
	}

	destinationFilename := s.randomStringProvider.RandomString(64) + ext
	destinationFilePath := fmt.Sprintf("%s/%s", s.submissionsDir, destinationFilename)

	destination, err := os.Create(destinationFilePath)
	if err != nil {
		return nil, nil, err
	}
	defer destination.Close()

	utils.LogCtx(ctx).Debugf("copying submission file to '%s'...", destinationFilePath)

	md5sum := md5.New()
	sha256sum := sha256.New()
	multiWriter := io.MultiWriter(destination, sha256sum, md5sum)

	nBytes, err := io.Copy(multiWriter, file)
	if err != nil {
		return &destinationFilePath, nil, err
	}
	if nBytes != fileHeader.Size() {
		return &destinationFilePath, nil, fmt.Errorf("incorrect number of bytes copied to destination")
	}

	utils.LogCtx(ctx).Debug("storing submission...")

	var submissionID int64

	isSubmissionNew := true

	if sid == nil {
		submissionID, err = s.dal.StoreSubmission(dbs, submissionLevel)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			return &destinationFilePath, nil, dberr(err)
		}
	} else {
		submissionID = *sid
		isSubmissionNew = false
	}

	// send notification about new file uploaded
	if !isSubmissionNew {
		if err := s.createNotification(dbs, uid, submissionID, constants.ActionUpload); err != nil {
			return &destinationFilePath, nil, err
		}
	}

	if err := s.dal.SubscribeUserToSubmission(dbs, uid, submissionID); err != nil {
		utils.LogCtx(ctx).Error(err)
		return &destinationFilePath, nil, dberr(err)
	}

	sf := &types.SubmissionFile{
		SubmissionID:     submissionID,
		SubmitterID:      uid,
		OriginalFilename: fileHeader.Filename(),
		CurrentFilename:  destinationFilename,
		Size:             fileHeader.Size(),
		UploadedAt:       s.clock.Now(),
		MD5Sum:           hex.EncodeToString(md5sum.Sum(nil)),
		SHA256Sum:        hex.EncodeToString(sha256sum.Sum(nil)),
	}

	fid, err := s.dal.StoreSubmissionFile(dbs, sf)
	if err != nil {
		me, ok := err.(*mysql.MySQLError)
		if ok {
			if me.Number == 1062 {
				return &destinationFilePath, nil, perr(fmt.Sprintf("file '%s' with checksums md5:%s sha256:%s already present in the DB", fileHeader.Filename(), sf.MD5Sum, sf.SHA256Sum), http.StatusConflict)
			}
		}
		utils.LogCtx(ctx).Error(err)
		return &destinationFilePath, nil, dberr(err)
	}

	utils.LogCtx(ctx).Debug("storing submission comment...")

	c := &types.Comment{
		AuthorID:     uid,
		SubmissionID: submissionID,
		Message:      nil,
		Action:       constants.ActionUpload,
		CreatedAt:    s.clock.Now(),
	}

	if err := s.dal.StoreComment(dbs, c); err != nil {
		utils.LogCtx(ctx).Error(err)
		return &destinationFilePath, nil, dberr(err)
	}

	utils.LogCtx(ctx).Debug("processing curation meta...")

	vr, err := s.validator.Validate(ctx, destinationFilePath, submissionID, fid)
	if err != nil {
		return &destinationFilePath, nil, err
	}

	if vr.IsExtreme {
		yes := "Yes"
		vr.Meta.Extreme = &yes
	} else {
		no := "No"
		vr.Meta.Extreme = &no
	}

	if err := s.dal.StoreCurationMeta(dbs, &vr.Meta); err != nil {
		utils.LogCtx(ctx).Error(err)
		return &destinationFilePath, nil, dberr(err)
	}

	// feed the curation feed
	isCurationValid := len(vr.CurationErrors) == 0 && len(vr.CurationWarnings) == 0
	if err := s.createCurationFeedMessage(dbs, uid, submissionID, isSubmissionNew, isCurationValid, &vr.Meta); err != nil {
		return &destinationFilePath, nil, dberr(err)
	}

	// save images
	imageFilePaths := make([]string, 0, len(vr.Images))
	for _, image := range vr.Images {
		imageData, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return &destinationFilePath, imageFilePaths, err
		}
		imageFilename := s.randomStringProvider.RandomString(64)
		imageFilenameFilePath := fmt.Sprintf("%s/%s", s.submissionImagesDir, imageFilename)

		imageFilePaths = append(imageFilePaths, imageFilenameFilePath)

		if err := ioutil.WriteFile(imageFilenameFilePath, imageData, 0644); err != nil {
			return &destinationFilePath, imageFilePaths, err
		}

		ci := &types.CurationImage{
			SubmissionFileID: fid,
			Type:             image.Type,
			Filename:         imageFilename,
		}

		if _, err := s.dal.StoreCurationImage(dbs, ci); err != nil {
			utils.LogCtx(ctx).Error(err)
			return &destinationFilePath, imageFilePaths, dberr(err)
		}
	}

	utils.LogCtx(ctx).Debug("processing bot event...")

	bc := s.convertValidatorResponseToComment(vr)
	if err := s.dal.StoreComment(dbs, bc); err != nil {
		utils.LogCtx(ctx).Error(err)
		return &destinationFilePath, imageFilePaths, dberr(err)
	}

	return &destinationFilePath, imageFilePaths, nil
}

// createNotification formats and stores notification
func (s *SiteService) createNotification(dbs database.DBSession, authorID, sid int64, action string) error {
	validAction := false
	for _, a := range constants.GetActionsWithNotification() {
		if action == a {
			validAction = true
			break
		}
	}
	if !validAction {
		return nil
	}

	mentionUserIDs, err := s.dal.GetUsersForNotification(dbs, authorID, sid, action)
	if err != nil {
		utils.LogCtx(dbs.Ctx()).Error(err)
		return err
	}

	if len(mentionUserIDs) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("You've got mail!\n")
	b.WriteString(fmt.Sprintf("<https://fpfss.unstable.life/submission/%d>\n", sid))

	if action == constants.ActionComment {
		b.WriteString(fmt.Sprintf("There is a new comment on the submission."))
	} else if action == constants.ActionApprove {
		b.WriteString(fmt.Sprintf("The submission has been approved."))
	} else if action == constants.ActionRequestChanges {
		b.WriteString(fmt.Sprintf("User has requested changes on the submission."))
	} else if action == constants.ActionMarkAdded {
		b.WriteString(fmt.Sprintf("The submission has been marked as added to Flashpoint."))
	} else if action == constants.ActionUpload {
		b.WriteString(fmt.Sprintf("A new version has been uploaded by <@%d>", authorID))
	}
	b.WriteString("\n")

	for _, userID := range mentionUserIDs {
		b.WriteString(fmt.Sprintf(" <@%d>", userID))
	}

	b.WriteString("\n----------------------------------------------------------\n")
	msg := b.String()

	if err := s.dal.StoreNotification(dbs, msg, constants.NotificationDefault); err != nil {
		utils.LogCtx(dbs.Ctx()).Error(err)
		return dberr(err)
	}

	return nil
}

// createCurationFeedMessage formats and stores message for the curation feed
func (s *SiteService) createCurationFeedMessage(dbs database.DBSession, authorID, sid int64, isSubmissionNew, isCurationValid bool, meta *types.CurationMeta) error {
	var b strings.Builder

	if isSubmissionNew {
		b.WriteString(fmt.Sprintf("A new submission has been uploaded by <@%d>\n", authorID))
	} else {
		b.WriteString(fmt.Sprintf("A submission update has been uploaded by <@%d>\n", authorID))
	}
	b.WriteString(fmt.Sprintf("<https://fpfss.unstable.life/submission/%d>\n", sid))

	if !isCurationValid {
		b.WriteString("Unfortunately, it does not quite reach the quality required to satisfy the cool crab.\n")
	}

	if meta.Library != nil && meta.Platform != nil && meta.Title != nil && meta.Extreme != nil {
		llib := strings.ToLower(*meta.Library)
		if strings.Contains(llib, "arcade") {
			b.WriteString("🎮")
		} else if strings.Contains(llib, "theatre") {
			b.WriteString("🎞️")
		} else {
			b.WriteString("❓")
		}

		b.WriteString(" ")

		lplat := strings.ToLower(*meta.Platform)
		if strings.Contains(lplat, "3d groove fx") {
			b.WriteString("<:3DGroove:569691574276063242>")
		} else if strings.Contains(lplat, "3dvia player") {
			b.WriteString("<:3DVIA_Player:496151464784166946")
		} else if strings.Contains(lplat, "axel player") {
			b.WriteString("<:AXEL_Player:813079894267265094>")
		} else if strings.Contains(lplat, "activex") {
			b.WriteString("<:ActiveX:699093212949643365>")
		} else if strings.Contains(lplat, "atmosphere") {
			b.WriteString("<:Atmosphere:781105689002901524>")
		} else if strings.Contains(lplat, "authorware") {
			b.WriteString("<:Authorware:582105144410243073>")
		} else if strings.Contains(lplat, "burster") {
			b.WriteString("<:Burster:743995494736461854>")
		} else if strings.Contains(lplat, "cult3d") {
			b.WriteString("<:Cult3D:806277196473040896>")
		} else if strings.Contains(lplat, "deepv") {
			b.WriteString("<:DeepV:812079774843142255>")
		} else if strings.Contains(lplat, "flash") {
			b.WriteString("<:Flash:750823911326875648>")
		} else if strings.Contains(lplat, "gobit") {
			b.WriteString("<:GoBit:629511736608686080>")
		} else if strings.Contains(lplat, "html5") {
			b.WriteString("<:HTML5:701930562746712142>")
		} else if strings.Contains(lplat, "hyper-g") {
			b.WriteString("<:HyperG:817543962088570880>")
		} else if strings.Contains(lplat, "hypercosm") {
			b.WriteString("<:Hypercosm:814623525038063697>")
		} else if strings.Contains(lplat, "java") {
			b.WriteString("<:Java:482697866377297920>")
		} else if strings.Contains(lplat, "livemath") {
			b.WriteString("<:LiveMath_Plugin:808999958043951104>")
		} else if strings.Contains(lplat, "octree view") {
			b.WriteString("<:Octree_View:809147835927756831>")
		} else if strings.Contains(lplat, "play3d") {
			b.WriteString("<:Play3D:812079775152734209>")
		} else if strings.Contains(lplat, "popcap plugin") {
			b.WriteString("<:PopCap:604433459179552798>")
		} else if strings.Contains(lplat, "protoplay") {
			b.WriteString("<:ProtoPlay:806614012829761587>")
		} else if strings.Contains(lplat, "pulse") {
			b.WriteString("<:Pulse:720682372982505472>")
		} else if strings.Contains(lplat, "rebol") {
			b.WriteString("<:REBOL:806995243085987862>")
		} else if strings.Contains(lplat, "shiva3d") {
			b.WriteString("<:ShiVa3d:643124144812326934>")
		} else if strings.Contains(lplat, "shockwave") {
			b.WriteString("<:Shockwave:727436274625019965>")
		} else if strings.Contains(lplat, "silverlight") {
			b.WriteString("<:Silverlight:492112373625257994>")
		} else if strings.Contains(lplat, "tcl") {
			b.WriteString("<:Tcl:737419431067779144>")
		} else if strings.Contains(lplat, "unity") {
			b.WriteString("<:Unity:600478910169481216>")
		} else if strings.Contains(lplat, "vrml") {
			b.WriteString("<:VRML:737049432817664070>")
		} else if strings.Contains(lplat, "viscape") {
			b.WriteString("<:Viscape:814623877039652886>")
		} else if strings.Contains(lplat, "vitalize") {
			b.WriteString("<:Vitalize:700924839912800332>")
		} else if strings.Contains(lplat, "xara plugin") {
			b.WriteString("<:Xara_Plugin:807439131768258561>")
		} else if strings.Contains(lplat, "alambik") {
			b.WriteString("<:Alambik:814621713350262856>")
		} else if strings.Contains(lplat, "animaflex") {
			b.WriteString("<:AnimaFlex:807016001618968596>")
		} else {
			b.WriteString("❓")
		}

		b.WriteString(" ")

		if *meta.Extreme == "Yes" {
			b.WriteString("<:extreme:778145279714918400>")
		}

		b.WriteString(" ")

		b.WriteString(*meta.Title)
		b.WriteString("\n")
	}

	b.WriteString("----------------------------------------------------------\n")
	msg := b.String()

	if err := s.dal.StoreNotification(dbs, msg, constants.NotificationCurationFeed); err != nil {
		return err
	}

	return nil
}

// convertValidatorResponseToComment produces appropriate comment based on validator response
func (s *SiteService) convertValidatorResponseToComment(vr *types.ValidatorResponse) *types.Comment {
	c := &types.Comment{
		AuthorID:     constants.ValidatorID,
		SubmissionID: vr.Meta.SubmissionID,
		CreatedAt:    s.clock.Now().Add(time.Second),
	}

	approvalMessage := "Looks good to me 🤖"
	message := ""

	if len(vr.CurationErrors) > 0 {
		message += "Your curation is invalid:\n"
	}
	if len(vr.CurationErrors) == 0 && len(vr.CurationWarnings) > 0 {
		message += "Your curation might have some problems:\n"
	}

	for _, e := range vr.CurationErrors {
		message += fmt.Sprintf("🚫 %s\n", e)
	}
	for _, w := range vr.CurationWarnings {
		message += fmt.Sprintf("🚫 %s\n", w)
	}

	c.Message = &message

	c.Action = constants.ActionRequestChanges
	if len(vr.CurationErrors) == 0 && len(vr.CurationWarnings) == 0 {
		c.Action = constants.ActionApprove
		c.Message = &approvalMessage
	}

	return c
}

func (s *SiteService) ReceiveComments(ctx context.Context, uid int64, sids []int64, formAction, formMessage, formIgnoreDupeActions string) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	var message *string
	if formMessage != "" {
		message = &formMessage
	}

	// TODO refactor these validators into a function and cover with tests
	actions := constants.GetAllowedActions()
	isActionValid := false
	for _, a := range actions {
		if formAction == a {
			isActionValid = true
			break
		}
	}

	if !isActionValid {
		return perr("invalid comment action", http.StatusBadRequest)
	}

	actionsWithMandatoryMessage := constants.GetActionsWithMandatoryMessage()
	isActionWithMandatoryMessage := false
	for _, a := range actionsWithMandatoryMessage {
		if formAction == a {
			isActionWithMandatoryMessage = true
			break
		}
	}

	if isActionWithMandatoryMessage && (message == nil || *message == "") {
		return perr(fmt.Sprintf("cannot post comment action '%s' without a message", formAction), http.StatusBadRequest)
	}

	ignoreDupeActions := false
	if formIgnoreDupeActions == "true" {
		ignoreDupeActions = true
	}

	utils.LogCtx(ctx).Debugf("searching submissions for comment batch")
	foundSubmissions, err := s.dal.SearchSubmissions(dbs, &types.SubmissionsFilter{SubmissionIDs: sids})
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	for _, sid := range sids {
		found := false
		for _, s := range foundSubmissions {
			if sid == s.SubmissionID {
				found = true
			}
		}
		if !found {
			return perr(fmt.Sprintf("submission %d not found", sid), http.StatusNotFound)
		}
	}

	// TODO optimize batch operation even more
SubmissionLoop:
	for _, submission := range foundSubmissions {
		sid := submission.SubmissionID

		uidIn := func(ids []int64) bool {
			for _, assignedUserID := range ids {
				if uid == assignedUserID {
					return true
				}
			}
			return false
		}

		// stop (or ignore) double actions
		if formAction == constants.ActionAssignTesting {
			if uidIn(submission.AssignedTestingUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are already assigned to test submission %d", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionUnassignTesting {
			if !uidIn(submission.AssignedTestingUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are not assigned to test submission %d", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionAssignVerification {
			if uidIn(submission.AssignedVerificationUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are already assigned to verify submission %d", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionUnassignVerification {
			if !uidIn(submission.AssignedVerificationUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are not assigned to verify submission %d", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionApprove {
			if uidIn(submission.ApprovedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already approved submission %d", sid), http.StatusBadRequest)
			}

		} else if formAction == constants.ActionRequestChanges {
			if uidIn(submission.RequestedChangesUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already requested changes on submission %d", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionVerify {
			if uidIn(submission.VerifiedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already verified submission %d", sid), http.StatusBadRequest)
			}

		}

		// don't let the same user assign the submission to himself for more than one type of assignment
		if formAction == constants.ActionAssignTesting {
			if uidIn(submission.AssignedVerificationUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are already assigned to verify submission %d so you cannot assign it for verification", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionAssignVerification {
			if uidIn(submission.AssignedTestingUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are already assigned to test submission %d so you cannot assign it for testing", sid), http.StatusBadRequest)
			}
		}

		// don't let the same user approve and verify the submission
		if formAction == constants.ActionAssignTesting {
			if uidIn(submission.VerifiedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already verified submission %d so you cannot assign it for testing", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionApprove {
			if uidIn(submission.VerifiedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already verified submission %d so you cannot approve it", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionAssignVerification {
			if uidIn(submission.ApprovedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already approved (tested) submission %d so you cannot assign it for verification", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionVerify {
			if uidIn(submission.ApprovedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already approved (tested) submission %d so you cannot verify it", sid), http.StatusBadRequest)
			}
		}

		// don't let users do actions without assigning first
		if formAction == constants.ActionApprove {
			if !uidIn(submission.AssignedTestingUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are not assigned to test submission %d so you cannot approve it", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionVerify {
			if !uidIn(submission.AssignedVerificationUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you are not assigned to verify submission %d so you cannot verify it", sid), http.StatusBadRequest)
			}
		}

		// don't let users verify before approve
		if formAction == constants.ActionAssignVerification {
			if len(submission.ApprovedUserIDs) == 0 {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("submission %d is not approved (tested) so you cannot assign it for verification", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionVerify {
			if len(submission.ApprovedUserIDs) == 0 {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("submission %d is not approved (tested) so you cannot verify it", sid), http.StatusBadRequest)
			}
		}

		// don't let users assign submission they have already confirmed to be good
		if formAction == constants.ActionAssignTesting {
			if uidIn(submission.ApprovedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already approved submission %d so you cannot assign it for testing", sid), http.StatusBadRequest)
			}
		} else if formAction == constants.ActionAssignVerification {
			if uidIn(submission.VerifiedUserIDs) {
				if ignoreDupeActions {
					continue SubmissionLoop
				}
				return perr(fmt.Sprintf("you have already verified submission %d so you cannot assign it for verification", sid), http.StatusBadRequest)
			}
		}

		// actually store the comment
		c := &types.Comment{
			AuthorID:     uid,
			SubmissionID: sid,
			Message:      message,
			Action:       formAction,
			CreatedAt:    s.clock.Now(),
		}

		if err := s.dal.StoreComment(dbs, c); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}

		// unassign if needed
		if formAction == constants.ActionApprove {
			c = &types.Comment{
				AuthorID:     uid,
				SubmissionID: sid,
				Message:      message,
				Action:       constants.ActionUnassignTesting,
				CreatedAt:    s.clock.Now().Add(time.Second),
			}

			if err := s.dal.StoreComment(dbs, c); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
		} else if formAction == constants.ActionVerify {
			c = &types.Comment{
				AuthorID:     uid,
				SubmissionID: sid,
				Message:      message,
				Action:       constants.ActionUnassignVerification,
				CreatedAt:    s.clock.Now().Add(time.Second),
			}

			if err := s.dal.StoreComment(dbs, c); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
		}

		if err := s.createNotification(dbs, uid, sid, formAction); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	s.announceNotification()

	return nil
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

func (s *SiteService) SoftDeleteSubmissionFile(ctx context.Context, sfid int64) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if err := s.dal.SoftDeleteSubmissionFile(dbs, sfid); err != nil {
		if err.Error() == constants.ErrorCannotDeleteLastSubmissionFile {
			return perr(err.Error(), http.StatusBadRequest)
		}
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) SoftDeleteSubmission(ctx context.Context, sid int64) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if err := s.dal.SoftDeleteSubmission(dbs, sid); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) SoftDeleteComment(ctx context.Context, cid int64) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	if err := s.dal.SoftDeleteComment(dbs, cid); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

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
