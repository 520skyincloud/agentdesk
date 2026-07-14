package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestWxWorkNotifyBuildTextContent(t *testing.T) {
	svc := newWxWorkNotifyService()
	got := svc.buildTextContent("工单提醒", "这是一条测试消息")
	if got != "工单提醒\n\n这是一条测试消息" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestWxWorkNotifyDefaultRecipients(t *testing.T) {
	db := setupWxWorkNotifyTestDB(t)
	config.SetCurrent(&config.Config{
		WxWork: config.WxWorkConfig{
			CorpID: "corp-1",
			Notify: config.WxWorkNotifyConfig{
				Enabled: true,
				ToUsers: []int64{11, 11, 12},
			},
		},
	})
	now := time.Now()
	for _, identity := range []*models.UserIdentity{
		{
			UserID:         11,
			Provider:       enums.ThirdProviderWxWork,
			ProviderUserID: "wx_user_a",
			ProviderCorpID: "corp-1",
			Status:         enums.StatusOk,
			LastAuthAt:     &now,
		},
		{
			UserID:         12,
			Provider:       enums.ThirdProviderWxWork,
			ProviderUserID: "wx_user_b",
			ProviderCorpID: "corp-1",
			Status:         enums.StatusOk,
			LastAuthAt:     &now,
		},
	} {
		if err := repositories.UserIdentityRepository.Create(db, identity); err != nil {
			t.Fatalf("create user identity error = %v", err)
		}
	}

	svc := newWxWorkNotifyService()
	toUsers := svc.defaultToUsers()
	if len(toUsers) != 2 || toUsers[0] != "wx_user_a" || toUsers[1] != "wx_user_b" {
		t.Fatalf("unexpected users: %#v", toUsers)
	}
}

func TestWxWorkNotifyNormalizeDuplicateCheckInterval(t *testing.T) {
	svc := newWxWorkNotifyService()
	if got := svc.normalizeDuplicateCheckInterval(0); got != 1800 {
		t.Fatalf("expected default interval 1800, got %d", got)
	}
	if got := svc.normalizeDuplicateCheckInterval(20000); got != 14400 {
		t.Fatalf("expected capped interval 14400, got %d", got)
	}
	if got := svc.normalizeDuplicateCheckInterval(600); got != 600 {
		t.Fatalf("expected interval 600, got %d", got)
	}
}

func TestWxWorkNotifyTenantScopedRecipients(t *testing.T) {
	db := setupWxWorkNotifyTestDB(t)
	config.SetCurrent(&config.Config{
		WxWork: config.WxWorkConfig{
			CorpID: "corp-tenant-scope",
			Notify: config.WxWorkNotifyConfig{Enabled: true, ToUsers: []int64{21, 22, 23}},
		},
	})
	users := []*models.User{
		{ID: 21, TenantID: 101, Username: "tenant-a-notify", Status: enums.StatusOk},
		{ID: 22, TenantID: 202, Username: "tenant-b-notify", Status: enums.StatusOk},
		{ID: 23, TenantID: 0, Username: "platform-notify", Status: enums.StatusOk},
	}
	for _, user := range users {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user %d: %v", user.ID, err)
		}
	}
	now := time.Now()
	for _, identity := range []*models.UserIdentity{
		{UserID: 21, Provider: enums.ThirdProviderWxWork, ProviderUserID: "wx_tenant_a", ProviderCorpID: "corp-tenant-scope", Status: enums.StatusOk, LastAuthAt: &now},
		{UserID: 22, Provider: enums.ThirdProviderWxWork, ProviderUserID: "wx_tenant_b", ProviderCorpID: "corp-tenant-scope", Status: enums.StatusOk, LastAuthAt: &now},
		{UserID: 23, Provider: enums.ThirdProviderWxWork, ProviderUserID: "wx_platform", ProviderCorpID: "corp-tenant-scope", Status: enums.StatusOk, LastAuthAt: &now},
	} {
		if err := repositories.UserIdentityRepository.Create(db, identity); err != nil {
			t.Fatalf("create identity for user %d: %v", identity.UserID, err)
		}
	}

	svc := newWxWorkNotifyService()
	if got := svc.resolveToUsersByUserIDsInTenant([]int64{22}, 101, false); len(got) != 0 {
		t.Fatalf("foreign assignee resolved as tenant A recipient: %#v", got)
	}
	if got := svc.resolveTenantToUsers(22, 101); len(got) != 0 {
		t.Fatalf("foreign assignee fell back to tenant A defaults: %#v", got)
	}
	got := svc.defaultToUsersInTenant(101)
	if len(got) != 2 || got[0] != "wx_tenant_a" || got[1] != "wx_platform" {
		t.Fatalf("tenant A fallback recipients = %#v", got)
	}
}

func setupWxWorkNotifyTestDB(t *testing.T) *gorm.DB {
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
	if err := db.AutoMigrate(&models.User{}, &models.UserIdentity{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}
