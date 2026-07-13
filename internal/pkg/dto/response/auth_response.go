package response

import "agent-desk/internal/pkg/enums"

type AuthUserResponse struct {
	ID       int64        `json:"id"`
	Username string       `json:"username"`
	Nickname string       `json:"nickname"`
	Avatar   string       `json:"avatar"`
	Status   enums.Status `json:"status"`
	Roles    []string     `json:"roles"`
}

type LoginResponse struct {
	AccessToken string            `json:"accessToken"`
	ExpiresAt   string            `json:"expiresAt"`
	User        *AuthUserResponse `json:"user"`
	Permissions []string          `json:"permissions"`
	Roles       []string          `json:"roles"`
}

type AuthOptionsResponse struct {
	WxWorkEnabled    bool `json:"wxworkEnabled"`
	OIDCEnabled      bool `json:"oidcEnabled"`
	EmailCodeEnabled bool `json:"emailCodeEnabled"`
}

type EmailCodeChallengeResponse struct {
	ExpiresAt         string `json:"expiresAt"`
	RetryAfterSeconds int    `json:"retryAfterSeconds"`
}

type EmailVerificationResponse struct {
	VerificationToken string `json:"verificationToken"`
	ExpiresAt         string `json:"expiresAt"`
}
