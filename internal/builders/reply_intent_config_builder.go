package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildReplyIntentConfig(item *models.ReplyIntentConfig) *response.ReplyIntentConfigResponse {
	if item == nil {
		return nil
	}
	return &response.ReplyIntentConfigResponse{
		ID:                 item.ID,
		Code:               item.Code,
		Name:               item.Name,
		Description:        item.Description,
		IntentProfileID:    item.IntentProfileID,
		ScopeType:          item.ScopeType,
		CompanyID:          item.CompanyID,
		StoreID:            item.StoreID,
		WxWorkInstanceID:   item.WxWorkInstanceID,
		Priority:           item.Priority,
		MatchMode:          item.MatchMode,
		Keywords:           item.Keywords,
		PositiveExamples:   item.PositiveExamples,
		NegativeExamples:   item.NegativeExamples,
		RequiredContext:    item.RequiredContext,
		NeedsKnowledge:     item.NeedsKnowledge,
		NeedsResource:      item.NeedsResource,
		ResourceType:       item.ResourceType,
		NeedsTool:          item.NeedsTool,
		ToolCodes:          item.ToolCodes,
		NeedsHumanRoute:    item.NeedsHumanRoute,
		HumanRoutePolicy:   item.HumanRoutePolicy,
		PromptPack:         item.PromptPack,
		ReplyPlanTemplate:  item.ReplyPlanTemplate,
		ValidationRules:    item.ValidationRules,
		NoReplyWhenMatched: item.NoReplyWhenMatched,
		Status:             item.Status,
		SortNo:             item.SortNo,
		Remark:             item.Remark,
		CreatedAt:          utils.FormatTime(item.CreatedAt),
		UpdatedAt:          utils.FormatTime(item.UpdatedAt),
		CreateUserName:     item.CreateUserName,
		UpdateUserName:     item.UpdateUserName,
	}
}

func BuildReplyIntentConfigs(list []models.ReplyIntentConfig) []response.ReplyIntentConfigResponse {
	results := make([]response.ReplyIntentConfigResponse, 0, len(list))
	for _, item := range list {
		if result := BuildReplyIntentConfig(&item); result != nil {
			results = append(results, *result)
		}
	}
	return results
}
