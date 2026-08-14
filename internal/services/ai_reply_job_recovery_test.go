package services

import "testing"

func TestDecideRecoveryNeverHandsOffTechnicalFailure(t *testing.T) {
	classes := []AIReplyFailureClass{FailureProtocol, FailureNetwork, FailureDatabase, FailureScope, FailureContent, FailureKnowledge}
	for _, class := range classes {
		decision := DecideRecovery(class, ExecutionCheckpoint{StageAttemptCount: 99, MaxStageAttempts: 3, IsLatestTurnVersion: true})
		if decision.Action == "handoff" || decision.ResumeStage == "handoff" {
			t.Fatalf("technical failure class %s must never hand off: %+v", class, decision)
		}
		if decision.StageAttemptRetry(class) {
			t.Fatalf("exhausted budget must not retry: %+v", decision)
		}
	}
}

func TestDecideRecoveryRetriesWithinBudget(t *testing.T) {
	decision := DecideRecovery(FailureNetwork, ExecutionCheckpoint{StageAttemptCount: 1, MaxStageAttempts: 3})
	if decision.Action != "retry_stage" {
		t.Fatalf("network failure within budget must retry stage: %+v", decision)
	}
}

func TestDecideRecoveryBusinessHandoffRequiresCapabilityRoute(t *testing.T) {
	blocked := DecideRecovery(FailureBusiness, ExecutionCheckpoint{CapabilityHandoffRoute: false, IsLatestTurnVersion: true})
	if blocked.Action == "handoff" {
		t.Fatalf("business failure without capability route must not hand off: %+v", blocked)
	}
	allowed := DecideRecovery(FailureBusiness, ExecutionCheckpoint{CapabilityHandoffRoute: true})
	if allowed.Action != "handoff" || allowed.ResumeStage != "handoff" {
		t.Fatalf("capability-routed business handoff must dispatch: %+v", allowed)
	}
}

func TestDecideRecoveryPartialSuccessCommitsNotice(t *testing.T) {
	decision := DecideRecovery(FailureDatabase, ExecutionCheckpoint{
		StageAttemptCount: 9, MaxStageAttempts: 3, HasAnySuccess: true,
	})
	if decision.Action != "terminal_notice" || decision.ReasonCode != "partial_success_technical_notice" {
		t.Fatalf("partial success must commit a technical notice: %+v", decision)
	}
}

func TestDecideRecoveryMediaTerminalRequestsResend(t *testing.T) {
	decision := DecideRecovery(FailureProtocol, ExecutionCheckpoint{
		StageAttemptCount: 9, MaxStageAttempts: 3, MediaAnalysisTerminal: true,
	})
	if decision.ReasonCode != "media_analysis_terminal_resend_request" {
		t.Fatalf("terminal media analysis must request resend: %+v", decision)
	}
}

func (d RecoveryDecision) StageAttemptRetry(class AIReplyFailureClass) bool {
	return d.Action == "retry_stage" && class != ""
}
