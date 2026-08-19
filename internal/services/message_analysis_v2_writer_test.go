package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
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

func TestLegacyProcessingMediaFingerprintReusesUnderstoodPayload(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	if err := db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	asset := &models.Asset{
		TenantID: 2, AssetID: "legacy-asset", Provider: enums.AssetProviderLocal,
		StorageKey: "wx_protocol/legacy-voice.mp3", Filename: "wx_protocol_voice.mp3",
		FileSize: 1024, MimeType: "audio/mpeg", AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID: 2, ConversationID: 22, SessionNo: 1, ClientMsgID: "legacy-understood-voice", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "wx_protocol_voice.mp3",
		Payload:     `{"assetId":"legacy-asset","filename":"wx_protocol_voice.mp3","mimeType":"audio/mpeg","url":"","mediaText":"早餐几点开始，停车免费吗？","mediaSummary":"早餐几点开始，停车免费吗？","mediaUnderstandingStatus":"understood","wxMedia":{"aes_key":"key","file_id":"file-1","file_size":1024,"length":3,"md5":"abc","size":1024}}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	legacyPayload := `{"assetId":"legacy-asset","provider":"local","storageKey":"wx_protocol/legacy-voice.mp3","filename":"wx_protocol_voice.mp3","fileSize":1024,"mimeType":"audio/mpeg","wxMedia":{"file_id":"file-1","aes_key":"key","size":1024,"file_size":1024,"md5":"abc","length":3}}`
	reconstructed, ok := reconstructLegacyWxWorkMediaSourcePayload(message)
	if !ok || reconstructed != legacyPayload {
		t.Fatalf("legacy production payload was not reconstructed exactly:\nwant=%s\n got=%s", legacyPayload, reconstructed)
	}
	legacyFingerprint := messageAnalysisFingerprintForPayload(message, legacyPayload)
	if legacyFingerprint == MessageAnalysisService.ContentFingerprint(message) {
		t.Fatal("fixture must reproduce the 2026-08-15 legacy struct fingerprint")
	}
	item := &models.MessageAnalysis{
		TenantID: message.TenantID, MessageID: message.ID, SourceRevision: 1,
		ContentFingerprint: legacyFingerprint, AnalysisStatus: messageAnalysisStatusProcessing,
		SchemaVersion: contracts.MessageAnalysisV1SchemaVersion,
		AnalyzerKind:  "asr", AnalyzerName: "media_understanding", AnalyzerVersion: "v1",
		ClaimedBy: "legacy-owner", AttemptCount: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	if err := MediaUnderstandingService.executeClaimedAnalysis(context.Background(), item, "legacy-owner"); err != nil {
		t.Fatalf("understood legacy media must complete without another ASR call: %v", err)
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	newFingerprint := MessageAnalysisService.ContentFingerprint(message)
	if stored == nil || stored.AnalysisStatus != messageAnalysisStatusReady || strings.TrimSpace(stored.AnalysisJSON) == "" ||
		stored.ContentFingerprint != newFingerprint || stored.SchemaVersion != contracts.MessageAnalysisV2SchemaVersion ||
		!strings.Contains(stored.AnalysisJSON, newFingerprint) || strings.Contains(stored.AnalysisJSON, legacyFingerprint) {
		t.Fatalf("legacy processing row did not become ready: %#v", stored)
	}
	decoded, err := MessageAnalysisService.ReadyForMessage(message)
	if err != nil || decoded == nil || decoded.Result == nil || decoded.Result.NormalizedText != "早餐几点开始，停车免费吗？" {
		t.Fatalf("legacy ready result not reusable: decoded=%#v err=%v", decoded, err)
	}
	reused, err := MessageAnalysisService.EnsurePending(message, 1, MessageAnalyzerIdentity{Kind: "asr", Name: "media_understanding", Version: "v1"})
	if err != nil || reused == nil || reused.ID != item.ID || reused.AnalysisStatus != messageAnalysisStatusReady {
		t.Fatalf("ensure created/staled a duplicate legacy analysis: reused=%#v err=%v", reused, err)
	}
}

func TestLegacyReadyMediaFingerprintMigratesRowAndEvidenceJSON(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	if err := db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	asset := &models.Asset{
		TenantID: 2, AssetID: "legacy-ready-asset", Provider: enums.AssetProviderLocal,
		StorageKey: "wx_protocol/legacy-ready.mp3", Filename: "legacy-ready.mp3",
		FileSize: 2048, MimeType: "audio/mpeg", AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID: 2, ConversationID: 24, SessionNo: 1, ClientMsgID: "legacy-ready-voice", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "legacy-ready.mp3",
		Payload:     `{"assetId":"legacy-ready-asset","filename":"legacy-ready.mp3","mimeType":"audio/mpeg","url":"","wxMedia":{"aes_key":"ready-key","file_id":"ready-file","file_size":2048,"length":5,"md5":"ready-md5","size":2048}}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	legacyPayload := `{"assetId":"legacy-ready-asset","provider":"local","storageKey":"wx_protocol/legacy-ready.mp3","filename":"legacy-ready.mp3","fileSize":2048,"mimeType":"audio/mpeg","wxMedia":{"file_id":"ready-file","aes_key":"ready-key","size":2048,"file_size":2048,"md5":"ready-md5","length":5}}`
	legacyFingerprint := messageAnalysisFingerprintForPayload(message, legacyPayload)
	analysis := contracts.MessageAnalysisV2{
		SchemaVersion: contracts.MessageAnalysisV2SchemaVersion, MessageID: message.ID, SourceRevision: 1,
		ContentFingerprint: legacyFingerprint, Status: messageAnalysisStatusReady, MediaType: "voice",
		Analyzer:       contracts.MessageAnalysisAnalyzerV2{Kind: "asr", Name: "media_understanding", Version: "v1"},
		NormalizedText: "语音里的完整问题",
		Quality:        contracts.MessageAnalysisQualityV2{OverallConfidence: 0.9, Completeness: "complete", Warnings: []string{}, UncertainRanges: []contracts.MessageAnalysisUncertainV2{}},
		Observations:   []contracts.ObservationV2Item{},
	}
	raw, err := encodeReadyMessageAnalysisV2(analysis, now)
	if err != nil {
		t.Fatal(err)
	}
	item := &models.MessageAnalysis{
		TenantID: message.TenantID, MessageID: message.ID, SourceRevision: 1, ContentFingerprint: legacyFingerprint,
		AnalysisStatus: messageAnalysisStatusReady, AnalysisJSON: string(raw), SchemaVersion: contracts.MessageAnalysisV2SchemaVersion,
		AnalyzerKind: "asr", AnalyzerName: "media_understanding", AnalyzerVersion: "v1", AnalyzedAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	decoded, err := MessageAnalysisService.ReadyForMessage(message)
	if err != nil || decoded == nil || decoded.Result == nil || decoded.Result.NormalizedText != "语音里的完整问题" {
		t.Fatalf("legacy ready media was not reusable: decoded=%#v err=%v", decoded, err)
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	newFingerprint := MessageAnalysisService.ContentFingerprint(message)
	if stored == nil || stored.ContentFingerprint != newFingerprint || !strings.Contains(stored.AnalysisJSON, newFingerprint) || strings.Contains(stored.AnalysisJSON, legacyFingerprint) {
		t.Fatalf("legacy ready row and JSON were not migrated together: %#v", stored)
	}
}

func TestLegacyMediaFingerprintStillRejectsChangedSource(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 2, ConversationID: 23, SessionNo: 1, ClientMsgID: "legacy-changed-voice", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "original.mp3",
		Payload:     `{"assetId":"legacy-asset","filename":"original.mp3","mimeType":"audio/mpeg","url":"","mediaText":"原语音内容","mediaSummary":"原语音内容","mediaUnderstandingStatus":"understood","wxMedia":{"file_id":"file-original","md5":"original"}}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item := &models.MessageAnalysis{
		TenantID: message.TenantID, MessageID: message.ID, SourceRevision: 1,
		ContentFingerprint: messageAnalysisFingerprintForPayload(message, message.Payload),
		AnalysisStatus:     messageAnalysisStatusProcessing, SchemaVersion: contracts.MessageAnalysisV1SchemaVersion,
		AnalyzerKind: "asr", AnalyzerName: "media_understanding", AnalyzerVersion: "v1", ClaimedBy: "old-owner",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	replacementPayload := `{"assetId":"replacement-asset","filename":"replacement.mp3","mimeType":"audio/mpeg","url":"","mediaUnderstandingStatus":"pending","wxMedia":{"file_id":"file-replacement","md5":"replacement"}}`
	if err := repositories.MessageRepository.UpdatesInTenant(db, message.ID, message.TenantID, map[string]any{
		"content": "replacement.mp3", "payload": replacementPayload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := MediaUnderstandingService.executeClaimedAnalysis(context.Background(), item, "old-owner"); err == nil {
		t.Fatal("genuinely changed media source must stop before invoking ASR")
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	if stored == nil || stored.AnalysisStatus != messageAnalysisStatusStale || stored.ClaimedBy != "" || stored.LeaseExpiresAt != nil {
		t.Fatalf("changed legacy media source was not marked stale: %#v", stored)
	}
	latestMessage := repositories.MessageRepository.GetInTenant(db, message.ID, message.TenantID)
	if latestMessage == nil || latestMessage.Payload != replacementPayload {
		t.Fatalf("stale preflight changed the replacement media payload: %#v", latestMessage)
	}
	if due := repositories.MessageAnalysisRepository.FindClaimableMedia(db, now.Add(24*time.Hour), 10); len(due) != 0 {
		t.Fatalf("stale source was claimable again: %#v", due)
	}
	current, err := MediaUnderstandingService.EnsureInboundMessageAnalysis(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.SourceRevision != 2 || current.AnalysisStatus != messageAnalysisStatusPending ||
		current.ContentFingerprint != MessageAnalysisService.ContentFingerprint(latestMessage) {
		t.Fatalf("replacement source did not receive revision 2: %#v", current)
	}
	reused, err := MessageAnalysisService.EnsurePending(message, 1, MessageAnalyzerIdentity{
		Kind: "asr", Name: "media_understanding", Version: "v1",
	})
	if err != nil || reused == nil || reused.ID != current.ID {
		t.Fatalf("stale caller snapshot did not reuse current source revision: reused=%#v err=%v", reused, err)
	}
	if revision3 := repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, 3); revision3 != nil {
		t.Fatalf("stale caller created a higher obsolete revision: %#v", revision3)
	}
	revision1 := repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, 1)
	revision2 := repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, 2)
	if revision1 == nil || revision1.AnalysisStatus != messageAnalysisStatusStale || revision2 == nil || revision2.ID != current.ID {
		t.Fatalf("unexpected revision history: revision1=%#v revision2=%#v", revision1, revision2)
	}
	if claimed, err := repositories.MessageAnalysisRepository.TryClaim(db, revision1.ID, revision1.TenantID, "stale-owner", now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("stale revision 1 became claimable: claimed=%v err=%v", claimed, err)
	}
	if claimed, err := repositories.MessageAnalysisRepository.TryClaim(db, revision2.ID, revision2.TenantID, "replacement-owner", now, now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("replacement revision 2 was not claimable: claimed=%v err=%v", claimed, err)
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
		TriggerKind: enums.AIReplyJobTriggerKindMedia, Status: enums.AIReplyJobStatusProcessing, AttemptCount: 1,
	}
	if !aiReplyJobPersistentTechnicalRetry(job) {
		t.Fatal("processing media job must survive its nominal expiry while analysis is pending")
	}
	if err := db.Model(&models.MessageAnalysis{}).Where("id = ?", item.ID).Update("analysis_status", string(enums.MessageAnalysisStatusFailedTerminal)).Error; err != nil {
		t.Fatal(err)
	}
	job.AttemptCount = 1
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
