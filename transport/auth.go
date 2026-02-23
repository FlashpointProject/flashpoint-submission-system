package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/FlashpointProject/flashpoint-submission-system/clients"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/gofrs/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

type discordUserResponse struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	Avatar        string  `json:"avatar"`
	Discriminator string  `json:"discriminator"`
	PublicFlags   int64   `json:"public_flags"`
	Flags         int64   `json:"flags"`
	Locale        string  `json:"locale"`
	MFAEnabled    bool    `json:"mfa_enabled"`
	GlobalName    *string `json:"global_name,omitempty"`
}

type StateKeeper struct {
	sync.Mutex
	states            map[string]time.Time
	expirationSeconds uint64
}

type State struct {
	Nonce string `json:"nonce"`
	Dest  string `json:"dest"`
}

var authScopes = []types.AuthScope{
	{
		Name:        types.AuthScopeIdentity,
		Description: "Read your username, avatar, Flashpoint discord server roles, and FPFSS notification settings",
	},
	{
		Name:        types.AuthScopeProfileEdit,
		Description: "Edit your notification and submission subscription settings",
	},
	{
		Name:        types.AuthScopeProfileAppsRead,
		Description: "Read capabilities of your profile's client applications",
	},
	{
		Name:        types.AuthScopeSubmissionRead,
		Description: "Read basic submission information (comments, metadata)",
	},
	{
		Name:        types.AuthScopeSubmissionReadFiles,
		Description: "Read and download submission files",
	},
	{
		Name:        types.AuthScopeSubmissionEdit,
		Description: "Edit submission information",
	},
	{
		Name:        types.AuthScopeSubmissionUpload,
		Description: "Upload new submission files",
	},
	{
		Name:        types.AuthScopeGameDataRead,
		Description: "Read Game Data info / file indexes",
	},
	{
		Name:        types.AuthScopeGameDataEdit,
		Description: "Edit Game Data info",
	},
	{
		Name:        types.AuthScopeGameRead,
		Description: "Read Game metadata",
	},
	{
		Name:        types.AuthScopeGameEdit,
		Description: "Edit Game metadata",
	},
}

// Generate generates state and returns base64-encoded form
func (sk *StateKeeper) Generate(dest string) (string, error) {
	sk.Clean()
	u, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	s := &State{
		Nonce: u.String(),
		Dest:  dest,
	}
	sk.Lock()
	sk.states[s.Nonce] = time.Now()
	sk.Unlock()

	j, err := json.Marshal(s)
	if err != nil {
		return "", err
	}

	b := base64.URLEncoding.EncodeToString(j)

	return b, nil
}

// Consume consumes base64-encoded state and returns destination URL
func (sk *StateKeeper) Consume(b string) (string, bool) {
	sk.Clean()
	sk.Lock()
	defer sk.Unlock()

	j, err := base64.URLEncoding.DecodeString(b)
	if err != nil {
		return "", false
	}

	s := &State{}

	err = json.Unmarshal(j, s)
	if err != nil {
		return "", false
	}

	_, ok := sk.states[s.Nonce]
	if ok {
		delete(sk.states, s.Nonce)
	}
	return s.Dest, ok
}

func (sk *StateKeeper) Clean() {
	sk.Lock()
	defer sk.Unlock()
	for k, v := range sk.states {
		if v.After(v.Add(time.Duration(sk.expirationSeconds))) {
			delete(sk.states, k)
		}
	}
}

type OauthState struct {
	RedirectURI string `json:"redirect_uri"`
	Scope       string `json:"scope"`
	ClientID    string `json:"client_id"`
	State       string `json:"state"`
}

var stateKeeper = StateKeeper{
	states:            make(map[string]time.Time),
	expirationSeconds: 30,
}

type OIDCConfig struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	UserinfoEndpoint       string   `json:"userinfo_endpoint"`
	SupportedScopes        string   `json:"scopes_supported"`
	ResponseTypesSupported []string `json:"response_types_supported"`
}

func (a *App) GetOpenIdConfiguration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	config := OIDCConfig{
		Issuer:                 "https://fpfss.unstable.life",
		AuthorizationEndpoint:  "https://fpfss.unstable.life/auth/authorize",
		TokenEndpoint:          "https://fpfss.unstable.life/auth/token",
		UserinfoEndpoint:       "https://fpfss.unstable.life/api/profile",
		SupportedScopes:        "identity",
		ResponseTypesSupported: []string{"code"},
	}

	w.Header().Set("Content-Type", "application/json")
	writeResponse(ctx, w, config, http.StatusOK)
}

func (a *App) HandleDiscordAuth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dest := r.FormValue("dest")
	if dest == "" {
		dest = "/web/profile"
	}

	state, err := stateKeeper.Generate(dest)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to generate state", http.StatusInternalServerError))
		return
	}

	http.Redirect(w, r, a.Conf.OauthConf.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

func (a *App) HandleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// verify state

	dest, ok := stateKeeper.Consume(r.FormValue("state"))
	if !ok {
		writeError(ctx, w, perr("state does not match", http.StatusBadRequest))
		return
	}

	// obtain token
	token, err := a.Conf.OauthConf.Exchange(context.Background(), r.FormValue("code"))

	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to obtain discord auth token", http.StatusInternalServerError))
		return
	}

	// obtain user data
	resp, err := a.Conf.OauthConf.Client(context.Background(), token).Get("https://discordapp.com/api/users/@me")

	if err != nil || resp.StatusCode != 200 {
		writeError(ctx, w, perr("failed to obtain discord user data", http.StatusInternalServerError))
		return
	}
	defer resp.Body.Close()

	var discordUserResp discordUserResponse
	err = json.NewDecoder(resp.Body).Decode(&discordUserResp)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse discord response", http.StatusInternalServerError))
		return
	}

	uid, err := strconv.ParseInt(discordUserResp.ID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse discord response", http.StatusInternalServerError))
		return
	}
	username := discordUserResp.Username
	if discordUserResp.GlobalName != nil && *discordUserResp.GlobalName != "" {
		username = *discordUserResp.GlobalName
	}

	discordUser := &types.DiscordUser{
		ID:            uid,
		Username:      username,
		Avatar:        discordUserResp.Avatar,
		Discriminator: discordUserResp.Discriminator,
		PublicFlags:   discordUserResp.PublicFlags,
		Flags:         discordUserResp.Flags,
		Locale:        discordUserResp.Locale,
		MFAEnabled:    discordUserResp.MFAEnabled,
	}

	// Check if internal or external, based on redirect_uri host
	ipAddr := logging.RequestGetRemoteAddress(r)

	createdAt, err := discordgo.SnowflakeTimestamp(strconv.FormatInt(discordUser.ID, 10))
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("user ID error", http.StatusInternalServerError))
		return
	}

	ageThreshold := time.Now().Add(-time.Hour * 24 * 30)
	if !createdAt.Before(ageThreshold) {
		utils.LogCtx(ctx).Warnf("user %d forbidden from logging in, account not old enough", discordUser.ID)
		writeError(ctx, w, perr("access denied", http.StatusForbidden))
		return
	}

	//userIsLongEnoughInServer, err := a.Service.IsUserLongEnoughInServer(ctx, discordUser.ID)
	//if err != nil {
	//	utils.LogCtx(ctx).Error(err)
	//	writeError(ctx, w, perr("discord check failed", http.StatusInternalServerError))
	//	return
	//}
	//
	//if !userIsLongEnoughInServer {
	//	utils.LogCtx(ctx).Warnf("user %d forbidden from logging in, not long enough in server", discordUser.ID)
	//	writeError(ctx, w, perr("access denied", http.StatusForbidden))
	//	return
	//}

	// Logging into FPFSS itself
	authToken, err := a.Service.SaveUser(ctx, discordUser, types.AuthScopeAll, "FPFSS", ipAddr)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, dberr(err))
		return
	}

	if err := a.CC.SetSecureCookie(w, utils.Cookies.Login, service.MapAuthToken(authToken), (int)(a.Conf.SessionExpirationSeconds)); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to set cookie", http.StatusInternalServerError))
		return
	}

	if len(dest) == 0 || !isReturnURLValid(dest) {
		http.Redirect(w, r, "/web/profile", http.StatusFound)
		return
	}

	http.Redirect(w, r, dest, http.StatusFound)
}

func (a *App) HandleIndexRequest(w http.ResponseWriter, r *http.Request) {
	if a.Conf.IndexServiceUrl != "" {
		// Remove /index prefix
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/index")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}

		if r.URL.Path == "/" {
			http.Redirect(w, r, "/index/browse/", http.StatusFound)
			return
		}

		// Set up index browser url
		targetURL := a.Conf.IndexServiceUrl + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		// Create new request
		proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, "Error creating proxy request", http.StatusInternalServerError)
			return
		}

		// Copy headers
		for key, values := range r.Header {
			for _, value := range values {
				proxyReq.Header.Add(key, value)
			}
		}

		// Make the request
		client := &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // Don't follow redirects
			},
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, "Error contacting service", http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		// Copy response headers
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Copy status code
		w.WriteHeader(resp.StatusCode)

		// Copy response body
		io.Copy(w, resp.Body)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

func (a *App) HandleOauthAuthorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)
	err := r.ParseForm()
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
		return
	}

	// Validate client application
	client_id := r.Form.Get("client_id")
	if client_id == "" {
		writeError(ctx, w, perr("missing client_id", http.StatusBadRequest))
		return
	}
	var client *types.ClientApplication
	for _, app := range clients.ClientApps {
		if app.ClientId == client_id {
			client = &app
			break
		}
	}
	if client == nil {
		writeError(ctx, w, perr("invalid client_id", http.StatusBadRequest))
		return
	}

	// Validate scope
	scope := r.Form.Get("scope")
	if scope == "" {
		scope = types.AuthScopeIdentity
	}
	var validScopes []string
	for _, scopeStr := range strings.Split(scope, " ") {
		for _, allowedClientScope := range client.Scopes {
			if scopeStr == allowedClientScope {
				validScopes = append(validScopes, scopeStr)
			}
		}
	}

	// Validate redirect uri
	redirect_uri := r.Form.Get("redirect_uri")
	if redirect_uri == "" {
		writeError(ctx, w, perr("missing redirect_uri", http.StatusBadRequest))
		return
	}
	valid := false
	for _, allowedRedirectURI := range client.RedirectURIs {
		if redirect_uri == allowedRedirectURI {
			valid = true
			break
		}
	}
	if !valid {
		writeError(ctx, w, perr("invalid redirect_uri", http.StatusBadRequest))
		return
	}

	if r.Method == http.MethodPost {
		// User has authorized app, generate a code and redirect
		state := r.Form.Get("state")
		u, err := url.Parse(redirect_uri)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("invalid redirect_uri", http.StatusBadRequest))
			return
		}
		q := u.Query()

		// Generate code
		code, err := a.AuthCodeStorage.NewToken(utils.UserID(ctx), client.ClientId, redirect_uri, strings.Join(validScopes, " "), logging.RequestGetRemoteAddress(r))
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to create auth code", http.StatusInternalServerError))
			return
		}

		if err := a.Service.EmitAuthNewTokenEvent(ctx, uid, client.ClientId); err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("internal error", http.StatusInternalServerError))
			return
		}

		q.Add("code", code.Code)
		q.Add("state", state)
		u.RawQuery = q.Encode()

		w.Write([]byte(u.String()))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Get auth page data
	bpd, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failure getting page data", http.StatusInternalServerError))
		return
	}
	var states = types.DeviceAuthStates{
		Pending:  types.DeviceFlowPending,
		Denied:   types.DeviceFlowErrorDenied,
		Expired:  types.DeviceFlowErrorExpired,
		Complete: types.DeviceFlowComplete,
	}
	var scopesList []types.AuthScope
	for _, scope := range validScopes {
		for _, authScope := range authScopes {
			if scope == authScope.Name {
				scopesList = append(scopesList, authScope)
			}
		}
	}
	pageData := types.DeviceAuthPageData{
		BasePageData: *bpd,
		// Reuse
		Token: &types.DeviceFlowToken{
			ClientApplication: client,
			Scope:             strings.Join(validScopes, " "),
			FlowState:         types.DeviceFlowPending,
		},
		States: states,
		Scopes: scopesList,
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/oauth-authorize.gohtml")
}

func (a *App) HandleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	const msg = "unable to log out, please clear your cookies"
	cookieMap, err := a.CC.GetSecureCookie(r, utils.Cookies.Login)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(msg, http.StatusInternalServerError))
		return
	}

	token, err := service.ParseAuthToken(cookieMap) // TODO move this into the Logout method
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(msg, http.StatusInternalServerError))
		return
	}

	if err := a.Service.Logout(ctx, token); err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(msg, http.StatusInternalServerError))
		return
	}

	utils.UnsetCookie(w, utils.Cookies.Login)
	http.Redirect(w, r, "/web", http.StatusFound)
}

func (a *App) HandlePollDeviceAuth(w http.ResponseWriter, ctx context.Context, deviceCode string, clientID string) {
	// Get device auth token from storage
	dfToken := a.DFStorage.GetUserAuthToken(deviceCode, clientID)
	if dfToken == nil {
		writeError(ctx, w, perr("no tokens found", http.StatusBadRequest))
		return
	}

	switch dfToken.FlowState {
	case types.DeviceFlowComplete:
		if dfToken.AuthToken == nil {
			writeError(ctx, w, perr("auth token already claimed", http.StatusInternalServerError))
			return
		}
		// Encode the auth token
		authJson, err := json.Marshal(dfToken.AuthToken)
		if err != nil {
			writeError(ctx, w, perr("failure marshalling token", http.StatusInternalServerError))
			return
		}
		encodedData := base64.StdEncoding.EncodeToString(authJson)
		jsonData := types.DeviceFlowPollResponse{
			Token: encodedData,
		}
		writeResponse(ctx, w, jsonData, http.StatusOK)
		return
	case types.DeviceFlowPending:
		jsonData := types.DeviceFlowPollResponse{
			Error: "authorization_pending",
		}
		writeResponse(ctx, w, jsonData, http.StatusOK)
		return
	case types.DeviceFlowErrorDenied:
		jsonData := types.DeviceFlowPollResponse{
			Error: "access_denied",
		}
		writeResponse(ctx, w, jsonData, http.StatusOK)
		return
	case types.DeviceFlowErrorExpired:
		jsonData := types.DeviceFlowPollResponse{
			Error: "expired_token",
		}
		writeResponse(ctx, w, jsonData, http.StatusOK)
		return
	}

}

func (a *App) HandleOauthDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		// User confirmation page
		query := r.URL.Query()
		code := query.Get("user_code")

		// Get device auth token from storage
		token, err := a.DFStorage.Get(code)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
			return
		}

		// Load scopes
		var scopesList []types.AuthScope
		for _, scope := range strings.Split(token.Scope, " ") {
			for _, authScope := range authScopes {
				if scope == authScope.Name {
					scopesList = append(scopesList, authScope)
				}
			}
		}
		bpd, err := a.Service.GetBasePageData(ctx)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failure getting page data", http.StatusInternalServerError))
			return
		}
		var states = types.DeviceAuthStates{
			Pending:  types.DeviceFlowPending,
			Denied:   types.DeviceFlowErrorDenied,
			Expired:  types.DeviceFlowErrorExpired,
			Complete: types.DeviceFlowComplete,
		}
		pageData := types.DeviceAuthPageData{
			BasePageData: *bpd,
			Token:        token,
			States:       states,
			Scopes:       scopesList,
		}

		a.RenderTemplates(ctx, w, r, pageData,
			"templates/oauth-device-auth.gohtml")
		return
	}

	if r.Method == http.MethodPost {
		// Client asking for a new device code
		err := r.ParseForm()
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
			return
		}

		// Validate client application
		client_id := r.Form.Get("client_id")
		if client_id == "" {
			writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
			return
		}
		var client *types.ClientApplication
		for _, app := range clients.ClientApps {
			if app.ClientId == client_id {
				client = &app
				break
			}
		}
		if client == nil {
			writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
			return
		}

		// Validate scope
		scope := r.Form.Get("scope")
		if scope != "" {
			// Filter out invalid scopes
			var validScopes []string
			for _, scopeStr := range strings.Split(scope, " ") {
				for _, allowedClientScope := range client.Scopes {
					if scopeStr == allowedClientScope {
						validScopes = append(validScopes, scopeStr)
					}
				}
			}
			scope = strings.Join(validScopes, " ")
			if scope == "" {
				// No valid scopes found, but scope given, give advice
				scopeNames := make([]string, len(authScopes))
				for i, authScope := range authScopes {
					scopeNames[i] = authScope.Name
				}
				writeError(ctx, w, perr("invalid scope: Must be of ["+strings.Join(scopeNames, ", ")+"]", http.StatusBadRequest))
				return
			}
		} else {
			scope = types.AuthScopeIdentity
		}

		// Issue token
		token, err := a.DFStorage.NewToken(scope, client)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to create token", http.StatusInternalServerError))
			return
		}

		writeResponse(ctx, w, token, http.StatusOK)
		return
	}

}

func GetClientId(r *http.Request) (*types.ClientApplication, bool) {
	// Validate client application
	client_id := r.Form.Get("client_id")
	if client_id == "" {
		return nil, false
	}
	var client *types.ClientApplication
	for _, app := range clients.ClientApps {
		if app.ClientId == client_id {
			client = &app
			break
		}
	}
	if client == nil {
		return nil, false
	}

	return client, true
}

func (a *App) GetServerUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := mux.Vars(r)
	uidStr := params[constants.ResourceKeyUserID]
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("invalid user id", http.StatusBadRequest))
		return
	}

	user, err := a.Service.GetServerUser(ctx, uid)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to get user server info", http.StatusInternalServerError))
		return
	}

	writeResponse(ctx, w, user, http.StatusOK)
}

func (a *App) HandleOauthDeviceResponse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := utils.UserID(ctx)

	query := r.URL.Query()
	code := query.Get("user_code")

	// Get device auth token from storage
	token, err := a.DFStorage.Get(code)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
		return
	}

	action := query.Get("action")
	if action == "approve" {
		// Create a new auth token
		ipAddr := logging.RequestGetRemoteAddress(r)
		authToken, err := a.Service.GenAuthToken(ctx, uid, token.Scope, token.ClientApplication.ClientId, ipAddr)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to create new auth token", http.StatusInternalServerError))
			return
		}

		// Save inside device auth
		token.FlowState = types.DeviceFlowComplete
		token.AuthToken = authToken
		err = a.DFStorage.Save(token)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to save device token", http.StatusInternalServerError))
			return
		}

		if err := a.Service.EmitAuthDeviceEvent(ctx, uid, token.ClientApplication.ClientId, true); err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("internal error", http.StatusInternalServerError))
			return
		}
	} else if action == "deny" {
		token.FlowState = types.DeviceFlowErrorDenied
		err := a.DFStorage.Save(token)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("failed to save token", http.StatusInternalServerError))
			return
		}

		if err := a.Service.EmitAuthDeviceEvent(ctx, uid, token.ClientApplication.ClientId, false); err != nil {
			utils.LogCtx(ctx).Error(err)
			writeError(ctx, w, perr("internal error", http.StatusInternalServerError))
			return
		}
	} else {
		writeError(ctx, w, perr("invalid action, must be 'approve' or 'deny'", http.StatusBadRequest))
		return
	}
}

func (a *App) HandleOauthToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := r.ParseForm()
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failed to parse form", http.StatusBadRequest))
		return
	}

	ipAddr := logging.RequestGetRemoteAddress(r)

	grant_type := r.Form.Get("grant_type")

	switch grant_type {
	case "authorization_code":
		{
			code := r.Form.Get("code")
			if code == "" {
				writeError(ctx, w, perr("invalid_request", http.StatusBadRequest))
				return
			}

			token, err := a.AuthCodeStorage.Get(code)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
				return
			}

			authToken, err := a.Service.GenAuthToken(ctx, token.UserID, token.Scope, token.ClientID, token.IPAddr)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to create new auth token", http.StatusInternalServerError))
				return
			}

			authJson, err := json.Marshal(authToken)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to marshal token", http.StatusInternalServerError))
				return
			}
			encodedData := base64.StdEncoding.EncodeToString(authJson)

			token.State = types.AuthCodeComplete
			err = a.AuthCodeStorage.Save(token)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to save token", http.StatusInternalServerError))
				return
			}

			authTokenResponse := &types.AuthTokenResponse{
				AccessToken: encodedData,
				TokenType:   "Bearer",
				ExpiresIn:   a.Conf.SessionExpirationSeconds,
			}

			respData, err := json.Marshal(authTokenResponse)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to marshal token", http.StatusInternalServerError))
				return
			}
			w.Write(respData)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}

	case "client_credentials":
		{
			client_id, client_secret, ok := r.BasicAuth()
			if !ok {
				writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
				return
			}
			var client *types.ClientApplication
			for _, app := range clients.ClientApps {
				if app.ClientId == client_id {
					client = &app
					break
				}
			}
			if client == nil {
				writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
				return
			}

			stored_secret, err := a.Service.GetClientSecret(ctx, client.ClientId)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to get client secret from storage", http.StatusInternalServerError))
				return
			}
			if client_secret == "" {
				writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
				return
			}
			if !matchClientSecret(client_secret, stored_secret) {
				writeError(ctx, w, perr("unauthorized_client", http.StatusBadRequest))
				return
			}

			// Validate scope
			scope := r.Form.Get("scope")
			if scope != "" {
				// Filter out invalid scopes
				var validScopes []string
				for _, scopeStr := range strings.Split(scope, " ") {
					for _, allowedClientScope := range client.ClientCredsScopes {
						if scopeStr == allowedClientScope {
							validScopes = append(validScopes, scopeStr)
						}
					}
				}
				scope = strings.Join(validScopes, " ")
				if scope == "" {
					// No valid scopes found, but scope given, give advice
					scopeNames := make([]string, len(authScopes))
					for i, authScope := range authScopes {
						scopeNames[i] = authScope.Name
					}
				}
			} else {
				scope = types.AuthScopeIdentity
			}

			authToken, err := a.Service.GenAuthToken(ctx, client.UserID, scope, client.ClientId, ipAddr)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to create new auth token", http.StatusInternalServerError))
				return
			}

			authJson, err := json.Marshal(authToken)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to marshal token", http.StatusInternalServerError))
				return
			}
			encodedData := base64.StdEncoding.EncodeToString(authJson)

			authTokenResponse := &types.AuthTokenResponse{
				AccessToken: encodedData,
				TokenType:   "Bearer",
				ExpiresIn:   a.Conf.SessionExpirationSeconds,
			}

			respData, err := json.Marshal(authTokenResponse)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to marshal token", http.StatusInternalServerError))
				return
			}

			w.Write(respData)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}

	case "urn:ietf:params:oauth:grant-type:device_code":
		{
			// Validate client application
			client, valid := GetClientId(r)
			if !valid {
				writeError(ctx, w, perr("invalid_client", http.StatusBadRequest))
				return
			}

			deviceCode := r.Form.Get("device_code")
			if deviceCode == "" {
				writeError(ctx, w, perr("invalid_request", http.StatusBadRequest))
				return
			}

			// Device code given, must be polling
			a.HandlePollDeviceAuth(w, ctx, deviceCode, client.ClientId)
			return
		}
	default:
		{
			writeError(ctx, w, perr("invalid_grant", http.StatusBadRequest))
			return
		}
	}
}

func (a *App) HandleApproveDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query := r.URL.Query()
	code := query.Get("user_code")

	// Get device auth token from storage
	token, err := a.DFStorage.Get(code)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr(err.Error(), http.StatusBadRequest))
		return
	}

	if r.Method == http.MethodPost {
		// POST User has responded
		action := query.Get("action")
		if action == "approve" {
			// Create a new auth token
			uid := utils.UserID(ctx)
			ipAddr := logging.RequestGetRemoteAddress(r)
			authToken, err := a.Service.GenAuthToken(ctx, uid, token.Scope, token.ClientApplication.ClientId, ipAddr)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to create new auth token", http.StatusInternalServerError))
				return
			}

			// Save inside device auth
			token.FlowState = types.DeviceFlowComplete
			token.AuthToken = authToken
			err = a.DFStorage.Save(token)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to save device token", http.StatusInternalServerError))
				return
			}
		} else if action == "deny" {
			token.FlowState = types.DeviceFlowErrorDenied
			err := a.DFStorage.Save(token)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				writeError(ctx, w, perr("failed to save token", http.StatusInternalServerError))
				return
			}
		} else {
			writeError(ctx, w, perr("invalid action, must be 'approve' or 'deny'", http.StatusBadRequest))
			return
		}
		// POST Action complete continue to show result same as GET
	}

	// Load scopes
	var scopesList []types.AuthScope
	for _, scope := range strings.Split(token.Scope, " ") {
		for _, authScope := range authScopes {
			if scope == authScope.Name {
				scopesList = append(scopesList, authScope)
			}
		}
	}
	bpd, err := a.Service.GetBasePageData(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		writeError(ctx, w, perr("failure getting page data", http.StatusInternalServerError))
		return
	}
	var states = types.DeviceAuthStates{
		Pending:  types.DeviceFlowPending,
		Denied:   types.DeviceFlowErrorDenied,
		Expired:  types.DeviceFlowErrorExpired,
		Complete: types.DeviceFlowComplete,
	}
	pageData := types.DeviceAuthPageData{
		BasePageData: *bpd,
		Token:        token,
		States:       states,
		Scopes:       scopesList,
	}

	a.RenderTemplates(ctx, w, r, pageData,
		"templates/device_auth.gohtml")
}

const deviceCodeCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const userCodeCharset = "BCDFGHJKLMNPQRSTVWXZ"

type DeviceFlowUserAuthToken struct {
	AuthToken  string
	DeviceCode string
}

type AuthCodeStorage struct {
	tokens map[string]*types.AuthCodeToken
}

type DeviceFlowStorage struct {
	tokens          map[string]*types.DeviceFlowToken
	authTokens      map[int64]*[]DeviceFlowUserAuthToken
	verificationUrl string
}

func NewDeviceFlowStorage(baseUrl string) *DeviceFlowStorage {
	return &DeviceFlowStorage{
		tokens:          make(map[string]*types.DeviceFlowToken),
		authTokens:      make(map[int64]*[]DeviceFlowUserAuthToken),
		verificationUrl: strings.TrimRight(baseUrl, "/") + "/auth/device",
	}
}

func (s *DeviceFlowStorage) GetUserAuthToken(deviceCode string, clientID string) *types.DeviceFlowToken {
	var dfToken types.DeviceFlowToken
	for i, token := range s.tokens {
		if token.DeviceCode == deviceCode && token.ClientApplication.ClientId == clientID {
			dfToken = *token
			// Remove from server now it has been claimed
			s.tokens[i].AuthToken = nil
		}
	}

	return &dfToken
}

func (s *DeviceFlowStorage) SaveUserAuthToken(uid int64, token string, deviceCode string) {
	userToken := DeviceFlowUserAuthToken{
		AuthToken:  token,
		DeviceCode: deviceCode,
	}
	*s.authTokens[uid] = append(*s.authTokens[uid], userToken)
}

func (s *DeviceFlowStorage) GetUserAuthTokens(uid int64) *[]DeviceFlowUserAuthToken {
	return s.authTokens[uid]
}

func (s *DeviceFlowStorage) NewToken(scope string, client *types.ClientApplication) (*types.DeviceFlowToken, error) {
	// Generate the code
	deviceCode := make([]byte, 32)
	for i := range deviceCode {
		deviceCode[i] = deviceCodeCharset[rand.Intn(len(deviceCodeCharset))]
	}

	userCode := make([]byte, 32)
	for i := range userCode {
		userCode[i] = userCodeCharset[rand.Intn(len(userCodeCharset))]
	}

	expiresAt := time.Now()
	expiresAt = expiresAt.Add(900 * time.Second)

	token := types.DeviceFlowToken{
		DeviceCode:              string(deviceCode),
		Scope:                   scope,
		UserCode:                string(userCode),
		VerificationURI:         s.verificationUrl,
		VerificationURIComplete: s.verificationUrl + "?user_code=" + string(userCode),
		ExpiresIn:               900,
		ExpiresAt:               expiresAt,
		Interval:                3,
		FlowState:               types.DeviceFlowPending,
		ClientApplication:       client,
	}

	err := s.Save(&token)
	if err != nil {
		return &token, err
	}

	return &token, nil
}

func (s *DeviceFlowStorage) Save(token *types.DeviceFlowToken) error {
	s.tokens[token.UserCode] = token
	return nil
}

func (s *DeviceFlowStorage) Get(userCode string) (*types.DeviceFlowToken, error) {
	token, found := s.tokens[userCode]
	if !found {
		return nil, errors.New("device code not found")
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, errors.New("device code has expired")
	}
	return token, nil
}

func (s *DeviceFlowStorage) Delete(deviceCode string) {
	delete(s.tokens, deviceCode)
}

func (s *DeviceFlowStorage) Cleanup() {
	for deviceCode, token := range s.tokens {
		if time.Now().After(token.ExpiresAt) {
			s.Delete(deviceCode)
		}
	}
}

func NewAuthCodeStorage() *AuthCodeStorage {
	return &AuthCodeStorage{
		tokens: make(map[string]*types.AuthCodeToken),
	}
}

func (s *AuthCodeStorage) Get(code string) (*types.AuthCodeToken, error) {
	token, found := s.tokens[code]
	if !found {
		return nil, errors.New("auth code not found")
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, errors.New("auth code has expired")
	}
	if token.State == types.AuthCodeComplete {
		return nil, errors.New("auth code has already been used")
	}
	return token, nil
}

func (s *AuthCodeStorage) NewToken(uid int64, clientId string, redirectUri string, scope string, ipAddr string) (*types.AuthCodeToken, error) {
	// Generate the code
	code := make([]byte, 32)
	for i := range code {
		code[i] = deviceCodeCharset[rand.Intn(len(deviceCodeCharset))]
	}

	expiresAt := time.Now()
	expiresAt = expiresAt.Add(300 * time.Second)

	token := &types.AuthCodeToken{
		UserID:      uid,
		Code:        string(code),
		RedirectUri: redirectUri,
		ClientID:    clientId,
		ExpiresAt:   expiresAt,
		Scope:       scope,
		IPAddr:      ipAddr,
		State:       types.AuthCodePending,
	}

	err := s.Save(token)
	if err != nil {
		return nil, err
	}

	return token, nil
}

func (s *AuthCodeStorage) Save(token *types.AuthCodeToken) error {
	s.tokens[token.Code] = token
	return nil
}

func (s *AuthCodeStorage) Delete(deviceCode string) {
	delete(s.tokens, deviceCode)
}

func (s *AuthCodeStorage) Cleanup() {
	for deviceCode, token := range s.tokens {
		if time.Now().After(token.ExpiresAt) || token.State == types.AuthCodeComplete {
			s.Delete(deviceCode)
		}
	}
}

func hashClientSecret(secret string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), 14)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func matchClientSecret(secret string, hashedSecret string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedSecret), []byte(secret))
	return err == nil
}
