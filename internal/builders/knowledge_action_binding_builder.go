package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildKnowledgeActionBinding(item *models.KnowledgeActionBinding) *response.KnowledgeActionBindingResponse {
	if item == nil {
		return nil
	}
	return &response.KnowledgeActionBindingResponse{
		ID:              item.ID,
		TenantID:        item.TenantID,
		StoreID:         item.StoreID,
		KnowledgeBaseID: item.KnowledgeBaseID,
		SourceRecordID:  item.SourceRecordID,
		ActionCode:      item.ActionCode,
		Enabled:         item.Enabled,
		SortNo:          item.SortNo,
		Remark:          item.Remark,
		CreatedAt:       utils.FormatTime(item.CreatedAt),
		UpdatedAt:       utils.FormatTime(item.UpdatedAt),
		CreateUserName:  item.CreateUserName,
		UpdateUserName:  item.UpdateUserName,
	}
}

func BuildKnowledgeActionBindings(list []models.KnowledgeActionBinding) []response.KnowledgeActionBindingResponse {
	results := make([]response.KnowledgeActionBindingResponse, 0, len(list))
	for _, item := range list {
		if result := BuildKnowledgeActionBinding(&item); result != nil {
			results = append(results, *result)
		}
	}
	return results
}
