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

type TestReplyIntentProfileRequest struct {
	ID int64 `json:"id"`
}

type PublishReplyIntentProfileRequest struct {
	ID              int64 `json:"id"`
	Revision        int64 `json:"revision"`
	ConfirmRevision bool  `json:"confirmRevision"`
}

type CreateIndustryTagDefinitionRequest struct {
	IntentProfileID int64        `json:"intentProfileId"`
	ParentID        int64        `json:"parentId"`
	Name            string       `json:"name"`
	SemanticKey     string       `json:"semanticKey"`
	Aliases         string       `json:"aliases"`
	ConflictGroup   string       `json:"conflictGroup"`
	ApplicableScene string       `json:"applicableScene"`
	AIEnabled       bool         `json:"aiEnabled"`
	ReplyEnabled    bool         `json:"replyEnabled"`
	SortNo          int          `json:"sortNo"`
	Status          enums.Status `json:"status"`
}

type UpdateIndustryTagDefinitionRequest struct {
	ID              int64        `json:"id"`
	IntentProfileID int64        `json:"intentProfileId"`
	ParentID        int64        `json:"parentId"`
	Name            string       `json:"name"`
	SemanticKey     string       `json:"semanticKey"`
	Aliases         string       `json:"aliases"`
	ConflictGroup   string       `json:"conflictGroup"`
	ApplicableScene string       `json:"applicableScene"`
	AIEnabled       bool         `json:"aiEnabled"`
	ReplyEnabled    bool         `json:"replyEnabled"`
	SortNo          int          `json:"sortNo"`
	Status          enums.Status `json:"status"`
}
