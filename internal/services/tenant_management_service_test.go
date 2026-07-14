package services

import (
	"encoding/base64"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
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

var tenantManagementTestDBSequence atomic.Uint64

func TestTenantServiceCreateTenantBuildsAtomicCompanyFoundation(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	req := tenantManagementCreateRequest("atomic", "91350100MA8A1B2C3D")

	result, err := TenantService.CreateTenant(req, operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if result.Tenant == nil || result.Supervisor == nil || result.DefaultAgentTeam == nil || result.Invitation == nil || result.SupervisorPassword == "" {
		t.Fatalf("incomplete tenant creation result: %+v", result)
	}
	if result.Supervisor.TenantID != result.Tenant.ID || result.Supervisor.ApprovalStatus != enums.UserApprovalStatusApproved || !result.Supervisor.MustChangePassword {
		t.Fatalf("unexpected supervisor account: %+v", result.Supervisor)
	}
	if result.DefaultAgentTeam.TenantID != result.Tenant.ID || !result.DefaultAgentTeam.IsDefault {
		t.Fatalf("unexpected default agent team: %+v", result.DefaultAgentTeam)
	}
	if result.Invitation.TenantID != result.Tenant.ID || result.Invitation.Version != 1 || result.Invitation.Status != enums.StatusOk {
		t.Fatalf("unexpected invitation: %+v", result.Invitation)
	}
	if result.Invitation.CodeHash != hashTenantInvitationCode(result.InvitationCode) {
		t.Fatal("invitation hash does not match the one-time plaintext")
	}
	decrypted, err := decryptTenantInvitationCode(result.Invitation.CodeCiphertext, config.Current().Auth.InvitationEncryptionKey)
	if err != nil || decrypted != result.InvitationCode {
		t.Fatalf("decrypt invitation = %q, %v", decrypted, err)
	}

	tenantAdmin := repositories.RoleRepository.GetByCode(db, constants.RoleCodeTenantAdmin)
	if tenantAdmin == nil {
		t.Fatal("tenant admin role missing")
	}
	var relationCount int64
	if err := db.Model(&models.UserRole{}).
		Where("user_id = ? AND role_id = ?", result.Supervisor.ID, tenantAdmin.ID).
		Count(&relationCount).Error; err != nil {
		t.Fatalf("count supervisor role: %v", err)
	}
	if relationCount != 1 {
		t.Fatalf("supervisor tenant_admin role count = %d", relationCount)
	}

	current, code, err := TenantInvitationService.Current(result.Tenant.ID)
	if err != nil || current.ID != result.Invitation.ID || code != result.InvitationCode {
		t.Fatalf("current invitation mismatch: current=%+v code=%q err=%v", current, code, err)
	}
}

func TestTenantServiceCreateTenantRollsBackOnSupervisorConflict(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	now := time.Now()
	existing := &models.User{
		Username:       "conflict_supervisor",
		Nickname:       "Existing",
		ApprovalStatus: enums.UserApprovalStatusApproved,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(existing).Error; err != nil {
		t.Fatalf("create conflicting user: %v", err)
	}
	req := tenantManagementCreateRequest("conflict", "91350100MA8E4F5G6H")
	req.Supervisor.Username = existing.Username

	if _, err := TenantService.CreateTenant(req, operator); err == nil {
		t.Fatal("expected supervisor conflict to fail tenant creation")
	}
	assertTenantFoundationCounts(t, db, 0, 0, 0)
}

func TestTenantServiceRejectsDuplicateRegistrationAndKeepsTenantCodeImmutable(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	first, err := TenantService.CreateTenant(tenantManagementCreateRequest("first", "91350100MA8J7K8L9M"), operator)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	if _, err = TenantService.CreateTenant(tenantManagementCreateRequest("second", "91350100MA8J7K8L9M"), operator); err == nil {
		t.Fatal("expected duplicate legal registration to be rejected")
	}
	assertTenantFoundationCounts(t, db, 1, 1, 1)

	originalCode := first.Tenant.TenantCode
	err = TenantService.UpdateTenant(request.UpdateTenantRequest{
		ID:               first.Tenant.ID,
		LegalName:        "Updated Legal Name",
		ShortName:        "Updated",
		RegistrationType: first.Tenant.RegistrationType,
		RegistrationNo:   first.Tenant.RegistrationNo,
		ContactName:      "Updated Contact",
		ContactMobile:    "13800009999",
		ContactEmail:     "updated@example.com",
		Address:          "Updated Address",
	}, operator)
	if err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	updated := TenantService.Get(first.Tenant.ID)
	if updated == nil || updated.TenantCode != originalCode || updated.LegalName != "Updated Legal Name" {
		t.Fatalf("unexpected updated tenant: %+v", updated)
	}
}

func TestTenantInvitationServiceRotateInvalidatesOldCode(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("rotate", "91350100MA8N1P2Q3R"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	operator.ActiveTenantID = created.Tenant.ID
	rotated, newCode, err := TenantInvitationService.Rotate(created.Tenant.ID, operator)
	if err != nil {
		t.Fatalf("rotate invitation: %v", err)
	}
	if rotated.Version != 2 || newCode == created.InvitationCode {
		t.Fatalf("unexpected rotated invitation: %+v code=%q", rotated, newCode)
	}
	old := repositories.TenantInvitationRepository.Get(db, created.Invitation.ID)
	if old == nil || old.Status != enums.StatusDisabled || old.RotatedAt == nil {
		t.Fatalf("old invitation was not invalidated: %+v", old)
	}
	if current := repositories.TenantInvitationRepository.FindCurrent(db, created.Tenant.ID); current == nil || current.ID != rotated.ID {
		t.Fatalf("current invitation = %+v, want %d", current, rotated.ID)
	}
	var count int64
	if err := db.Model(&models.TenantInvitation{}).Where("tenant_id = ?", created.Tenant.ID).Count(&count).Error; err != nil {
		t.Fatalf("count invitation history: %v", err)
	}
	if count != 2 {
		t.Fatalf("invitation history count = %d", count)
	}

	if err := db.Model(&models.TenantInvitation{}).Where("id = ?", created.Invitation.ID).Update("status", enums.StatusOk).Error; err != nil {
		t.Fatalf("restore stale active invitation: %v", err)
	}
	rotatedAgain, _, err := TenantInvitationService.Rotate(created.Tenant.ID, operator)
	if err != nil {
		t.Fatalf("rotate invitation with stale active version: %v", err)
	}
	if rotatedAgain.Version != 3 {
		t.Fatalf("second rotation version = %d, want 3", rotatedAgain.Version)
	}
	var activeCount int64
	if err := db.Model(&models.TenantInvitation{}).
		Where("tenant_id = ? AND status = ?", created.Tenant.ID, enums.StatusOk).
		Count(&activeCount).Error; err != nil {
		t.Fatalf("count active invitations: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active invitation count = %d, want 1", activeCount)
	}
}

func TestTenantServiceCreateTenantRequiresConfiguredEncryptionKey(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	cfg := config.Current()
	cfg.Auth.InvitationEncryptionKey = ""
	config.SetCurrent(&cfg)
	if _, err := TenantService.CreateTenant(tenantManagementCreateRequest("nokey", "91350100MA8T4U5W6X"), operator); err == nil {
		t.Fatal("expected missing invitation key to reject tenant creation")
	}
	assertTenantFoundationCounts(t, db, 0, 0, 0)
}

func TestTenantServiceRejectsInvalidLegalIdentityAndEmailFormats(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	invalidRegistration := tenantManagementCreateRequest("bad-registration", "91350100INVALID")
	if _, err := TenantService.CreateTenant(invalidRegistration, operator); err == nil {
		t.Fatal("expected malformed unified social credit code to be rejected")
	}
	invalidEmail := tenantManagementCreateRequest("bad-email", "91350100MA8Y7A8B9C")
	invalidEmail.Supervisor.Email = "not-an-email"
	if _, err := TenantService.CreateTenant(invalidEmail, operator); err == nil {
		t.Fatal("expected malformed supervisor email to be rejected")
	}
	assertTenantFoundationCounts(t, db, 0, 0, 0)
}

func setupTenantManagementTestDB(t *testing.T) (*gorm.DB, *dto.AuthPrincipal) {
	t.Helper()
	dbName := "tenant_management_" + strconv.FormatUint(tenantManagementTestDBSequence.Add(1), 10)
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite connection: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	if err := db.AutoMigrate(
		&models.Tenant{},
		&models.TenantInvitation{},
		&models.User{},
		&models.Role{},
		&models.UserRole{},
		&models.AgentTeam{},
	); err != nil {
		t.Fatalf("migrate tenant management tables: %v", err)
	}
	now := time.Now()
	roles := []*models.Role{
		{Name: "Super Admin", Code: constants.RoleCodeSuperAdmin, Scope: constants.RoleScopePlatform, AuthorityLevel: constants.RoleAuthoritySuperAdmin, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{Name: "Tenant Admin", Code: constants.RoleCodeTenantAdmin, Scope: constants.RoleScopeTenant, AuthorityLevel: constants.RoleAuthorityTenantAdmin, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
	}
	if err := db.Create(&roles).Error; err != nil {
		t.Fatalf("create roles: %v", err)
	}
	sqls.SetDB(db)
	config.SetCurrent(&config.Config{Auth: config.AuthConfig{
		InvitationEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	}})
	operator := &dto.AuthPrincipal{
		UserID:            9001,
		Username:          "platform_super",
		Roles:             []string{constants.RoleCodeSuperAdmin},
		Permissions:       []string{constants.PermissionTenantCreate.Code, constants.PermissionTenantUpdate.Code, constants.PermissionTenantUpdateStatus.Code, constants.PermissionTenantInviteRotate.Code},
		IsPlatformAccount: true,
	}
	return db, operator
}

func tenantManagementCreateRequest(suffix, registrationNo string) request.CreateTenantRequest {
	return request.CreateTenantRequest{
		LegalName:        "Tenant " + suffix,
		ShortName:        "T-" + suffix,
		RegistrationType: "unified_social_credit_code",
		RegistrationNo:   registrationNo,
		ContactName:      "Contact " + suffix,
		ContactMobile:    "13800000000",
		ContactEmail:     suffix + "@tenant.example.com",
		Address:          "Test Address",
		Supervisor: request.CreateTenantSupervisorRequest{
			Username: "supervisor_" + suffix,
			Nickname: "Supervisor " + suffix,
			Mobile:   "139" + registrationNo[len(registrationNo)-8:],
			Email:    "supervisor_" + suffix + "@tenant.example.com",
		},
	}
}

func assertTenantFoundationCounts(t *testing.T, db *gorm.DB, tenants, invitations, teams int64) {
	t.Helper()
	checks := []struct {
		model any
		want  int64
		name  string
	}{
		{model: &models.Tenant{}, want: tenants, name: "tenants"},
		{model: &models.TenantInvitation{}, want: invitations, name: "invitations"},
		{model: &models.AgentTeam{}, want: teams, name: "agent teams"},
	}
	for _, check := range checks {
		var got int64
		if err := db.Model(check.model).Count(&got).Error; err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Fatalf("%s count = %d, want %d", check.name, got, check.want)
		}
	}
}
