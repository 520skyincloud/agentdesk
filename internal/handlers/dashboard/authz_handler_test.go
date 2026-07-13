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
