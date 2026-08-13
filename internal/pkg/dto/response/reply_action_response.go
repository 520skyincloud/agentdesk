package response

// ReplyActionResponse 动作目录列表项。
type ReplyActionResponse struct {
	ID                  int64  `json:"id"`
	Code                string `json:"code"`
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	Description         string `json:"description"`
	InputSchema         string `json:"inputSchema"`
	RequireConfirmation bool   `json:"requireConfirmation"`
	ExecutorRef         string `json:"executorRef"`
	Enabled             bool   `json:"enabled"`
	Provisioned         bool   `json:"provisioned"`
	SortNo              int    `json:"sortNo"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
	CreateUserName      string `json:"createUserName"`
	UpdateUserName      string `json:"updateUserName"`
}
