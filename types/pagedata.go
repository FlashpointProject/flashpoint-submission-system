package types

type BasePageData struct {
	Username      string
	UserID        int64
	AvatarURL     string
	UserRoles     []string
	IsDevInstance bool
}

type ProfilePageData struct {
	BasePageData
	NotificationActions []string
}
type MetadataStatsPageDataBare struct {
	TotalGames      int64
	TotalAnimations int64
	TotalTags       int64
	TotalPlatforms  int64
	TotalLegacy     int64
}
type MetadataStatsPageData struct {
	BasePageData
	MetadataStatsPageDataBare
}

type TagsPageData struct {
	BasePageData
	Tags       []*Tag
	Categories []*TagCategory
	TotalCount int64
}

type TagsPageDataJSON struct {
	Tags       []*Tag         `json:"tags"`
	Categories []*TagCategory `json:"categories"`
}

type PlatformsPageData struct {
	BasePageData
	Platforms  []*Platform
	TotalCount int64
}

type TagPageData struct {
	BasePageData
	Tag        *Tag
	Categories []*TagCategory
	Revisions  []*RevisionInfo
	GamesUsing int64
}

type GamePageData struct {
	BasePageData
	Game                *Game
	GameAvatarURL       string
	GameAuthorID        int64
	GameUsername        string
	Revisions           []*RevisionInfo
	LogoUrl             string
	ScreenshotUrl       string
	ImagesCdn           string
	RedirectsTo         string
	ValidDeleteReasons  []string
	ValidRestoreReasons []string
}

type GameDataIndexFile struct {
	SHA256 string `json:"sha256" example:"06c8bf04fd9a3d49fa9e1fe7bb54e4f085aae4163f7f9fbca55c8622bc2a6278"`
	SHA1   string `json:"sha1" example:"d435e0d0eefe30d437f0df41c926449077cab22e"`
	CRC32  string `json:"crc32" example:"b102ef01"`
	MD5    string `json:"md5" example:"d32d41389d088db60d177d731d83f839"`
	Path   string `json:"path" example:"content/uploads.ungrounded.net/59000/59593_alien_booya202c.swf"`
	Size   int64  `json:"size" example:"2037879"`
} // @name GameDataIndexFile

type GameDataIndex struct {
	GameID string              `json:"game_id" example:"08143aa7-f3ae-45b0-a1d4-afa4ac44c845"`
	Date   int64               `json:"date_added" example:"1704945196068"`
	Data   []GameDataIndexFile `json:"data"`
} // @name GameDataIndex

type GameDataPageData struct {
	BasePageData
	GameData *GameData
}

type GameDataIndexPageData struct {
	BasePageData
	Index *GameDataIndex
}

type GameRedirectsPageData struct {
	BasePageData
	GameRedirects []*GameRedirect
}

type SubmissionsPageData struct {
	BasePageData
	Submissions  []*ExtendedSubmission
	TotalCount   int64
	Filter       SubmissionsFilter
	FilterLayout string
}

type ApplyContentPatchPageData struct {
	BasePageData
	SubmissionID int64
	CurationMeta *CurationMeta
	ExistingMeta *Game
}

type EditSubmissionPageData struct {
	BasePageData
	SubmissionID int64
	ExistingMeta *CurationMeta
}

type ViewSubmissionPageData struct {
	SubmissionsPageData
	CurationMeta         *CurationMeta
	Comments             []*ExtendedComment
	IsUserSubscribed     bool
	CurationImageIDs     []int64
	NextSubmissionID     *int64
	PreviousSubmissionID *int64
	TagList              []Tag
}

type SubmissionsFilesPageData struct {
	BasePageData
	SubmissionFiles []*ExtendedSubmissionFile
}

type SearchFlashfreezePageData struct {
	BasePageData
	FlashfreezeFiles []*ExtendedFlashfreezeItem
	TotalCount       int64
	Filter           FlashfreezeFilter
}

type StatisticsPageData struct {
	BasePageData
	SubmissionCount             int64
	SubmissionCountBotHappy     int64
	SubmissionCountBotSad       int64
	SubmissionCountApproved     int64
	SubmissionCountVerified     int64
	SubmissionCountRejected     int64
	SubmissionCountInFlashpoint int64
	UserCount                   int64
	CommentCount                int64
	FlashfreezeCount            int64
	FlashfreezeFileCount        int64
	TotalSubmissionSize         int64
	TotalFlashfreezeSize        int64
}

type UserStatisticsPageData struct {
	BasePageData
	Users []*UserStatistics
}

type DeviceAuthStates struct {
	Pending  int64
	Complete int64
	Expired  int64
	Denied   int64
}

type DeviceAuthPageData struct {
	BasePageData
	Token  *DeviceFlowToken
	States DeviceAuthStates
	Scopes []AuthScope
}
