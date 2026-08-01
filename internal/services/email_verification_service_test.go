package services

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type captureEmailSender struct {
	code string
	err  error
}

func (s *captureEmailSender) SendVerificationCode(_ context.Context, _ string, code string, _ string) error {
	s.code = code
	return s.err
}

func TestEmailSendFailureDoesNotLeaveCooldownChallenge(t *testing.T) {
	db := setupEmailLifecycleTestDB(t)
	sender := &captureEmailSender{err: fmt.Errorf("smtp unavailable")}
	service := newEmailVerificationService(sender)

	if _, err := service.SendCode(context.Background(), EmailVerificationPurposeRemoteSetup, "owner@example.com", "setup-token", "127.0.0.1", "test"); err == nil {
		t.Fatal("expected SMTP failure")
	}
	var stored models.EmailVerificationCode
	if err := db.First(&stored).Error; err != nil {
		t.Fatalf("load failed challenge: %v", err)
	}
	if stored.ConsumedAt == nil {
		t.Fatal("failed email challenge must be closed immediately")
	}
	sender.err = nil
	if _, err := service.SendCode(context.Background(), EmailVerificationPurposeRemoteSetup, "owner@example.com", "setup-token", "127.0.0.1", "test"); err != nil {
		t.Fatalf("retry after SMTP recovery: %v", err)
	}
}

func TestRemoteSetupEmailCodeVerifiesExistingStoreStaffAccount(t *testing.T) {
	db := setupEmailLifecycleTestDB(t)
	sender := &captureEmailSender{}
	service := newEmailVerificationService(sender)
	original := EmailVerificationService
	EmailVerificationService = service
	t.Cleanup(func() { EmailVerificationService = original })

	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	email := "owner@example.com"
	user := &models.User{TenantID: 101, Username: "existing-store-owner", Nickname: "测试独立门店", Email: &email, Password: "test", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create existing user: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	store := &models.Store{TenantID: 101, StoreCode: "existing-store", Name: "测试独立门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create stable store: %v", err)
	}
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, ActiveUserID: positiveInt64Pointer(user.ID),
		StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "device-a", ChannelID: 1, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		RemoteSetupToken: "setup-token", Status: enums.StatusDisabled,
		ManualTimeoutMinutes: 10,
	}
	instance.CreatedAt = time.Now()
	instance.UpdatedAt = time.Now()
	expiresAt := time.Now().Add(time.Hour)
	instance.RemoteSetupExpiresAt = &expiresAt
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	var usersBefore, rolesBefore, userRolesBefore int64
	db.Model(&models.User{}).Count(&usersBefore)
	db.Model(&models.Role{}).Count(&rolesBefore)
	db.Model(&models.UserRole{}).Count(&userRolesBefore)

	if _, err := service.SendCode(context.Background(), EmailVerificationPurposeRemoteSetup, "owner@example.com", instance.RemoteSetupToken, "127.0.0.1", "test"); err != nil {
		t.Fatalf("send code: %v", err)
	}
	if sender.code == "" {
		t.Fatal("expected sender to receive code")
	}
	var stored models.EmailVerificationCode
	if err := db.First(&stored).Error; err != nil {
		t.Fatalf("load code: %v", err)
	}
	if strings.Contains(stored.CodeHash, sender.code) || stored.CodeHash == sender.code {
		t.Fatal("verification code must not be stored in plaintext")
	}
	verified, err := service.VerifyCode(EmailVerificationPurposeRemoteSetup, "owner@example.com", instance.RemoteSetupToken, sender.code)
	if err != nil {
		t.Fatalf("verify code: %v", err)
	}
	updated, err := StoreIdentityLifecycleService.CompleteBindingSetup(instance, request.UpdateWxWorkProtocolRemoteSetupRequest{
		Token: instance.RemoteSetupToken, Email: "owner@example.com", EmailVerificationToken: verified.VerificationToken,
		EmployeeName: "门店员工", StoreName: "测试独立门店", FallbackToHQ: true, ManualTimeoutMinutes: 10,
	})
	if err != nil {
		t.Fatalf("complete remote setup: %v", err)
	}
	if updated.StoreID != store.ID || updated.StoreStaffBindingID != binding.ID {
		t.Fatalf("stable store binding missing: %#v", updated)
	}
	if updated.AIReplyEnabled {
		t.Fatal("AI must remain disabled until industry and dataset are ready")
	}
	currentBinding := repositories.StoreStaffBindingRepository.Get(db, updated.StoreStaffBindingID)
	if currentBinding == nil || currentBinding.UserID != user.ID || currentBinding.StoreID != store.ID {
		t.Fatalf("primary store user binding changed: %#v", currentBinding)
	}
	currentUser := repositories.UserRepository.Get(db, user.ID)
	if currentUser == nil || currentUser.Email == nil || *currentUser.Email != "owner@example.com" || currentUser.EmailVerifiedAt == nil {
		t.Fatalf("existing verified email user missing: %#v", currentUser)
	}
	var usersAfter, rolesAfter, userRolesAfter int64
	db.Model(&models.User{}).Count(&usersAfter)
	db.Model(&models.Role{}).Count(&rolesAfter)
	db.Model(&models.UserRole{}).Count(&userRolesAfter)
	if usersAfter != usersBefore || rolesAfter != rolesBefore || userRolesAfter != userRolesBefore {
		t.Fatalf("remote setup changed account or role counts: users %d->%d roles %d->%d userRoles %d->%d", usersBefore, usersAfter, rolesBefore, rolesAfter, userRolesBefore, userRolesAfter)
	}
	if _, err := StoreIdentityLifecycleService.CompleteBindingSetup(instance, request.UpdateWxWorkProtocolRemoteSetupRequest{
		Token: instance.RemoteSetupToken, Email: "owner@example.com", EmailVerificationToken: verified.VerificationToken, StoreName: "测试独立门店",
	}); err == nil {
		t.Fatal("verification token and remote setup must be single-use")
	}
}

func TestRemoteSetupReplacementMovesArrivalRuntimeWithoutRewritingTicket(t *testing.T) {
	db := setupEmailLifecycleTestDB(t)
	sender := &captureEmailSender{}
	service := newEmailVerificationService(sender)
	original := EmailVerificationService
	EmailVerificationService = service
	t.Cleanup(func() { EmailVerificationService = original })

	now := time.Now()
	email := "replacement-owner@example.com"
	user := &models.User{TenantID: 101, Username: "replacement-owner", Nickname: "替换门店员工", Email: &email, Password: "test", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	store := &models.Store{TenantID: 101, StoreCode: "replacement-store", Name: "替换测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, ActiveUserID: positiveInt64Pointer(user.ID), StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	old := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "replacement-old-guid", ChannelID: 1, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		EmployeeUserID: "replacement-old-employee", EmployeeName: "原企微员工", AIReplyEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(old).Error; err != nil {
		t.Fatalf("create old instance: %v", err)
	}
	expiresAt := now.Add(time.Hour)
	replacement := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "replacement-new-guid", ChannelID: 1, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		EmployeeUserID: "replacement-new-employee", EmployeeName: "新企微员工", ReplacesInstanceID: old.ID,
		RemoteSetupToken: "replacement-setup-token", RemoteSetupExpiresAt: &expiresAt, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(replacement).Error; err != nil {
		t.Fatalf("create replacement instance: %v", err)
	}
	connection := &models.StoreArrivalConnection{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, StoreScene: "replacement-arrival-scene",
		ContactProviderMode: enums.ArrivalContactProviderModeStaticPluginTicket, StaticContactPlugID: "replacement-plug",
		WxWorkProtocolInstanceID: old.ID, ConnectionStatus: enums.ArrivalConnectionStatusActive, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(connection).Error; err != nil {
		t.Fatalf("create arrival connection: %v", err)
	}
	providerIdentity := &models.MiniProgramIdentity{
		TenantID: 101, AppID: "replacement-app", OpenIDCiphertext: "provider-open-ciphertext", OpenIDNonce: "provider-open-nonce",
		OpenIDFingerprint: "provider-open-fingerprint", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	cardIdentity := &models.MiniProgramIdentity{
		TenantID: 101, AppID: "replacement-app", OpenIDCiphertext: "card-open-ciphertext", OpenIDNonce: "card-open-nonce",
		OpenIDFingerprint: "card-open-fingerprint", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(providerIdentity).Error; err != nil {
		t.Fatalf("create provider identity: %v", err)
	}
	if err := db.Create(cardIdentity).Error; err != nil {
		t.Fatalf("create card identity: %v", err)
	}
	ticket := &models.ArrivalBindingTicket{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, WxWorkProtocolInstanceID: old.ID,
		CustomerID: 9101, ConversationID: 9201, TicketHash: "replacement-ticket-hash", TokenEntropyHash: "replacement-ticket-entropy",
		TicketStatus: enums.ArrivalBindingTicketStatusConsumed, ExpiresAt: now.Add(time.Hour), ConsumedAt: &now,
		ConsumedMiniProgramIdentityID: cardIdentity.ID, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(ticket).Error; err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	providerBinding := &models.ArrivalStoreBinding{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, MiniProgramIdentityID: providerIdentity.ID,
		WxWorkProtocolInstanceID: old.ID, CustomerID: 9100, ConversationID: 9200,
		ProtocolConversationCiphertext: "provider-stale-ciphertext", ProtocolConversationNonce: "provider-stale-nonce",
		ProtocolConversationFingerprint: "provider-stale-fingerprint", BindingProofType: enums.ArrivalBindingProofTypeProviderCallback,
		BindingStatus: enums.ArrivalBindingStatusBound, EvidenceHash: "provider-evidence", ProtocolMappedAt: &now,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	cardBinding := &models.ArrivalStoreBinding{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, MiniProgramIdentityID: cardIdentity.ID,
		WxWorkProtocolInstanceID: old.ID, CustomerID: ticket.CustomerID, ConversationID: ticket.ConversationID,
		ProtocolConversationCiphertext: "card-stale-ciphertext", ProtocolConversationNonce: "card-stale-nonce",
		ProtocolConversationFingerprint: "card-stale-fingerprint", BindingProofType: enums.ArrivalBindingProofTypeCardTicket,
		BindingTicketID: ticket.ID, BindingStatus: enums.ArrivalBindingStatusBound, EvidenceHash: "card-evidence", ProtocolMappedAt: &now,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(providerBinding).Error; err != nil {
		t.Fatalf("create provider binding: %v", err)
	}
	if err := db.Create(cardBinding).Error; err != nil {
		t.Fatalf("create card binding: %v", err)
	}

	if _, err := service.SendCode(context.Background(), EmailVerificationPurposeRemoteSetup, email, replacement.RemoteSetupToken, "127.0.0.1", "test"); err != nil {
		t.Fatalf("send verification code: %v", err)
	}
	verified, err := service.VerifyCode(EmailVerificationPurposeRemoteSetup, email, replacement.RemoteSetupToken, sender.code)
	if err != nil {
		t.Fatalf("verify code: %v", err)
	}
	updated, err := StoreIdentityLifecycleService.CompleteBindingSetup(replacement, request.UpdateWxWorkProtocolRemoteSetupRequest{
		Token: replacement.RemoteSetupToken, Email: email, EmailVerificationToken: verified.VerificationToken, EmployeeName: "新企微员工",
	})
	if err != nil {
		t.Fatalf("complete replacement: %v", err)
	}
	if updated == nil || updated.ID != replacement.ID || !updated.AIReplyEnabled {
		t.Fatalf("replacement did not inherit current runtime settings: %+v", updated)
	}
	retired := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, old.ID, old.TenantID)
	if retired == nil || retired.Status != enums.StatusDisabled || retired.ReplacedByInstanceID != replacement.ID || retired.AIReplyEnabled {
		t.Fatalf("old instance was not retired atomically: %+v", retired)
	}
	currentConnection := repositories.ArrivalRepository.GetConnection(db, connection.ID, connection.TenantID)
	if currentConnection == nil || currentConnection.StoreStaffBindingID != binding.ID || currentConnection.WxWorkProtocolInstanceID != replacement.ID {
		t.Fatalf("arrival connection still points to old instance: %+v", currentConnection)
	}
	for _, tc := range []struct {
		name       string
		id         int64
		wantStatus enums.ArrivalBindingStatus
	}{
		{name: "provider", id: providerBinding.ID, wantStatus: enums.ArrivalBindingStatusLegacyUnmapped},
		{name: "card ticket", id: cardBinding.ID, wantStatus: enums.ArrivalBindingStatusBound},
	} {
		current := repositories.ArrivalRepository.GetBinding(db, tc.id, 101)
		if current == nil || current.WxWorkProtocolInstanceID != replacement.ID || current.BindingStatus != tc.wantStatus {
			t.Fatalf("%s binding not moved to replacement: %+v", tc.name, current)
		}
		if current.ProtocolConversationCiphertext != "" || current.ProtocolConversationNonce != "" ||
			current.ProtocolConversationFingerprint != "" || current.ProtocolMappedAt != nil {
			t.Fatalf("%s stale protocol mapping survived replacement: %+v", tc.name, current)
		}
	}
	currentTicket := repositories.ArrivalRepository.GetBindingTicket(db, ticket.ID, ticket.TenantID)
	if currentTicket == nil || currentTicket.StoreStaffBindingID != binding.ID || currentTicket.WxWorkProtocolInstanceID != old.ID || currentTicket.ConversationID != ticket.ConversationID {
		t.Fatalf("immutable ticket evidence was rewritten: %+v", currentTicket)
	}
	var audits []models.ArrivalAuditLog
	if err := db.Where("request_id = ?", "wxwork_instance_replacement_"+fmt.Sprint(replacement.ID)).Order("id ASC").Find(&audits).Error; err != nil {
		t.Fatalf("load replacement audits: %v", err)
	}
	if len(audits) != 3 {
		t.Fatalf("replacement audit count=%d, want 3", len(audits))
	}
	for _, audit := range audits {
		if !strings.Contains(audit.DetailJSON, `"mappingMode":"same_store_staff_binding_instance_replacement"`) {
			t.Fatalf("replacement audit missing mapping mode: %s", audit.DetailJSON)
		}
		for _, secret := range []string{old.Guid, replacement.Guid, old.EmployeeUserID, replacement.EmployeeUserID, "stale-ciphertext", "stale-nonce", "stale-fingerprint"} {
			if strings.Contains(audit.DetailJSON, secret) {
				t.Fatalf("replacement audit leaked protocol value %q", secret)
			}
		}
	}
}

func setupEmailLifecycleTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.EmailVerificationCode{}, &models.User{}, &models.Role{}, &models.UserRole{}, &models.UserRoleChangeLog{},
		&models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{},
		&models.MiniProgramIdentity{}, &models.StoreArrivalConnection{}, &models.ArrivalStoreBinding{},
		&models.ArrivalBindingTicket{}, &models.ArrivalAuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}
