package types

import (
	"time"
)

const (
	DeviceFlowPending      = 0
	DeviceFlowErrorExpired = 1
	DeviceFlowErrorDenied  = 2
	DeviceFlowComplete     = 3
)

const (
	AuthCodePending  = 0
	AuthCodeComplete = 1
)

type AuthCodeToken struct {
	Code        string
	UserID      int64
	RedirectUri string
	ClientID    string
	ExpiresAt   time.Time
	Scope       string
	IPAddr      string
	State       int64
}

type AuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type DeviceFlowToken struct {
	DeviceCode              string             `json:"device_code"`
	Scope                   string             `json:"-"`
	UserCode                string             `json:"user_code"`
	VerificationURI         string             `json:"verification_uri"`
	VerificationURIComplete string             `json:"verification_uri_complete"`
	ExpiresIn               int64              `json:"expires_in"`
	Interval                int64              `json:"interval"`
	ClientApplication       *ClientApplication `json:"-"`
	ExpiresAt               time.Time          `json:"-"`
	FlowState               int64              `json:"-"`
	AuthToken               map[string]string  `json:"-"` // Explictly add this to responses when suitable
}

type DeviceFlowPollResponse struct {
	Error string `json:"error,omitempty"`
	Token string `json:"access_token,omitempty"`
}

type AuthScope struct {
	Name        string
	Description string
}

const (
	AuthScopeNone                 = ""
	AuthScopeAll                  = "all"
	AuthScopeIdentity             = "identity"
	AuthScopeProfileEdit          = "profile:edit"
	AuthScopeProfileAppsRead      = "profile:apps:read"
	AuthScopeUsersRead            = "users:read"
	AuthScopeSubmissionRead       = "submission:read"
	AuthScopeSubmissionReadFiles  = "submission:read-files"
	AuthScopeSubmissionEdit       = "submission:edit"
	AuthScopeSubmissionUpload     = "submission:upload"
	AuthScopeTagEdit              = "tag:edit"
	AuthScopeGameDataRead         = "game-data:read"
	AuthScopeGameDataEdit         = "game-data:edit"
	AuthScopeGameRead             = "game:read"
	AuthScopeGameEdit             = "game:edit"
	AuthScopeIndexRead            = "index:read"
	AuthScopeRedirectEdit         = "redirect:edit"
)

type ClientApplication struct {
	UserID            int64    `json:"user_id"`
	UserRoles         []string `json:"user_roles"`
	ClientId          string   `json:"client_id"`
	Name              string   `json:"name"`
	ClientCredsScopes []string `json:"client_creds_scopes"`
	Scopes            []string `json:"scopes"`
	RedirectURIs      []string `json:"redirect_uris"`
	OwnerUID          int64    `json:"owner_uid"`
}

type SessionInfo struct {
	ID        int64  `json:"id"`
	UID       int64  `json:"uid"`
	Scope     string `json:"scope"`
	Client    string `json:"client"`
	ExpiresAt int64  `json:"expires_at"`
	IpAddr    string `json:"ip_addr"`
}
