package request

type AIAgentMCPToolRequest struct {
	ToolCode    string            `json:"toolCode"`
	ServerCode  string            `json:"serverCode"`
	ToolName    string            `json:"toolName"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Arguments   map[string]string `json:"arguments"`
}
