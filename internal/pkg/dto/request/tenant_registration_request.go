package request

import "agent-desk/internal/pkg/enums"

type ValidateTenantInvitationRequest struct {
	InvitationCode string `json:"invitationCode"`
}

type RegisterTenantUserRequest struct {
	Username        string  `json:"username"`
	Nickname        string  `json:"nickname"`
	Mobile          string  `json:"mobile"`
	Email           string  `json:"email"`
	Password        string  `json:"password"`
	ConfirmPassword string  `json:"confirmPassword"`
	InvitationCode  string  `json:"invitationCode"`
	TenantID        *int64  `json:"tenantId,omitempty"`
	RoleIDs         []int64 `json:"roleIds,omitempty"`
	AgentTeamID     *int64  `json:"agentTeamId,omitempty"`
	StoreID         *int64  `json:"storeId,omitempty"`
}

type ReviewTenantRegistrationRequest struct {
	UserID   int64                                  `json:"userId"`
	Decision enums.TenantRegistrationReviewDecision `json:"decision"`
	RoleIDs  []int64                                `json:"roleIds"`
	Remark   string                                 `json:"remark"`
}
