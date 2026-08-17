package services

import (
	"encoding/json"
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
		if stored.SchemaVersion != "message_analysis.v2" {
			t.Fatalf("%s analysis row must declare v2, got %q", tc.kind, stored.SchemaVersion)
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

func TestCommitMediaCandidateReadyAtomicallyUpdatesPayloadAndV2Evidence(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 11, ConversationID: 22, SessionNo: 1, ClientMsgID: "atomic-media", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "voice.mp3",
		Payload:     `{"assetId":"asset-voice","filename":"voice.mp3"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	originalFingerprint := MessageAnalysisService.ContentFingerprint(message)
	payload := messageMediaPayload{AssetID: "asset-voice", Filename: "voice.mp3", MediaText: "附近有什么好玩的？", MediaStatus: "understood"}
	candidate := defaultMediaAnalysisCandidate(message, payload.MediaText)
	payload.ResponseExpectation = candidate.ResponseExpectation
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := MessageAnalysisService.CommitMediaCandidateReady(message, string(payloadRaw), candidate, false, MessageAnalyzerIdentity{
		Kind: "asr", Name: "media-understanding", Version: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || !strings.Contains(updated.Payload, `"mediaUnderstandingStatus":"understood"`) {
		t.Fatalf("compatibility payload was not committed: %+v", updated)
	}
	if got := MessageAnalysisService.ContentFingerprint(updated); got != originalFingerprint {
		t.Fatalf("derived payload fields changed source fingerprint: before=%s after=%s", originalFingerprint, got)
	}
	stored := repositories.MessageAnalysisRepository.GetLatestInTenant(db, message.TenantID, message.ID)
	if stored == nil || stored.SchemaVersion != "message_analysis.v2" || stored.AnalysisStatus != messageAnalysisStatusReady {
		t.Fatalf("authoritative v2 evidence was not committed: %+v", stored)
	}
	ready, err := MessageAnalysisService.ReadyV2ForMessages([]models.Message{*updated})
	if err != nil || ready[message.ID].NormalizedText != payload.MediaText || len(ready[message.ID].Observations) == 0 {
		t.Fatalf("ready v2 evidence unavailable: %+v err=%v", ready, err)
	}
}

func TestCommitMediaCandidateReadyRollsBackPayloadOnEvidenceConflict(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 11, ConversationID: 22, SessionNo: 1, ClientMsgID: "atomic-conflict", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "voice.mp3",
		Payload:     `{"assetId":"asset-voice","filename":"voice.mp3"}`,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	analyzer := MessageAnalyzerIdentity{Kind: "asr", Name: "media-understanding", Version: "v2"}
	if err := MessageAnalysisService.RecordMediaReady(message, "第一版转写", analyzer); err != nil {
		t.Fatal(err)
	}
	changedPayload := `{"assetId":"asset-voice","filename":"voice.mp3","mediaText":"第二版转写","mediaUnderstandingStatus":"understood"}`
	if _, err := MessageAnalysisService.CommitMediaCandidateReady(message, changedPayload, defaultMediaAnalysisCandidate(message, "第二版转写"), false, analyzer); err == nil {
		t.Fatal("expected immutable evidence conflict")
	}
	stored := repositories.MessageRepository.GetInTenant(db, message.ID, message.TenantID)
	if stored == nil || stored.Payload != message.Payload {
		t.Fatalf("payload changed despite analysis rollback: %+v", stored)
	}
}
