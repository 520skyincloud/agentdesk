package tools

import (
	"testing"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"
)

func TestTicketConfirmationToolBuildsForExistingConversation(t *testing.T) {
	definition := NewCreateTicketGraphTool()
	ctx := registry.Context{Conversation: models.Conversation{ID: 7}, AIAgent: models.AIAgent{ID: 9}}
	tool, err := definition.Build(ctx)
	if err != nil || tool == nil || !definition.Enabled(ctx) || definition.Code() != toolx.GraphCreateTicketConfirm.Code {
		t.Fatalf("existing confirmation graph is unavailable: %v, %#v", err, tool)
	}
}

func TestTicketConfirmationToolRequiresConversationAndAgent(t *testing.T) {
	for _, ctx := range []registry.Context{
		{},
		{Conversation: models.Conversation{ID: 7}},
		{AIAgent: models.AIAgent{ID: 9}},
	} {
		tool, err := NewCreateTicketGraphTool().Build(ctx)
		if err != nil || tool != nil {
			t.Fatalf("unscoped ticket tool was enabled: %v, %#v", err, tool)
		}
	}
}
