package executor

import (
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
)

func handoffTaskFixture() HandoffTaskView {
	return HandoffTaskView{
		TaskKey: "t1", TurnID: 323, TurnVersion: 8, TenantID: 1, StoreID: 1,
		StoreStaffBindingID: 7, ProtocolInstanceID: 4, ConversationID: 2, SessionNo: 1,
	}
}

func TestDecideHandoffTechnicalFailureNeverPersists(t *testing.T) {
	decision := DecideHandoff(handoffTaskFixture(), CapabilityDecisionV1{Route: "business_handoff"}, HandoffFailureTechnical)
	if decision.Pending() {
		t.Fatalf("technical failure must never create pending handoff: %+v", decision)
	}
	if decision.ReasonCode != "technical_failure" {
		t.Fatalf("unexpected reason: %+v", decision)
	}
}

func TestDecideHandoffKnowledgeGapNeverPersists(t *testing.T) {
	decision := DecideHandoff(handoffTaskFixture(), CapabilityDecisionV1{}, HandoffFailureKnowledgeGap)
	if decision.Pending() {
		t.Fatalf("knowledge gap must not persist pending action: %+v", decision)
	}
}

func TestDecideHandoffSafetyDispatches(t *testing.T) {
	task := handoffTaskFixture()
	task.SafetyCritical = true
	decision := DecideHandoff(task, CapabilityDecisionV1{}, HandoffFailureNone)
	if decision.Mode != contracts.HandoffModeDispatch || decision.OriginType != contracts.HandoffOriginSafety {
		t.Fatalf("safety critical must dispatch: %+v", decision)
	}
}

func TestDecideHandoffCapabilityRouteConfirms(t *testing.T) {
	decision := DecideHandoff(handoffTaskFixture(), CapabilityDecisionV1{Route: "business_handoff", ExecutionMode: "human"}, HandoffFailureNone)
	if decision.Mode != contracts.HandoffModeConfirm || decision.OriginType != contracts.HandoffOriginBusiness {
		t.Fatalf("capability route must confirm: %+v", decision)
	}
	action, err := BuildHandoffPendingAction(decision, 1306, "request-id", 0)
	if err != nil {
		t.Fatalf("pending action: %v", err)
	}
	if !action.Valid() || action.OriginMessageID != 1306 || len(action.TaskKeys) != 1 {
		t.Fatalf("pending action invalid: %+v", action)
	}
}

func TestBuildHandoffPendingActionRejectsModeNone(t *testing.T) {
	decision := DecideHandoff(handoffTaskFixture(), CapabilityDecisionV1{}, HandoffFailureNone)
	if _, err := BuildHandoffPendingAction(decision, 1, "r", time.Minute); err == nil {
		t.Fatal("mode=none must not persist a pending action")
	}
}
