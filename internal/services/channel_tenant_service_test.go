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

type channelTenantFixture struct {
	db       *gorm.DB
	adminA   *dto.AuthPrincipal
	adminB   *dto.AuthPrincipal
	aiAgentA models.AIAgent
	aiAgentB models.AIAgent
}

func TestChannelServiceEnforcesTenantContextAcrossCRUD(t *testing.T) {
	fixture := setupChannelTenantFixture(t)
	channelA, err := ChannelService.CreateChannel(channelTenantCreateRequest("A租户官网", fixture.aiAgentA.ID), fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A channel: %v", err)
	}
	channelB, err := ChannelService.CreateChannel(channelTenantCreateRequest("B租户官网", fixture.aiAgentB.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B channel: %v", err)
	}
	if channelA.TenantID != fixture.adminA.ActiveTenantID || channelB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected channel tenants: A=%d B=%d", channelA.TenantID, channelB.TenantID)
	}

	channels, paging := ChannelService.FindPageInTenant(channelTenantPage(), fixture.adminA)
	if len(channels) != 1 || channels[0].ID != channelA.ID || paging.Total != 1 {
		t.Fatalf("tenant A channels=%+v paging=%+v", channels, paging)
	}
	if ChannelService.GetInTenant(channelB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B channel")
	}
	if err := ChannelService.UpdateChannel(request.UpdateChannelRequest{
		ID: channelB.ID, CreateChannelRequest: channelTenantCreateRequest("越权更新渠道", fixture.aiAgentA.ID),
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
		ID: channelA.ID, CreateChannelRequest: channelTenantCreateRequest("A租户已更新官网", fixture.aiAgentA.ID),
	}, fixture.adminA); err != nil {
		t.Fatalf("update tenant A channel: %v", err)
	}
	if secret, err := ChannelService.ResetUserTokenSecret(channelA.ID, fixture.adminA); err != nil || secret == "" {
		t.Fatalf("reset tenant A channel secret: secret=%q error=%v", secret, err)
	}
	if current := ChannelService.GetInTenant(channelA.ID, fixture.adminA); current == nil || current.Name != "A租户已更新官网" {
		t.Fatalf("tenant A channel was not updated: %+v", current)
	}
	assertTenantBChannelUnchanged(t, fixture, channelB)
}

func TestChannelRepositoryKeepsTenantInFinalWritePredicate(t *testing.T) {
	fixture := setupChannelTenantFixture(t)
	channelB, err := ChannelService.CreateChannel(channelTenantCreateRequest("B租户受保护渠道", fixture.aiAgentB.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B channel: %v", err)
	}
	if err := repositories.ChannelRepository.UpdatesInTenant(fixture.db, channelB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "越权渠道"}); err != nil {
		t.Fatalf("scoped channel update: %v", err)
	}
	assertTenantBChannelUnchanged(t, fixture, channelB)
}

func TestChannelServiceRequiresActiveTenant(t *testing.T) {
	fixture := setupChannelTenantFixture(t)
	withoutTenant := &dto.AuthPrincipal{UserID: 9000, Username: "platform-admin"}
	if _, err := ChannelService.CreateChannel(channelTenantCreateRequest("无租户渠道", fixture.aiAgentA.ID), withoutTenant); err == nil {
		t.Fatal("channel create without active tenant must fail")
	}
	if channels, _ := ChannelService.FindPageInTenant(channelTenantPage(), withoutTenant); len(channels) != 0 {
		t.Fatalf("channels without tenant=%+v want empty", channels)
	}
}

func setupChannelTenantFixture(t *testing.T) channelTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "channel-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Channel{}, &models.AIAgent{}); err != nil {
		t.Fatalf("migrate channel tenant tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	fixture := channelTenantFixture{
		db:       db,
		adminA:   &dto.AuthPrincipal{UserID: 9001, Username: "admin-a", ActiveTenantID: 101},
		adminB:   &dto.AuthPrincipal{UserID: 9002, Username: "admin-b", ActiveTenantID: 202},
		aiAgentA: models.AIAgent{TenantID: 101, Name: "A 测试接待 Agent", Status: enums.StatusOk},
		aiAgentB: models.AIAgent{TenantID: 202, Name: "B 测试接待 Agent", Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.aiAgentA).Error; err != nil {
		t.Fatalf("create tenant A AI Agent fixture: %v", err)
	}
	if err := db.Create(&fixture.aiAgentB).Error; err != nil {
		t.Fatalf("create tenant B AI Agent fixture: %v", err)
	}
	return fixture
}

func channelTenantCreateRequest(name string, aiAgentID int64) request.CreateChannelRequest {
	return request.CreateChannelRequest{ChannelType: enums.ChannelTypeWeb, AIAgentID: aiAgentID, Name: name, Status: int(enums.StatusOk)}
}

func channelTenantPage() *sqls.Cnd {
	cnd := sqls.NewCnd().Asc("id")
	cnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	return cnd
}

func assertTenantBChannelUnchanged(t *testing.T, fixture channelTenantFixture, channel *models.Channel) {
	t.Helper()
	current := repositories.ChannelRepository.Get(fixture.db, channel.ID)
	if current == nil || current.TenantID != fixture.adminB.ActiveTenantID || current.Name != channel.Name || current.Status != channel.Status || current.ConfigJSON != channel.ConfigJSON {
		t.Fatalf("tenant B channel changed: current=%+v original=%+v", current, channel)
	}
}
