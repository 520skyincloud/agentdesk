package executor

import (
	"context"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"gorm.io/gorm"
)

func TestObservationReadinessDefersBeforeKnowledgeWhenAnalysisIsPending(t *testing.T) {
	db, req, binding := setupObservationReadinessFixture(t, enums.MessageAnalysisStatusPending)
	_ = db
	plans := []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "image-question", Intent: "hotel_info", SubIntent: "facility",
		ObservationBindings: []callbacks.TaskObservationBindingTraceData{binding},
	}}

	ready, deferred, err := partitionRuntimePlansByObservationReadiness(req, plans)
	if err != nil {
		t.Fatalf("partition observation readiness: %v", err)
	}
	if len(ready) != 0 || len(deferred) != 1 || deferred[0] != "image-question" {
		t.Fatalf("pending observation entered knowledge/generate: ready=%+v deferred=%+v", ready, deferred)
	}
}

func TestObservationReadinessAllowsExactReadyV2Revision(t *testing.T) {
	_, req, binding := setupObservationReadinessFixture(t, enums.MessageAnalysisStatusReady)
	plans := []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "image-question", Intent: "hotel_info", SubIntent: "facility",
		ObservationBindings: []callbacks.TaskObservationBindingTraceData{binding},
	}}

	ready, deferred, err := partitionRuntimePlansByObservationReadiness(req, plans)
	if err != nil {
		t.Fatalf("partition observation readiness: %v", err)
	}
	if len(ready) != 1 || len(deferred) != 0 {
		t.Fatalf("ready observation was not released: ready=%+v deferred=%+v", ready, deferred)
	}
}

func TestObservationReadinessTreatsTerminalAnalysisAsTechnicalInvariant(t *testing.T) {
	_, req, binding := setupObservationReadinessFixture(t, enums.MessageAnalysisStatusFailedTerminal)
	_, _, err := partitionRuntimePlansByObservationReadiness(req, []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "image-question", ObservationBindings: []callbacks.TaskObservationBindingTraceData{binding},
	}})
	code, ok := servicesAIReplyExecutionErrorCode(err)
	if !ok || code != "resource_invariant_broken" {
		t.Fatalf("terminal media analysis must be a controlled technical failure, got %v", err)
	}
}

func TestConditionalKnowledgeProbeDoesNotRunBeforeBoundObservationReady(t *testing.T) {
	_, req, binding := setupObservationReadinessFixture(t, enums.MessageAnalysisStatusPending)
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{99}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "interaction", SubIntent: "clarify", ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: "interaction", SubIntent: "clarify", RequestMode: "clarify_previous",
			Text: "这个是什么", ObservationBindings: []callbacks.TaskObservationBindingTraceData{binding},
		}},
	}

	got, probes, err := probeConditionalKnowledgeTasksWithRetriever(
		context.Background(), req, adapter.HistoryBuildResult{}, intent, retriever,
	)
	if err != nil {
		t.Fatalf("conditional knowledge probe: %v", err)
	}
	if retriever.called || len(probes) != 0 {
		t.Fatalf("pending observation executed FastGPT: called=%t probes=%+v", retriever.called, probes)
	}
	if got.IntentTasks[0].Intent != "interaction" || got.IntentTasks[0].NeedsKnowledge {
		t.Fatalf("pending media task was incorrectly promoted: %+v", got.IntentTasks[0])
	}
}

func setupObservationReadinessFixture(
	t *testing.T,
	status enums.MessageAnalysisStatus,
) (*gorm.DB, RunInput, callbacks.TaskObservationBindingTraceData) {
	t.Helper()
	db := setupRuntimeIntentConfigTestDB(t)
	if err := db.AutoMigrate(&models.MessageAnalysis{}); err != nil {
		t.Fatalf("migrate message analysis: %v", err)
	}
	now := time.Now()
	message := models.Message{
		ID: 91001, TenantID: 1, ConversationID: 7, SessionNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage,
		Content: "room.jpg", SentAt: &now,
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("create bound media message: %v", err)
	}
	if status == enums.MessageAnalysisStatusReady {
		if err := services.MessageAnalysisService.RecordMediaReady(
			&message,
			"房间内有一台电视和一张床。",
			services.MessageAnalyzerIdentity{Kind: "vision", Name: "fixture", Version: "v2"},
		); err != nil {
			t.Fatalf("record ready message analysis: %v", err)
		}
	} else {
		analysis := models.MessageAnalysis{
			TenantID: 1, MessageID: message.ID, SourceRevision: 1,
			ContentFingerprint: services.MessageAnalysisService.ContentFingerprint(&message), AnalysisStatus: string(status),
			SchemaVersion: contracts.MessageAnalysisV2SchemaVersion,
		}
		if err := db.Create(&analysis).Error; err != nil {
			t.Fatalf("create message analysis: %v", err)
		}
	}
	req := RunInput{
		Conversation: models.Conversation{ID: 7, TenantID: 1, StoreID: 1},
		UserMessage: models.Message{
			ID: 91002, TenantID: 1, ConversationID: 7, SessionNo: 1,
			SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		},
	}
	return db, req, callbacks.TaskObservationBindingTraceData{MessageID: message.ID, SourceRevision: 1}
}

func servicesAIReplyExecutionErrorCode(err error) (string, bool) {
	code, ok := services.AIReplyExecutionErrorCodeOf(err)
	return string(code), ok
}
