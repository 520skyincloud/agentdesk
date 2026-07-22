package response

import "agent-desk/internal/pkg/enums"

type TagResponse struct {
	ID              int64        `json:"id"`
	CompanyID       int64        `json:"companyId"`
	ParentID        int64        `json:"parentId"`
	Name            string       `json:"name"`
	SemanticKey     string       `json:"semanticKey"`
	Aliases         string       `json:"aliases"`
	ConflictGroup   string       `json:"conflictGroup"`
	AIEnabled       bool         `json:"aiEnabled"`
	ReplyEnabled    bool         `json:"replyEnabled"`
	ApplicableScene string       `json:"applicableScene"`
	MergedIntoTagID int64        `json:"mergedIntoTagId"`
	Remark          string       `json:"remark"`
	SortNo          int          `json:"sortNo"`
	Status          enums.Status `json:"status"`
	CreatedAt       string       `json:"createdAt"`
	UpdatedAt       string       `json:"updatedAt"`
}

type TagTreeResponse struct {
	ID              int64              `json:"id"`
	CompanyID       int64              `json:"companyId"`
	ParentID        int64              `json:"parentId"`
	Name            string             `json:"name"`
	SemanticKey     string             `json:"semanticKey"`
	Aliases         string             `json:"aliases"`
	ConflictGroup   string             `json:"conflictGroup"`
	AIEnabled       bool               `json:"aiEnabled"`
	ReplyEnabled    bool               `json:"replyEnabled"`
	ApplicableScene string             `json:"applicableScene"`
	MergedIntoTagID int64              `json:"mergedIntoTagId"`
	Remark          string             `json:"remark"`
	SortNo          int                `json:"sortNo"`
	Status          enums.Status       `json:"status"`
	CreatedAt       string             `json:"createdAt"`
	UpdatedAt       string             `json:"updatedAt"`
	Children        []*TagTreeResponse `json:"children"`
}
