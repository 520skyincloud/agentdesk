package builders

import (
	"agent-desk/internal/ai/runtime/actions"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildReplyAction(item *models.ReplyActionDefinition) *response.ReplyActionResponse {
	if item == nil {
		return nil
	}
	return &response.ReplyActionResponse{
		ID:                  item.ID,
		Code:                item.Code,
		Name:                item.Name,
		Kind:                item.Kind,
		Description:         item.Description,
		InputSchema:         item.InputSchema,
		RequireConfirmation: item.RequireConfirmation,
		ExecutorRef:         item.ExecutorRef,
		Enabled:             item.Enabled,
		Provisioned:         actions.Provisioned(item.Code),
		SortNo:              item.SortNo,
		CreatedAt:           utils.FormatTime(item.CreatedAt),
		UpdatedAt:           utils.FormatTime(item.UpdatedAt),
		CreateUserName:      item.CreateUserName,
		UpdateUserName:      item.UpdateUserName,
	}
}

func BuildReplyActions(list []models.ReplyActionDefinition) []response.ReplyActionResponse {
	results := make([]response.ReplyActionResponse, 0, len(list))
	for _, item := range list {
		if result := BuildReplyAction(&item); result != nil {
			results = append(results, *result)
		}
	}
	return results
}
