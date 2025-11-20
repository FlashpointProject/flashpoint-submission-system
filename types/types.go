package types

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

type EditCurationMeta struct {
	Title               *string `json:"Title"`
	AlternateTitles     *string `json:"AlternateTitles"`
	Version             *string `json:"Version"`
	Developer           *string `json:"Developer"`
	Publisher           *string `json:"Publisher"`
	ReleaseDate         *string `json:"ReleaseDate"`
	Series              *string `json:"Series"`
	Source              *string `json:"Source"`
	Status              *string `json:"Status"`
	Tags                *string `json:"Tags"`
	Languages           *string `json:"Languages"`
	OriginalDescription *string `json:"OriginalDescription"`
	GameNotes           *string `json:"GameNotes"`
}

type CurationMeta struct {
	SubmissionID        int64
	SubmissionFileID    int64
	GameExists          bool                     `json:"game_exists"`
	UUID                *string                  `json:"UUID"`
	ApplicationPath     *string                  `json:"Application Path"`
	Developer           *string                  `json:"Developer"`
	Extreme             *string                  `json:"Extreme"`
	GameNotes           *string                  `json:"Game Notes"`
	Languages           *string                  `json:"Languages"`
	LaunchCommand       *string                  `json:"Launch Command"`
	OriginalDescription *string                  `json:"Original Description"`
	PlayMode            *string                  `json:"Play Mode"`
	PrimaryPlatform     *string                  `json:"Primary Platform"`
	Platform            *string                  `json:"Platforms"`
	Publisher           *string                  `json:"Publisher"`
	ReleaseDate         *string                  `json:"Release Date"`
	Series              *string                  `json:"Series"`
	Source              *string                  `json:"Source"`
	Status              *string                  `json:"Status"`
	Tags                *string                  `json:"Tags"`
	TagCategories       *string                  `json:"Tag Categories"`
	Title               *string                  `json:"Title"`
	AlternateTitles     *string                  `json:"Alternate Titles"`
	Library             *string                  `json:"Library"`
	Version             *string                  `json:"Version"`
	CurationNotes       *string                  `json:"Curation Notes"`
	MountParameters     *string                  `json:"Mount Parameters"`
	Extras              *string                  `json:"Extras"`
	Message             *string                  `json:"Message"`
	AdditionalApps      []*CurationAdditionalApp `json:"Additional Applications"`
	RuffleSupport       *string                  `json:"Ruffle Support"`
}

type CurationAdditionalApp struct {
	Heading         *string `json:"Heading"`
	ApplicationPath *string `json:"Application Path"`
	LaunchCommand   *string `json:"Launch Command"`
}

type MasterDatabaseGame struct {
	UUID                string
	Title               *string
	AlternateTitles     *string
	Series              *string
	Developer           *string
	Publisher           *string
	Platform            *string
	Extreme             *string
	PlayMode            *string
	Status              *string
	GameNotes           *string
	Source              *string
	LaunchCommand       *string
	ReleaseDate         *string
	Version             *string
	OriginalDescription *string
	Languages           *string
	Library             *string
	Tags                *string
	DateAdded           time.Time
	DateModified        time.Time
}

type Comment struct {
	ID           int64
	AuthorID     int64
	SubmissionID int64
	Action       string
	Message      *string
	CreatedAt    time.Time
}

type SubmissionFile struct {
	ID               int64
	SubmitterID      int64
	SubmissionID     int64
	OriginalFilename string
	CurrentFilename  string
	Size             int64
	UploadedAt       time.Time
	MD5Sum           string
	SHA256Sum        string
}

type ExtendedSubmissionFile struct {
	FileID             int64
	SubmissionID       int64
	SubmitterID        int64
	SubmitterUsername  string
	SubmitterAvatarURL string
	OriginalFilename   string
	CurrentFilename    string
	Size               int64
	UploadedAt         time.Time
	MD5Sum             string
	SHA256Sum          string
}

type ExtendedSubmission struct {
	SubmissionID                int64
	SubmissionLevel             string
	SubmitterID                 int64     // oldest file
	SubmitterUsername           string    // oldest file
	SubmitterAvatarURL          string    // oldest file
	UpdaterID                   int64     // newest file
	UpdaterUsername             string    // newest file
	UpdaterAvatarURL            string    // newest file
	FileID                      int64     // newest file
	OriginalFilename            string    // newest file
	CurrentFilename             string    // newest file
	Size                        int64     // newest file
	UploadedAt                  time.Time // oldest file
	UpdatedAt                   time.Time // newest file
	LastUploaderID              int64     // newest file
	CurationTitle               *string   // newest file
	CurationAlternateTitles     *string   // newest file
	CurationPlatform            *string   // newest file
	CurationLaunchCommand       *string   // newest file
	CurationLibrary             *string   // newest file
	CurationExtreme             *string   // newest file
	BotAction                   string
	FileCount                   uint64
	AssignedTestingUserIDs      []int64
	AssignedVerificationUserIDs []int64
	RequestedChangesUserIDs     []int64
	ApprovedUserIDs             []int64
	VerifiedUserIDs             []int64
	DistinctActions             []string
	GameExists                  bool
	IsFrozen                    bool
	ShouldAutofreeze            bool
}

type SubmissionsFilter struct {
	SubmissionIDs                  []int64  `schema:"submission-id"`
	SubmitterID                    *int64   `schema:"submitter-id"`
	TitlePartial                   *string  `schema:"title-partial"`
	SubmitterUsernamePartial       *string  `schema:"submitter-username-partial"`
	PlatformPartial                *string  `schema:"platform-partial"`
	LibraryPartial                 *string  `schema:"library-partial"`
	OriginalFilenamePartialAny     *string  `schema:"original-filename-partial-any"`
	CurrentFilenamePartialAny      *string  `schema:"current-filename-partial-any"`
	MD5SumPartialAny               *string  `schema:"md5sum-partial-any"`
	SHA256SumPartialAny            *string  `schema:"sha256sum-partial-any"`
	BotActions                     []string `schema:"bot-action"`
	ResultsPerPage                 *int64   `schema:"results-per-page"`
	Page                           *int64   `schema:"page"`
	AssignedStatusTesting          *string  `schema:"assigned-status-testing"`
	AssignedStatusVerification     *string  `schema:"assigned-status-verification"`
	RequestedChangedStatus         *string  `schema:"requested-changes-status"`
	ApprovalsStatus                *string  `schema:"approvals-status"`
	VerificationStatus             *string  `schema:"verification-status"`
	SubmissionLevels               []string `schema:"sumbission-level"`
	AssignedStatusTestingMe        *string  `schema:"assigned-status-testing-me"`
	AssignedStatusVerificationMe   *string  `schema:"assigned-status-verification-me"`
	RequestedChangedStatusMe       *string  `schema:"requested-changes-status-me"`
	ApprovalsStatusMe              *string  `schema:"approvals-status-me"`
	VerificationStatusMe           *string  `schema:"verification-status-me"`
	AssignedStatusUserID           *int64   `schema:"assigned-status-user-id"`
	AssignedStatusTestingUser      *string  `schema:"assigned-status-testing-user"`
	AssignedStatusVerificationUser *string  `schema:"assigned-status-verification-user"`
	RequestedChangedStatusUser     *string  `schema:"requested-changes-status-user"`
	ApprovalsStatusUser            *string  `schema:"approvals-status-user"`
	VerificationStatusUser         *string  `schema:"verification-status-user"`
	IsExtreme                      *string  `schema:"is-extreme"`
	DistinctActions                []string `schema:"distinct-action"`
	DistinctActionsNot             []string `schema:"distinct-action-not"`
	LaunchCommandFuzzy             *string  `schema:"launch-command-fuzzy"`
	LastUploaderNotMe              *string  `schema:"last-uploader-not-me"`
	OrderBy                        *string  `schema:"order-by"`
	AscDesc                        *string  `schema:"asc-desc"`
	SubscribedMe                   *string  `schema:"subscribed-me"`
	IsContentChange                *string  `schema:"is-content-change"`
	ExcludeLegacy                  bool
	UpdatedByID                    *int64
	IsFrozen                       *string `schema:"is-frozen"`
}

func unzeroNilPointers(x interface{}) {
	v := reflect.ValueOf(x).Elem() // fucking schema zeroing out my nil pointers
	t := reflect.TypeOf(x).Elem()
	for i := 0; i < v.NumField(); i++ {
		if t.Field(i).Type.Kind() == reflect.Ptr {
			f := v.Field(i)
			e := f.Elem()
			if e.Kind() == reflect.Int64 && e.Int() == 0 {
				f.Set(reflect.Zero(f.Type()))
			}
			if e.Kind() == reflect.String && e.String() == "" {
				f.Set(reflect.Zero(f.Type()))
			}
		}
	}
}

func (sf *SubmissionsFilter) Validate() error {
	unzeroNilPointers(sf)

	for _, sid := range sf.SubmissionIDs {
		if sid < 1 {
			{
				return fmt.Errorf("submission id must be >= 1")
			}
		}
	}
	if sf.SubmitterID != nil && *sf.SubmitterID < 1 {
		if *sf.SubmitterID == 0 {
			sf.SubmitterID = nil
		} else {
			return fmt.Errorf("submitter id must be >= 1")
		}
	}
	if sf.ResultsPerPage != nil && *sf.ResultsPerPage < 1 {
		if *sf.ResultsPerPage == 0 {
			sf.ResultsPerPage = nil
		} else {
			return fmt.Errorf("results per page must be >= 1")
		}
	}
	if sf.Page != nil && *sf.Page < 1 {
		if *sf.Page == 0 {
			sf.Page = nil
		} else {
			return fmt.Errorf("page must be >= 1")
		}
	}

	if sf.AssignedStatusTesting != nil && *sf.AssignedStatusTesting != "unassigned" && *sf.AssignedStatusTesting != "assigned" {
		return fmt.Errorf("invalid assigned-status-testing")
	}
	if sf.AssignedStatusVerification != nil && *sf.AssignedStatusVerification != "unassigned" && *sf.AssignedStatusVerification != "assigned" {
		return fmt.Errorf("invalid assigned-status-verification")
	}
	if sf.AssignedStatusTesting != nil && *sf.AssignedStatusTesting != "unassigned" && *sf.AssignedStatusTesting != "assigned" {
		return fmt.Errorf("invalid assigned-status")
	}
	if sf.RequestedChangedStatus != nil && *sf.RequestedChangedStatus != "none" && *sf.RequestedChangedStatus != "ongoing" {
		return fmt.Errorf("invalid requested-changes-status")
	}
	if sf.ApprovalsStatus != nil && *sf.ApprovalsStatus != "none" && *sf.ApprovalsStatus != "approved" {
		return fmt.Errorf("invalid approvals-status")
	}
	if sf.VerificationStatus != nil && *sf.VerificationStatus != "none" && *sf.VerificationStatus != "verified" {
		return fmt.Errorf("invalid verificaton-status")
	}

	if sf.AssignedStatusTestingMe != nil && *sf.AssignedStatusTestingMe != "unassigned" && *sf.AssignedStatusTestingMe != "assigned" {
		return fmt.Errorf("invalid assigned-status-testing-me")
	}
	if sf.AssignedStatusVerificationMe != nil && *sf.AssignedStatusVerificationMe != "unassigned" && *sf.AssignedStatusVerificationMe != "assigned" {
		return fmt.Errorf("invalid assigned-status-verification-me")
	}
	if sf.RequestedChangedStatusMe != nil && *sf.RequestedChangedStatusMe != "none" && *sf.RequestedChangedStatusMe != "ongoing" {
		return fmt.Errorf("invalid requested-changes-status-me")
	}
	if sf.ApprovalsStatusMe != nil && *sf.ApprovalsStatusMe != "no" && *sf.ApprovalsStatusMe != "yes" {
		return fmt.Errorf("invalid approvals-status-me")
	}
	if sf.VerificationStatusMe != nil && *sf.VerificationStatusMe != "no" && *sf.VerificationStatusMe != "yes" {
		return fmt.Errorf("invalid verificaton-status-me")
	}

	if sf.AssignedStatusUserID != nil && *sf.AssignedStatusUserID < 1 {
		if *sf.AssignedStatusUserID == 0 {
			sf.AssignedStatusUserID = nil
		} else {
			return fmt.Errorf("assigned-status-user-id id must be >= 1")
		}
	}
	if sf.AssignedStatusTestingUser != nil && *sf.AssignedStatusTestingUser != "unassigned" && *sf.AssignedStatusTestingUser != "assigned" {
		return fmt.Errorf("invalid assigned-status-testing-user")
	}
	if sf.AssignedStatusVerificationUser != nil && *sf.AssignedStatusVerificationUser != "unassigned" && *sf.AssignedStatusVerificationUser != "assigned" {
		return fmt.Errorf("invalid assigned-status-verification-user")
	}
	if sf.RequestedChangedStatusUser != nil && *sf.RequestedChangedStatusUser != "none" && *sf.RequestedChangedStatusUser != "ongoing" {
		return fmt.Errorf("invalid requested-changes-status-user")
	}
	if sf.ApprovalsStatusUser != nil && *sf.ApprovalsStatusUser != "no" && *sf.ApprovalsStatusUser != "yes" {
		return fmt.Errorf("invalid approvals-status-user")
	}
	if sf.VerificationStatusUser != nil && *sf.VerificationStatusUser != "no" && *sf.VerificationStatusUser != "yes" {
		return fmt.Errorf("invalid verificaton-status-user")
	}
	if sf.AssignedStatusUserID != nil && !(sf.AssignedStatusTestingUser != nil ||
		sf.AssignedStatusVerificationUser != nil ||
		sf.RequestedChangedStatusUser != nil ||
		sf.ApprovalsStatusUser != nil ||
		sf.VerificationStatusUser != nil) {
		return fmt.Errorf("assigned-status-user-id must be set when any of the relevant *user filters are set")
	}

	if sf.LastUploaderNotMe != nil && *sf.LastUploaderNotMe != "yes" {
		return fmt.Errorf("last-uploader-not-me")
	}
	if sf.OrderBy != nil && *sf.OrderBy != "uploaded" && *sf.OrderBy != "updated" && *sf.OrderBy != "size" {
		return fmt.Errorf("invalid order-by")
	}
	if sf.AscDesc != nil && *sf.AscDesc != "asc" && *sf.AscDesc != "desc" {
		return fmt.Errorf("invalid asc-desc")
	}
	if sf.SubscribedMe != nil && *sf.SubscribedMe != "no" && *sf.SubscribedMe != "yes" {
		return fmt.Errorf("invalid subscribed-me")
	}
	return nil
}

type ExtendedComment struct {
	CommentID    int64
	AuthorID     int64
	Username     string
	AvatarURL    string
	SubmissionID int64
	Action       string
	Message      *string
	CreatedAt    time.Time
}

type UpdateNotificationSettings struct {
	NotificationActions []string `schema:"notification-action"`
}

type UpdateSubscriptionSettings struct {
	Subscribe bool `schema:"subscribe"`
}

type Notification struct {
	ID        int64
	Type      string
	Message   string
	CreatedAt time.Time
	SentAt    time.Time
}

type CurationImage struct {
	ID               int64
	SubmissionFileID int64
	Type             string
	Filename         string
}

type ValidatorResponseImage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type ValidatorRepackResponse struct {
	Error    *string                  `json:"error,omitempty"`
	FilePath *string                  `json:"path,omitempty"`
	Meta     CurationMeta             `json:"meta"`
	Images   []ValidatorResponseImage `json:"images"`
}

type ValidatorResponse struct {
	Filename         string                   `json:"filename"`
	Path             string                   `json:"path"`
	CurationErrors   []string                 `json:"curation_errors"`
	CurationWarnings []string                 `json:"curation_warnings"`
	IsExtreme        bool                     `json:"is_extreme"`
	CurationType     int                      `json:"curation_type"`
	Meta             CurationMeta             `json:"meta"`
	Images           []ValidatorResponseImage `json:"images"`
}

type ValidatorEditMetaResponse struct {
	Filename         string   `json:"filename"`
	Path             string   `json:"path"`
	CurationErrors   []string `json:"curation_errors"`
	CurationWarnings []string `json:"curation_warnings"`
}

type ReceiveFileTempNameResp struct {
	Message  string  `json:"message"`
	TempName *string `json:"temp_name"`
}

type ReceiveFileResp struct {
	Message string  `json:"message"`
	URL     *string `json:"url"`
}

type SimilarityAttributes struct {
	ID                 string
	Title              *string
	LaunchCommand      *string
	TitleRatio         float64
	LaunchCommandRatio float64
}

type DeletedGame struct {
	ID           string    `json:"id"`
	DateModified time.Time `json:"date_modified"`
	Reason       string    `json:"reason"`
} // @name DeletedGame

type GameDump struct {
	ID              string           `json:"id"`
	ParentGameID    *string          `json:"parent_game_id,omitempty"`
	Title           string           `json:"title"`
	AlternateTitles string           `json:"alternate_titles"`
	Series          string           `json:"series"`
	Developer       string           `json:"developer"`
	Publisher       string           `json:"publisher"`
	PrimaryPlatform string           `json:"platform_name,omitempty"`
	Platforms       []*Platform      `json:"platforms,omitempty"`
	DateAdded       time.Time        `json:"date_added"`
	DateModified    time.Time        `json:"date_modified"`
	PlayMode        string           `json:"play_mode"`
	Status          string           `json:"status"`
	Notes           string           `json:"notes"`
	Tags            []*Tag           `json:"tags,omitempty"`
	Source          string           `json:"source"`
	ApplicationPath string           `json:"legacy_application_path"`
	LaunchCommand   string           `json:"legacy_launch_command"`
	ReleaseDate     string           `json:"release_date"`
	Version         string           `json:"version"`
	OriginalDesc    string           `json:"original_description"`
	Language        string           `json:"language"`
	Library         string           `json:"library"`
	AddApps         []*AdditionalApp `json:"add_apps"`
	ActiveDataID    *int             `json:"active_data_id,omitempty"`
	Data            []*GameData      `json:"data,omitempty"`
	RuffleSupport   string           `json:"ruffle_support,omitempty"`
	LogoPath        string           `json:"logo_path,omitempty"`
	ScreenshotPath  string           `json:"screenshot_path,omitempty"`
	Action          string           `json:"action"`
	Reason          string           `json:"reason"`
	Deleted         bool
	UserID          int64
}

type Game struct {
	ID              string           `json:"id" example:"08143aa7-f3ae-45b0-a1d4-afa4ac44c845"`
	ParentGameID    *string          `json:"parent_game_id,omitempty"`
	Title           string           `json:"title" example:"Alien Hominid"`
	AlternateTitles string           `json:"alternate_titles"`
	Series          string           `json:"series" example:""`
	Developer       string           `json:"developer" example:"Dan Paladin / DanPaladin / Synj; Tom Fulp / TomFulp; FDA"`
	Publisher       string           `json:"publisher" example:"Newgrounds"`
	PrimaryPlatform string           `json:"platform_name,omitempty" example:"Flash"`
	Platforms       []*Platform      `json:"platforms,omitempty"`
	PlatformsStr    string           `json:"platforms_str" example:"Flash"`
	DateAdded       time.Time        `json:"date_added" example:"2018-01-12T02:13:56.633Z"`
	DateModified    time.Time        `json:"date_modified" example:"2024-11-07T20:10:17.239011Z"`
	PlayMode        string           `json:"play_mode" example:"Single Player"`
	Status          string           `json:"status" example:"Playable"`
	Notes           string           `json:"notes" example:""`
	Tags            []*Tag           `json:"tags,omitempty"`
	TagsStr         string           `json:"tags_str" example:"Alien Hominid; Action; Arcade; Beat 'Em Up; Platformer; Run 'n' Gun; Score-Attack; Shooter; Cartoon; Officially Licensed; Side View; Alien; Blood; Moderate Violence"`
	Source          string           `json:"source" example:"https://www.newgrounds.com/portal/view/59593"`
	ApplicationPath string           `json:"application_path" example:""`
	LaunchCommand   string           `json:"launch_command" example:""`
	ReleaseDate     string           `json:"release_date" example:"2002-08-07"`
	Version         string           `json:"version" example:""`
	OriginalDesc    string           `json:"original_description" example:"Alien Hominid HD is now available on Xbox 360 Live Arcade! Go try it and buy it!\n\nYour UFO has crash landed, and the FBI is out to get you! Time to take them out!\n\nProgramming by Tom Fulp of Newgrounds.com!\nArt by Dan Paladin!\n\nControls:\nUse the arrows to run around and aim your gun. The 'a' key shoots and the 's' key jumps. When jumping over an enemy, press DOWN and 's' to do a freak attack!\n\n****HINTS****\n* You can ride enemy heads past roadblocks. they can run right through while they are freaking out!\n\n* Eat enemy skulls in front of other enemies while on their shoulders -- their friend's reaction will give you a free cheapshot!\n\n* If all else fails, you can try crawling your way to the end like the scum you are! haha\n\n8/20/02 UPDATE:\nFixed grenade / Freak Attack Glitch\nFixed CAPS LOCK issues\nRemoved first grenade guy (now just 1)\nAdded first level intro cinema!"`
	Language        string           `json:"language" example:"en"`
	Library         string           `json:"library" example:"arcade"`
	AddApps         []*AdditionalApp `json:"add_apps"`
	ActiveDataID    *int             `json:"active_data_id,omitempty"`
	Data            []*GameData      `json:"data,omitempty"`
	Action          string           `json:"action" example:"update"`
	Reason          string           `json:"reason" example:"User changed metadata"`
	ArchiveState    ArchiveState     `json:"archive_state" example:"2"`
	RuffleSupport   string           `json:"ruffle_support" example:"Standalone"`
	LogoPath        string           `json:"logo_path"`
	ScreenshotPath  string           `json:"screenshot_path"`
	Deleted         bool
	UserID          int64 `example:"529007944449261600"`
} // @name Game

type GameSlimInfo struct {
	ID              string    `json:"id" example:"08143aa7-f3ae-45b0-a1d4-afa4ac44c845"`
	Title           string    `json:"title" example:"Alien Hominid"`
	PrimaryPlatform string    `json:"platform_name,omitempty" example:"Flash"`
	DateAdded       time.Time `json:"date_added" example:"2018-01-12T02:13:56.633Z"`
} // @name GameSlim

type GameData struct {
	ID              int       `json:"id,omitempty"`
	GameID          string    `json:"game_id"`
	Title           string    `json:"title"`
	DateAdded       time.Time `json:"date_added,omitempty"`
	SHA256          string    `json:"sha_256"`
	CRC32           int       `json:"crc_32"`
	Size            int64     `json:"size"`
	Parameters      *string   `json:"parameters"`
	ApplicationPath string    `json:"application_path"`
	LaunchCommand   string    `json:"launch_command"`
	Indexed         bool      `json:"indexed"`
	IndexError      bool      `json:"index_error"`
} // @name GameData

type AdditionalApp struct {
	ID              string `json:"id,omitempty"`
	ApplicationPath string `json:"application_path"`
	AutoRunBefore   bool   `json:"auto_run_before"`
	LaunchCommand   string `json:"launch_command"`
	Name            string `json:"name"`
	WaitForExit     bool   `json:"wait_for_exit"`
	ParentGameID    string `json:"parent_game_id"`
} // @name AdditionalApp

type Platform struct {
	ID           int64     `json:"id" example:"24"`
	Name         string    `json:"name" example:"Flash"`
	Description  string    `json:"description" example:""`
	DateModified time.Time `json:"date_modified" example:"2023-04-26T19:27:31.849994Z"`
	Aliases      *string   `json:"aliases,omitempty" example:"Flash"`
	Action       string    `json:"action" example:""`
	Reason       string    `json:"reason" example:""`
	Deleted      bool
	UserID       int64 `json:"user_id" example:"810112564787675100"`
} // @name Platform

type PlatformAlias struct {
	PlatformID int64  `json:"platform_id"`
	Name       string `json:"name"`
} // @name PlatformAlias

type Tag struct {
	ID           int64     `json:"id" example:"6"`
	Name         string    `json:"name" example:"Action"`
	Description  string    `json:"description" example:""`
	DateModified time.Time `json:"date_modified" example:"2023-04-26T19:27:31.849994Z"`
	Category     string    `json:"category" example:"genre"`
	Aliases      *string   `json:"aliases,omitempty" example:"Action"`
	Action       string    `json:"action" example:"create"`
	Reason       string    `json:"reason" example:"Database Import"`
	Deleted      bool
	UserID       int64 `json:"user_id" example:"810112564787675166"`
} // @name Tag

type TagAlias struct {
	TagID int64  `json:"tag_id"`
	Name  string `json:"name"`
} // @name TagAlias

type TagCategory struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
} // @name TagCategory

type LauncherDumpRelation struct {
	GameID string `json:"g"`
	Value  string `json:"v"`
}

type LauncherDump struct {
	Games             LauncherDumpGames      `json:"games"`
	Tags              LauncherDumpTags       `json:"tags"`
	Platforms         LauncherDumpPlatforms  `json:"platforms"`
	TagRelations      []LauncherDumpRelation `json:"tag_relations"`
	PlatformRelations []LauncherDumpRelation `json:"platform_relations"`
}

type LauncherDumpGames struct {
	AddApps  []AdditionalApp `json:"add_apps"`
	GameData []GameData      `json:"game_data"`
	Games    []GameDump      `json:"games"`
}

type LauncherDumpTags struct {
	Categories []TagCategory         `json:"categories"`
	Aliases    []TagAlias            `json:"aliases"`
	Tags       []LauncherDumpTagsTag `json:"tags"`
}

type LauncherDumpPlatforms struct {
	Aliases   []PlatformAlias                 `json:"aliases"`
	Platforms []LauncherDumpPlatformsPlatform `json:"platforms"`
}

type LauncherDumpTagsAliases struct {
	TagID int64  `json:"tagId"`
	Name  string `json:"name"`
}

type LauncherDumpTagsTag struct {
	ID           int64  `json:"id"`
	CategoryID   int64  `json:"category_id"`
	Description  string `json:"description"`
	PrimaryAlias string `json:"primary_alias"`
}

type LauncherDumpPlatformsPlatform struct {
	ID           int64  `json:"id"`
	Description  string `json:"description"`
	PrimaryAlias string `json:"primary_alias"`
}

func (t *LauncherDumpTagsTag) UnmarshalJSON(data []byte) error {
	type Alias LauncherDumpTagsTag
	aux := &struct {
		*Alias
		Description *string `json:"description"`
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Description != nil {
		t.Description = *aux.Description
	} else {
		t.Description = ""
	}
	return nil
}

func (p *LauncherDumpPlatformsPlatform) UnmarshalJSON(data []byte) error {
	type Alias LauncherDumpPlatformsPlatform
	aux := &struct {
		*Alias
		Description *string `json:"description"`
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Description != nil {
		p.Description = *aux.Description
	} else {
		p.Description = ""
	}
	return nil
}

type ValidatorTagResponse struct {
	Tags []Tag `json:"tags"`
}

type ResumableParams struct {
	ResumableChunkNumber      int    `schema:"resumableChunkNumber"`
	ResumableChunkSize        uint64 `schema:"resumableChunkSize"`
	ResumableTotalSize        int64  `schema:"resumableTotalSize"`
	ResumableIdentifier       string `schema:"resumableIdentifier"`
	ResumableFilename         string `schema:"resumableFilename"`
	ResumableRelativePath     string `schema:"resumableRelativePath"`
	ResumableCurrentChunkSize int64  `schema:"resumableCurrentChunkSize"`
	ResumableTotalChunks      int    `schema:"resumableTotalChunks"`
}

type FlashfreezeFile struct {
	ID               int64
	UserID           int64
	OriginalFilename string
	CurrentFilename  string
	Size             int64
	UploadedAt       time.Time
	MD5Sum           string
	SHA256Sum        string
}

type IndexerResp struct {
	ArchiveFilename string              `json:"archive_filename"`
	Files           []*IndexedFileEntry `json:"files"`
	IndexingErrors  uint64              `json:"indexing_errors"`
}

type IndexedFileEntry struct {
	Name             string `json:"name"`
	SizeCompressed   int64  `json:"size_compressed"`
	SizeUncompressed int64  `json:"size_uncompressed"`
	FileUtilOutput   string `json:"file_util_output"`
	SHA256           string `json:"sha256"`
	MD5              string `json:"md5"`
}

type ExtendedFlashfreezeItem struct {
	FileID            int64
	SubmitterID       int64
	SubmitterUsername string
	OriginalFilename  string
	MD5Sum            string
	SHA256Sum         string
	Size              int64
	UploadedAt        *time.Time // only for root files
	Description       *string    // only for inner files
	IsRootFile        bool
	IsDeepFile        bool
	IndexingTime      *time.Duration // only for root files
	FileCount         *int64         // only for root files
	IndexingErrors    *int64         // only for root files
}

type FlashfreezeFilter struct {
	FileIDs     []int64 `schema:"file-id"`
	SubmitterID *int64  `schema:"submitter-id"`

	NameFulltext        *string `schema:"name-fulltext"`
	DescriptionFulltext *string `schema:"description-fulltext"` // only for inner files

	NamePrefix        *string `schema:"name-prefix"`
	DescriptionPrefix *string `schema:"description-prefix"` // only for inner files

	SizeMin *int64 `schema:"size-min"`
	SizeMax *int64 `schema:"size-max"`

	SubmitterUsernamePartial *string `schema:"submitter-username-partial"`
	MD5SumPartial            *string `schema:"md5sum-partial"`
	SHA256SumPartial         *string `schema:"sha256sum-partial"`

	SearchFiles            *bool `schema:"search-files"`
	SearchFilesRecursively *bool `schema:"search-files-recursively"`

	ResultsPerPage *int64 `schema:"results-per-page"`
	Page           *int64 `schema:"page"`
}

func (ff *FlashfreezeFilter) Validate() error {

	v := reflect.ValueOf(ff).Elem() // fucking schema zeroing out my nil pointers
	t := reflect.TypeOf(ff).Elem()
	for i := 0; i < v.NumField(); i++ {
		if t.Field(i).Type.Kind() == reflect.Ptr {
			f := v.Field(i)
			e := f.Elem()
			if e.Kind() == reflect.Int64 && e.Int() == 0 {
				f.Set(reflect.Zero(f.Type()))
			}
			if e.Kind() == reflect.String && e.String() == "" {
				f.Set(reflect.Zero(f.Type()))
			}
		}
	}

	if ff.SubmitterID != nil && *ff.SubmitterID < 1 {
		if *ff.SubmitterID == 0 {
			ff.SubmitterID = nil
		} else {
			return fmt.Errorf("submitter id must be >= 1")
		}
	}
	if ff.SizeMin != nil && *ff.SizeMin < 1 {
		if *ff.SizeMin == 0 {
			ff.SizeMin = nil
		} else {
			return fmt.Errorf("size-uncompressed-min must be >= 1")
		}
	}
	if ff.SizeMax != nil && *ff.SizeMax < 1 {
		if *ff.SizeMax == 0 {
			ff.SizeMax = nil
		} else {
			return fmt.Errorf("size-uncompressed-max must be >= 1")
		}

		if ff.SizeMin != nil && *ff.SizeMin > *ff.SizeMax {
			return fmt.Errorf("size-uncompressed-min cannot be greater than size-uncompressed-max")
		}
	}

	if ff.ResultsPerPage != nil && *ff.ResultsPerPage < 1 {
		if *ff.ResultsPerPage == 0 {
			ff.ResultsPerPage = nil
		} else {
			return fmt.Errorf("results per page must be >= 1")
		}
	}
	if ff.Page != nil && *ff.Page < 1 {
		if *ff.Page == 0 {
			ff.Page = nil
		} else {
			return fmt.Errorf("page must be >= 1")
		}
	}

	return nil
}

type DeleteUserSessionsRequest struct {
	DiscordID int64 `schema:"discord-user-id"`
}

type GameContentPatch struct {
	Title           *string `json:"Title,omitempty"`
	AlternateTitles *string `json:"AlternateTitles,omitempty"`
	Series          *string `json:"Series,omitempty"`
	Developer       *string `json:"Developer,omitempty"`
	Publisher       *string `json:"Publisher,omitempty"`
	PlayMode        *string `json:"PlayMode,omitempty"`
	Status          *string `json:"status,omitempty"`
	Notes           *string `json:"Notes,omitempty"`
	Source          *string `json:"Source,omitempty"`
	ReleaseDate     *string `json:"ReleaseDate,omitempty"`
	Version         *string `json:"Version,omitempty"`
	OriginalDesc    *string `json:"OriginalDesc,omitempty"`
	Language        *string `json:"Language,omitempty"`
	Library         *string `json:"Library,omitempty"`
	RuffleSupport   *string `json:"RuffleSupport,omitempty"`
}

type GameCountSinceDateJSON struct {
	Total int `json:"total"`
}

type GamesDeletedSinceDateJSON struct {
	Games []*DeletedGame `json:"games"`
}

type GamePageResJSON struct {
	Games             []*Game          `json:"games"`
	AddApps           []*AdditionalApp `json:"add_apps"`
	GameData          []*GameData      `json:"game_data"`
	TagRelations      [][]string       `json:"tag_relations"`
	PlatformRelations [][]string       `json:"platform_relations"`
}

type UserStatistics struct {
	UserID           string
	Username         string
	Role             string
	LastUserActivity time.Time
	// these are actions by the user
	UserCommentedCount         int64
	UserRequestedChangesCount  int64
	UserApprovedCount          int64
	UserVerifiedCount          int64
	UserAddedToFlashpointCount int64
	UserRejectedCount          int64
	// these are action of other users on this user's submissions, and the latest state is counted (so, a verified submission is not counted as approved)
	SubmissionsCount                  int64
	SubmissionsBotHappyCount          int64
	SubmissionsBotUnhappyCount        int64
	SubmissionsRequestedChangesCount  int64
	SubmissionsApprovedCount          int64
	SubmissionsVerifiedCount          int64
	SubmissionsAddedToFlashpointCount int64
	SubmissionsRejectedCount          int64
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type SubmissionStatus struct {
	Status       string  `json:"status"`
	Message      *string `json:"message"`
	SubmissionID *int64  `json:"submission_id"`
}

type IndexMatchPathResult struct {
	Paths   []string          `json:"paths" example:"content/uploads.ungrounded.net/59000/"`
	Games   []*GameSlimInfo   `json:"games"`
	Matches []*IndexMatchData `json:"data"`
} // @name IndexPathResponse

type IndexMatchResult struct {
	Results []*IndexMatchResultData `json:"results"`
}

type IndexMatchResultData struct {
	HashType string            `json:"type" example:"md5"`
	Hash     string            `json:"hash" example:"d32d41389d088db60d177d731d83f839"`
	Games    []*GameSlimInfo   `json:"games"`
	Matches  []*IndexMatchData `json:"data"`
} // @name IndexHashResponse

type IndexMatchData struct {
	SHA256 string `json:"sha256" example:"06c8bf04fd9a3d49fa9e1fe7bb54e4f085aae4163f7f9fbca55c8622bc2a6278"`
	SHA1   string `json:"sha1" example:"d435e0d0eefe30d437f0df41c926449077cab22e"`
	CRC32  string `json:"crc32" example:"b102ef01"`
	MD5    string `json:"md5" example:"d32d41389d088db60d177d731d83f839"`
	Path   string `json:"path" example:"content/uploads.ungrounded.net/59000/59593_alien_booya202c.swf"`
	Size   int64  `json:"size" example:"2037879"`
	GameID string `json:"game_id" example:"08143aa7-f3ae-45b0-a1d4-afa4ac44c845"`
	Date   int64  `json:"date_added" example:"1704945196068"`
} // @name IndexMatch

type GameRedirect struct {
	SourceId  string    `json:"source_id"`
	DestId    string    `json:"id"`
	DateAdded time.Time `json:"date_added"`
}

type NotContentPatch struct {
}

func (ncp NotContentPatch) Error() string {
	return "Not a content patch"
}

type RepackError string

func (ce RepackError) Error() string {
	return fmt.Sprintf("Error repacking submission: %s", string(ce))
}

type NotEnoughImages string

func (nei NotEnoughImages) Error() string {
	return fmt.Sprintf("Submission does not have 2 images: has %s", string(nei))
}

type InvalidTagUpdate struct {
}

func (itu InvalidTagUpdate) Error() string {
	return "Invalid tag aliases"
}

type NoGameDataFound struct {
}

func (ngdf NoGameDataFound) Error() string {
	return "No Game Data Found"
}

type InvalidAddApps struct {
}

func (iaa InvalidAddApps) Error() string {
	return "Add app invalid"
}

type MissingLaunchParams struct {
}

func (mlp MissingLaunchParams) Error() string {
	return "Missing application path or launch command"
}

type RevisionInfo struct {
	Action    string
	Reason    string
	CreatedAt time.Time
	AvatarURL string
	AuthorID  int64
	Username  string
	Game      *Game
	Diff      *RevisionDiff
}

type RevisionDiff struct {
	ParentGameID    *RevisionDiffField
	Title           *RevisionDiffField
	AlternateTitles *RevisionDiffField
	Series          *RevisionDiffField
	Developer       *RevisionDiffField
	Publisher       *RevisionDiffField
	PrimaryPlatform *RevisionDiffField
	Platforms       *RevisionDiffField // String representation via custom function
	PlayMode        *RevisionDiffField
	Status          *RevisionDiffField
	Notes           *RevisionDiffField
	Tags            *RevisionDiffField // String representation via custom function
	Source          *RevisionDiffField
	ApplicationPath *RevisionDiffField
	LaunchCommand   *RevisionDiffField
	ReleaseDate     *RevisionDiffField
	Version         *RevisionDiffField
	OriginalDesc    *RevisionDiffField
	Language        *RevisionDiffField
	Library         *RevisionDiffField
	AddApps         *RevisionDiffField // String representation via custom function
	ActiveDataID    *RevisionDiffFieldNumber
	Data            *RevisionDiffField // String representation via custom function
	ArchiveState    *RevisionDiffField
	RuffleSupport   *RevisionDiffField
	LogoPath        *RevisionDiffField
	ScreenshotPath  *RevisionDiffField
}

type RevisionDiffField struct {
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

type RevisionDiffFieldNumber struct {
	Previous *int `json:"previous"`
	Current  *int `json:"current"`
}

type ArchiveState int8

const (
	NotArchived ArchiveState = iota
	Archived
	Available
)

func (as ArchiveState) String() string {
	switch as {
	case NotArchived:
		return "Not Archived"
	case Archived:
		return "Archived"
	case Available:
		return "Available"
	}
	return "Unknown"
}

type FetchGamesRequest struct {
	GameIDs []string `json:"game_ids"`
}

type ActivityEventsFilter struct {
	UserID int64 `schema:"uid"`
	From   int64 `schema:"from"`
	To     int64 `schema:"to"`
}

type AddGameRedirectRequest struct {
	SourceId string `json:"sourceId"`
	DestId   string `json:"destId"`
}

type IndexPathRequest struct {
	Path string `json:"path"`
} // @name IndexPathRequest

type AutounfreezerGame struct {
	GameID      string
	ReleaseDate string
}
