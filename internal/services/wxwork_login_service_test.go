package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/wxwork"

	"github.com/mlogclub/simple/sqls"
)

func TestWxWorkLoginOnlyBindsAnExistingVerifiedAccount(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	email := "existing@example.com"
	now := time.Now()
	user := &models.User{
		TenantID: 1, Username: "existing-wxwork-user", Nickname: "已有账号",
		Email: &email, EmailVerifiedAt: &now, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create existing account: %v", err)
	}

	var boundUser *models.User
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		boundUser, _, err = WxWorkLoginService.bindExistingWxWorkUser(ctx, &wxwork.LoginUser{
			CorpID: "corp-a", UserID: "wx-existing", Name: "企微姓名", Email: email,
		})
		return err
	}); err != nil {
		t.Fatalf("bind existing account: %v", err)
	}
	if boundUser == nil || boundUser.ID != user.ID {
		t.Fatalf("bound user=%+v want=%d", boundUser, user.ID)
	}
	var roleCount int64
	if err := db.Model(&models.UserRole{}).Where("user_id = ?", user.ID).Count(&roleCount).Error; err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roleCount != 0 {
		t.Fatalf("wxwork login assigned %d roles", roleCount)
	}

	var before int64
	if err := db.Model(&models.User{}).Count(&before).Error; err != nil {
		t.Fatalf("count users before missing account: %v", err)
	}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		_, _, bindErr := WxWorkLoginService.bindExistingWxWorkUser(ctx, &wxwork.LoginUser{
			CorpID: "corp-a", UserID: "wx-missing", Email: "missing@example.com",
		})
		return bindErr
	})
	if err == nil || !strings.Contains(err.Error(), "公司主管") {
		t.Fatalf("missing account error=%v", err)
	}
	var after int64
	if err := db.Model(&models.User{}).Count(&after).Error; err != nil {
		t.Fatalf("count users after missing account: %v", err)
	}
	if after != before {
		t.Fatalf("wxwork login created a user: before=%d after=%d", before, after)
	}
}
