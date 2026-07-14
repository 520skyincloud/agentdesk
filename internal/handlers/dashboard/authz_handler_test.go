package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"

	"github.com/gin-gonic/gin"
)

func TestRoleUpdateSortRequiresUpdatePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "[1]", &dto.AuthPrincipal{
		UserID:      11,
		Username:    "viewer",
		Permissions: []string{constants.PermissionRoleView.Code},
	})

	RolePostUpdate_sort(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestTagUpdateSortRequiresUpdatePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "[1]", &dto.AuthPrincipal{
		UserID:         22,
		TenantID:       101,
		ActiveTenantID: 101,
		Username:       "tag_viewer",
		Permissions:    []string{constants.PermissionTagView.Code},
	})

	TagPostUpdate_sort(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestUserCreateWithRolesRequiresAssignRolePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, `{"username":"new_user","roleIds":[1]}`, &dto.AuthPrincipal{
		UserID:      12,
		Username:    "creator",
		Permissions: []string{constants.PermissionUserCreate.Code},
	})

	UserPostCreate(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestTenantCreateRequiresTenantCreatePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, `{}`, &dto.AuthPrincipal{
		UserID:      13,
		Username:    "tenant_viewer",
		Permissions: []string{constants.PermissionTenantView.Code},
	})

	TenantPostCreate(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestChannelDetailRequiresUpdatePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:         131,
		TenantID:       9,
		ActiveTenantID: 9,
		Username:       "channel_viewer",
		Permissions:    []string{constants.PermissionChannelView.Code},
	})
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}

	ChannelGetBy(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestTenantManagementActionsRequireMatchingPermissions(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		permission string
		handler    func(*gin.Context)
	}{
		{name: "update", body: `{}`, permission: constants.PermissionTenantView.Code, handler: TenantPostUpdate},
		{name: "update status", body: `{}`, permission: constants.PermissionTenantView.Code, handler: TenantPostUpdateStatus},
		{name: "view invitation", permission: constants.PermissionTenantView.Code, handler: TenantInvitationGetCurrent},
		{name: "rotate invitation", permission: constants.PermissionTenantInviteView.Code, handler: TenantInvitationPostRotate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, tt.body, &dto.AuthPrincipal{
				UserID:      14,
				Username:    "tenant_limited_user",
				Permissions: []string{tt.permission},
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestTenantListRejectsTenantAccountEvenWithPlatformPermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:            15,
		Username:          "misconfigured_tenant_user",
		Permissions:       []string{constants.PermissionTenantView.Code},
		IsPlatformAccount: false,
		TenantID:          9,
		ActiveTenantID:    9,
	})

	TenantAnyList(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestStorageSettingRejectsTenantAccountEvenWithPlatformPermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:            151,
		Username:          "misconfigured_tenant_storage_admin",
		Permissions:       []string{constants.PermissionStorageSettingView.Code},
		IsPlatformAccount: false,
		TenantID:          9,
		ActiveTenantID:    9,
	})

	StorageSettingGet(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestStorageSettingDoesNotReuseAssetPermissions(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		permissions []string
		handler     func(*gin.Context)
	}{
		{name: "view", permissions: []string{constants.PermissionAssetView.Code}, handler: StorageSettingGet},
		{name: "update", body: `{}`, permissions: []string{constants.PermissionAssetCreate.Code}, handler: StorageSettingPostUpdate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, tt.body, &dto.AuthPrincipal{
				UserID:            152,
				Username:          "platform_asset_operator",
				Permissions:       tt.permissions,
				IsPlatformAccount: true,
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestWxWorkDevicePoolRejectsTenantAndChannelPermissions(t *testing.T) {
	tests := []struct {
		name      string
		principal *dto.AuthPrincipal
		handler   func(*gin.Context)
	}{
		{
			name: "tenant account with platform permission",
			principal: &dto.AuthPrincipal{
				UserID: 161, Username: "tenant_device_pool_viewer", TenantID: 9, ActiveTenantID: 9,
				Permissions: []string{constants.PermissionWxWorkDevicePoolView.Code},
			},
			handler: WxWorkProtocolDevicePoolGetSettings,
		},
		{
			name: "platform account with channel view",
			principal: &dto.AuthPrincipal{
				UserID: 162, Username: "platform_channel_viewer", IsPlatformAccount: true,
				Permissions: []string{constants.PermissionChannelView.Code},
			},
			handler: WxWorkProtocolDevicePoolGetSettings,
		},
		{
			name: "platform account with channel update",
			principal: &dto.AuthPrincipal{
				UserID: 163, Username: "platform_channel_editor", IsPlatformAccount: true,
				Permissions: []string{constants.PermissionChannelUpdate.Code},
			},
			handler: WxWorkProtocolDevicePoolPostSync,
		},
		{
			name: "platform device pool editor without sync permission",
			principal: &dto.AuthPrincipal{
				UserID: 164, Username: "platform_device_pool_editor", IsPlatformAccount: true,
				Permissions: []string{constants.PermissionWxWorkDevicePoolUpdate.Code},
			},
			handler: WxWorkProtocolDevicePoolPostSync,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, "", tt.principal)
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestTenantRegistrationListRequiresViewPermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:         16,
		Username:       "registration_reviewer",
		TenantID:       7,
		ActiveTenantID: 7,
		Permissions:    []string{constants.PermissionTenantRegistrationReview.Code},
	})

	TenantRegistrationAnyList(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestTenantRegistrationReviewRequiresReviewPermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, `{}`, &dto.AuthPrincipal{
		UserID:         17,
		Username:       "registration_viewer",
		TenantID:       7,
		ActiveTenantID: 7,
		Permissions:    []string{constants.PermissionTenantRegistrationView.Code},
	})

	TenantRegistrationPostReview(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestTenantRegistrationApprovalRequiresAssignRolePermission(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, `{"userId":9,"decision":"approve","roleIds":[3]}`, &dto.AuthPrincipal{
		UserID:         18,
		Username:       "registration_reviewer",
		TenantID:       7,
		ActiveTenantID: 7,
		Permissions:    []string{constants.PermissionTenantRegistrationReview.Code},
	})
	ctx.Request.Header.Set("X-Request-Id", "review-without-role-assignment")

	TenantRegistrationPostReview(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}

func TestAgentOrganizationListHandlersRequireActiveTenant(t *testing.T) {
	tests := []struct {
		name       string
		permission string
		handler    func(*gin.Context)
	}{
		{name: "team list", permission: constants.PermissionAgentTeamView.Code, handler: AgentTeamAnyList},
		{name: "profile list", permission: constants.PermissionAgentView.Code, handler: AgentAnyList},
		{name: "squad list", permission: constants.PermissionAgentTeamView.Code, handler: AgentTeamSquadAnyList},
		{name: "schedule list", permission: constants.PermissionAgentTeamScheduleView.Code, handler: AgentTeamScheduleAnyList},
		{name: "user list", permission: constants.PermissionUserView.Code, handler: UserAnyList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
				UserID:            19,
				Username:          "platform_viewer",
				IsPlatformAccount: true,
				Roles:             []string{constants.RoleCodeAdmin},
				Permissions:       []string{tt.permission},
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestCompanyAndChannelListHandlersRequireActiveTenant(t *testing.T) {
	tests := []struct {
		name       string
		permission string
		handler    func(*gin.Context)
	}{
		{name: "company list", permission: constants.PermissionCompanyView.Code, handler: CompanyAnyList},
		{name: "channel list", permission: constants.PermissionChannelView.Code, handler: ChannelAnyList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
				UserID:            20,
				Username:          "platform-company-viewer",
				IsPlatformAccount: true,
				Roles:             []string{constants.RoleCodeAdmin},
				Permissions:       []string{tt.permission},
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestCustomerListHandlersRequireActiveTenant(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*gin.Context)
	}{
		{name: "customer list", handler: CustomerPostList},
		{name: "customer contact list", handler: CustomerContactAnyList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, `{}`, &dto.AuthPrincipal{
				UserID:            21,
				Username:          "platform-customer-viewer",
				IsPlatformAccount: true,
				Roles:             []string{constants.RoleCodeAdmin},
				Permissions:       []string{constants.PermissionCustomerView.Code},
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func TestTicketAndTagListHandlersRequireActiveTenant(t *testing.T) {
	tests := []struct {
		name       string
		permission string
		handler    func(*gin.Context)
	}{
		{name: "ticket list", permission: constants.PermissionTicketView.Code, handler: TicketAnyList},
		{name: "ticket views", permission: constants.PermissionTicketView.Code, handler: TicketAnyView_list},
		{name: "tag list", permission: constants.PermissionTagView.Code, handler: TagAnyList},
		{name: "tag tree", permission: constants.PermissionTagView.Code, handler: TagGetList_all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
				UserID:            23,
				Username:          "platform-ticket-tag-viewer",
				IsPlatformAccount: true,
				Permissions:       []string{tt.permission},
			})
			tt.handler(ctx)
			assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
		})
	}
}

func newAuthzHandlerTestContext(t *testing.T, body string, principal *dto.AuthPrincipal) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("authPrincipal", principal)
	return ctx, recorder
}

func assertAuthzErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, expected int) {
	t.Helper()
	var payload struct {
		ErrorCode int `json:"errorCode"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if payload.ErrorCode != expected {
		t.Fatalf("errorCode = %d, want %d; response=%s", payload.ErrorCode, expected, recorder.Body.String())
	}
}
