package response

import "agent-desk/internal/pkg/enums"

type ReplyIntentConfigResponse struct {
	ID                 int64        `json:"id"`
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	IntentProfileID    int64        `json:"intentProfileId"`
	ScopeType          string       `json:"scopeType"`
	CompanyID          int64        `json:"companyId"`
	StoreID            int64        `json:"storeId"`
	WxWorkInstanceID   int64        `json:"wxWorkInstanceId"`
	Priority           int          `json:"priority"`
	MatchMode          string       `json:"matchMode"`
	Keywords           string       `json:"keywords"`
	PositiveExamples   string       `json:"positiveExamples"`
	NegativeExamples   string       `json:"negativeExamples"`
	RequiredContext    string       `json:"requiredContext"`
	NeedsKnowledge     bool         `json:"needsKnowledge"`
	NeedsResource      bool         `json:"needsResource"`
	ResourceType       string       `json:"resourceType"`
	NeedsTool          bool         `json:"needsTool"`
	ToolCodes          string       `json:"toolCodes"`
	NeedsHumanRoute    bool         `json:"needsHumanRoute"`
	HumanRoutePolicy   string       `json:"humanRoutePolicy"`
	PromptPack         string       `json:"promptPack"`
	ReplyPlanTemplate  string       `json:"replyPlanTemplate"`
	ValidationRules    string       `json:"validationRules"`
	NoReplyWhenMatched bool         `json:"noReplyWhenMatched"`
	Status             enums.Status `json:"status"`
	SortNo             int          `json:"sortNo"`
	Remark             string       `json:"remark"`
	CreatedAt          string       `json:"createdAt"`
	UpdatedAt          string       `json:"updatedAt"`
	CreateUserName     string       `json:"createUserName"`
	UpdateUserName     string       `json:"updateUserName"`
}
