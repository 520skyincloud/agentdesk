package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/errorsx"

	"github.com/gin-gonic/gin"
)

func TestTenantRegistrationRegisterRequiresExplicitRequestIDAndDisablesCaching(t *testing.T) {
	config.SetCurrent(&config.Config{})
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	TenantRegistrationPostRegister(ctx)

	assertTenantRegistrationAPIErrorCode(t, recorder, errorsx.CodeInvalidParam)
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", got)
	}
}

func TestTenantRegistrationValidateInviteHonorsDisabledSwitchAndDisablesCaching(t *testing.T) {
	config.SetCurrent(&config.Config{})
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/register/validate_invite", strings.NewReader(`{"invitationCode":"inv_000000000000000000000000000000000000000000000000"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Request-Id", "validate-disabled-registration")

	TenantRegistrationPostValidateInvite(ctx)

	assertTenantRegistrationAPIErrorCode(t, recorder, errorsx.CodeBusinessError+33)
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", got)
	}
}

func assertTenantRegistrationAPIErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, expected int) {
	t.Helper()
	var payload struct {
		ErrorCode int `json:"errorCode"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if payload.ErrorCode != expected {
		t.Fatalf("errorCode=%d want %d; response=%s", payload.ErrorCode, expected, recorder.Body.String())
	}
}
