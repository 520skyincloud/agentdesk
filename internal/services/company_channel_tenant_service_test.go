package services

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type companyChannelTenantFixture struct {
	db      *gorm.DB
	adminA  *dto.AuthPrincipal
	adminB  *dto.AuthPrincipal
	aiAgent models.AIAgent
}

func TestCompanyServiceEnforcesTenantContextAcrossCRUD(t *testing.T) {
	fixture := setupCompanyChannelTenantFixture(t)
	companyA, err := CompanyService.CreateCompany(request.CreateCompanyRequest{Name: "A租户客户企业", Code: "A-CUSTOMER"}, fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A company: %v", err)
	}
	companyB, err := CompanyService.CreateCompany(request.CreateCompanyRequest{Name: "B租户客户企业", Code: "B-CUSTOMER"}, fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B company: %v", err)
	}
	if companyA.TenantID != fixture.adminA.ActiveTenantID || companyB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected company tenants: A=%d B=%d", companyA.TenantID, companyB.TenantID)
	}

	companies, paging := CompanyService.FindPageInTenant(companyChannelTenantPage(), fixture.adminA)
	if len(companies) != 1 || companies[0].ID != companyA.ID || paging.Total != 1 {
		t.Fatalf("tenant A companies=%+v paging=%+v", companies, paging)
	}
	if CompanyService.GetInTenant(companyB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B company")
	}
	if err := CompanyService.UpdateCompany(request.UpdateCompanyRequest{
		ID: companyB.ID, CreateCompanyRequest: request.CreateCompanyRequest{Name: "越权更新企业"},
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B company")
	}
	if err := CompanyService.UpdateStatus(companyB.ID, int(enums.StatusDisabled), fixture.adminA); err == nil {
		t.Fatal("tenant A must not change tenant B company status")
	}
	if err := CompanyService.DeleteCompany(companyB.ID, *fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B company")
	}
	if err := CompanyService.UpdateCompany(request.UpdateCompanyRequest{
		ID: companyA.ID, CreateCompanyRequest: request.CreateCompanyRequest{Name: "A租户已更新企业", Code: "A-UPDATED"},
	}, fixture.adminA); err != nil {
		t.Fatalf("update tenant A company: %v", err)
	}
	if current := CompanyService.GetInTenant(companyA.ID, fixture.adminA); current == nil || current.Name != "A租户已更新企业" {
		t.Fatalf("tenant A company was not updated: %+v", current)
	}
	assertCompanyChannelTenantBUnchanged(t, fixture, companyB, nil)
}

func TestChannelServiceEnforcesTenantContextAcrossCRUD(t *testing.T) {
	fixture := setupCompanyChannelTenantFixture(t)
	channelA, err := ChannelService.CreateChannel(companyChannelTenantCreateChannelRequest("A租户官网", fixture.aiAgent.ID), fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A channel: %v", err)
	}
	channelB, err := ChannelService.CreateChannel(companyChannelTenantCreateChannelRequest("B租户官网", fixture.aiAgent.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B channel: %v", err)
	}
	if channelA.TenantID != fixture.adminA.ActiveTenantID || channelB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected channel tenants: A=%d B=%d", channelA.TenantID, channelB.TenantID)
	}

	channels, paging := ChannelService.FindPageInTenant(companyChannelTenantPage(), fixture.adminA)
	if len(channels) != 1 || channels[0].ID != channelA.ID || paging.Total != 1 {
		t.Fatalf("tenant A channels=%+v paging=%+v", channels, paging)
	}
	if ChannelService.GetInTenant(channelB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B channel")
	}
	if err := ChannelService.UpdateChannel(request.UpdateChannelRequest{
		ID: channelB.ID, CreateChannelRequest: companyChannelTenantCreateChannelRequest("越权更新渠道", fixture.aiAgent.ID),
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B channel")
	}
	if err := ChannelService.UpdateStatus(channelB.ID, int(enums.StatusDisabled), fixture.adminA); err == nil {
		t.Fatal("tenant A must not change tenant B channel status")
	}
	if _, err := ChannelService.ResetUserTokenSecret(channelB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not reset tenant B channel secret")
	}
	if err := ChannelService.DeleteChannel(channelB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B channel")
	}
	if err := ChannelService.UpdateChannel(request.UpdateChannelRequest{
		ID: channelA.ID, CreateChannelRequest: companyChannelTenantCreateChannelRequest("A租户已更新官网", fixture.aiAgent.ID),
	}, fixture.adminA); err != nil {
		t.Fatalf("update tenant A channel: %v", err)
	}
	if secret, err := ChannelService.ResetUserTokenSecret(channelA.ID, fixture.adminA); err != nil || secret == "" {
		t.Fatalf("reset tenant A channel secret: secret=%q error=%v", secret, err)
	}
	if current := ChannelService.GetInTenant(channelA.ID, fixture.adminA); current == nil || current.Name != "A租户已更新官网" {
		t.Fatalf("tenant A channel was not updated: %+v", current)
	}
	assertCompanyChannelTenantBUnchanged(t, fixture, nil, channelB)
}

func TestCompanyAndChannelRepositoriesKeepTenantInFinalWritePredicate(t *testing.T) {
	fixture := setupCompanyChannelTenantFixture(t)
	companyB, err := CompanyService.CreateCompany(request.CreateCompanyRequest{Name: "B租户受保护企业"}, fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B company: %v", err)
	}
	channelB, err := ChannelService.CreateChannel(companyChannelTenantCreateChannelRequest("B租户受保护渠道", fixture.aiAgent.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B channel: %v", err)
	}

	if err := repositories.CompanyRepository.UpdatesInTenant(fixture.db, companyB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "越权企业"}); err != nil {
		t.Fatalf("scoped company update: %v", err)
	}
	if err := repositories.ChannelRepository.UpdatesInTenant(fixture.db, channelB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "越权渠道"}); err != nil {
		t.Fatalf("scoped channel update: %v", err)
	}
	assertCompanyChannelTenantBUnchanged(t, fixture, companyB, channelB)
}

func TestCompanyAndChannelServicesRequireActiveTenant(t *testing.T) {
	fixture := setupCompanyChannelTenantFixture(t)
	withoutTenant := &dto.AuthPrincipal{UserID: 9000, Username: "platform-admin"}
	if _, err := CompanyService.CreateCompany(request.CreateCompanyRequest{Name: "无租户企业"}, withoutTenant); err == nil {
		t.Fatal("company create without active tenant must fail")
	}
	if _, err := ChannelService.CreateChannel(companyChannelTenantCreateChannelRequest("无租户渠道", fixture.aiAgent.ID), withoutTenant); err == nil {
		t.Fatal("channel create without active tenant must fail")
	}
	if companies, _ := CompanyService.FindPageInTenant(companyChannelTenantPage(), withoutTenant); len(companies) != 0 {
		t.Fatalf("companies without tenant=%+v want empty", companies)
	}
	if channels, _ := ChannelService.FindPageInTenant(companyChannelTenantPage(), withoutTenant); len(channels) != 0 {
		t.Fatalf("channels without tenant=%+v want empty", channels)
	}
}

func setupCompanyChannelTenantFixture(t *testing.T) companyChannelTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "company-channel-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Company{}, &models.Channel{}, &models.AIAgent{}); err != nil {
		t.Fatalf("migrate company and channel tenant tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	fixture := companyChannelTenantFixture{
		db:      db,
		adminA:  &dto.AuthPrincipal{UserID: 9001, Username: "admin-a", ActiveTenantID: 101},
		adminB:  &dto.AuthPrincipal{UserID: 9002, Username: "admin-b", ActiveTenantID: 202},
		aiAgent: models.AIAgent{Name: "测试接待 Agent", Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.aiAgent).Error; err != nil {
		t.Fatalf("create AI Agent fixture: %v", err)
	}
	return fixture
}

func companyChannelTenantCreateChannelRequest(name string, aiAgentID int64) request.CreateChannelRequest {
	return request.CreateChannelRequest{ChannelType: enums.ChannelTypeWeb, AIAgentID: aiAgentID, Name: name, Status: int(enums.StatusOk)}
}

func companyChannelTenantPage() *sqls.Cnd {
	cnd := sqls.NewCnd().Asc("id")
	cnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	return cnd
}

func assertCompanyChannelTenantBUnchanged(t *testing.T, fixture companyChannelTenantFixture, company *models.Company, channel *models.Channel) {
	t.Helper()
	if company != nil {
		current := repositories.CompanyRepository.Get(fixture.db, company.ID)
		if current == nil || current.TenantID != fixture.adminB.ActiveTenantID || current.Name != company.Name || current.Status != company.Status {
			t.Fatalf("tenant B company changed: current=%+v original=%+v", current, company)
		}
	}
	if channel != nil {
		current := repositories.ChannelRepository.Get(fixture.db, channel.ID)
		if current == nil || current.TenantID != fixture.adminB.ActiveTenantID || current.Name != channel.Name || current.Status != channel.Status || current.ConfigJSON != channel.ConfigJSON {
			t.Fatalf("tenant B channel changed: current=%+v original=%+v", current, channel)
		}
	}
}
