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
