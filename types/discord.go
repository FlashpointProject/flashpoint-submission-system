package types

type DiscordUser struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Avatar        string `json:"avatar"`
	Discriminator string `json:"discriminator"`
	PublicFlags   int64  `json:"public_flags"`
	Flags         int64  `json:"flags"`
	Locale        string `json:"locale"`
	MFAEnabled    bool   `json:"mfa_enabled"`
}

type DiscordRole struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type FlashpointDiscordRole struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type FlashpointDiscordUser struct {
	ID    string                   `json:"id"`
	Roles []*FlashpointDiscordRole `json:"roles"`
	Color string                   `json:"color"`
}
