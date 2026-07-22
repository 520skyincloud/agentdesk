package response

import "agent-desk/internal/pkg/enums"

type ReplyIntentProfileResponse struct {
	ID                 int64        `json:"id"`
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	IndustryCode       string       `json:"industryCode"`
	Description        string       `json:"description"`
	IntentDetectPrompt string       `json:"intentDetectPrompt"`
	IntentJSONSchema   string       `json:"intentJsonSchema"`
	Revision           int64        `json:"revision"`
	PublishedAt        string       `json:"publishedAt,omitempty"`
	Status             enums.Status `json:"status"`
	SortNo             int          `json:"sortNo"`
	Remark             string       `json:"remark"`
	CreatedAt          string       `json:"createdAt"`
	UpdatedAt          string       `json:"updatedAt"`
	CreateUserName     string       `json:"createUserName"`
	UpdateUserName     string       `json:"updateUserName"`
}
