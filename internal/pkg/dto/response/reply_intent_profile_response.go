package response

import "agent-desk/internal/pkg/enums"

type ReplyIntentProfileOptionResponse struct {
	ID           int64        `json:"id"`
	Code         string       `json:"code"`
	IndustryCode string       `json:"industryCode"`
	Name         string       `json:"name"`
	Revision     int64        `json:"revision"`
	Status       enums.Status `json:"status"`
}

type ReplyIntentProfileValidationResponse struct {
	ProfileID          int64    `json:"profileId"`
	Revision           int64    `json:"revision"`
	Valid              bool     `json:"valid"`
	Errors             []string `json:"errors"`
	Warnings           []string `json:"warnings"`
	ActiveIntentCount  int      `json:"activeIntentCount"`
	TagCategoryCount   int      `json:"tagCategoryCount"`
	TagCount           int      `json:"tagCount"`
	ConflictGroupCount int      `json:"conflictGroupCount"`
}

type IndustryTagDefinitionResponse struct {
	ID                 int64        `json:"id"`
	IntentProfileID    int64        `json:"intentProfileId"`
	ParentID           int64        `json:"parentId"`
	Name               string       `json:"name"`
	SemanticKey        string       `json:"semanticKey"`
	Aliases            string       `json:"aliases"`
	ConflictGroup      string       `json:"conflictGroup"`
	ApplicableScene    string       `json:"applicableScene"`
	AIEnabled          bool         `json:"aiEnabled"`
	ReplyEnabled       bool         `json:"replyEnabled"`
	DefinitionRevision int64        `json:"definitionRevision"`
	SortNo             int          `json:"sortNo"`
	Status             enums.Status `json:"status"`
	CreatedAt          string       `json:"createdAt"`
	UpdatedAt          string       `json:"updatedAt"`
	CreateUserName     string       `json:"createUserName"`
	UpdateUserName     string       `json:"updateUserName"`
}
