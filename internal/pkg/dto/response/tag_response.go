package response

import "agent-desk/internal/pkg/enums"

type TagResponse struct {
	ID                   int64        `json:"id"`
	IntentProfileID      int64        `json:"intentProfileId"`
	TemplateDefinitionID int64        `json:"templateDefinitionId"`
	ParentID             int64        `json:"parentId"`
	Name                 string       `json:"name"`
	DisplayAlias         string       `json:"displayAlias"`
	SemanticKey          string       `json:"semanticKey"`
	ConflictGroup        string       `json:"conflictGroup"`
	ApplicableScene      string       `json:"applicableScene"`
	AIEnabled            bool         `json:"aiEnabled"`
	ReplyEnabled         bool         `json:"replyEnabled"`
	SystemDefined        bool         `json:"systemDefined"`
	Remark               string       `json:"remark"`
	SortNo               int          `json:"sortNo"`
	Status               enums.Status `json:"status"`
	CreatedAt            string       `json:"createdAt"`
	UpdatedAt            string       `json:"updatedAt"`
}

type TagTreeResponse struct {
	ID                   int64              `json:"id"`
	IntentProfileID      int64              `json:"intentProfileId"`
	TemplateDefinitionID int64              `json:"templateDefinitionId"`
	ParentID             int64              `json:"parentId"`
	Name                 string             `json:"name"`
	DisplayAlias         string             `json:"displayAlias"`
	SemanticKey          string             `json:"semanticKey"`
	ConflictGroup        string             `json:"conflictGroup"`
	ApplicableScene      string             `json:"applicableScene"`
	AIEnabled            bool               `json:"aiEnabled"`
	ReplyEnabled         bool               `json:"replyEnabled"`
	SystemDefined        bool               `json:"systemDefined"`
	Remark               string             `json:"remark"`
	SortNo               int                `json:"sortNo"`
	Status               enums.Status       `json:"status"`
	CreatedAt            string             `json:"createdAt"`
	UpdatedAt            string             `json:"updatedAt"`
	Children             []*TagTreeResponse `json:"children"`
}
