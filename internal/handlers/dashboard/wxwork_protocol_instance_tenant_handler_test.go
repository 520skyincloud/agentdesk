package dashboard

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestWxWorkProtocolActionsRejectCrossTenantInstanceBeforeProtocolCall(t *testing.T) {
	db := setupWxWorkProtocolTenantHandlerDB(t)
	foreign := &models.WxWorkProtocolInstance{TenantID: 202, Guid: "handler-foreign-guid", Status: enums.StatusOk}
	if err := db.Create(foreign).Error; err != nil {
		t.Fatalf("create foreign instance: %v", err)
	}
	principal := &dto.AuthPrincipal{
		UserID: 1, Username: "tenant-a-admin", ActiveTenantID: 101,
		Roles: []string{constants.RoleCodeTenantAdmin},
		Permissions: []string{
			constants.PermissionChannelView.Code,
			constants.PermissionChannelCreate.Code,
			constants.PermissionChannelUpdate.Code,
			constants.PermissionChannelDelete.Code,
			constants.PermissionConversationSend.Code,
		},
	}

	tests := []struct {
		name    string
		body    string
		handler func(*gin.Context)
	}{
		{name: "update", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostUpdate},
		{name: "delete", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostDelete},
		{name: "notify url", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSet_notify_url},
		{name: "toggle ai", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSet_ai_reply_enabled},
		{name: "update ai settings", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostUpdate_ai_settings},
		{name: "login qrcode", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostLogin_qrcode},
		{name: "check login qrcode", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostCheck_login_qrcode},
		{name: "verify login", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostVerify_login},
		{name: "recover", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostRecover},
		{name: "stop", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostStop},
		{name: "logout", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostLogout},
		{name: "sync profile", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSync_profile},
		{name: "corp info", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostGet_corp_info},
		{name: "set proxy", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSet_proxy},
		{name: "sync friend requests", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSync_friend_requests},
		{name: "accept friend request", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostAccept_friend_request},
		{name: "room list", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostRoom_list},
		{name: "room detail", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostRoom_detail},
		{name: "room member detail", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostRoom_member_detail},
		{name: "sync room info", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostSync_room_info},
		{name: "invite room member", body: instanceActionBody(foreign.ID), handler: WxWorkProtocolInstancePostInvite_room_member},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, tt.body, principal)
			tt.handler(ctx)
			assertWxWorkProtocolTenantError(t, recorder.Body.Bytes(), "企微员工号实例不存在")
		})
	}
}

func TestWxWorkProtocolActionRejectsInstanceOutsideAgentScope(t *testing.T) {
	db := setupWxWorkProtocolTenantHandlerDB(t)
	instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "handler-out-of-scope-guid", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	principal := &dto.AuthPrincipal{
		UserID: 11, Username: "limited-agent", ActiveTenantID: 101,
		Roles:       []string{constants.RoleCodeCsUser},
		Permissions: []string{constants.PermissionChannelUpdate.Code},
	}
	ctx, recorder := newAuthzHandlerTestContext(t, instanceActionBody(instance.ID), principal)

	WxWorkProtocolInstancePostLogin_qrcode(ctx)

	assertWxWorkProtocolTenantError(t, recorder.Body.Bytes(), "无权访问该企微员工号实例")
}

func TestWxWorkProtocolListRequiresActiveTenant(t *testing.T) {
	principal := &dto.AuthPrincipal{
		UserID: 21, Username: "platform-channel-viewer", IsPlatformAccount: true,
		Permissions: []string{constants.PermissionChannelView.Code},
	}
	ctx, recorder := newAuthzHandlerTestContext(t, "", principal)

	WxWorkProtocolInstanceAnyList(ctx)

	assertWxWorkProtocolTenantError(t, recorder.Body.Bytes(), "请先选择接入公司")
}

func setupWxWorkProtocolTenantHandlerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wxwork-handler-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.Store{}, &models.WxWorkProtocolInstance{}, &models.AIUsageEvent{}); err != nil {
		t.Fatalf("migrate wxwork instance: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	return db
}

func instanceActionBody(instanceID int64) string {
	return `{"id":` + jsonInt64(instanceID) + `}`
}

func jsonInt64(value int64) string {
	bytes, _ := json.Marshal(value)
	return string(bytes)
}

func assertWxWorkProtocolTenantError(t *testing.T, payload []byte, expectedMessage string) {
	t.Helper()
	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("decode response %q: %v", string(payload), err)
	}
	if !strings.Contains(result.Message, expectedMessage) {
		t.Fatalf("message=%q want %q; response=%s", result.Message, expectedMessage, string(payload))
	}
}
