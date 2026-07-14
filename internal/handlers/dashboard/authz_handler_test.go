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
