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
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, StoreID: store.ID, Status: enums.StatusOk}
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
	if updated.StoreID != store.ID || updated.StoreStaffBindingID != binding.ID || updated.CompanyID != 0 {
		t.Fatalf("stable store binding missing: %#v", updated)
	}
	if updated.AIReplyEnabled {
		t.Fatal("AI must remain disabled until industry and dataset are ready")
	}
	currentBinding := repositories.StoreStaffBindingRepository.Get(db, updated.StoreStaffBindingID)
	if currentBinding == nil || currentBinding.UserID != user.ID || currentBinding.StoreID != store.ID || currentBinding.CompanyID != 0 {
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
