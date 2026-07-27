package models

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestUnifiedAIModelsAutoMigrateAndEnforceScopeKeys(t *testing.T) {
	db := openUnifiedAISchemaDB(t)
	if err := db.AutoMigrate(unifiedAIModelsForTest()...); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}

	now := time.Now()
	profile := &ModelProfileTemplate{Code: "standard", Name: "Standard", Revision: 1, Status: enums.ModelProfileStatusDraft, AuditFields: schemaAuditFields(now)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	assertDuplicateRejected(t, db.Create(&ModelProfileTemplate{Code: "standard", Name: "Duplicate", Revision: 1, Status: enums.ModelProfileStatusDraft, AuditFields: schemaAuditFields(now)}).Error, "profile revision")
	if err := db.Create(&ModelProfileTemplate{Code: "standard", Name: "Revision 2", Revision: 2, Status: enums.ModelProfileStatusDraft, AuditFields: schemaAuditFields(now)}).Error; err != nil {
		t.Fatalf("create second profile revision: %v", err)
	}

	slot := &ModelProfileSlot{TemplateID: profile.ID, UsageCode: enums.ModelUsageSlotReplyLLM, DisplayName: "Reply", ModelType: enums.AIModelTypeLLM, Provider: "newapi", ModelName: "model-a", Enabled: true, AuditFields: schemaAuditFields(now)}
	if err := db.Create(slot).Error; err != nil {
		t.Fatalf("create profile slot: %v", err)
	}
	duplicateSlot := *slot
	duplicateSlot.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateSlot).Error, "profile usage slot")

	assignment := StoreModelProfileAssignment{TenantID: 1, StoreID: 10, TemplateID: profile.ID, TemplateRevision: 1, Status: enums.StoreModelAssignmentStatusAssigned, AssignedAt: now, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	duplicateAssignment := assignment
	duplicateAssignment.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateAssignment).Error, "store assignment")
	differentTenantAssignment := assignment
	differentTenantAssignment.ID = 0
	differentTenantAssignment.TenantID = 2
	if err := db.Create(&differentTenantAssignment).Error; err != nil {
		t.Fatalf("same store id in another tenant must remain isolated: %v", err)
	}

	credential := StoreModelCredential{TenantID: 1, StoreID: 10, Status: enums.StoreCredentialStatusUnconfigured, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	duplicateCredential := credential
	duplicateCredential.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateCredential).Error, "store credential")

	policy := StoreCredentialPolicy{TenantID: 1, StoreID: 10, Status: enums.StatusOk, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create credential policy: %v", err)
	}
	duplicatePolicy := policy
	duplicatePolicy.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicatePolicy).Error, "store credential policy")
}

func TestRequiredModelUsageSlotsAreCompleteAndUnique(t *testing.T) {
	if got, want := len(enums.RequiredModelUsageSlots), 9; got != want {
		t.Fatalf("required model usage slots=%d want=%d", got, want)
	}
	seen := make(map[enums.ModelUsageSlot]struct{}, len(enums.RequiredModelUsageSlots))
	for _, slot := range enums.RequiredModelUsageSlots {
		if slot == "" {
			t.Fatal("required model usage slots contain an empty value")
		}
		if _, exists := seen[slot]; exists {
			t.Fatalf("required model usage slot %q is duplicated", slot)
		}
		seen[slot] = struct{}{}
	}
}

func TestFreshUnifiedSchemaContainsNoRetiredAIKnowledgeOrConversationTagObjects(t *testing.T) {
	db := openUnifiedAISchemaDB(t)
	if err := db.AutoMigrate(Models...); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	assertFreshUnifiedSchemaNoRetiredObjects(t, db)
}

func assertFreshUnifiedSchemaNoRetiredObjects(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{
		"t_ai_config",
		"t_tenant_ai_model_grant",
		"t_store_ai_model_setting",
		"t_conversation_tag",
		"t_knowledge_document",
		"t_knowledge_faq",
		"t_knowledge_chunk",
	} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("retired table %s exists in the fresh unified schema", table)
		}
	}
	for _, target := range []struct {
		model  any
		column string
	}{
		{&ConversationServiceSession{}, "tag_ids_json"},
		{&AIAgent{}, "ai_config_id"},
		{&AgentRunLog{}, "ai_config_id"},
		{&AIUsageEvent{}, "ai_config_id"},
		{&SkillRunLog{}, "ai_config_id"},
	} {
		if db.Migrator().HasColumn(target.model, target.column) {
			t.Fatalf("retired column %s still exists on %T", target.column, target.model)
		}
	}
}

func TestUnifiedAIModelsAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open MySQL schema database: %v", err)
	}
	if err := db.AutoMigrate(unifiedAIModelsForTest()...); err != nil {
		t.Fatalf("MySQL AutoMigrate() error=%v", err)
	}
	if err := db.AutoMigrate(
		&ConversationServiceSession{}, &AIAgent{}, &AgentRunLog{}, &AIUsageEvent{}, &SkillRunLog{},
	); err != nil {
		t.Fatalf("MySQL active model AutoMigrate() error=%v", err)
	}
	assertFreshUnifiedSchemaNoRetiredObjects(t, db)
}

func TestUnifiedTagModelsEnforceIndustryAndStoreIsolation(t *testing.T) {
	db := openUnifiedAISchemaDB(t)
	if err := db.AutoMigrate(unifiedAIModelsForTest()...); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	now := time.Now()
	definition := IndustryTagDefinition{IntentProfileID: 7, Name: "Quiet", SemanticKey: "room.quiet", DefinitionRevision: 1, Status: enums.StatusOk, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&definition).Error; err != nil {
		t.Fatalf("create industry tag definition: %v", err)
	}
	duplicateDefinition := definition
	duplicateDefinition.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateDefinition).Error, "industry semantic key")
	otherIndustryDefinition := definition
	otherIndustryDefinition.ID = 0
	otherIndustryDefinition.IntentProfileID = 8
	if err := db.Create(&otherIndustryDefinition).Error; err != nil {
		t.Fatalf("same semantic key in another industry must be allowed: %v", err)
	}

	templateID := definition.ID
	tenantTag := Tag{TenantID: 1, IntentProfileID: 7, TemplateDefinitionID: &templateID, Name: "Quiet", SemanticKey: "room.quiet", Status: enums.StatusOk, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&tenantTag).Error; err != nil {
		t.Fatalf("create tenant tag: %v", err)
	}
	duplicateTenantTag := tenantTag
	duplicateTenantTag.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateTenantTag).Error, "tenant template projection")
	otherTenantTag := tenantTag
	otherTenantTag.ID = 0
	otherTenantTag.TenantID = 2
	if err := db.Create(&otherTenantTag).Error; err != nil {
		t.Fatalf("same template in another tenant must be allowed: %v", err)
	}

	relation := CustomerTagRelation{TenantID: 1, StoreID: 10, CustomerID: 20, StoreCustomerRelationID: 30, TagID: tenantTag.ID, RelationStatus: "active", AuditFields: schemaAuditFields(now)}
	if err := db.Create(&relation).Error; err != nil {
		t.Fatalf("create customer tag relation: %v", err)
	}
	duplicateRelation := relation
	duplicateRelation.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateRelation).Error, "customer tag relation")
	otherTenantRelation := relation
	otherTenantRelation.ID = 0
	otherTenantRelation.TenantID = 2
	if err := db.Create(&otherTenantRelation).Error; err != nil {
		t.Fatalf("same relation ids in another tenant must be allowed: %v", err)
	}

	state := ConversationEvolutionState{TenantID: 1, ConversationID: 40, SessionNo: 1, StoreID: 10, Status: enums.StatusOk, AuditFields: schemaAuditFields(now)}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create evolution state: %v", err)
	}
	duplicateState := state
	duplicateState.ID = 0
	assertDuplicateRejected(t, db.Create(&duplicateState).Error, "conversation evolution state")
}

func TestStoreCredentialModelsContainNoPlaintextAPIKeyField(t *testing.T) {
	for _, modelType := range []reflect.Type{
		reflect.TypeOf(StoreModelCredential{}),
		reflect.TypeOf(StoreModelCredentialAuditLog{}),
	} {
		for i := 0; i < modelType.NumField(); i++ {
			name := strings.ToLower(modelType.Field(i).Name)
			if strings.Contains(name, "apikey") || strings.Contains(name, "plaintext") {
				t.Fatalf("%s contains forbidden secret field %s", modelType.Name(), modelType.Field(i).Name)
			}
		}
	}
}

func openUnifiedAISchemaDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "unified-ai-schema.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open schema database: %v", err)
	}
	return db
}

func unifiedAIModelsForTest() []any {
	return []any{
		&ModelProfileTemplate{}, &ModelProfileSlot{}, &ModelProfileTestRun{}, &StoreModelProfileAssignment{},
		&StoreModelCredential{}, &StoreCredentialPolicy{}, &StoreModelCredentialAuditLog{},
		&IndustryTagDefinition{}, &Tag{}, &TenantCustomerTagPolicy{}, &StoreCustomerTagRuntimePolicy{},
		&CustomerTagRelation{}, &CustomerTagChangeLog{}, &StoreCustomerTagDecision{},
		&ConversationEvolutionState{}, &ConversationEvolutionRun{},
	}
}

func schemaAuditFields(now time.Time) AuditFields {
	return AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "test", UpdateUserName: "test"}
}

func assertDuplicateRejected(t *testing.T, err error, scope string) {
	t.Helper()
	if err == nil {
		t.Fatalf("duplicate %s was accepted", scope)
	}
}
