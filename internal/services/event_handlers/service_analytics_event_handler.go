package event_handlers

import (
	"context"
	"log/slog"

	"agent-desk/internal/events"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/services"
)

func init() {
	eventbus.
		Register[events.ConversationAssignedEvent]().
		Subscribe(handleConversationAssignedAnalytics)
}

func handleConversationAssignedAnalytics(_ context.Context, event events.ConversationAssignedEvent) error {
	if event.ConversationID <= 0 {
		return nil
	}
	if err := services.ServiceAnalyticsCaptureService.RecordCurrentAssignment(event.ConversationID); err != nil {
		slog.Warn("capture conversation assignment analytics failed", "conversationId", event.ConversationID, "error", err)
	}
	if err := services.ServiceAnalyticsCaptureService.RecordDispatchDecision(event.ConversationID, event.ToUserID, event.OperatorID, event.AssignType, event.Reason); err != nil {
		slog.Warn("capture dispatch decision analytics failed", "conversationId", event.ConversationID, "error", err)
	}
	return nil
}
