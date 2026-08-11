package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestMessageAnalysisReadyIsEvidenceIdempotent(t *testing.T) {
	db := setupMessageAnalysisTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	message := &models.Message{
		TenantID: 11, ConversationID: 22, SessionNo: 1, ClientMsgID: "analysis-message", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有停车场吗",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	item, err := MessageAnalysisService.EnsurePending(message, 1, MessageAnalyzerIdentity{Kind: "rule", Name: "message-rules", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	analysis := validMessageAnalysis(message, item.ContentFingerprint, "有停车场吗")
	if err := MessageAnalysisService.CompleteReady(item.ID, message.TenantID, analysis); err != nil {
		t.Fatal(err)
	}
	if err := MessageAnalysisService.CompleteReady(item.ID, message.TenantID, analysis); err != nil {
		t.Fatalf("same evidence must be idempotent: %v", err)
	}

	different := analysis
	different.Result = &contracts.MessageAnalysisResult{
		Language: "zh-CN", DialogueAct: "question", RelationToPrior: "new_topic", NormalizedText: "停车收费吗",
		Entities: []contracts.MessageAnalysisEntity{}, MentionedTagKeys: []string{}, RiskSignals: []string{"none"}, Confidence: 0.92,
	}
	if err := MessageAnalysisService.CompleteReady(item.ID, message.TenantID, different); err == nil || !strings.Contains(err.Error(), "different evidence") {
		t.Fatalf("different evidence reused ready revision: %v", err)
	}
	stored := repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, 1)
	if stored == nil || stored.AnalysisStatus != messageAnalysisStatusReady || !strings.Contains(stored.AnalysisJSON, "有停车场吗") {
		t.Fatalf("ready evidence was changed: %+v", stored)
	}
}

func validMessageAnalysis(message *models.Message, fingerprint, normalizedText string) contracts.MessageAnalysisV1 {
	return contracts.MessageAnalysisV1{
		SchemaVersion: contracts.MessageAnalysisV1SchemaVersion,
		MessageID:     message.ID, SourceRevision: 1, ContentFingerprint: fingerprint,
		Analyzer: contracts.MessageAnalysisAnalyzer{Kind: "rule", Name: "message-rules", Version: "v1"},
		Result: &contracts.MessageAnalysisResult{
			Language: "zh-CN", DialogueAct: "question", RelationToPrior: "new_topic", NormalizedText: normalizedText,
			Entities: []contracts.MessageAnalysisEntity{}, MentionedTagKeys: []string{}, RiskSignals: []string{"none"}, Confidence: 0.95,
		},
	}
}

func setupMessageAnalysisTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Message{}, &models.MessageAnalysis{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	return db
}
