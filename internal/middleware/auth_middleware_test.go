package middleware

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestAuthenticateRequestPreservesTenantAuthorizationError(t *testing.T) {
	db := setupAuthMiddlewareTestDB(t)
	now := time.Now()
	tenant := &models.Tenant{
		TenantCode:         "disabled-tenant",
		LegalName:          "Disabled Tenant",
		ShortName:          "Disabled",
		RegistrationType:   "test",
		RegistrationNo:     "DISABLED-TENANT",
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             enums.StatusDisabled,
		AuditFields:        models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &models.User{
		TenantID:       tenant.ID,
		Username:       "disabled_tenant_user",
		Nickname:       "Disabled Tenant User",
		ApprovalStatus: enums.UserApprovalStatusApproved,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	role := &models.Role{
		Name:           "Tenant Member",
		Code:           "tenant_member_test",
		Scope:          constants.RoleScopeTenant,
		AuthorityLevel: constants.RoleAuthorityMember,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := db.Create(&models.UserRole{
		UserID:      user.ID,
		RoleID:      role.ID,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("assign role: %v", err)
	}
	const token = "ak_disabled_tenant"
	if err := db.Create(&models.LoginSession{
		UserID:      user.ID,
		Token:       token,
		ClientType:  constants.ClientTypeAdminWeb,
		ExpiredAt:   now.Add(time.Hour),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create login session: %v", err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/dashboard/user/list", nil)
	ctx.Request.Header.Set("Authorization", "Bearer "+token)

	if authenticateRequest(ctx) {
		t.Fatal("disabled tenant request must be rejected")
	}
	var payload struct {
		ErrorCode int    `json:"errorCode"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != errorsx.CodeAuthForbidden {
		t.Fatalf("errorCode = %d, want %d; response=%s", payload.ErrorCode, errorsx.CodeAuthForbidden, recorder.Body.String())
	}
	if payload.Message != "所属接入公司不存在或已停用" {
		t.Fatalf("tenant authorization message was rewritten: %q", payload.Message)
	}
}

func setupAuthMiddlewareTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{},
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.LoginSession{},
	); err != nil {
		t.Fatalf("migrate auth middleware tables: %v", err)
	}
	sqls.SetDB(db)
	return db
}
