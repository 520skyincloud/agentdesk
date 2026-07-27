package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildReplyIntentProfile(item *models.ReplyIntentProfile) *response.ReplyIntentProfileResponse {
	if item == nil {
		return nil
	}
	return &response.ReplyIntentProfileResponse{
		ID:                 item.ID,
		Code:               item.Code,
		Name:               item.Name,
		IndustryCode:       item.IndustryCode,
		Description:        item.Description,
		IntentDetectPrompt: item.IntentDetectPrompt,
		IntentJSONSchema:   item.IntentJSONSchema,
		Revision:           item.Revision,
		PublishedAt:        utils.FormatTimePtr(item.PublishedAt),
		Status:             item.Status,
		SortNo:             item.SortNo,
		Remark:             item.Remark,
		CreatedAt:          utils.FormatTime(item.CreatedAt),
		UpdatedAt:          utils.FormatTime(item.UpdatedAt),
		CreateUserName:     item.CreateUserName,
		UpdateUserName:     item.UpdateUserName,
	}
}

func BuildReplyIntentProfiles(list []models.ReplyIntentProfile) []response.ReplyIntentProfileResponse {
	results := make([]response.ReplyIntentProfileResponse, 0, len(list))
	for _, item := range list {
		if result := BuildReplyIntentProfile(&item); result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func BuildIndustryTagDefinition(item *models.IndustryTagDefinition) *response.IndustryTagDefinitionResponse {
	if item == nil {
		return nil
	}
	return &response.IndustryTagDefinitionResponse{
		ID:                 item.ID,
		IntentProfileID:    item.IntentProfileID,
		ParentID:           item.ParentID,
		Name:               item.Name,
		SemanticKey:        item.SemanticKey,
		Aliases:            item.Aliases,
		ConflictGroup:      item.ConflictGroup,
		ApplicableScene:    item.ApplicableScene,
		AIEnabled:          item.AIEnabled,
		ReplyEnabled:       item.ReplyEnabled,
		DefinitionRevision: item.DefinitionRevision,
		SortNo:             item.SortNo,
		Status:             item.Status,
		CreatedAt:          utils.FormatTime(item.CreatedAt),
		UpdatedAt:          utils.FormatTime(item.UpdatedAt),
		CreateUserName:     item.CreateUserName,
		UpdateUserName:     item.UpdateUserName,
	}
}

func BuildIndustryTagDefinitions(list []models.IndustryTagDefinition) []response.IndustryTagDefinitionResponse {
	results := make([]response.IndustryTagDefinitionResponse, 0, len(list))
	for i := range list {
		if result := BuildIndustryTagDefinition(&list[i]); result != nil {
			results = append(results, *result)
		}
	}
	return results
}
