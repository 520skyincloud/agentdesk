package services_test

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestEnsureExternalCustomerUpdatesNameFromExternalIdentity(t *testing.T) {
	db := setupCustomerServiceTestDB(t)

	var firstID int64
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		id, err := services.CustomerService.EnsureExternalCustomer(ctx, 101, openidentity.ExternalUser{
			ExternalSource: enums.ExternalSourceUser,
			ExternalID:     "user-1",
			ExternalName:   "张三",
		})
		firstID = id
		return err
	}); err != nil {
		t.Fatalf("EnsureExternalCustomer() first error = %v", err)
	}

	conversation := &models.Conversation{
		CustomerID:   firstID,
		CustomerName: "张三",
		Status:       enums.IMConversationStatusActive,
		AuditFields:  models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}

	var secondID int64
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		id, err := services.CustomerService.EnsureExternalCustomer(ctx, 101, openidentity.ExternalUser{
			ExternalSource: enums.ExternalSourceUser,
			ExternalID:     "user-1",
			ExternalName:   "李四",
		})
		secondID = id
		return err
	}); err != nil {
		t.Fatalf("EnsureExternalCustomer() second error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected same customer id, got %d and %d", firstID, secondID)
	}

	customer := services.CustomerService.Get(firstID)
	if customer == nil {
		t.Fatalf("expected customer to exist")
	}
	if customer.Name != "李四" {
		t.Fatalf("expected customer name updated, got %q", customer.Name)
	}

	var updatedConversation models.Conversation
	if err := db.First(&updatedConversation, conversation.ID).Error; err != nil {
		t.Fatalf("get conversation error = %v", err)
	}
	if updatedConversation.CustomerName != "李四" {
		t.Fatalf("expected conversation customer name updated, got %q", updatedConversation.CustomerName)
	}
}

func TestLoadCustomerPresentationDataAggregatesRelatedModels(t *testing.T) {
	db := setupCustomerServiceTestDB(t)
	store := &models.Store{TenantID: 101, StoreCode: "presentation-store", Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "presentation-instance", StoreID: store.ID, EmployeeName: "测试员工号", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wxwork instance: %v", err)
	}
	customer := &models.Customer{TenantID: 101, Name: "测试客户", Status: enums.StatusOk}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	relation := &models.StoreCustomerRelation{
		TenantID: 101, CustomerID: customer.ID, StoreID: store.ID, WxWorkInstanceID: instance.ID, Status: enums.StatusOk,
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create store relation: %v", err)
	}

	data := services.CustomerService.LoadPresentationData([]models.Customer{*customer}, true)
	if data.StoresByID[store.ID] == nil || data.WxWorkInstancesByID[instance.ID] == nil {
		t.Fatalf("missing related presentation data: %+v", data)
	}
	if relations := data.StoreRelationsByCustomerID[customer.ID]; len(relations) != 1 || relations[0].ID != relation.ID {
		t.Fatalf("unexpected customer relations: %+v", relations)
	}
}

func TestLoadCustomerPresentationDataRejectsCrossTenantEnrichment(t *testing.T) {
	db := setupCustomerServiceTestDB(t)
	foreignStore := &models.Store{TenantID: 202, StoreCode: "foreign-presentation-store", Name: "其他租户门店", Status: enums.StatusOk}
	if err := db.Create(foreignStore).Error; err != nil {
		t.Fatalf("create foreign store: %v", err)
	}
	foreignInstance := &models.WxWorkProtocolInstance{
		TenantID: 202, Guid: "foreign-presentation-instance", StoreID: foreignStore.ID,
		EmployeeName: "其他租户员工号", Status: enums.StatusOk,
	}
	if err := db.Create(foreignInstance).Error; err != nil {
		t.Fatalf("create foreign wxwork instance: %v", err)
	}
	customer := &models.Customer{TenantID: 101, Name: "当前租户客户", Status: enums.StatusOk}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	relation := &models.StoreCustomerRelation{
		TenantID: 101, CustomerID: customer.ID, StoreID: foreignStore.ID,
		WxWorkInstanceID: foreignInstance.ID, Status: enums.StatusOk,
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create corrupt cross-tenant store relation: %v", err)
	}

	data := services.CustomerService.LoadPresentationData([]models.Customer{*customer}, true)
	if data.StoresByID[foreignStore.ID] != nil {
		t.Fatalf("foreign store leaked into customer presentation data")
	}
	if data.WxWorkInstancesByID[foreignInstance.ID] != nil {
		t.Fatalf("foreign wxwork instance leaked into customer presentation data")
	}
	if relations := data.StoreRelationsByCustomerID[customer.ID]; len(relations) != 1 || relations[0].ID != relation.ID {
		t.Fatalf("expected local relation to remain visible without foreign enrichment, got %+v", relations)
	}
}

func setupCustomerServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.Customer{},
		&models.CustomerIdentity{},
		&models.Conversation{},
		&models.Store{},
		&models.StoreCustomerRelation{},
		&models.WxWorkProtocolInstance{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}
