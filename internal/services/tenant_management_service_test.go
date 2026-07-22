package services

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var tenantManagementTestDBSequence atomic.Uint64

const tenantManagementIndustryProfileID int64 = 7001

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
	internalAgent := repositories.AIAgentRepository.FindOne(db, sqls.NewCnd().Eq("tenant_id", result.Tenant.ID).Eq("status", enums.StatusOk))
	if internalAgent == nil || internalAgent.AIConfigID != 0 || internalAgent.Name != "默认接待策略" {
		t.Fatalf("unexpected internal runtime identity: %+v", internalAgent)
	}
	if result.Invitation.TenantID != result.Tenant.ID || result.Invitation.Version != 1 || result.Invitation.Status != enums.StatusOk {
		t.Fatalf("unexpected invitation: %+v", result.Invitation)
	}
	assertFreshTenantInvitationExpiry(t, result.Invitation)
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
	assertSingleUserRoleChangeLog(t, db, result.Supervisor.ID, result.Tenant.ID, operator.UserID,
		nil, []int64{tenantAdmin.ID}, nil, []string{constants.RoleCodeTenantAdmin},
	)

	operator.ActiveTenantID = result.Tenant.ID
	current, code, err := TenantInvitationService.Current(result.Tenant.ID, operator)
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
		IntentProfileID:  first.Tenant.IntentProfileID,
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
	assertFreshTenantInvitationExpiry(t, rotated)
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

func TestTenantInvitationCurrentRequiresActiveTenantAndViewPermission(t *testing.T) {
	_, operator := setupTenantManagementTestDB(t)
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("current-auth", "91350100MA8N1P2Q3R"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	operator.ActiveTenantID = created.Tenant.ID

	withoutView := *operator
	withoutView.Permissions = []string{constants.PermissionTenantInviteRotate.Code}
	if _, _, err := TenantInvitationService.Current(created.Tenant.ID, &withoutView); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("Current() without view error = %v, want forbidden", err)
	}
	wrongTenant := *operator
	wrongTenant.ActiveTenantID = created.Tenant.ID + 1
	if _, _, err := TenantInvitationService.Current(created.Tenant.ID, &wrongTenant); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("Current() cross-tenant error = %v, want forbidden", err)
	}
	invitation, code, err := TenantInvitationService.Current(created.Tenant.ID, operator)
	if err != nil || invitation == nil || invitation.ID != created.Invitation.ID || code != created.InvitationCode {
		t.Fatalf("Current() = invitation:%+v code:%q error:%v", invitation, code, err)
	}
}

func TestTenantManagementMutationsUseTenantRowLock(t *testing.T) {
	tests := []struct {
		name   string
		action func(created *CreateTenantResult, operator *dto.AuthPrincipal) error
	}{
		{
			name: "update",
			action: func(created *CreateTenantResult, operator *dto.AuthPrincipal) error {
				return TenantService.UpdateTenant(request.UpdateTenantRequest{
					ID: created.Tenant.ID, IntentProfileID: created.Tenant.IntentProfileID,
					LegalName: "Locked Tenant", ShortName: "Locked",
					RegistrationType: created.Tenant.RegistrationType, RegistrationNo: created.Tenant.RegistrationNo,
					ContactName: "Locked Contact", ContactMobile: "13800008888", ContactEmail: "locked@example.com",
				}, operator)
			},
		},
		{
			name: "status",
			action: func(created *CreateTenantResult, operator *dto.AuthPrincipal) error {
				return TenantService.UpdateTenantStatus(request.UpdateTenantStatusRequest{ID: created.Tenant.ID, Status: int(enums.StatusDisabled)}, operator)
			},
		},
		{
			name: "rotate invitation",
			action: func(created *CreateTenantResult, operator *dto.AuthPrincipal) error {
				operator.ActiveTenantID = created.Tenant.ID
				_, _, err := TenantInvitationService.Rotate(created.Tenant.ID, operator)
				return err
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, operator := setupTenantManagementTestDB(t)
			slug := strings.ReplaceAll(tt.name, " ", "-")
			registrationNo := fmt.Sprintf("91350100MA8L%06d", i+1)
			created, err := TenantService.CreateTenant(tenantManagementCreateRequest("lock-"+slug, registrationNo), operator)
			if err != nil {
				t.Fatalf("create tenant: %v", err)
			}
			callbackName := "test:tenant-" + slug + "-locking-clause"
			seenLock := false
			if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Tenant" {
					if _, locked := tx.Statement.Clauses["FOR"]; locked {
						seenLock = true
					}
				}
			}); err != nil {
				t.Fatalf("register tenant locking callback: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove tenant locking callback: %v", err)
				}
			})

			if err := tt.action(created, operator); err != nil {
				t.Fatalf("%s tenant: %v", tt.name, err)
			}
			if !seenLock {
				t.Fatalf("%s did not lock the Tenant row", tt.name)
			}
		})
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

func TestTenantOperationalStatsStayIsolatedAndExcludeDeletedResources(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	tenantA, err := TenantService.CreateTenant(tenantManagementCreateRequest("stats-a", "91350100MA8Y7A8B9C"), operator)
	if err != nil {
		t.Fatalf("create tenant A: %v", err)
	}
	tenantB, err := TenantService.CreateTenant(tenantManagementCreateRequest("stats-b", "91350100MA8Y7A8B8D"), operator)
	if err != nil {
		t.Fatalf("create tenant B: %v", err)
	}

	base := time.Date(2026, time.July, 14, 9, 0, 0, 0, time.UTC)
	loginA := base.Add(2 * time.Hour)
	loginB := base.Add(3 * time.Hour)
	if err := db.Model(&models.User{}).Where("id = ?", tenantA.Supervisor.ID).Update("last_login_at", loginA).Error; err != nil {
		t.Fatalf("update tenant A login: %v", err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", tenantB.Supervisor.ID).Update("last_login_at", loginB).Error; err != nil {
		t.Fatalf("update tenant B login: %v", err)
	}

	deletedAt := base.Add(9 * time.Hour)
	deletedLogin := base.Add(10 * time.Hour)
	deletedUser := &models.User{
		TenantID:    tenantA.Tenant.ID,
		Username:    "deleted_stats_user",
		Nickname:    "Deleted Stats User",
		Status:      enums.StatusDeleted,
		LastLoginAt: &deletedLogin,
		DeletedAt:   &deletedAt,
		AuditFields: tenantManagementAuditFields(base),
	}
	if err := db.Create(deletedUser).Error; err != nil {
		t.Fatalf("create deleted user: %v", err)
	}

	resources := []any{
		&models.AgentProfile{TenantID: tenantA.Tenant.ID, UserID: tenantA.Supervisor.ID, AgentCode: "stats-agent-a", Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(base)},
		&models.AgentProfile{TenantID: tenantA.Tenant.ID, UserID: deletedUser.ID, AgentCode: "stats-agent-deleted", Status: enums.StatusDeleted, AuditFields: tenantManagementAuditFields(base)},
		&models.AgentProfile{TenantID: tenantB.Tenant.ID, UserID: tenantB.Supervisor.ID, AgentCode: "stats-agent-b", Status: enums.StatusDisabled, AuditFields: tenantManagementAuditFields(base)},
		&models.Store{TenantID: tenantA.Tenant.ID, StoreCode: "stats-store-a", Name: "Store A", Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(base)},
		&models.Store{TenantID: tenantA.Tenant.ID, StoreCode: "stats-store-deleted", Name: "Deleted Store", Status: enums.StatusDeleted, AuditFields: tenantManagementAuditFields(base)},
		&models.Store{TenantID: tenantB.Tenant.ID, StoreCode: "stats-store-b", Name: "Store B", Status: enums.StatusDisabled, AuditFields: tenantManagementAuditFields(base)},
		&models.AgentTeam{TenantID: tenantA.Tenant.ID, Name: "Deleted Team A", Status: enums.StatusDeleted, AuditFields: tenantManagementAuditFields(base)},
		&models.AgentTeam{TenantID: tenantB.Tenant.ID, Name: "Deleted Team B", Status: enums.StatusDeleted, AuditFields: tenantManagementAuditFields(base)},
	}
	for _, resource := range resources {
		if err := db.Create(resource).Error; err != nil {
			t.Fatalf("create stats resource %T: %v", resource, err)
		}
	}

	conversationAActive := base.Add(time.Hour)
	conversationBActive := base.Add(4 * time.Hour)
	conversations := []*models.Conversation{
		{TenantID: tenantA.Tenant.ID, CustomerName: "Customer A", LastActiveAt: conversationAActive, LastMessageAt: conversationAActive, AuditFields: tenantManagementAuditFields(base)},
		{TenantID: tenantB.Tenant.ID, CustomerName: "Customer B", LastActiveAt: conversationBActive, LastMessageAt: conversationBActive, AuditFields: tenantManagementAuditFields(base)},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create conversations: %v", err)
	}

	stats, err := TenantService.FindOperationalStats([]int64{tenantA.Tenant.ID, tenantB.Tenant.ID})
	if err != nil {
		t.Fatalf("find operational stats: %v", err)
	}
	statsA := stats[tenantA.Tenant.ID]
	if statsA.AgentCount != 1 || statsA.StoreCount != 1 || statsA.AgentTeamCount != 1 {
		t.Fatalf("tenant A stats = agents:%d stores:%d teams:%d, want 1/1/1", statsA.AgentCount, statsA.StoreCount, statsA.AgentTeamCount)
	}
	if statsA.LastActiveAt == nil || !statsA.LastActiveAt.Equal(loginA) {
		t.Fatalf("tenant A last active = %v, want latest non-deleted login %v", statsA.LastActiveAt, loginA)
	}
	statsB := stats[tenantB.Tenant.ID]
	if statsB.AgentCount != 1 || statsB.StoreCount != 1 || statsB.AgentTeamCount != 1 {
		t.Fatalf("tenant B stats = agents:%d stores:%d teams:%d, want 1/1/1", statsB.AgentCount, statsB.StoreCount, statsB.AgentTeamCount)
	}
	if statsB.LastActiveAt == nil || !statsB.LastActiveAt.Equal(conversationBActive) {
		t.Fatalf("tenant B last active = %v, want latest conversation activity %v", statsB.LastActiveAt, conversationBActive)
	}
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
		&models.TenantIndustryChangeLog{},
		&models.TenantInvitation{},
		&models.User{},
		&models.Role{},
		&models.UserRole{},
		&models.UserRoleChangeLog{},
		&models.AgentTeam{},
		&models.AIAgent{},
		&models.AgentProfile{},
		&models.Store{},
		&models.WxWorkProtocolInstance{},
		&models.Conversation{},
		&models.ReplyIntentProfile{},
		&models.ReplyIntentConfig{},
		&models.IndustryTagDefinition{},
		&models.Tag{},
		&models.TenantCustomerTagPolicy{},
		&models.CustomerTagRelation{},
		&models.CustomerTagChangeLog{},
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
	profile := &models.ReplyIntentProfile{
		ID: tenantManagementIndustryProfileID, Code: "test-service", Name: "测试服务行业",
		IndustryCode: "test-service", IntentDetectPrompt: "detect", IntentJSONSchema: "schema",
		Revision: 1, PublishedAt: &now, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create industry profile: %v", err)
	}
	if err := db.Create(&models.ReplyIntentConfig{
		Code: "service", Name: "服务", IntentProfileID: profile.ID, ScopeType: "global",
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create industry intent: %v", err)
	}
	parent := &models.IndustryTagDefinition{
		IntentProfileID: profile.ID, Name: "分类", SemanticKey: "category.test",
		DefinitionRevision: 1, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatalf("create industry tag category: %v", err)
	}
	if err := db.Create(&models.IndustryTagDefinition{
		IntentProfileID: profile.ID, ParentID: parent.ID, Name: "标签", SemanticKey: "test.label",
		AIEnabled: true, DefinitionRevision: 1, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create industry tag: %v", err)
	}
	sqls.SetDB(db)
	config.SetCurrent(&config.Config{Auth: config.AuthConfig{
		InvitationEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	}})
	operator := &dto.AuthPrincipal{
		UserID:            9001,
		Username:          "platform_super",
		Roles:             []string{constants.RoleCodeSuperAdmin},
		Permissions:       []string{constants.PermissionTenantCreate.Code, constants.PermissionTenantUpdate.Code, constants.PermissionTenantUpdateStatus.Code, constants.PermissionTenantInviteView.Code, constants.PermissionTenantInviteRotate.Code},
		IsPlatformAccount: true,
	}
	return db, operator
}

func tenantManagementCreateRequest(suffix, registrationNo string) request.CreateTenantRequest {
	return request.CreateTenantRequest{
		IntentProfileID:  tenantManagementIndustryProfileID,
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

func assertFreshTenantInvitationExpiry(t *testing.T, invitation *models.TenantInvitation) {
	t.Helper()
	if invitation == nil || invitation.ExpiresAt == nil {
		t.Fatalf("invitation expiry missing: %+v", invitation)
	}
	want := time.Duration(constants.TenantInvitationValidityDays) * 24 * time.Hour
	got := invitation.ExpiresAt.Sub(invitation.CreatedAt)
	if got < want-time.Minute || got > want+time.Minute {
		t.Fatalf("invitation validity=%v want approximately %v", got, want)
	}
}

func tenantManagementAuditFields(at time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt:      at,
		CreateUserName: "test",
		UpdatedAt:      at,
		UpdateUserName: "test",
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
