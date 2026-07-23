package response

import (
	"agent-desk/internal/pkg/enums"
)

type AIAgentOptionResponse struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Status enums.Status `json:"status"`
}
