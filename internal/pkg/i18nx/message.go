package i18nx

import (
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

func T(ctx *gin.Context, messageID string, data map[string]any) string {
	if ctx != nil {
		if value, exists := ctx.Get(contextLocaleKey); exists {
			if locale, ok := value.(string); ok {
				return TLocale(locale, messageID, data)
			}
		}
	}
	return TLocale(LocaleZhCN, messageID, data)
}

func TLocale(locale string, messageID string, data map[string]any) string {
	normalized := NormalizeLocale(locale)
	message := localize(normalized, messageID, data)
	if message != "" {
		return message
	}
	if normalized != LocaleZhCN {
		message = localize(LocaleZhCN, messageID, data)
		if message != "" {
			return message
		}
	}
	return messageID
}

func localize(locale string, messageID string, data map[string]any) string {
	localizer := i18n.NewLocalizer(Bundle(), locale, LocaleZhCN)
	message, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: data,
	})
	if err != nil {
		return ""
	}
	return message
}
