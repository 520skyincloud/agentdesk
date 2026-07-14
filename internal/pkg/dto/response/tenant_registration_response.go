package response

import "agent-desk/internal/pkg/enums"

type TenantInvitationValidationResponse struct {
	Valid           bool   `json:"valid"`
	TenantLegalName string `json:"tenantLegalName,omitempty"`
	TenantShortName string `json:"tenantShortName,omitempty"`
}

type TenantUserRegistrationResponse struct {
	UserID         int64                    `json:"userId"`
	Username       string                   `json:"username"`
	TenantName     string                   `json:"tenantName"`
	ApprovalStatus enums.UserApprovalStatus `json:"approvalStatus"`
	Replayed       bool                     `json:"replayed"`
}

type TenantRegistrationResponse struct {
	UserID             int64                        `json:"userId"`
	Username           string                       `json:"username"`
	Nickname           string                       `json:"nickname"`
	Mobile             string                       `json:"mobile,omitempty"`
	Email              string                       `json:"email,omitempty"`
	Status             enums.Status                 `json:"status"`
	ApprovalStatus     enums.UserApprovalStatus     `json:"approvalStatus"`
	ApprovalRemark     string                       `json:"approvalRemark"`
	RegistrationSource enums.UserRegistrationSource `json:"registrationSource"`
	CreatedAt          string                       `json:"createdAt"`
	ReviewedAt         string                       `json:"reviewedAt,omitempty"`
	ReviewedBy         int64                        `json:"reviewedBy,omitempty"`
}
