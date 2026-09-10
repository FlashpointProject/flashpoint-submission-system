package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

type Config struct {
	SubmissionDBEngine            string
	Port                          int64
	OauthConf                     *oauth2.Config
	HostBaseURL                   string
	AuthBotToken                  string
	FlashpointServerID            string
	SecurecookieHashKeyPrevious   string
	SecurecookieBlockKeyPrevious  string
	SecurecookieHashKeyCurrent    string
	SecurecookieBlockKeyCurrent   string
	SessionExpirationSeconds      int64
	ValidatorServerURL            string
	DBRootUser                    string
	DBRootPassword                string
	DBUser                        string
	DBPassword                    string
	DBIP                          string
	DBPort                        int64
	DBName                        string
	PostgresUser                  string
	PostgresPassword              string
	PostgresHost                  string
	PostgresPort                  int64
	NotificationBotToken          string
	NotificationChannelID         string
	CurationFeedChannelID         string
	IsDev                         bool
	ResumableUploadDirFullPath    string
	ArchiveIndexerServerURL       string
	SubmissionsDirFullPath        string
	SubmissionImagesDirFullPath   string
	SystemUid                     int64
	MinLauncherVersion            string
	DataPacksDir                  string
	FrozenPacksDir                string
	ImagesDir                     string
	DeletedDataPacksDir           string
	DeletedImagesDir              string
	FlashpointSourceOnlyMode      bool
	FlashpointSourceOnlyAdminMode bool
	RecommendationEngineURL       string
	DoNotUnfreezeGameList         []string
	IndexServiceUrl               string
}

func EnvString(name string) string {
	s := os.Getenv(name)
	if s == "" {
		panic(fmt.Sprintf("env variable '%s' is not set", name))
	}
	return s
}

func EnvInt(name string) int64 {
	s := os.Getenv(name)
	if s == "" {
		panic(fmt.Sprintf("env variable '%s' is not set", name))
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return i
}

func EnvBool(name string) bool {
	s := os.Getenv(name)
	if s == "" {
		panic(fmt.Sprintf("env variable '%s' is not set", name))
	} else if s == "True" {
		return true
	} else if s == "False" {
		return false
	}
	panic(fmt.Sprintf("invalid value of env variable '%s'", name))
}

func EnvJSONList(name string) []string {
	s := os.Getenv(name)
	if s == "" {
		panic(fmt.Sprintf("env variable '%s' is not set", name))
	}

	var result []string
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		panic(fmt.Sprintf("invalid json env variable '%s': %v", name, err))
	}

	return result
}

func GetConfig(l *logrus.Entry) *Config {
	engine := os.Getenv("SUBMISSION_DB_ENGINE")
	if engine == "" {
		engine = "mariadb"
	}
	if engine != "mariadb" && engine != "postgres" {
		panic("SUBMISSION_DB_ENGINE must be mariadb or postgres")
	}
	submissionString := func(name string) string {
		if engine == "postgres" {
			return os.Getenv(name)
		}
		return EnvString(name)
	}
	submissionInt := func(name string) int64 {
		if engine == "postgres" && os.Getenv(name) == "" {
			return 0
		}
		return EnvInt(name)
	}
	const ScopeIdentify = "identify"

	return &Config{
		SubmissionDBEngine: engine,
		Port:               EnvInt("PORT"),
		OauthConf: &oauth2.Config{
			RedirectURL:  EnvString("OAUTH_REDIRECT_URL"),
			ClientID:     EnvString("OAUTH_CLIENT_ID"),
			ClientSecret: EnvString("OAUTH_CLIENT_SECRET"),
			Scopes:       []string{ScopeIdentify},
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://discordapp.com/api/oauth2/authorize",
				TokenURL:  "https://discordapp.com/api/oauth2/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		HostBaseURL:                   EnvString("HOST_BASE_URL"),
		AuthBotToken:                  EnvString("AUTH_BOT_TOKEN"),
		FlashpointServerID:            EnvString("FLASHPOINT_SERVER_ID"),
		SecurecookieHashKeyPrevious:   EnvString("SECURECOOKIE_HASH_KEY_PREVIOUS"),
		SecurecookieBlockKeyPrevious:  EnvString("SECURECOOKIE_BLOCK_KEY_PREVIOUS"),
		SecurecookieHashKeyCurrent:    EnvString("SECURECOOKIE_HASH_KEY_CURRENT"),
		SecurecookieBlockKeyCurrent:   EnvString("SECURECOOKIE_BLOCK_KEY_CURRENT"),
		SessionExpirationSeconds:      EnvInt("SESSION_EXPIRATION_SECONDS"),
		ValidatorServerURL:            EnvString("VALIDATOR_SERVER_URL"),
		DBUser:                        submissionString("DB_USER"),
		DBPassword:                    submissionString("DB_PASSWORD"),
		DBIP:                          submissionString("DB_IP"),
		DBPort:                        submissionInt("DB_PORT"),
		DBName:                        submissionString("DB_NAME"),
		PostgresUser:                  EnvString("POSTGRES_USER"),
		PostgresPassword:              EnvString("POSTGRES_PASSWORD"),
		PostgresHost:                  EnvString("POSTGRES_HOST"),
		PostgresPort:                  EnvInt("POSTGRES_PORT"),
		NotificationBotToken:          EnvString("NOTIFICATION_BOT_TOKEN"),
		NotificationChannelID:         EnvString("NOTIFICATION_CHANNEL_ID"),
		CurationFeedChannelID:         EnvString("CURATION_FEED_CHANNEL_ID"),
		IsDev:                         EnvBool("IS_DEV"),
		ResumableUploadDirFullPath:    EnvString("RESUMABLE_UPLOAD_DIR_FULL_PATH"),
		ArchiveIndexerServerURL:       EnvString("ARCHIVE_INDEXER_SERVER_URL"),
		SubmissionsDirFullPath:        EnvString("SUBMISSIONS_DIR_FULL_PATH"),
		SubmissionImagesDirFullPath:   EnvString("SUBMISSION_IMAGES_DIR_FULL_PATH"),
		SystemUid:                     EnvInt("SYSTEM_UID"),
		MinLauncherVersion:            EnvString("MIN_LAUNCHER_VERSION"),
		DataPacksDir:                  EnvString("DATA_PACKS_PATH"),
		FrozenPacksDir:                EnvString("FROZEN_PACKS_PATH"),
		ImagesDir:                     EnvString("IMAGES_PATH"),
		DeletedDataPacksDir:           EnvString("DELETED_DATA_PACKS_PATH"),
		DeletedImagesDir:              EnvString("DELETED_IMAGES_PATH"),
		FlashpointSourceOnlyMode:      EnvBool("FLASHPOINT_SOURCE_ONLY_MODE"),
		FlashpointSourceOnlyAdminMode: EnvBool("FLASHPOINT_SOURCE_ONLY_ADMIN_MODE"),
		RecommendationEngineURL:       EnvString("RECOMMENDATION_ENGINE_URL"),
		DoNotUnfreezeGameList:         EnvJSONList("DO_NOT_UNFREEZE_GAME_LIST"),
		IndexServiceUrl:               EnvString("INDEX_SERVICE_URL"),
	}
}
