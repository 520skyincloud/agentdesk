package migration

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBackfillKnowledgeDomainTenantsCoversChildrenAndIsIdempotent(t *testing.T) {
	db := setupKnowledgeTenantBackfillDB(t)
	legacy := createKnowledgeTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createKnowledgeTenant(t, db, "knowledge-a")
	userA := createKnowledgeTenantUser(t, db, tenantA.ID, "knowledge-user-a")

	baseA := &models.KnowledgeBase{Name: "A knowledge", Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	legacyBase := &models.KnowledgeBase{Name: "Legacy knowledge", Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")}
	for _, item := range []*models.KnowledgeBase{baseA, legacyBase} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create knowledge base: %v", err)
		}
	}
	store := &models.Store{TenantID: tenantA.ID, StoreCode: "knowledge-store-a", Name: "A store", KnowledgeBaseID: baseA.ID, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: tenantA.ID, Guid: "knowledge-instance-a", StoreID: store.ID, KnowledgeBaseID: baseA.ID, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wxwork instance: %v", err)
	}
	conversation := &models.Conversation{TenantID: tenantA.ID, CustomerName: "A customer", Status: enums.IMConversationStatusAIServing, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	route := &models.ConversationRouteState{TenantID: tenantA.ID, ConversationID: conversation.ID, StoreID: store.ID, KnowledgeBaseID: baseA.ID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	candidate := &models.KnowledgeCandidate{StoreID: store.ID, KnowledgeBaseID: baseA.ID, ConversationID: conversation.ID, Question: "question", Status: enums.KnowledgeCandidateStatusPending, AuditFields: knowledgeTenantAuditFields(userA.ID, userA.Username)}
	retrieveLog := &models.KnowledgeRetrieveLog{KnowledgeBaseID: baseA.ID, ConversationID: conversation.ID, Question: "question", CreatedAt: time.Now()}
	for _, item := range []any{candidate, retrieveLog} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create knowledge child %T: %v", item, err)
		}
	}
	hit := &models.KnowledgeRetrieveHit{RetrieveLogID: retrieveLog.ID, KnowledgeBaseID: baseA.ID, RankNo: 1, CreatedAt: time.Now()}
	feedback := &models.KnowledgeFeedback{RetrieveLogID: retrieveLog.ID, FeedbackType: 1, UserID: userA.ID, CreatedAt: time.Now()}
	for _, item := range []any{hit, feedback} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create knowledge child %T: %v", item, err)
		}
	}

	if err := db.Transaction(backfillKnowledgeDomainTenants); err != nil {
		t.Fatalf("backfill knowledge tenants: %v", err)
	}
	if err := db.Transaction(backfillKnowledgeDomainTenants); err != nil {
		t.Fatalf("repeat knowledge tenant backfill: %v", err)
	}

	for _, item := range []struct {
		model any
		id    int64
	}{{&models.KnowledgeBase{}, baseA.ID}, {&models.KnowledgeCandidate{}, candidate.ID}, {&models.KnowledgeRetrieveLog{}, retrieveLog.ID}, {&models.KnowledgeRetrieveHit{}, hit.ID}, {&models.KnowledgeFeedback{}, feedback.ID}} {
		assertKnowledgeTenant(t, db, item.model, item.id, tenantA.ID)
	}
	assertKnowledgeTenant(t, db, &models.KnowledgeBase{}, legacyBase.ID, legacy.ID)
}

func TestBackfillKnowledgeDomainTenantsDoesNotCreateRetiredLocalKnowledgeTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	activeModels := []any{
		&models.Tenant{}, &models.User{}, &models.Store{}, &models.WxWorkProtocolInstance{},
		&models.Conversation{}, &models.ConversationRouteState{}, &models.KnowledgeBase{},
		&models.KnowledgeCandidate{}, &models.KnowledgeRetrieveLog{}, &models.KnowledgeRetrieveHit{},
		&models.KnowledgeFeedback{},
	}
	if err := db.AutoMigrate(activeModels...); err != nil {
		t.Fatalf("auto migrate active knowledge schema: %v", err)
	}
	createKnowledgeTenant(t, db, constants.LegacyDefaultTenantCode)

	if err := db.Transaction(backfillKnowledgeDomainTenants); err != nil {
		t.Fatalf("backfill fresh knowledge schema: %v", err)
	}
	for _, retired := range []string{"t_knowledge_document", "t_knowledge_faq", "t_knowledge_chunk"} {
		if db.Migrator().HasTable(retired) {
			t.Fatalf("retired local knowledge table %s must not be created on a fresh database", retired)
		}
	}
}

func TestBackfillKnowledgeDomainTenantsRejectsSharedKnowledgeBaseAndRollsBack(t *testing.T) {
	db := setupKnowledgeTenantBackfillDB(t)
	createKnowledgeTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createKnowledgeTenant(t, db, "knowledge-conflict-a")
	tenantB := createKnowledgeTenant(t, db, "knowledge-conflict-b")
	base := &models.KnowledgeBase{Name: "shared", Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")}
	if err := db.Create(base).Error; err != nil {
		t.Fatalf("create knowledge base: %v", err)
	}
	for _, store := range []*models.Store{
		{TenantID: tenantA.ID, StoreCode: "knowledge-conflict-store-a", Name: "A", KnowledgeBaseID: base.ID, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")},
		{TenantID: tenantB.ID, StoreCode: "knowledge-conflict-store-b", Name: "B", KnowledgeBaseID: base.ID, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")},
	} {
		if err := db.Create(store).Error; err != nil {
			t.Fatalf("create store: %v", err)
		}
	}

	err := db.Transaction(backfillKnowledgeDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with store") {
		t.Fatalf("backfill error=%v want shared knowledge conflict", err)
	}
	assertKnowledgeTenant(t, db, &models.KnowledgeBase{}, base.ID, 0)
}

func TestBackfillKnowledgeDomainTenantsRejectsChildConflictAndRollsBack(t *testing.T) {
	db := setupKnowledgeTenantBackfillDB(t)
	createKnowledgeTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createKnowledgeTenant(t, db, "knowledge-child-a")
	tenantB := createKnowledgeTenant(t, db, "knowledge-child-b")
	base := &models.KnowledgeBase{TenantID: tenantA.ID, Name: "A knowledge", Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")}
	if err := db.Create(base).Error; err != nil {
		t.Fatalf("create knowledge base: %v", err)
	}
	candidate := &models.KnowledgeCandidate{
		TenantID: tenantB.ID, KnowledgeBaseID: base.ID, Question: "cross tenant",
		Status: enums.KnowledgeCandidateStatusPending, AuditFields: knowledgeTenantAuditFields(0, "system"),
	}
	if err := db.Create(candidate).Error; err != nil {
		t.Fatalf("create knowledge candidate: %v", err)
	}

	err := db.Transaction(backfillKnowledgeDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with knowledge base") {
		t.Fatalf("backfill error=%v want child tenant conflict", err)
	}
	assertKnowledgeTenant(t, db, &models.KnowledgeCandidate{}, candidate.ID, tenantB.ID)
}

func setupKnowledgeTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	modelsToMigrate := []any{&models.Tenant{}, &models.User{}, &models.Store{}, &models.WxWorkProtocolInstance{}, &models.Conversation{}, &models.ConversationRouteState{}, &models.KnowledgeBase{}, &models.KnowledgeCandidate{}, &models.KnowledgeRetrieveLog{}, &models.KnowledgeRetrieveHit{}, &models.KnowledgeFeedback{}}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func createKnowledgeTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: code, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createKnowledgeTenantUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	item := &models.User{TenantID: tenantID, Username: username, Nickname: username, Status: enums.StatusOk, AuditFields: knowledgeTenantAuditFields(0, "system")}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return item
}

func knowledgeTenantAuditFields(userID int64, username string) models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, CreateUserID: userID, CreateUserName: username, UpdatedAt: now, UpdateUserID: userID, UpdateUserName: username}
}

func assertKnowledgeTenant(t *testing.T, db *gorm.DB, model any, id, want int64) {
	t.Helper()
	var row struct{ TenantID int64 }
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read tenant for %T %d: %v", model, id, err)
	}
	if row.TenantID != want {
		t.Fatalf("%T %d tenant=%d want=%d", model, id, row.TenantID, want)
	}
}
