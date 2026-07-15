package request

import "agent-desk/internal/pkg/enums"

type CreateReplyIntentProfileRequest struct {
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	IndustryCode       string       `json:"industryCode"`
	Description        string       `json:"description"`
	IntentDetectPrompt string       `json:"intentDetectPrompt"`
	IntentJSONSchema   string       `json:"intentJsonSchema"`
	Status             enums.Status `json:"status"`
	SortNo             int          `json:"sortNo"`
	Remark             string       `json:"remark"`
}

type UpdateReplyIntentProfileRequest struct {
	ID int64 `json:"id"`
	CreateReplyIntentProfileRequest
}

type DeleteReplyIntentProfileRequest struct {
	ID int64 `json:"id"`
}
