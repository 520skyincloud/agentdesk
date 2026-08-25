package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/services"
)

func TestHandoffCompletionMetadataUsesDirectDispatchStatuses(t *testing.T) {
	tests := []struct {
		status services.HandoffDispatchStatus
		finish string
	}{
		{status: services.HandoffDispatchStatusAwaitingRoomNumber, finish: "intent_human_route_awaiting_room_number"},
		{status: services.HandoffDispatchStatusDispatched, finish: "intent_human_route_dispatched"},
		{status: services.HandoffDispatchStatusAlreadyActive, finish: "intent_human_route_already_active"},
		{status: services.HandoffDispatchStatusOffHours, finish: "intent_human_route_off_hours"},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			finish, generate, validate := handoffCompletionMetadata("intent_human_route", string(tt.status))
			if finish != tt.finish {
				t.Fatalf("unexpected finish reason %q", finish)
			}
			assertNoHandoffConfirmationProtocol(t, strings.Join([]string{finish, generate, validate}, "\n"))
		})
	}
}

func TestCompleteRuntimeHandoffDirectiveTraceUsesDirectDispatchLanguage(t *testing.T) {
	tests := []struct {
		name          string
		status        services.HandoffDispatchStatus
		afterGenerate bool
		finish        string
	}{
		{name: "direct dispatch before generate", status: services.HandoffDispatchStatusDispatched, finish: "handoff_directive_dispatched"},
		{name: "room collection before generate", status: services.HandoffDispatchStatusAwaitingRoomNumber, finish: "handoff_directive_awaiting_room_number"},
		{name: "generated promise replaced by direct dispatch", status: services.HandoffDispatchStatusDispatched, afterGenerate: true, finish: "handoff_directive_dispatched"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := &RunResult{handoffDispatchStatus: string(tt.status)}
			collector := callbacks.NewRuntimeTraceCollector()
			result, err := completeRuntimeHandoffDirective(summary, collector, nil, tt.afterGenerate)
			if err != nil || result != summary {
				t.Fatalf("completeRuntimeHandoffDirective() result=%p err=%v", result, err)
			}
			if !strings.Contains(summary.TraceData, `"finishReason":"`+tt.finish+`"`) {
				t.Fatalf("expected finish reason %q in trace, got %s", tt.finish, summary.TraceData)
			}
			assertNoHandoffConfirmationProtocol(t, summary.TraceData)
		})
	}
}
