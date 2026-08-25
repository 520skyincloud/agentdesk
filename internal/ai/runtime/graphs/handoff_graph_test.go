package graphs

import (
	"context"
	"errors"
	"testing"

	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/services"
)

func TestHandoffGraphDispatchesWithoutConfirmation(t *testing.T) {
	previousEnabled := isAutoHandoffEnabledForConversation
	isAutoHandoffEnabledForConversation = func(int64) bool { return true }
	t.Cleanup(func() { isAutoHandoffEnabledForConversation = previousEnabled })

	tests := []struct {
		name   string
		status services.HandoffDispatchStatus
	}{
		{name: "awaiting room number", status: services.HandoffDispatchStatusAwaitingRoomNumber},
		{name: "dispatched", status: services.HandoffDispatchStatusDispatched},
		{name: "already active", status: services.HandoffDispatchStatusAlreadyActive},
		{name: "off hours", status: services.HandoffDispatchStatusOffHours},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := dispatchHandoffByAI
			t.Cleanup(func() { dispatchHandoffByAI = previous })

			conversation := models.Conversation{ID: 101}
			aiAgent := models.AIAgent{ID: 202}
			var gotConversationID int64
			var gotAIAgent models.AIAgent
			var gotReason string
			var gotRequestID string
			dispatchHandoffByAI = func(conversationID int64, agent models.AIAgent, reason string, requestID string) (*services.HandoffDispatchResult, error) {
				gotConversationID = conversationID
				gotAIAgent = agent
				gotReason = reason
				gotRequestID = requestID
				return &services.HandoffDispatchResult{Status: tt.status}, nil
			}

			ctx := tracex.ContextWithRequestID(context.Background(), "req-graph-handoff")
			reply, err := NewHandoffGraph(conversation, aiAgent).Run(ctx, `{"reason":"用户明确要求人工"}`)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			result, ok := tooling.ParseToolResult(reply)
			if !ok {
				t.Fatalf("expected structured tool result, got %q", reply)
			}
			if !result.Handled || !result.Terminal || !result.ReplySent || result.ShouldRetry {
				t.Fatalf("unexpected tool result: %+v", result)
			}
			if result.Action != string(tt.status) {
				t.Fatalf("expected action %q, got %q", tt.status, result.Action)
			}
			if gotConversationID != conversation.ID || gotAIAgent.ID != aiAgent.ID {
				t.Fatalf("unexpected dispatch target: conversation=%d agent=%d", gotConversationID, gotAIAgent.ID)
			}
			if gotReason != "用户明确要求人工" {
				t.Fatalf("unexpected reason %q", gotReason)
			}
			if gotRequestID != "req-graph-handoff" {
				t.Fatalf("unexpected request id %q", gotRequestID)
			}
		})
	}
}

func TestHandoffGraphDoesNotDispatchWhenAutoHandoffDisabled(t *testing.T) {
	previousEnabled := isAutoHandoffEnabledForConversation
	previousDispatch := dispatchHandoffByAI
	t.Cleanup(func() {
		isAutoHandoffEnabledForConversation = previousEnabled
		dispatchHandoffByAI = previousDispatch
	})

	var checkedConversationID int64
	isAutoHandoffEnabledForConversation = func(conversationID int64) bool {
		checkedConversationID = conversationID
		return false
	}
	dispatchCalled := false
	dispatchHandoffByAI = func(_ int64, _ models.AIAgent, _ string, _ string) (*services.HandoffDispatchResult, error) {
		dispatchCalled = true
		return &services.HandoffDispatchResult{Status: services.HandoffDispatchStatusDispatched}, nil
	}

	reply, err := NewHandoffGraph(models.Conversation{ID: 101}, models.AIAgent{ID: 202}).Run(context.Background(), `{"reason":"用户明确要求人工"}`)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if checkedConversationID != 101 {
		t.Fatalf("expected setting check for conversation 101, got %d", checkedConversationID)
	}
	if dispatchCalled {
		t.Fatal("dispatch must not be called when automatic handoff is disabled")
	}
	result, ok := tooling.ParseToolResult(reply)
	if !ok {
		t.Fatalf("expected structured tool result, got %q", reply)
	}
	if result.Action != "auto_handoff_disabled" {
		t.Fatalf("expected auto_handoff_disabled action, got %q", result.Action)
	}
	if result.Handled || result.Terminal || result.ReplySent || result.ShouldRetry || result.ReplyText != "" {
		t.Fatalf("disabled handoff must leave normal answering available without claiming a reply: %+v", result)
	}
}

func TestHandoffGraphUsesDefaultReason(t *testing.T) {
	previousEnabled := isAutoHandoffEnabledForConversation
	previous := dispatchHandoffByAI
	isAutoHandoffEnabledForConversation = func(int64) bool { return true }
	t.Cleanup(func() {
		isAutoHandoffEnabledForConversation = previousEnabled
		dispatchHandoffByAI = previous
	})

	var gotReason string
	dispatchHandoffByAI = func(_ int64, _ models.AIAgent, reason string, _ string) (*services.HandoffDispatchResult, error) {
		gotReason = reason
		return &services.HandoffDispatchResult{Status: services.HandoffDispatchStatusDispatched}, nil
	}

	if _, err := NewHandoffGraph(models.Conversation{ID: 1}, models.AIAgent{ID: 2}).Run(context.Background(), `{}`); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotReason != "用户需要转人工支持" {
		t.Fatalf("unexpected default reason %q", gotReason)
	}
}

func TestHandoffGraphReturnsDispatchError(t *testing.T) {
	previousEnabled := isAutoHandoffEnabledForConversation
	previous := dispatchHandoffByAI
	isAutoHandoffEnabledForConversation = func(int64) bool { return true }
	t.Cleanup(func() {
		isAutoHandoffEnabledForConversation = previousEnabled
		dispatchHandoffByAI = previous
	})

	wantErr := errors.New("dispatch failed")
	dispatchHandoffByAI = func(_ int64, _ models.AIAgent, _ string, _ string) (*services.HandoffDispatchResult, error) {
		return nil, wantErr
	}

	reply, err := NewHandoffGraph(models.Conversation{ID: 1}, models.AIAgent{ID: 2}).Run(context.Background(), `{}`)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected dispatch error, got reply=%q err=%v", reply, err)
	}
}
