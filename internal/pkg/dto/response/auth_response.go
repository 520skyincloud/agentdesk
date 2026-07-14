package response

import "agent-desk/internal/pkg/enums"

type AuthUserResponse struct {
	ID                 int64                        `json:"id"`
	TenantID           int64                        `json:"tenantId"`
	Username           string                       `json:"username"`
	Nickname           string                       `json:"nickname"`
	Avatar             string                       `json:"avatar"`
	Status             enums.Status                 `json:"status"`
	Roles              []string                     `json:"roles"`
	RegistrationSource enums.UserRegistrationSource `json:"registrationSource"`
	ApprovalStatus     enums.UserApprovalStatus     `json:"approvalStatus"`
	MustChangePassword bool                         `json:"mustChangePassword"`
}

type LoginResponse struct {
	AccessToken       string            `json:"accessToken"`
	ExpiresAt         string            `json:"expiresAt"`
	User              *AuthUserResponse `json:"user"`
	Permissions       []string          `json:"permissions"`
	Roles             []string          `json:"roles"`
	ActiveTenantID    int64             `json:"activeTenantId"`
	ActiveTenantName  string            `json:"activeTenantName"`
	CanSwitchTenant   bool              `json:"canSwitchTenant"`
	IsPlatformAccount bool              `json:"isPlatformAccount"`
}

type AuthOptionsResponse struct {
	WxWorkEnabled             bool `json:"wxworkEnabled"`
	OIDCEnabled               bool `json:"oidcEnabled"`
	TenantRegistrationEnabled bool `json:"tenantRegistrationEnabled"`
}
