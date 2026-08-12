package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildReplyIntentProfileOptions(list []models.ReplyIntentProfile) []response.ReplyIntentProfileOptionResponse {
	results := make([]response.ReplyIntentProfileOptionResponse, 0, len(list))
	for i := range list {
		results = append(results, response.ReplyIntentProfileOptionResponse{
			ID:           list[i].ID,
			Code:         list[i].Code,
			IndustryCode: list[i].IndustryCode,
			Name:         list[i].Name,
			Revision:     list[i].Revision,
			Status:       list[i].Status,
		})
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
