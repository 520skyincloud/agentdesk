package request

// UpdateReplyActionStatusRequest 开关动作目录。
type UpdateReplyActionStatusRequest struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}
