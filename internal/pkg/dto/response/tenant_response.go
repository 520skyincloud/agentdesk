package response

import (
	"agent-desk/internal/pkg/enums"
)

type TenantResponse struct {
	ID                 int64                          `json:"id"`
	IntentProfileID    int64                          `json:"intentProfileId"`
	IndustryCode       string                         `json:"industryCode"`
	IndustryName       string                         `json:"industryName"`
	IndustryRevision   int64                          `json:"industryRevision"`
	TenantCode         string                         `json:"tenantCode"`
	LegalName          string                         `json:"legalName"`
	ShortName          string                         `json:"shortName"`
	RegistrationType   string                         `json:"registrationType"`
	RegistrationNo     string                         `json:"registrationNo"`
	ContactName        string                         `json:"contactName"`
	ContactMobile      string                         `json:"contactMobile"`
	ContactEmail       string                         `json:"contactEmail"`
	Address            string                         `json:"address"`
	VerificationStatus enums.TenantVerificationStatus `json:"verificationStatus"`
	VerifiedAt         string                         `json:"verifiedAt,omitempty"`
	Status             enums.Status                   `json:"status"`
	Remark             string                         `json:"remark"`
	SupervisorUserID   int64                          `json:"supervisorUserId"`
	SupervisorUsername string                         `json:"supervisorUsername"`
	SupervisorNickname string                         `json:"supervisorNickname"`
	AgentCount         int64                          `json:"agentCount"`
	StoreCount         int64                          `json:"storeCount"`
	AgentTeamCount     int64                          `json:"agentTeamCount"`
	LastActiveAt       string                         `json:"lastActiveAt,omitempty"`
	CreatedAt          string                         `json:"createdAt"`
	UpdatedAt          string                         `json:"updatedAt"`
	CreateUserName     string                         `json:"createUserName"`
	UpdateUserName     string                         `json:"updateUserName"`
}

type TenantIndustryOptionResponse struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"`
	IndustryCode string `json:"industryCode"`
	Name         string `json:"name"`
	Revision     int64  `json:"revision"`
}

type TenantInvitationResponse struct {
	TenantID   int64  `json:"tenantId"`
	TenantName string `json:"tenantName"`
	Code       string `json:"code"`
	CodeLast4  string `json:"codeLast4"`
	InviteLink string `json:"inviteLink"`
	Version    int    `json:"version"`
	UsedCount  int64  `json:"usedCount"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Expired    bool   `json:"expired"`
	CreatedAt  string `json:"createdAt"`
	RotatedAt  string `json:"rotatedAt,omitempty"`
}

type CreateTenantResultResponse struct {
	Tenant             *TenantResponse           `json:"tenant"`
	SupervisorUsername string                    `json:"supervisorUsername"`
	SupervisorPassword string                    `json:"supervisorPassword"`
	DefaultAgentTeamID int64                     `json:"defaultAgentTeamId"`
	Invitation         *TenantInvitationResponse `json:"invitation"`
}
