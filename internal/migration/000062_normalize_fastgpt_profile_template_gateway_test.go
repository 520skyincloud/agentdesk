package migration

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeFastGPTProfileTemplateGateway(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.FastGPTProfileTemplate{},
		&models.FastGPTStoreTenant{},
	); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	const gateway = "https://new-api.example.com/v1"
	if err := db.Create(&models.FastGPTProfileTemplate{
		ID: 1, Name: "门店模板", Revision: 3, Status: "active",
		ChatProvider: "OpenAI", ChatBaseURL: "https://chat-direct.example.com/v1", ChatModel: "chat-current",
		ASRProvider: "OpenAI", ASRBaseURL: "https://asr-direct.example.com/v1", ASRModel: "asr-current",
		EmbeddingProvider: "OpenAI", EmbeddingBaseURL: gateway, EmbeddingModel: "embedding-current",
		DocumentParserProvider: "OpenAI", DocumentParserBaseURL: gateway, DocumentParserModel: "document-current",
		VisionProvider: "OpenAI", VisionBaseURL: gateway, VisionModel: "vision-current",
		RerankProvider: "OpenAI", RerankBaseURL: gateway, RerankModel: "rerank-current",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FastGPTStoreTenant{
		StoreID: 7, Status: "active", ProfileTemplateRevision: 2,
		ProfileTemplateTargetRevision: 3, ProfileTemplateSyncStatus: "blocked",
		ProfileTemplateLastError: "store_credential_unconfigured",
		AuditFields:              models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := normalizeFastGPTProfileTemplateGateway(db); err != nil {
		t.Fatal(err)
	}

	var template models.FastGPTProfileTemplate
	if err := db.First(&template, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if template.Revision != 4 || !fastGPTProfileTemplateUsesGateway(&template, gateway) {
		t.Fatalf("template gateway was not normalized: %#v", template)
	}
	if template.ChatModel != "chat-current" || template.ASRModel != "asr-current" ||
		template.EmbeddingModel != "embedding-current" || template.RerankModel != "rerank-current" {
		t.Fatalf("migration changed model names: %#v", template)
	}

	var tenant models.FastGPTStoreTenant
	if err := db.First(&tenant, "store_id = ?", 7).Error; err != nil {
		t.Fatal(err)
	}
	if tenant.ProfileTemplateTargetRevision != 4 ||
		tenant.ProfileTemplateSyncStatus != "pending" ||
		tenant.ProfileTemplateLastError != "" {
		t.Fatalf("tenant was not queued for normalized template: %#v", tenant)
	}

	if err := normalizeFastGPTProfileTemplateGateway(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&template, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if template.Revision != 4 {
		t.Fatalf("idempotent rerun changed revision to %d", template.Revision)
	}
}

func TestNormalizeFastGPTProfileTemplateGatewaySkipsAmbiguousExistingSlots(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.FastGPTProfileTemplate{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&models.FastGPTProfileTemplate{
		ID: 1, Name: "门店模板", Revision: 5, Status: "active",
		EmbeddingBaseURL:      "https://gateway-a.example.com/v1",
		DocumentParserBaseURL: "https://gateway-b.example.com/v1",
		VisionBaseURL:         "https://gateway-a.example.com/v1",
		RerankBaseURL:         "https://gateway-a.example.com/v1",
		AuditFields:           models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := normalizeFastGPTProfileTemplateGateway(db); err != nil {
		t.Fatal(err)
	}

	var template models.FastGPTProfileTemplate
	if err := db.First(&template, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if template.Revision != 5 {
		t.Fatalf("ambiguous gateway should require manual correction, revision=%d", template.Revision)
	}
}
