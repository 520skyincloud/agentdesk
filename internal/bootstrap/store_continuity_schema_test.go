package bootstrap

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type legacyStoreStaffBindingSchema struct {
	ID      int64 `gorm:"primaryKey;autoIncrement"`
	StoreID int64 `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	models.AuditFields
}

func (legacyStoreStaffBindingSchema) TableName() string { return "t_store_staff_binding" }

type legacyCustomerIdentitySchema struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	TenantID       int64  `gorm:"type:bigint;not null;default:0"`
	CustomerID     int64  `gorm:"type:bigint;not null;uniqueIndex:uk_customer_external"`
	ExternalSource string `gorm:"type:varchar(30);uniqueIndex:uk_customer_external"`
	ExternalID     string `gorm:"type:varchar(128);uniqueIndex:uk_customer_external"`
	models.AuditFields
}

func (legacyCustomerIdentitySchema) TableName() string { return "t_customer_identity" }

type legacyStoreModelCredentialSchema struct {
	ID       int64 `gorm:"primaryKey;autoIncrement"`
	TenantID int64 `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_store_model_credential,priority:1"`
	StoreID  int64 `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_store_model_credential,priority:2"`
	models.AuditFields
}

func (legacyStoreModelCredentialSchema) TableName() string { return "t_store_model_credential" }

type legacyConversationContinuityLinkSchema struct {
	ID                        int64 `gorm:"primaryKey;autoIncrement"`
	TenantID                  int64 `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_conversation_continuity_predecessor,priority:1"`
	PredecessorConversationID int64 `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_conversation_continuity_predecessor,priority:2"`
	SuccessorConversationID   int64 `gorm:"type:bigint;not null;default:0;index"`
}

func (legacyConversationContinuityLinkSchema) TableName() string {
	return "t_conversation_continuity_link"
}

type legacyWxWorkCustomerHandoffSettingSchema struct {
	ID                 int64  `gorm:"primaryKey;autoIncrement"`
	TenantID           int64  `gorm:"type:bigint;not null;default:0;index"`
	CustomerID         int64  `gorm:"type:bigint;not null;uniqueIndex:uk_customer_wxwork_handoff_setting"`
	WxWorkInstanceID   int64  `gorm:"type:bigint;not null;uniqueIndex:uk_customer_wxwork_handoff_setting"`
	AutoHandoffEnabled bool   `gorm:"not null;default:true"`
	Remark             string `gorm:"type:varchar(255);not null;default:''"`
	models.AuditFields
}

func (legacyWxWorkCustomerHandoffSettingSchema) TableName() string {
	return "t_wx_work_customer_handoff_setting"
}

func TestStoreContinuitySchemaNormalizesVerifiedLegacyIndexes(t *testing.T) {
	db := openStoreContinuitySchemaTestDB(t)
	if err := db.AutoMigrate(&legacyStoreStaffBindingSchema{}, &legacyCustomerIdentitySchema{}, &legacyStoreModelCredentialSchema{}, &legacyWxWorkCustomerHandoffSettingSchema{}); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Create(&legacyStoreStaffBindingSchema{StoreID: 31}).Error; err != nil {
		t.Fatalf("seed legacy store binding: %v", err)
	}
	identities := []legacyCustomerIdentitySchema{
		{TenantID: 101, CustomerID: 1, ExternalSource: "wxwork_protocol", ExternalID: "same-external"},
		{TenantID: 102, CustomerID: 2, ExternalSource: "wxwork_protocol", ExternalID: "same-external"},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatalf("seed legacy identities: %v", err)
	}
	if err := db.Create(&legacyStoreModelCredentialSchema{TenantID: 101, StoreID: 31}).Error; err != nil {
		t.Fatalf("seed legacy Store credential: %v", err)
	}
	if err := db.Create(&legacyWxWorkCustomerHandoffSettingSchema{TenantID: 101, CustomerID: 91, WxWorkInstanceID: 77}).Error; err != nil {
		t.Fatalf("seed legacy customer handoff setting: %v", err)
	}

	if err := normalizeStoreContinuitySchema(db); err != nil {
		t.Fatalf("normalize legacy schema: %v", err)
	}
	if err := db.AutoMigrate(&models.StoreStaffBinding{}, &models.CustomerIdentity{}, &models.Conversation{}, &models.ConversationChannelSession{}, &models.ConversationContinuityLink{}, &models.StoreModelCredential{}, &models.WxWorkCustomerHandoffSetting{}); err != nil {
		t.Fatalf("migrate current schema: %v", err)
	}
	if err := validateStoreContinuityIndexes(db); err != nil {
		t.Fatalf("validate current schema: %v", err)
	}
	bindings := []models.StoreStaffBinding{
		{TenantID: 101, UserID: 1, StoreID: 31, Status: enums.StatusOk},
		{TenantID: 101, UserID: 2, StoreID: 31, Status: enums.StatusOk},
	}
	if err := db.Create(&bindings).Error; err != nil {
		t.Fatalf("one store must accept multiple staff bindings after normalization: %v", err)
	}
	if err := normalizeStoreContinuitySchema(db); err != nil {
		t.Fatalf("current schema normalization must be idempotent: %v", err)
	}
	if db.Migrator().HasIndex(&models.StoreModelCredential{}, "uk_store_model_credential") {
		t.Fatal("verified legacy Store credential index still exists")
	}
	if db.Migrator().HasIndex(&models.WxWorkCustomerHandoffSetting{}, "uk_customer_wxwork_handoff_setting") {
		t.Fatal("verified legacy customer handoff index still exists")
	}
}

func TestStoreContinuitySchemaRejectsTenantIdentityDuplicates(t *testing.T) {
	db := openStoreContinuitySchemaTestDB(t)
	if err := db.AutoMigrate(&legacyCustomerIdentitySchema{}); err != nil {
		t.Fatalf("create legacy identity schema: %v", err)
	}
	identities := []legacyCustomerIdentitySchema{
		{TenantID: 101, CustomerID: 1, ExternalSource: "wxwork_protocol", ExternalID: "duplicate"},
		{TenantID: 101, CustomerID: 2, ExternalSource: "wxwork_protocol", ExternalID: "duplicate"},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatalf("seed identities accepted by legacy index: %v", err)
	}
	if err := normalizeStoreContinuitySchema(db); err == nil {
		t.Fatal("tenant identity duplicate must stop schema normalization")
	}
	definition, err := readIndexDefinition(db, &models.CustomerIdentity{}, "uk_customer_external")
	if err != nil {
		t.Fatalf("read preserved legacy index: %v", err)
	}
	if !definition.exists || !definition.unique || len(definition.fields) != 3 || definition.fields[0] != "customer_id" {
		t.Fatalf("failed normalization must preserve legacy index: %+v", definition)
	}
}

func TestStoreContinuitySchemaValidatesFreshSchema(t *testing.T) {
	db := openStoreContinuitySchemaTestDB(t)
	if err := normalizeStoreContinuitySchema(db); err != nil {
		t.Fatalf("normalize empty schema: %v", err)
	}
	if err := db.AutoMigrate(&models.StoreStaffBinding{}, &models.CustomerIdentity{}, &models.Conversation{}, &models.ConversationChannelSession{}, &models.ConversationContinuityLink{}, &models.StoreModelCredential{}, &models.WxWorkCustomerHandoffSetting{}); err != nil {
		t.Fatalf("create fresh schema: %v", err)
	}
	if err := validateStoreContinuityIndexes(db); err != nil {
		t.Fatalf("validate fresh schema: %v", err)
	}
}

func TestStoreContinuitySchemaRejectsMultipleConversationPredecessors(t *testing.T) {
	db := openStoreContinuitySchemaTestDB(t)
	if err := db.AutoMigrate(&legacyConversationContinuityLinkSchema{}); err != nil {
		t.Fatalf("create legacy continuity schema: %v", err)
	}
	links := []legacyConversationContinuityLinkSchema{
		{TenantID: 101, PredecessorConversationID: 1, SuccessorConversationID: 3},
		{TenantID: 101, PredecessorConversationID: 2, SuccessorConversationID: 3},
	}
	if err := db.Create(&links).Error; err != nil {
		t.Fatalf("seed ambiguous continuity links: %v", err)
	}
	if err := normalizeStoreContinuitySchema(db); err == nil {
		t.Fatal("multiple predecessors must stop schema normalization")
	}
	if db.Migrator().HasIndex(&models.ConversationContinuityLink{}, "uk_conversation_continuity_successor") {
		t.Fatal("failed normalization must not create the successor index")
	}
}

func openStoreContinuitySchemaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "store-continuity.db")), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	return db
}
