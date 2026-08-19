package services

import (
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"
)

func TestStandaloneOneTriggerBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		messageType enums.IMMessageType
		content     string
		turnID      int64
		turnVersion int
		want        enums.AIReplyJobTriggerKind
	}{
		{name: "exact", messageType: enums.IMMessageTypeText, content: "1", want: enums.AIReplyJobTriggerKindStandaloneOne},
		{name: "trimmed", messageType: enums.IMMessageTypeText, content: " 1 ", want: enums.AIReplyJobTriggerKindStandaloneOne},
		{name: "html", messageType: enums.IMMessageTypeHTML, content: "1", want: enums.AIReplyJobTriggerKindText},
		{name: "voice", messageType: enums.IMMessageTypeVoice, content: "1", want: enums.AIReplyJobTriggerKindMedia},
		{name: "double digit", messageType: enums.IMMessageTypeText, content: "11", want: enums.AIReplyJobTriggerKindText},
		{name: "punctuation", messageType: enums.IMMessageTypeText, content: "1。", want: enums.AIReplyJobTriggerKindText},
		{name: "embedded", messageType: enums.IMMessageTypeText, content: "入住1", want: enums.AIReplyJobTriggerKindText},
		{name: "fullwidth", messageType: enums.IMMessageTypeText, content: "１", want: enums.AIReplyJobTriggerKindText},
		{name: "turn bound", messageType: enums.IMMessageTypeText, content: "1", turnID: 9, turnVersion: 1, want: enums.AIReplyJobTriggerKindText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := aiReplyTriggerKind(&models.Message{
				SenderType:         enums.IMSenderTypeCustomer,
				MessageType:        tt.messageType,
				Content:            tt.content,
				AIReplyTurnID:      tt.turnID,
				AIReplyTurnVersion: tt.turnVersion,
			})
			if !ok || kind != tt.want {
				t.Fatalf("trigger kind=%q ok=%v want %q", kind, ok, tt.want)
			}
		})
	}
}

func TestStandaloneOneInboundDoesNotEnterTurnOrHandoffConfirmation(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "已有普通问题")
	if err := ConversationRouteService.SetPendingAction(
		fixture.conversation.ID,
		enums.ConversationPendingActionHumanHandoff,
		`{"reason":"test"}`,
		time.Now().Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var turnCountBefore int64
	if err := fixture.db.Model(&models.AIReplyTurn{}).Count(&turnCountBefore).Error; err != nil {
		t.Fatal(err)
	}
	external := openidentity.ExternalUser{ExternalSource: enums.ExternalSourceUser, ExternalID: "standalone-one", ExternalName: "独立入口客户"}
	message, err := MessageService.sendValidatedMessageWithOptions(
		fixture.conversation,
		enums.IMSenderTypeCustomer,
		0,
		"standalone-one-inbound",
		enums.IMMessageTypeText,
		" 1 ",
		"",
		nil,
		&external,
		"standalone-one-request",
		sendMessageOptions{sessionNo: fixture.message.SessionNo},
	)
	if err != nil {
		t.Fatal(err)
	}
	if message.AIReplyTurnID != 0 || message.AIReplyTurnVersion != 0 {
		t.Fatalf("standalone message entered turn: %#v", message)
	}
	job := repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, message.TenantID, message.ConversationID, message.ID)
	if job == nil || job.TriggerKind != enums.AIReplyJobTriggerKindStandaloneOne || job.TurnID != 0 || job.TurnVersion != 0 {
		t.Fatalf("standalone job scope mismatch: %#v", job)
	}
	var turnCountAfter int64
	if err := fixture.db.Model(&models.AIReplyTurn{}).Count(&turnCountAfter).Error; err != nil {
		t.Fatal(err)
	}
	if turnCountAfter != turnCountBefore {
		t.Fatalf("standalone input changed turn count: before=%d after=%d", turnCountBefore, turnCountAfter)
	}
	route := ConversationRouteService.GetByConversationID(fixture.conversation.ID)
	if route == nil || route.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		t.Fatalf("standalone input was consumed by handoff confirmation: %#v", route)
	}

	duplicate, err := MessageService.sendValidatedMessageWithOptions(
		fixture.conversation,
		enums.IMSenderTypeCustomer,
		0,
		"standalone-one-inbound",
		enums.IMMessageTypeText,
		"1",
		"",
		nil,
		&external,
		"standalone-one-request",
		sendMessageOptions{sessionNo: fixture.message.SessionNo},
	)
	if err != nil || duplicate == nil || duplicate.ID != message.ID || duplicate.AIReplyTurnID != 0 {
		t.Fatalf("duplicate standalone input changed scope: message=%#v err=%v", duplicate, err)
	}
}

func TestStandaloneOneAndNormalJobsDoNotSupersedeOrCancelEachOther(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	now := time.Now()
	standaloneMessage := createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 2, "standalone-one-isolation", now, false)
	standaloneMessage.Content = "1"
	if err := fixture.db.Model(&models.Message{}).Where("id = ?", standaloneMessage.ID).Update("content", "1").Error; err != nil {
		t.Fatal(err)
	}
	standaloneJob, created, err := fixture.service.EnsureForMessage(standaloneMessage.ID)
	if err != nil || !created || standaloneJob == nil || standaloneJob.TriggerKind != enums.AIReplyJobTriggerKindStandaloneOne {
		t.Fatalf("create standalone job: job=%#v created=%v err=%v", standaloneJob, created, err)
	}
	if decision := fixture.service.inspectFreshness(&aiReplyJobExecutionState{
		Job: fixture.job, Conversation: fixture.conversation, Message: fixture.message,
	}); decision != nil {
		t.Fatalf("standalone input superseded normal job: %#v", decision)
	}

	normalNewer := createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 3, "normal-after-standalone", now.Add(time.Second), false)
	if decision := fixture.service.inspectFreshness(&aiReplyJobExecutionState{
		Job: standaloneJob, Conversation: fixture.conversation, Message: standaloneMessage,
	}); decision != nil {
		t.Fatalf("normal input superseded standalone job: %#v", decision)
	}

	var normalCancelled atomic.Bool
	var standaloneCancelled atomic.Bool
	unregisterNormal := fixture.service.registerActiveExecution(fixture.job, func() { normalCancelled.Store(true) })
	unregisterStandalone := fixture.service.registerActiveExecution(standaloneJob, func() { standaloneCancelled.Store(true) })
	defer unregisterNormal()
	defer unregisterStandalone()
	fixture.service.NotifyNewerMessage(fixture.conversation.ID, standaloneMessage.ID)
	if normalCancelled.Load() || standaloneCancelled.Load() {
		t.Fatalf("standalone input cancelled active work: normal=%v standalone=%v", normalCancelled.Load(), standaloneCancelled.Load())
	}
	fixture.service.NotifyNewerMessage(fixture.conversation.ID, normalNewer.ID)
	if !normalCancelled.Load() || standaloneCancelled.Load() {
		t.Fatalf("normal input cancellation scope mismatch: normal=%v standalone=%v", normalCancelled.Load(), standaloneCancelled.Load())
	}
}
