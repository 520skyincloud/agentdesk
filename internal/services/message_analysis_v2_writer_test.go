package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// 契约 7：analyzer.kind=asr/vision 必须能通过 message_analysis.v2 完成权威
// ready 写入（生产根因：V1 Schema 拒绝 asr，行停在 pending）。
func TestRecordMediaReadyUsesV2SchemaForAsrAndVision(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	sqls.SetDB(db)
	now := time.Now().UTC().Truncate(time.Second)
	seq := 0
	for _, tc := range []struct {
		kind    string
		msgType enums.IMMessageType
		text    string
	}{
		{"asr", enums.IMMessageTypeVoice, "附近有什么好玩的呀？停车场又在哪里啊？"},
		{"vision", enums.IMMessageTypeImage, "图片中包含升房、优惠和发票抬头相关对话。"},
	} {
		seq++
		message := &models.Message{
			TenantID: 11, ConversationID: 22, SessionNo: 1, ClientMsgID: "v2-" + tc.kind, SeqNo: int64(seq),
			SenderType: enums.IMSenderTypeCustomer, MessageType: tc.msgType, Content: tc.text,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := db.Create(message).Error; err != nil {
			t.Fatal(err)
		}
		if err := MessageAnalysisService.RecordMediaReady(message, tc.text, MessageAnalyzerIdentity{
			Kind: tc.kind, Name: "media-understanding", Version: "v2",
		}); err != nil {
			t.Fatalf("RecordMediaReady(%s): %v", tc.kind, err)
		}
		stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
		if stored == nil || stored.AnalysisStatus != messageAnalysisStatusReady {
			t.Fatalf("%s analysis must be ready: %+v", tc.kind, stored)
		}
		if !strings.Contains(stored.AnalysisJSON, "message_analysis.v2") || !strings.Contains(stored.AnalysisJSON, tc.kind) {
			t.Fatalf("%s analysis must be encoded as v2 with analyzer kind: %s", tc.kind, stored.AnalysisJSON)
		}
		// V2 reader 必须能读回。
		decoded, err := MessageAnalysisService.ReadyForMessage(message)
		if err != nil || decoded == nil || decoded.Result == nil || decoded.Result.NormalizedText != tc.text {
			t.Fatalf("%s ReadyForMessage: %+v err=%v", tc.kind, decoded, err)
		}
	}
}

func TestMessageAnalysisFingerprintIgnoresDerivedMediaProjection(t *testing.T) {
	message := &models.Message{
		MessageType: enums.IMMessageTypeImage,
		Content:     "room.jpg",
		Payload:     `{"assetId":"local-before","filename":"before.jpg","wxMedia":{"file_id":"wx-source"},"mediaUnderstandingStatus":"pending"}`,
	}
	before := MessageAnalysisService.ContentFingerprint(message)
	message.Payload = `{"assetId":"local-after","filename":"after.jpg","wxMedia":{"file_id":"wx-source"},"mediaText":"图片里有一张床","mediaSummary":"客房图片","mediaUnderstandingStatus":"understood","mediaUnderstandingError":""}`
	after := MessageAnalysisService.ContentFingerprint(message)
	if before != after {
		t.Fatalf("derived media projection changed source fingerprint: before=%s after=%s", before, after)
	}
}

func TestClaimedMediaReadyUsesLatestPayloadAndCannotBeOverwrittenByFailure(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 11, ConversationID: 22, SessionNo: 1, ClientMsgID: "claimed-media", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage, Content: "room.jpg",
		Payload:     `{"assetId":"local-before","channelMeta":"keep-me","wxMedia":{"file_id":"wx-source"},"mediaUnderstandingStatus":"pending"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item, err := MessageAnalysisService.EnsurePending(message, 1, MessageAnalyzerIdentity{Kind: "vision", Name: "media_understanding", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repositories.MessageAnalysisRepository.TryClaim(db, item.ID, item.TenantID, "owner-ready", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	claimedAgain, err := repositories.MessageAnalysisRepository.TryClaim(db, item.ID, item.TenantID, "owner-late", now.Add(time.Second), now.Add(2*time.Minute))
	if err != nil || claimedAgain {
		t.Fatalf("active lease must not be stolen: claimed=%v err=%v", claimedAgain, err)
	}
	latestPayload := `{"assetId":"local-recovered","channelMeta":"keep-me","wxMedia":{"file_id":"wx-source"},"mediaUnderstandingStatus":"processing"}`
	if err := repositories.MessageRepository.UpdatesInTenant(db, message.ID, message.TenantID, map[string]any{"payload": latestPayload}); err != nil {
		t.Fatal(err)
	}
	committed, err := MessageAnalysisService.CommitClaimedMediaReady(item.ID, item.TenantID, "owner-ready", "图片里有一张床和两瓶水")
	if err != nil {
		t.Fatal(err)
	}
	if committed == nil || !strings.Contains(committed.Payload, `"assetId":"local-recovered"`) ||
		!strings.Contains(committed.Payload, `"channelMeta":"keep-me"`) || !strings.Contains(committed.Payload, `"mediaUnderstandingStatus":"understood"`) {
		t.Fatalf("ready commit did not preserve latest payload: %#v", committed)
	}
	if _, err := MessageAnalysisService.CommitClaimedMediaFailure(
		item.ID, item.TenantID, "owner-late", enums.MessageAnalysisStatusFailedTerminal,
		"upstream_error", "late_failure", "failed", nil,
	); err != nil {
		t.Fatalf("late failure should observe existing ready result: %v", err)
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	latest := repositories.MessageRepository.GetInTenant(db, message.ID, message.TenantID)
	if stored == nil || stored.AnalysisStatus != messageAnalysisStatusReady || latest == nil ||
		!strings.Contains(latest.Payload, `"mediaUnderstandingStatus":"understood"`) || strings.Contains(latest.Payload, "late_failure") {
		t.Fatalf("late failure overwrote ready result: analysis=%#v message=%#v", stored, latest)
	}
}

func TestTransientMediaFailureRemainsRetryablePastInitialBudget(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 31, ConversationID: 41, SessionNo: 1, ClientMsgID: "persistent-media", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "voice.amr",
		Payload:     `{"assetId":"voice-asset","mediaUnderstandingStatus":"pending"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item, err := MediaUnderstandingService.EnsureInboundMessageAnalysis(message.ID)
	if err != nil || item == nil {
		t.Fatalf("ensure media analysis: item=%#v err=%v", item, err)
	}
	for attempt := 1; attempt <= mediaAnalysisAlertAttempt+2; attempt++ {
		claimAt := now.Add(time.Duration(attempt) * time.Minute)
		if err := db.Model(&models.MessageAnalysis{}).Where("id = ?", item.ID).Updates(map[string]any{
			"next_retry_at": claimAt.Add(-time.Second),
		}).Error; err != nil {
			t.Fatal(err)
		}
		owner := "persistent-owner-" + formatInt64(int64(attempt))
		claimed, err := repositories.MessageAnalysisRepository.TryClaim(db, item.ID, item.TenantID, owner, claimAt, claimAt.Add(time.Minute))
		if err != nil || !claimed {
			t.Fatalf("attempt %d claim: claimed=%v err=%v", attempt, claimed, err)
		}
		if err := MediaUnderstandingService.failClaimedAnalysis(item, owner, errors.New("temporary upstream failure"), false, false); err == nil {
			t.Fatalf("attempt %d should return the upstream error", attempt)
		}
		stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
		if stored == nil || enums.NormalizeMessageAnalysisStatus(stored.AnalysisStatus) != enums.MessageAnalysisStatusFailedRetryable ||
			stored.NextRetryAt == nil || stored.AttemptCount != attempt {
			t.Fatalf("attempt %d became terminal or lost retry state: %#v", attempt, stored)
		}
	}
}

func TestEmptyMediaResultRemainsPersistentlyRetryable(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 51, ConversationID: 61, SessionNo: 1, ClientMsgID: "empty-media", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage, Content: "abstract.png",
		Payload:     `{"assetId":"image-asset","mediaUnderstandingStatus":"pending"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item, err := MediaUnderstandingService.EnsureInboundMessageAnalysis(message.ID)
	if err != nil || item == nil {
		t.Fatalf("ensure media analysis: item=%#v err=%v", item, err)
	}
	if err := db.Model(&models.MessageAnalysis{}).Where("id = ?", item.ID).Update("attempt_count", 99).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := repositories.MessageAnalysisRepository.TryClaim(db, item.ID, item.TenantID, "empty-owner", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim empty media analysis: claimed=%v err=%v", claimed, err)
	}
	if err := MediaUnderstandingService.failClaimedAnalysis(item, "empty-owner", errors.New("empty model result"), true, false); err == nil {
		t.Fatal("empty result should return its execution error")
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	latest := repositories.MessageRepository.GetInTenant(db, message.ID, message.TenantID)
	if stored == nil || enums.NormalizeMessageAnalysisStatus(stored.AnalysisStatus) != enums.MessageAnalysisStatusFailedRetryable ||
		stored.NextRetryAt == nil || stored.AttemptCount != 100 || latest == nil ||
		!strings.Contains(latest.Payload, `"mediaUnderstandingStatus":"retrying"`) ||
		!strings.Contains(latest.Payload, `"mediaUnderstandingError":"media_understanding_empty"`) {
		t.Fatalf("empty result became terminal: analysis=%#v message=%#v", stored, latest)
	}
}

func TestMediaReplyJobStaysAliveWhileAnalysisCanRecover(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 71, ConversationID: 81, SessionNo: 1, ClientMsgID: "media-job-persistent", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "voice.amr",
		Payload:     `{"assetId":"voice-asset","mediaUnderstandingStatus":"pending"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item, err := MediaUnderstandingService.EnsureInboundMessageAnalysis(message.ID)
	if err != nil || item == nil {
		t.Fatalf("ensure media analysis: item=%#v err=%v", item, err)
	}
	job := &models.AIReplyJob{
		TenantID: message.TenantID, MessageID: message.ID,
		TriggerKind: enums.AIReplyJobTriggerKindMedia, Status: enums.AIReplyJobStatusProcessing,
	}
	if !aiReplyJobPersistentTechnicalRetry(job) {
		t.Fatal("processing media job must survive its nominal expiry while analysis is pending")
	}
	if err := db.Model(&models.MessageAnalysis{}).Where("id = ?", item.ID).Update("analysis_status", string(enums.MessageAnalysisStatusFailedTerminal)).Error; err != nil {
		t.Fatal(err)
	}
	if aiReplyJobPersistentTechnicalRetry(job) {
		t.Fatal("invalid terminal media source must not keep an expired reply job alive")
	}
}

func TestVisionPromptDescribesAllVisibleContent(t *testing.T) {
	for _, required := range []string{"所有清晰可见的关键内容", "不因内容与酒店无关而省略", "颜色", "形状", "清晰文字", "看起来像"} {
		if !strings.Contains(visionUnderstandingSystemPrompt, required) {
			t.Fatalf("vision system prompt missing %q: %s", required, visionUnderstandingSystemPrompt)
		}
	}
	if strings.Contains(visionUnderstandingUserPrompt, "提取与酒店服务相关的信息") ||
		!strings.Contains(visionUnderstandingUserPrompt, "所有可见关键内容") {
		t.Fatalf("vision user prompt still narrows recognition to hotel content: %s", visionUnderstandingUserPrompt)
	}
}
