package services

import (
	"fmt"
	"hash/crc32"
	"strings"
	"sync"
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

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestTenantRegistrationServiceRejectsPublicActionsWhenDisabled(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	cfg := config.Current()
	cfg.TenantRegistration.Enabled = false
	config.SetCurrent(&cfg)

	if _, err := TenantRegistrationService.ValidateInvitation(fixture.invitationCode, registrationMeta("disabled-validate")); !hasCode(err, errorsx.CodeBusinessError+33) {
		t.Fatalf("ValidateInvitation() error=%v want disabled error", err)
	}
	if _, err := TenantRegistrationService.Register(registrationRequest(fixture.invitationCode, "disabled"), registrationMeta("disabled-register")); !hasCode(err, errorsx.CodeBusinessError+33) {
		t.Fatalf("Register() error=%v want disabled error", err)
	}
	assertRegistrationCount(t, fixture.db, 0, 0)
}

func TestTenantRegistrationValidateInvitationTracksCurrentLifecycle(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	result, err := TenantRegistrationService.ValidateInvitation(fixture.invitationCode, registrationMeta("validate-current"))
	if err != nil {
		t.Fatalf("ValidateInvitation() error = %v", err)
	}
	if !result.Valid || result.Tenant == nil || result.Tenant.ID != fixture.tenant.ID {
		t.Fatalf("unexpected validation result: %+v", result)
	}
	log := registrationLogByRequestID(t, fixture.db, "validate-current")
	if !log.Success || log.Action != enums.TenantRegistrationActionValidateInvite || log.RequestFingerprint != hashTenantInvitationCode(fixture.invitationCode) {
		t.Fatalf("unexpected validation log: %+v", log)
	}

	rotated, newCode, err := TenantInvitationService.Rotate(fixture.tenant.ID, fixture.operator)
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if rotated.ID == fixture.invitation.ID || newCode == fixture.invitationCode {
		t.Fatalf("invitation was not rotated")
	}
	oldResult, err := TenantRegistrationService.ValidateInvitation(fixture.invitationCode, registrationMeta("validate-old"))
	if err != nil || oldResult.Valid {
		t.Fatalf("old invitation result=%+v error=%v want invalid", oldResult, err)
	}
	newResult, err := TenantRegistrationService.ValidateInvitation(newCode, registrationMeta("validate-new"))
	if err != nil || !newResult.Valid {
		t.Fatalf("new invitation result=%+v error=%v want valid", newResult, err)
	}
	if _, err := TenantRegistrationService.Register(
		registrationRequest(fixture.invitationCode, "oldinvite"),
		registrationMeta("register-old-invite"),
	); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("old invitation Register() error=%v want invalid invitation", err)
	}

	if err := fixture.db.Model(&models.Tenant{}).Where("id = ?", fixture.tenant.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	disabledResult, err := TenantRegistrationService.ValidateInvitation(newCode, registrationMeta("validate-disabled-tenant"))
	if err != nil || disabledResult.Valid {
		t.Fatalf("disabled tenant result=%+v error=%v want invalid", disabledResult, err)
	}
	if _, err := TenantRegistrationService.Register(
		registrationRequest(newCode, "disabledtenant"),
		registrationMeta("register-disabled-tenant"),
	); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("disabled tenant Register() error=%v want invalid invitation", err)
	}
}

func TestTenantRegistrationCreatesPendingRolelessAccountAndReplaysExactly(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "pending")
	meta := registrationMeta("register-pending")

	result, err := TenantRegistrationService.Register(req, meta)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.User == nil || result.Tenant == nil || result.Replayed {
		t.Fatalf("unexpected registration result: %+v", result)
	}
	user := repositories.UserRepository.Get(fixture.db, result.User.ID)
	if user == nil || user.TenantID != fixture.tenant.ID || user.Status != enums.StatusDisabled || user.ApprovalStatus != enums.UserApprovalStatusPending || user.RegistrationSource != enums.UserRegistrationSourceInvitation {
		t.Fatalf("unexpected registered user: %+v", user)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		t.Fatalf("stored password is not the expected bcrypt hash: %v", err)
	}
	assertOnlyRegistrationRoles(t, fixture.db, user.ID)
	invitation := repositories.TenantInvitationRepository.Get(fixture.db, fixture.invitation.ID)
	if invitation == nil || invitation.UsedCount != 1 || invitation.LastUsedAt == nil {
		t.Fatalf("invitation usage was not updated: %+v", invitation)
	}
	log := registrationLogByRequestID(t, fixture.db, meta.RequestID)
	if !log.Success || log.UserID != user.ID || log.RequestFingerprint == "" || !strings.HasPrefix(log.Principal, "sha256:") {
		t.Fatalf("unexpected registration log: %+v", log)
	}
	for _, secret := range []string{req.Password, req.InvitationCode, req.Username, req.Mobile, req.Email} {
		if strings.Contains(strings.Join([]string{log.RequestFingerprint, log.InviteHash, log.Principal, log.Reason}, "|"), secret) {
			t.Fatalf("registration log leaked %q", secret)
		}
	}
	if _, err := AuthService.Login(request.LoginRequest{Username: req.Username, Password: req.Password}, config.Current().Auth, "127.0.0.1", "test"); !hasCode(err, errorsx.CodeAuthInvalidAccount) {
		t.Fatalf("pending account login error=%v want invalid account", err)
	}

	replayed, err := TenantRegistrationService.Register(req, meta)
	if err != nil {
		t.Fatalf("Register() replay error = %v", err)
	}
	if !replayed.Replayed || replayed.User.ID != user.ID {
		t.Fatalf("unexpected replay result: %+v", replayed)
	}
	assertRegistrationCount(t, fixture.db, 1, 1)
	if current := repositories.TenantInvitationRepository.Get(fixture.db, fixture.invitation.ID); current.UsedCount != 1 {
		t.Fatalf("replay incremented invitation usage to %d", current.UsedCount)
	}
}

func TestTenantRegistrationRejectsRequestIDReuseWithChangedPayload(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "fingerprint")
	meta := registrationMeta("register-fingerprint")
	if _, err := TenantRegistrationService.Register(req, meta); err != nil {
		t.Fatalf("initial Register() error = %v", err)
	}

	changed := req
	changed.Password = "DifferentPass123!"
	changed.ConfirmPassword = changed.Password
	if _, err := TenantRegistrationService.Register(changed, meta); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("changed Register() error=%v want request ID conflict", err)
	}
	assertRegistrationCount(t, fixture.db, 1, 1)
}

func TestTenantRegistrationConcurrentReplayCreatesOneAccount(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "concurrent")
	meta := registrationMeta("register-concurrent")
	const workers = 6
	results := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := TenantRegistrationService.Register(req, meta)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatalf("all concurrent registrations failed")
	}
	var count int64
	if err := fixture.db.Model(&models.User{}).Where("username = ?", req.Username).Count(&count).Error; err != nil {
		t.Fatalf("count concurrent users: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent registration created %d users", count)
	}
	if current := repositories.TenantInvitationRepository.Get(fixture.db, fixture.invitation.ID); current.UsedCount != 1 {
		t.Fatalf("concurrent registration incremented invitation %d times", current.UsedCount)
	}
	replayed, err := TenantRegistrationService.Register(req, meta)
	if err != nil || !replayed.Replayed {
		t.Fatalf("post-concurrency replay=%+v error=%v", replayed, err)
	}
}

func TestTenantRegistrationRejectsCallerControlledScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*request.RegisterTenantUserRequest)
	}{
		{name: "tenant", mutate: func(req *request.RegisterTenantUserRequest) { value := int64(99); req.TenantID = &value }},
		{name: "role", mutate: func(req *request.RegisterTenantUserRequest) { req.RoleIDs = []int64{1} }},
		{name: "agent team", mutate: func(req *request.RegisterTenantUserRequest) { value := int64(1); req.AgentTeamID = &value }},
		{name: "store", mutate: func(req *request.RegisterTenantUserRequest) { value := int64(1); req.StoreID = &value }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupTenantRegistrationTest(t)
			slug := strings.ReplaceAll(tt.name, " ", "-")
			req := registrationRequest(fixture.invitationCode, "scope"+strings.ReplaceAll(tt.name, " ", ""))
			tt.mutate(&req)
			if _, err := TenantRegistrationService.Register(req, registrationMeta("scope-"+slug)); !hasCode(err, errorsx.CodeInvalidParam) {
				t.Fatalf("Register() error=%v want invalid scope", err)
			}
			assertRegistrationCount(t, fixture.db, 0, 1)
		})
	}
}

func TestTenantRegistrationRateLimitsRepeatedPrincipal(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "limited")
	principal := publicRegistrationPrincipal(req)
	now := time.Now()
	for i := 0; i < publicRegistrationPrincipalLimit; i++ {
		if err := fixture.db.Create(&models.TenantRegistrationLog{
			RequestID: fmt.Sprintf("principal-attempt-%d", i), Action: enums.TenantRegistrationActionRegister,
			Principal: principal, ClientIP: fmt.Sprintf("192.0.2.%d", i+1), InviteHash: fmt.Sprintf("invite-%d", i), CreatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed rate-limit log: %v", err)
		}
	}
	if _, err := TenantRegistrationService.Register(req, registrationMeta("principal-rate-limited")); !hasCode(err, errorsx.CodeBusinessError+32) {
		t.Fatalf("Register() error=%v want rate limit", err)
	}
	if repositories.UserRepository.GetByUsername(fixture.db, req.Username) != nil {
		t.Fatalf("rate-limited registration created a user")
	}
}

func TestTenantRegistrationRateLimitsIPAndInvitation(t *testing.T) {
	tests := []struct {
		name      string
		logCount  int
		buildLog  func(fixture tenantRegistrationFixture, index int, now time.Time) models.TenantRegistrationLog
		requestIP string
	}{
		{
			name: "client IP", logCount: publicRegistrationIPLimit, requestIP: "192.0.2.10",
			buildLog: func(_ tenantRegistrationFixture, index int, now time.Time) models.TenantRegistrationLog {
				return models.TenantRegistrationLog{
					RequestID: fmt.Sprintf("ip-attempt-%d", index), Action: enums.TenantRegistrationActionRegister,
					ClientIP: "192.0.2.10", InviteHash: fmt.Sprintf("invite-%d", index),
					Principal: fmt.Sprintf("sha256:principal-%d", index), CreatedAt: now,
				}
			},
		},
		{
			name: "invitation", logCount: publicRegistrationInviteLimit, requestIP: "198.51.100.250",
			buildLog: func(fixture tenantRegistrationFixture, index int, now time.Time) models.TenantRegistrationLog {
				return models.TenantRegistrationLog{
					RequestID: fmt.Sprintf("invite-attempt-%d", index), Action: enums.TenantRegistrationActionRegister,
					ClientIP: fmt.Sprintf("198.51.100.%d", index+1), InviteHash: hashTenantInvitationCode(fixture.invitationCode),
					Principal: fmt.Sprintf("sha256:principal-%d", index), CreatedAt: now,
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupTenantRegistrationTest(t)
			now := time.Now()
			logs := make([]models.TenantRegistrationLog, 0, tt.logCount)
			for i := 0; i < tt.logCount; i++ {
				logs = append(logs, tt.buildLog(fixture, i, now))
			}
			if err := fixture.db.CreateInBatches(&logs, 50).Error; err != nil {
				t.Fatalf("seed %s rate-limit logs: %v", tt.name, err)
			}
			meta := registrationMeta("rate-limited-" + strings.ReplaceAll(tt.name, " ", "-"))
			meta.ClientIP = tt.requestIP
			if _, err := TenantRegistrationService.Register(
				registrationRequest(fixture.invitationCode, "limited"+strings.ReplaceAll(tt.name, " ", "")),
				meta,
			); !hasCode(err, errorsx.CodeBusinessError+32) {
				t.Fatalf("Register() error=%v want %s rate limit", err, tt.name)
			}
		})
	}
}

func TestTenantRegistrationReviewApprovesRoleAndRevokesOldSessions(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "approved")
	registered, err := TenantRegistrationService.Register(req, registrationMeta("register-approved"))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	now := time.Now()
	session := &models.LoginSession{
		UserID: registered.User.ID, Token: "ak_pre_review", ClientType: constants.ClientTypeAdminWeb,
		ExpiredAt: now.Add(time.Hour), AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(session).Error; err != nil {
		t.Fatalf("create old session: %v", err)
	}

	reviewReq := request.ReviewTenantRegistrationRequest{
		UserID: registered.User.ID, Decision: enums.TenantRegistrationReviewDecisionApprove,
		RoleIDs: []int64{fixture.csUserRole.ID}, Remark: "verified by supervisor",
	}
	reviewMeta := registrationMeta("review-approved")
	reviewed, err := TenantRegistrationService.Review(reviewReq, reviewMeta, fixture.operator)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if reviewed.Status != enums.StatusOk || reviewed.ApprovalStatus != enums.UserApprovalStatusApproved || reviewed.ApprovedAt == nil || reviewed.ApprovedBy != fixture.operator.UserID {
		t.Fatalf("unexpected approved user: %+v", reviewed)
	}
	assertOnlyRegistrationRoles(t, fixture.db, reviewed.ID, fixture.csUserRole.ID)
	if old := repositories.LoginSessionRepository.Get(fixture.db, session.ID); old == nil || old.RevokedAt == nil {
		t.Fatalf("old login session was not revoked: %+v", old)
	}
	log := registrationLogByRequestID(t, fixture.db, reviewMeta.RequestID)
	if !log.Success || log.Reason != string(enums.TenantRegistrationReviewDecisionApprove) || log.OperatorID != fixture.operator.UserID || log.RequestFingerprint == "" {
		t.Fatalf("unexpected review log: %+v", log)
	}
	login, err := AuthService.Login(request.LoginRequest{Username: req.Username, Password: req.Password}, config.Current().Auth, "127.0.0.1", "test")
	if err != nil || login == nil || login.AccessToken == "" {
		t.Fatalf("approved account login=%+v error=%v", login, err)
	}

	replayed, err := TenantRegistrationService.Review(reviewReq, reviewMeta, fixture.operator)
	if err != nil || replayed.ID != reviewed.ID {
		t.Fatalf("Review() replay=%+v error=%v", replayed, err)
	}
	changed := reviewReq
	changed.RoleIDs = []int64{fixture.teamLeaderRole.ID}
	if _, err := TenantRegistrationService.Review(changed, reviewMeta, fixture.operator); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("changed Review() error=%v want request ID conflict", err)
	}
}

func TestTenantRegistrationReviewRejectsWithoutRoles(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	req := registrationRequest(fixture.invitationCode, "rejected")
	registered, err := TenantRegistrationService.Register(req, registrationMeta("register-rejected"))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	reviewed, err := TenantRegistrationService.Review(request.ReviewTenantRegistrationRequest{
		UserID: registered.User.ID, Decision: enums.TenantRegistrationReviewDecisionReject, Remark: "identity could not be confirmed",
	}, registrationMeta("review-rejected"), fixture.operator)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if reviewed.Status != enums.StatusDisabled || reviewed.ApprovalStatus != enums.UserApprovalStatusRejected || reviewed.ApprovalRemark == "" {
		t.Fatalf("unexpected rejected user: %+v", reviewed)
	}
	assertOnlyRegistrationRoles(t, fixture.db, reviewed.ID)
	if _, err := AuthService.Login(request.LoginRequest{Username: req.Username, Password: req.Password}, config.Current().Auth, "127.0.0.1", "test"); !hasCode(err, errorsx.CodeAuthInvalidAccount) {
		t.Fatalf("rejected account login error=%v want invalid account", err)
	}
	if _, err := TenantRegistrationService.Review(request.ReviewTenantRegistrationRequest{
		UserID: registered.User.ID, Decision: enums.TenantRegistrationReviewDecisionReject, Remark: "again",
	}, registrationMeta("review-rejected-again"), fixture.operator); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("second Review() error=%v want already reviewed", err)
	}
}

func TestTenantRegistrationReviewEnforcesTenantAndRoleAuthority(t *testing.T) {
	fixture := setupTenantRegistrationTest(t)
	registered, err := TenantRegistrationService.Register(registrationRequest(fixture.invitationCode, "authority"), registrationMeta("register-authority"))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	peerReq := request.ReviewTenantRegistrationRequest{
		UserID: registered.User.ID, Decision: enums.TenantRegistrationReviewDecisionApprove,
		RoleIDs: []int64{fixture.tenantAdminRole.ID},
	}
	if _, err := TenantRegistrationService.Review(peerReq, registrationMeta("review-peer-role"), fixture.operator); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("peer role Review() error=%v want forbidden", err)
	}
	current := repositories.UserRepository.Get(fixture.db, registered.User.ID)
	if current.ApprovalStatus != enums.UserApprovalStatusPending || current.Status != enums.StatusDisabled {
		t.Fatalf("failed review changed user: %+v", current)
	}
	assertOnlyRegistrationRoles(t, fixture.db, current.ID)

	other, err := TenantService.CreateTenant(tenantManagementCreateRequest("registration-other", "91350100MA8C4D5E6F"), fixture.platformOperator)
	if err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	otherReq := registrationRequest(other.InvitationCode, "other")
	otherUser, err := TenantRegistrationService.Register(otherReq, registrationMeta("register-other"))
	if err != nil {
		t.Fatalf("register other tenant user: %v", err)
	}
	if _, err := TenantRegistrationService.Review(request.ReviewTenantRegistrationRequest{
		UserID: otherUser.User.ID, Decision: enums.TenantRegistrationReviewDecisionApprove, RoleIDs: []int64{fixture.csUserRole.ID},
	}, registrationMeta("review-cross-tenant"), fixture.operator); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("cross-tenant Review() error=%v want hidden target", err)
	}
}

type tenantRegistrationFixture struct {
	db               *gorm.DB
	tenant           *models.Tenant
	invitation       *models.TenantInvitation
	invitationCode   string
	operator         *dto.AuthPrincipal
	platformOperator *dto.AuthPrincipal
	csUserRole       *models.Role
	teamLeaderRole   *models.Role
	tenantAdminRole  *models.Role
}

func setupTenantRegistrationTest(t *testing.T) tenantRegistrationFixture {
	t.Helper()
	db, platformOperator := setupTenantManagementTestDB(t)
	if err := db.AutoMigrate(
		&models.TenantRegistrationLog{}, &models.LoginSession{}, &models.LoginCredentialLog{},
		&models.Permission{}, &models.RolePermission{},
	); err != nil {
		t.Fatalf("migrate registration tables: %v", err)
	}
	cfg := config.Current()
	cfg.TenantRegistration.Enabled = true
	config.SetCurrent(&cfg)
	created, err := TenantService.CreateTenant(
		tenantManagementCreateRequest("registration", "91350100MA8A2B3C4D"),
		platformOperator,
	)
	if err != nil {
		t.Fatalf("create registration tenant: %v", err)
	}
	csUserRole := createAuthorityRole(t, db, constants.RoleCodeCsUser, constants.RoleScopeTenant, constants.RoleAuthorityMember)
	teamLeaderRole := createAuthorityRole(t, db, constants.RoleCodeCsTeamLeader, constants.RoleScopeTenant, constants.RoleAuthorityTeamLeader)
	tenantAdminRole := repositories.RoleRepository.GetByCode(db, constants.RoleCodeTenantAdmin)
	operator := &dto.AuthPrincipal{
		UserID: created.Supervisor.ID, TenantID: created.Tenant.ID, ActiveTenantID: created.Tenant.ID,
		Username: created.Supervisor.Username, Roles: []string{constants.RoleCodeTenantAdmin},
		Permissions: []string{
			constants.PermissionTenantRegistrationView.Code,
			constants.PermissionTenantRegistrationReview.Code,
			constants.PermissionTenantInviteRotate.Code,
			constants.PermissionUserAssignRole.Code,
		},
	}
	return tenantRegistrationFixture{
		db: db, tenant: created.Tenant, invitation: created.Invitation, invitationCode: created.InvitationCode,
		operator: operator, platformOperator: platformOperator, csUserRole: csUserRole,
		teamLeaderRole: teamLeaderRole, tenantAdminRole: tenantAdminRole,
	}
}

func registrationRequest(invitationCode, suffix string) request.RegisterTenantUserRequest {
	checksum := crc32.ChecksumIEEE([]byte(suffix)) % 100000000
	password := "SecurePass123!"
	return request.RegisterTenantUserRequest{
		Username: "invite_" + suffix,
		Nickname: "Invite " + suffix,
		Mobile:   fmt.Sprintf("138%08d", checksum),
		Email:    "invite_" + suffix + "@example.com",
		Password: password, ConfirmPassword: password,
		InvitationCode: invitationCode,
	}
}

func registrationMeta(requestID string) PublicRegistrationMeta {
	return PublicRegistrationMeta{RequestID: requestID, ClientIP: "192.0.2.10", UserAgent: "tenant-registration-test"}
}

func registrationLogByRequestID(t *testing.T, db *gorm.DB, requestID string) *models.TenantRegistrationLog {
	t.Helper()
	log := repositories.TenantRegistrationLogRepository.GetByRequestID(db, requestID)
	if log == nil {
		t.Fatalf("registration log %q not found", requestID)
	}
	return log
}

func assertRegistrationCount(t *testing.T, db *gorm.DB, users, logs int64) {
	t.Helper()
	var userCount, logCount int64
	if err := db.Model(&models.User{}).Where("registration_source = ?", enums.UserRegistrationSourceInvitation).Count(&userCount).Error; err != nil {
		t.Fatalf("count invitation users: %v", err)
	}
	if err := db.Model(&models.TenantRegistrationLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count registration logs: %v", err)
	}
	if userCount != users || logCount != logs {
		t.Fatalf("registration counts users=%d logs=%d want users=%d logs=%d", userCount, logCount, users, logs)
	}
}

func assertOnlyRegistrationRoles(t *testing.T, db *gorm.DB, userID int64, wantRoleIDs ...int64) {
	t.Helper()
	var relations []models.UserRole
	if err := db.Where("user_id = ?", userID).Order("role_id ASC").Find(&relations).Error; err != nil {
		t.Fatalf("find user roles: %v", err)
	}
	if len(relations) != len(wantRoleIDs) {
		t.Fatalf("user roles=%+v want %v", relations, wantRoleIDs)
	}
	for i, roleID := range wantRoleIDs {
		if relations[i].RoleID != roleID {
			t.Fatalf("user roles=%+v want %v", relations, wantRoleIDs)
		}
	}
}
