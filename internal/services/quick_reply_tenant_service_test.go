package services

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestQuickReplyServiceEnforcesTenantContextAcrossCRUD(t *testing.T) {
	db := setupQuickReplyTenantServiceDB(t)
	tenantA := &dto.AuthPrincipal{UserID: 1, Username: "tenant-a", TenantID: 101, ActiveTenantID: 101}
	tenantB := &dto.AuthPrincipal{UserID: 2, Username: "tenant-b", TenantID: 202, ActiveTenantID: 202}
	req := request.CreateQuickReplyRequest{GroupName: "接待", Title: "欢迎", Content: "您好", Status: enums.StatusOk, SortNo: 10}
	createdA, err := QuickReplyService.CreateQuickReply(req, tenantA)
	if err != nil {
		t.Fatalf("create tenant A quick reply: %v", err)
	}
	createdB, err := QuickReplyService.CreateQuickReply(req, tenantB)
	if err != nil {
		t.Fatalf("create tenant B quick reply: %v", err)
	}
	if createdA.TenantID != tenantA.ActiveTenantID || createdB.TenantID != tenantB.ActiveTenantID {
		t.Fatalf("unexpected created tenants: A=%d B=%d", createdA.TenantID, createdB.TenantID)
	}
	if QuickReplyService.GetInTenant(createdB.ID, tenantA) != nil {
		t.Fatal("tenant A must not read tenant B quick reply")
	}
	listA := QuickReplyService.FindInTenant(sqls.NewCnd().Asc("id"), tenantA)
	if len(listA) != 1 || listA[0].ID != createdA.ID {
		t.Fatalf("tenant A list=%+v want only %d", listA, createdA.ID)
	}
	if err := QuickReplyService.UpdateQuickReply(request.UpdateQuickReplyRequest{
		ID: createdB.ID, CreateQuickReplyRequest: request.CreateQuickReplyRequest{Title: "越权", Content: "越权", Status: enums.StatusOk},
	}, tenantA); err == nil {
		t.Fatal("tenant A must not update tenant B quick reply")
	}
	if err := QuickReplyService.DeleteQuickReply(createdB.ID, tenantA); err == nil {
		t.Fatal("tenant A must not delete tenant B quick reply")
	}
	if err := QuickReplyService.UpdateQuickReply(request.UpdateQuickReplyRequest{
		ID: createdA.ID, CreateQuickReplyRequest: request.CreateQuickReplyRequest{GroupName: "售后", Title: "已更新", Content: "更新内容", Status: enums.StatusOk},
	}, tenantA); err != nil {
		t.Fatalf("update tenant A quick reply: %v", err)
	}
	if current := QuickReplyService.GetInTenant(createdA.ID, tenantA); current == nil || current.Title != "已更新" {
		t.Fatalf("tenant A quick reply was not updated: %+v", current)
	}
	if current := QuickReplyService.GetInTenant(createdB.ID, tenantB); current == nil {
		t.Fatal("tenant B quick reply was changed by cross-tenant operations")
	}
	if err := QuickReplyService.DeleteQuickReply(createdA.ID, tenantA); err != nil {
		t.Fatalf("delete tenant A quick reply: %v", err)
	}
	var count int64
	if err := db.Model(&models.QuickReply{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("quick reply count=%d error=%v want 1", count, err)
	}
}

func TestQuickReplyServiceRejectsCreateWithoutTenantContext(t *testing.T) {
	setupQuickReplyTenantServiceDB(t)
	operator := &dto.AuthPrincipal{UserID: 1}
	_, err := QuickReplyService.CreateQuickReply(request.CreateQuickReplyRequest{Title: "欢迎", Content: "您好", Status: enums.StatusOk}, operator)
	if err == nil {
		t.Fatal("expected create without active tenant to fail")
	}
	if list := QuickReplyService.FindInTenant(sqls.NewCnd(), operator); len(list) != 0 {
		t.Fatalf("list without active tenant=%+v want empty", list)
	}
}

func setupQuickReplyTenantServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quick-reply-service.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.QuickReply{}); err != nil {
		t.Fatalf("migrate quick reply: %v", err)
	}
	sqls.SetDB(db)
	return db
}
