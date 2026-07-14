package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
)

func BuildTenantInvitationValidation(result *services.TenantInvitationValidationResult) *response.TenantInvitationValidationResponse {
	if result == nil {
		return &response.TenantInvitationValidationResponse{}
	}
	ret := &response.TenantInvitationValidationResponse{Valid: result.Valid}
	if result.Valid && result.Tenant != nil {
		ret.TenantLegalName = result.Tenant.LegalName
		ret.TenantShortName = result.Tenant.ShortName
	}
	return ret
}

func BuildTenantUserRegistration(result *services.TenantUserRegistrationResult) *response.TenantUserRegistrationResponse {
	if result == nil || result.User == nil || result.Tenant == nil {
		return nil
	}
	return &response.TenantUserRegistrationResponse{
		UserID: result.User.ID, Username: result.User.Username, TenantName: result.Tenant.ShortName,
		ApprovalStatus: result.User.ApprovalStatus, Replayed: result.Replayed,
	}
}

func BuildTenantRegistration(item *models.User) *response.TenantRegistrationResponse {
	if item == nil {
		return nil
	}
	ret := &response.TenantRegistrationResponse{
		UserID: item.ID, Username: item.Username, Nickname: item.Nickname, Status: item.Status,
		ApprovalStatus: item.ApprovalStatus, ApprovalRemark: item.ApprovalRemark,
		RegistrationSource: item.RegistrationSource, CreatedAt: utils.FormatTime(item.CreatedAt),
		ReviewedAt: utils.FormatTimePtr(item.ApprovedAt), ReviewedBy: item.ApprovedBy,
	}
	if item.Mobile != nil {
		ret.Mobile = *item.Mobile
	}
	if item.Email != nil {
		ret.Email = *item.Email
	}
	return ret
}

func BuildTenantRegistrationList(items []models.User) []response.TenantRegistrationResponse {
	ret := make([]response.TenantRegistrationResponse, 0, len(items))
	for i := range items {
		if item := BuildTenantRegistration(&items[i]); item != nil {
			ret = append(ret, *item)
		}
	}
	return ret
}
