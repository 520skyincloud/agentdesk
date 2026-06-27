package i18nx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNormalizeLocale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default for blank", in: "", want: LocaleZhCN},
		{name: "exact chinese", in: "zh-CN", want: LocaleZhCN},
		{name: "underscore chinese", in: "zh_CN", want: LocaleZhCN},
		{name: "short chinese", in: "zh", want: LocaleZhCN},
		{name: "exact english", in: "en-US", want: LocaleEnUS},
		{name: "underscore english", in: "en_US", want: LocaleEnUS},
		{name: "short english", in: "en", want: LocaleEnUS},
		{name: "unsupported falls back", in: "fr-FR", want: LocaleZhCN},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeLocale(tt.in); got != tt.want {
				t.Fatalf("NormalizeLocale(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveLocaleFromHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/user/list", nil)
	req.Header.Set("Accept-Language", "fr-FR, en-US;q=0.9, zh-CN;q=0.8")

	if got := ResolveRequestLocale(req); got != LocaleEnUS {
		t.Fatalf("ResolveRequestLocale() = %q, want %q", got, LocaleEnUS)
	}
}

func TestResolveLocalePrefersXLocale(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/user/list", nil)
	req.Header.Set("X-Locale", "en-US")
	req.Header.Set("Accept-Language", "zh-CN")

	if got := ResolveRequestLocale(req); got != LocaleEnUS {
		t.Fatalf("ResolveRequestLocale() = %q, want %q", got, LocaleEnUS)
	}
}

func TestTranslateFallsBackToChinese(t *testing.T) {
	t.Parallel()

	if got := TLocale(LocaleEnUS, "error.auth.expired", nil); got != "Your session has expired. Please sign in again." {
		t.Fatalf("english translation = %q", got)
	}
	if got := TLocale("fr-FR", "error.auth.expired", nil); got != "未登录或登录已过期" {
		t.Fatalf("fallback translation = %q", got)
	}
}

func TestTranslateKnownMessageSupportsFormattedMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "batch limit",
			message: "单次最多生成 31 条排班",
			want:    "You can generate at most 31 schedule entries at a time.",
		},
		{
			name:    "start date",
			message: "开始时间格式错误，请使用 yyyy-MM-dd",
			want:    "Invalid start time format. Use yyyy-MM-dd.",
		},
		{
			name:    "end date time",
			message: "结束时间格式错误，请使用 yyyy-MM-dd HH:mm:ss 或 RFC3339",
			want:    "Invalid end time format. Use yyyy-MM-dd HH:mm:ss or RFC3339.",
		},
		{
			name:    "oidc issuer config",
			message: "OIDC issuer 未配置",
			want:    "OIDC issuer is not configured.",
		},
		{
			name:    "oidc login result",
			message: "登录结果不能为空",
			want:    "Sign-in result is required.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TranslateKnownMessage(LocaleEnUS, tt.message); got != tt.want {
				t.Fatalf("TranslateKnownMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMiddlewareStoresLocale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/ping", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, Locale(ctx))
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Locale", "en-US")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Body.String(); got != LocaleEnUS {
		t.Fatalf("middleware locale = %q, want %q", got, LocaleEnUS)
	}
}
