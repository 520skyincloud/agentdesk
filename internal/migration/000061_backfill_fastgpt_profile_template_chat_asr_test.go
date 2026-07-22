package migration

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBackfillFastGPTProfileTemplateChatASR(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.AIConfig{},
		&models.FastGPTProfileTemplate{},
		&models.FastGPTStoreTenant{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&models.AIConfig{
		Name: "现有对话模型", Provider: enums.AIProviderOpenAI,
		BaseURL: "https://model.example.com/v1", ModelType: enums.AIModelTypeLLM,
		ModelName: "chat-current", APIMode: "responses", Status: enums.StatusOk,
		SortNo: 100, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.AIConfig{
		Name: "现有语音模型", Provider: enums.AIProviderOpenAI,
		BaseURL: "https://asr.example.com/v1", ModelType: enums.AIModelTypeASR,
		ModelName: "asr-current", Status: enums.StatusOk,
		SortNo: 100, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FastGPTProfileTemplate{
		ID: 1, Name: "门店模板", Revision: 2, Status: "active",
		EmbeddingProvider: "OpenAI", EmbeddingBaseURL: "https://embedding.example.com/v1", EmbeddingModel: "embedding-current",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FastGPTStoreTenant{
		StoreID: 7, Status: "active", ProfileTemplateRevision: 2,
		ProfileTemplateTargetRevision: 2, ProfileTemplateSyncStatus: "ready",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := backfillFastGPTProfileTemplateChatASR(db); err != nil {
		t.Fatal(err)
	}

	var template models.FastGPTProfileTemplate
	if err := db.First(&template, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if template.Revision != 3 ||
		template.ChatModel != "chat-current" ||
		template.ChatAPIMode != "responses" ||
		template.ASRModel != "asr-current" {
		t.Fatalf("unexpected backfilled template: %#v", template)
	}
	var tenant models.FastGPTStoreTenant
	if err := db.First(&tenant, "store_id = ?", 7).Error; err != nil {
		t.Fatal(err)
	}
	if tenant.ProfileTemplateTargetRevision != 3 || tenant.ProfileTemplateSyncStatus != "pending" {
		t.Fatalf("tenant was not queued for template revision 3: %#v", tenant)
	}

	if err := backfillFastGPTProfileTemplateChatASR(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&template, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if template.Revision != 3 {
		t.Fatalf("idempotent rerun changed revision to %d", template.Revision)
	}
}
