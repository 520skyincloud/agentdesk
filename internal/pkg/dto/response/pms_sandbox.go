package response

import "agent-desk/internal/pms/sandbox"

type PMSSandboxStoreOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type PMSSandboxCustomerOption struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	ConversationID int64  `json:"conversationId"`
}

type PMSSandboxOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type PMSSandboxWorkspace struct {
	sandbox.Snapshot
	Active  bool                          `json:"active"`
	Options map[string][]PMSSandboxOption `json:"options"`
}
