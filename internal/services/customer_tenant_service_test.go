package services

import (
	"path/filepath"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type customerTenantFixture struct {
	db       *gorm.DB
	adminA   *dto.AuthPrincipal
	adminB   *dto.AuthPrincipal
	companyA models.Company
	companyB models.Company
}

func TestCustomerServiceEnforcesTenantContextAcrossCRUD(t *testing.T) {
	fixture := setupCustomerTenantFixture(t)
	customerA, err := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "A租户客户", CompanyID: fixture.companyA.ID}, fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A customer: %v", err)
	}
	customerB, err := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "B租户客户", CompanyID: fixture.companyB.ID}, fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B customer: %v", err)
	}
	if customerA.TenantID != fixture.adminA.ActiveTenantID || customerB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected customer tenants: A=%d B=%d", customerA.TenantID, customerB.TenantID)
	}

	listA, paging := CustomerService.ListCustomers(request.CustomerListRequest{Page: 1, Limit: 20}, fixture.adminA)
	if len(listA) != 1 || listA[0].ID != customerA.ID || paging.Total != 1 {
		t.Fatalf("tenant A customers=%+v paging=%+v", listA, paging)
	}
	if CustomerService.GetInTenant(customerB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B customer")
	}
	if err := CustomerService.UpdateCustomer(request.UpdateCustomerRequest{ID: customerB.ID, CreateCustomerRequest: request.CreateCustomerRequest{Name: "越权客户"}}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B customer")
	}
	if err := CustomerService.UpdateStatus(customerB.ID, int(enums.StatusDisabled), fixture.adminA); err == nil {
		t.Fatal("tenant A must not change tenant B customer status")
	}
	if err := CustomerService.DeleteCustomer(customerB.ID, *fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B customer")
	}
	if _, err := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "跨租户企业客户", CompanyID: fixture.companyB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not bind tenant B company")
	}
	assertTenantBCustomerUnchanged(t, fixture, customerB)
}

func TestExternalCustomerIdentityIsSeparatedByChannelTenant(t *testing.T) {
	fixture := setupCustomerTenantFixture(t)
	external := openidentity.ExternalUser{ExternalSource: enums.ExternalSourceGuest, ExternalID: "same-external-id", ExternalName: "同名访客"}
	customerA := ensureCustomerInTenant(t, fixture.adminA.ActiveTenantID, external)
	customerAAgain := ensureCustomerInTenant(t, fixture.adminA.ActiveTenantID, external)
	customerB := ensureCustomerInTenant(t, fixture.adminB.ActiveTenantID, external)
	if customerA != customerAAgain {
		t.Fatalf("same tenant identity created two customers: %d and %d", customerA, customerAAgain)
	}
	if customerA == customerB {
		t.Fatalf("different tenants reused customer %d for the same external id", customerA)
	}
	if CustomerService.GetByTenantID(customerA, fixture.adminA.ActiveTenantID) == nil || CustomerService.GetByTenantID(customerB, fixture.adminB.ActiveTenantID) == nil {
		t.Fatal("external customers were not assigned to their channel tenants")
	}
}

func TestConversationCreationDerivesCustomerTenantFromChannel(t *testing.T) {
	fixture := setupCustomerTenantFixture(t)
	aiAgentA := &models.AIAgent{TenantID: 101, Name: "A 租户会话测试 Agent", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIOnly}
	aiAgentB := &models.AIAgent{TenantID: 202, Name: "B 租户会话测试 Agent", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIOnly}
	for _, aiAgent := range []*models.AIAgent{aiAgentA, aiAgentB} {
		if err := fixture.db.Create(aiAgent).Error; err != nil {
			t.Fatalf("create AI Agent: %v", err)
		}
	}
	channelA := &models.Channel{TenantID: 101, Name: "A租户渠道", ChannelType: enums.ChannelTypeWeb, ChannelID: "customer-tenant-a", AIAgentID: aiAgentA.ID, Status: enums.StatusOk}
	channelB := &models.Channel{TenantID: 202, Name: "B租户渠道", ChannelType: enums.ChannelTypeWeb, ChannelID: "customer-tenant-b", AIAgentID: aiAgentB.ID, Status: enums.StatusOk}
	if err := fixture.db.Create(channelA).Error; err != nil {
		t.Fatalf("create tenant A channel: %v", err)
	}
	if err := fixture.db.Create(channelB).Error; err != nil {
		t.Fatalf("create tenant B channel: %v", err)
	}
	external := openidentity.ExternalUser{ExternalSource: enums.ExternalSourceGuest, ExternalID: "conversation-shared-id", ExternalName: "跨租户同 ID 访客"}
	conversationA, err := ConversationService.CreateWithoutWelcome(external, channelA.ID, aiAgentA.ID)
	if err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	conversationB, err := ConversationService.CreateWithoutWelcome(external, channelB.ID, aiAgentB.ID)
	if err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}
	if conversationA.CustomerID == conversationB.CustomerID {
		t.Fatalf("cross-tenant conversations reused customer %d", conversationA.CustomerID)
	}
	if CustomerService.GetByTenantID(conversationA.CustomerID, channelA.TenantID) == nil || CustomerService.GetByTenantID(conversationB.CustomerID, channelB.TenantID) == nil {
		t.Fatal("conversation customers do not match channel tenants")
	}
}

func TestCustomerContactServiceRejectsCrossTenantIDs(t *testing.T) {
	fixture := setupCustomerTenantFixture(t)
	customerA, _ := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "A联系人客户"}, fixture.adminA)
	customerB, _ := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "B联系人客户"}, fixture.adminB)
	contactB, err := CustomerContactService.CreateCustomerContact(request.CreateCustomerContactRequest{
		CustomerID: customerB.ID, ContactType: string(enums.ContactTypeMobile), ContactValue: "13900000000", IsPrimary: true, Status: int(enums.StatusOk),
	}, fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B contact: %v", err)
	}
	if contactB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("contact tenant=%d want %d", contactB.TenantID, fixture.adminB.ActiveTenantID)
	}
	if list := CustomerContactService.FindActiveByCustomerID(customerB.ID, fixture.adminA); len(list) != 0 {
		t.Fatalf("tenant A listed tenant B contacts: %+v", list)
	}
	if _, err := CustomerContactService.CreateCustomerContact(request.CreateCustomerContactRequest{
		CustomerID: customerB.ID, ContactType: string(enums.ContactTypeEmail), ContactValue: "cross@example.com", Status: int(enums.StatusOk),
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not create contact for tenant B customer")
	}
	if err := CustomerContactService.UpdateCustomerContact(request.UpdateCustomerContactRequest{
		ID: contactB.ID, ContactType: string(enums.ContactTypeMobile), ContactValue: "13911111111", Status: int(enums.StatusOk),
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B contact")
	}
	if err := CustomerContactService.DeleteCustomerContact(contactB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B contact")
	}
	if _, err := CustomerService.SaveCustomerProfile(request.SaveCustomerProfileRequest{
		ID: &customerA.ID, Name: customerA.Name, Contacts: []request.CustomerProfileContactItem{{ID: &contactB.ID, ContactType: string(enums.ContactTypeMobile), ContactValue: "13900000000"}},
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A profile save must reject tenant B contact id")
	}
	if current := repositories.CustomerContactRepository.Get(fixture.db, contactB.ID); current == nil || current.ContactValue != contactB.ContactValue || current.Status != contactB.Status {
		t.Fatalf("tenant B contact changed: current=%+v original=%+v", current, contactB)
	}
}

func TestCustomerRepositoriesKeepTenantInFinalWritePredicate(t *testing.T) {
	fixture := setupCustomerTenantFixture(t)
	customerB, _ := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "B受保护客户"}, fixture.adminB)
	contactB, _ := CustomerContactService.CreateCustomerContact(request.CreateCustomerContactRequest{
		CustomerID: customerB.ID, ContactType: string(enums.ContactTypeEmail), ContactValue: "protected@example.com", Status: int(enums.StatusOk),
	}, fixture.adminB)
	if err := CustomerService.TouchStoreRelation(customerB.ID, 88, 0, 0, time.Now()); err != nil {
		t.Fatalf("touch tenant B store relation: %v", err)
	}
	relation := repositories.StoreCustomerRelationRepository.Take(fixture.db, "customer_id = ?", customerB.ID)
	if relation == nil || relation.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected store relation: %+v", relation)
	}

	if err := repositories.CustomerRepository.UpdatesInTenant(fixture.db, customerB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "越权客户"}); err != nil {
		t.Fatalf("scoped customer update: %v", err)
	}
	if err := repositories.CustomerContactRepository.UpdatesInTenant(fixture.db, contactB.ID, fixture.adminA.ActiveTenantID, map[string]any{"contact_value": "cross@example.com"}); err != nil {
		t.Fatalf("scoped contact update: %v", err)
	}
	if err := repositories.StoreCustomerRelationRepository.UpdatesInTenant(fixture.db, relation.ID, fixture.adminA.ActiveTenantID, map[string]any{"visit_count": 999}); err != nil {
		t.Fatalf("scoped relation update: %v", err)
	}
	assertTenantBCustomerUnchanged(t, fixture, customerB)
	if current := repositories.CustomerContactRepository.Get(fixture.db, contactB.ID); current == nil || current.ContactValue != contactB.ContactValue {
		t.Fatalf("tenant B contact changed by final predicate test: %+v", current)
	}
	if current := repositories.StoreCustomerRelationRepository.Get(fixture.db, relation.ID); current == nil || current.VisitCount != relation.VisitCount {
		t.Fatalf("tenant B relation changed by final predicate test: %+v", current)
	}
}

func TestCustomerServicesRequireActiveTenant(t *testing.T) {
	setupCustomerTenantFixture(t)
	withoutTenant := &dto.AuthPrincipal{UserID: 9000, Username: "platform-admin"}
	if _, err := CustomerService.CreateCustomer(request.CreateCustomerRequest{Name: "无租户客户"}, withoutTenant); err == nil {
		t.Fatal("customer create without active tenant must fail")
	}
	if list, paging := CustomerService.ListCustomers(request.CustomerListRequest{Page: 1, Limit: 20}, withoutTenant); len(list) != 0 || paging.Total != 0 {
		t.Fatalf("customers without tenant=%+v paging=%+v", list, paging)
	}
}

func setupCustomerTenantFixture(t *testing.T) customerTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "customer-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Company{}, &models.Customer{}, &models.CustomerIdentity{}, &models.CustomerContact{},
		&models.StoreCustomerRelation{}, &models.Channel{}, &models.AIAgent{}, &models.Conversation{},
		&models.ConversationParticipant{}, &models.ConversationEventLog{}, &models.Message{},
	); err != nil {
		t.Fatalf("migrate customer tenant tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	fixture := customerTenantFixture{
		db:       db,
		adminA:   &dto.AuthPrincipal{UserID: 9001, Username: "admin-a", ActiveTenantID: 101},
		adminB:   &dto.AuthPrincipal{UserID: 9002, Username: "admin-b", ActiveTenantID: 202},
		companyA: models.Company{TenantID: 101, Name: "A租户客户企业", Status: enums.StatusOk},
		companyB: models.Company{TenantID: 202, Name: "B租户客户企业", Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.companyA).Error; err != nil {
		t.Fatalf("create tenant A company: %v", err)
	}
	if err := db.Create(&fixture.companyB).Error; err != nil {
		t.Fatalf("create tenant B company: %v", err)
	}
	return fixture
}

func ensureCustomerInTenant(t *testing.T, tenantID int64, external openidentity.ExternalUser) int64 {
	t.Helper()
	var customerID int64
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		id, err := CustomerService.EnsureExternalCustomer(ctx, tenantID, external)
		customerID = id
		return err
	}); err != nil {
		t.Fatalf("ensure external customer in tenant %d: %v", tenantID, err)
	}
	return customerID
}

func assertTenantBCustomerUnchanged(t *testing.T, fixture customerTenantFixture, original *models.Customer) {
	t.Helper()
	current := repositories.CustomerRepository.Get(fixture.db, original.ID)
	if current == nil || current.TenantID != fixture.adminB.ActiveTenantID || current.Name != original.Name || current.Status != original.Status || current.CompanyID != original.CompanyID {
		t.Fatalf("tenant B customer changed: current=%+v original=%+v", current, original)
	}
}
