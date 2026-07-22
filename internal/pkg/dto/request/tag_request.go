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
	Aliases         string `json:"aliases"`
	AIEnabled       bool   `json:"aiEnabled"`
	ReplyEnabled    bool   `json:"replyEnabled"`
	ApplicableScene string `json:"applicableScene"`
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

type CreateTagConflictGroupRequest struct {
	CompanyID int64   `json:"companyId"`
	TagIDs    []int64 `json:"tagIds"`
}

type AssignTagConflictGroupRequest struct {
	TagID    int64  `json:"tagId"`
	GroupKey string `json:"groupKey"`
}

type DeleteTagConflictGroupRequest struct {
	CompanyID int64  `json:"companyId"`
	GroupKey  string `json:"groupKey"`
}
