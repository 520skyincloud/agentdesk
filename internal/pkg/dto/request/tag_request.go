package request

type TagListRequest struct {
	ParentID int64  `json:"parentId"`
	Name     string `json:"name"`
	Status   int    `json:"status"`
}

type CreateTagRequest struct {
	CompanyID       int64  `json:"companyId"`
	ParentID        int64  `json:"parentId"`
	Name            string `json:"name"`
	SemanticKey     string `json:"semanticKey"`
	Aliases         string `json:"aliases"`
	ConflictGroup   string `json:"conflictGroup"`
	AIEnabled       bool   `json:"aiEnabled"`
	ReplyEnabled    bool   `json:"replyEnabled"`
	ApplicableScene string `json:"applicableScene"`
	MergedIntoTagID int64  `json:"mergedIntoTagId"`
	Remark          string `json:"remark"`
}

type UpdateTagRequest struct {
	ID int64 `json:"id"`
	CreateTagRequest
}

type DeleteTagRequest struct {
	ID int64 `json:"id"`
}

type UpdateTagStatusRequest struct {
	ID     int64 `json:"id"`
	Status int   `json:"status"`
}
