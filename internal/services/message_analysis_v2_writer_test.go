package services

import (
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
