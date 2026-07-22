package builders

import (
	"net/url"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

type TenantBuildOptions struct {
	Supervisor    *models.User
	Stats         *dto.TenantOperationalStats
	IntentProfile *models.ReplyIntentProfile
}

func BuildTenant(item *models.Tenant, options TenantBuildOptions) *response.TenantResponse {
	if item == nil {
		return nil
	}
	ret := &response.TenantResponse{
		ID:                 item.ID,
		IntentProfileID:    item.IntentProfileID,
		TenantCode:         item.TenantCode,
		LegalName:          item.LegalName,
		ShortName:          item.ShortName,
		RegistrationType:   item.RegistrationType,
		RegistrationNo:     item.RegistrationNo,
		ContactName:        item.ContactName,
		ContactMobile:      item.ContactMobile,
		ContactEmail:       item.ContactEmail,
		Address:            item.Address,
		VerificationStatus: item.VerificationStatus,
		VerifiedAt:         utils.FormatTimePtr(item.VerifiedAt),
		Status:             item.Status,
		Remark:             item.Remark,
		CreatedAt:          utils.FormatTime(item.CreatedAt),
		UpdatedAt:          utils.FormatTime(item.UpdatedAt),
		CreateUserName:     item.CreateUserName,
		UpdateUserName:     item.UpdateUserName,
	}
	if options.IntentProfile != nil {
		ret.IndustryCode = options.IntentProfile.IndustryCode
		ret.IndustryName = options.IntentProfile.Name
		ret.IndustryRevision = options.IntentProfile.Revision
	}
	if options.Supervisor != nil {
		ret.SupervisorUserID = options.Supervisor.ID
		ret.SupervisorUsername = options.Supervisor.Username
		ret.SupervisorNickname = options.Supervisor.Nickname
	}
	if options.Stats != nil {
		ret.AgentCount = options.Stats.AgentCount
		ret.StoreCount = options.Stats.StoreCount
		ret.AgentTeamCount = options.Stats.AgentTeamCount
		ret.LastActiveAt = utils.FormatTimePtr(options.Stats.LastActiveAt)
	}
	return ret
}

func BuildTenantList(list []models.Tenant, supervisors map[int64]*models.User, stats map[int64]dto.TenantOperationalStats, profiles map[int64]*models.ReplyIntentProfile) []response.TenantResponse {
	ret := make([]response.TenantResponse, 0, len(list))
	for i := range list {
		itemStats := stats[list[i].ID]
		item := BuildTenant(&list[i], TenantBuildOptions{
			Supervisor:    supervisors[list[i].ID],
			Stats:         &itemStats,
			IntentProfile: profiles[list[i].IntentProfileID],
		})
		if item != nil {
			ret = append(ret, *item)
		}
	}
	return ret
}

func BuildTenantIndustryOptions(items []models.ReplyIntentProfile) []response.TenantIndustryOptionResponse {
	ret := make([]response.TenantIndustryOptionResponse, 0, len(items))
	for i := range items {
		ret = append(ret, response.TenantIndustryOptionResponse{
			ID: items[i].ID, Code: items[i].Code, IndustryCode: items[i].IndustryCode,
			Name: items[i].Name, Revision: items[i].Revision,
		})
	}
	return ret
}

func BuildTenantInvitation(item *models.TenantInvitation, tenant *models.Tenant, code string) *response.TenantInvitationResponse {
	if item == nil || tenant == nil {
		return nil
	}
	return &response.TenantInvitationResponse{
		TenantID:   tenant.ID,
		TenantName: tenant.ShortName,
		Code:       code,
		CodeLast4:  item.CodeLast4,
		InviteLink: "/register?invite=" + url.QueryEscape(code),
		Version:    item.Version,
		UsedCount:  item.UsedCount,
		LastUsedAt: utils.FormatTimePtr(item.LastUsedAt),
		ExpiresAt:  utils.FormatTimePtr(item.ExpiresAt),
		Expired:    item.ExpiresAt == nil || !item.ExpiresAt.After(time.Now()),
		CreatedAt:  utils.FormatTime(item.CreatedAt),
		RotatedAt:  utils.FormatTimePtr(item.RotatedAt),
	}
}
