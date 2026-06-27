package i18nx

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
)

const (
	LocaleZhCN = "zh-CN"
	LocaleEnUS = "en-US"
)

var supportedLocales = map[string]string{
	"zh":      LocaleZhCN,
	"zh-cn":   LocaleZhCN,
	"zh_cn":   LocaleZhCN,
	"zh-hans": LocaleZhCN,
	"en":      LocaleEnUS,
	"en-us":   LocaleEnUS,
	"en_us":   LocaleEnUS,
}

func NormalizeLocale(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if key == "" {
		return LocaleZhCN
	}
	if locale, ok := supportedLocales[key]; ok {
		return locale
	}
	return LocaleZhCN
}

func ResolveRequestLocale(req *http.Request) string {
	if req == nil {
		return LocaleZhCN
	}
	if locale := normalizeSupportedLocale(req.Header.Get("X-Locale")); locale != "" {
		return locale
	}
	if locale := resolveAcceptLanguage(req.Header.Get("Accept-Language")); locale != "" {
		return locale
	}
	if locale := normalizeSupportedLocale(req.URL.Query().Get("locale")); locale != "" {
		return locale
	}
	return LocaleZhCN
}

func Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		SetLocale(ctx, ResolveRequestLocale(ctx.Request))
		ctx.Next()
	}
}

func normalizeSupportedLocale(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if key == "" {
		return ""
	}
	if locale, ok := supportedLocales[key]; ok {
		return locale
	}
	return ""
}

func resolveAcceptLanguage(value string) string {
	tags, _, err := language.ParseAcceptLanguage(value)
	if err != nil {
		return ""
	}
	for _, tag := range tags {
		if locale := normalizeSupportedLocale(tag.String()); locale != "" {
			return locale
		}
		base, _ := tag.Base()
		if locale := normalizeSupportedLocale(base.String()); locale != "" {
			return locale
		}
	}
	return ""
}
