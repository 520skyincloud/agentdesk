package imsource

import (
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/i18nx"
)

const (
	SendSourceLocal = "local"
	SendSourceWeb   = "web"
)

func DetectSendSource(senderType enums.IMSenderType, requestID, clientMsgID string) string {
	if senderType != enums.IMSenderTypeAgent {
		return ""
	}
	requestID = strings.TrimSpace(requestID)
	clientMsgID = strings.TrimSpace(clientMsgID)
	if requestID == "wx_protocol_self_echo" || strings.HasPrefix(clientMsgID, "wx_protocol:") {
		return SendSourceLocal
	}
	return SendSourceWeb
}

func SendSourceLabel(source, locale string) string {
	switch strings.TrimSpace(source) {
	case SendSourceLocal:
		if i18nx.NormalizeLocale(locale) == i18nx.LocaleEnUS {
			return "Local"
		}
		return "本地"
	case SendSourceWeb:
		if i18nx.NormalizeLocale(locale) == i18nx.LocaleEnUS {
			return "Web"
		}
		return "网页"
	default:
		return ""
	}
}
