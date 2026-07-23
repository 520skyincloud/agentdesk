package request

type TagListRequest struct {
	ParentID int64  `json:"parentId"`
	Name     string `json:"name"`
	Status   int    `json:"status"`
}

type UpdateTagRequest struct {
	ID           int64  `json:"id"`
	DisplayAlias string `json:"displayAlias"`
}

type UpdateTagStatusRequest struct {
	ID     int64 `json:"id"`
	Status int   `json:"status"`
}
