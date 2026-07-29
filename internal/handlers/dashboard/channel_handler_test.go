package dashboard

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestChannelListResponseRedactsSensitiveConfig(t *testing.T) {
	item := &models.Channel{
		TenantID:   12,
		ConfigJSON: `{"appKey":"visible-key","appSecret":"must-not-leak","callbackToken":"must-not-leak"}`,
	}

	listResponse := buildChannelPublicResponse(item)
	if listResponse.ConfigJSON != "" {
		t.Fatalf("list response exposed sensitive channel config: %s", listResponse.ConfigJSON)
	}

	detailResponse := buildChannelResponse(item)
	if detailResponse.ConfigJSON != item.ConfigJSON {
		t.Fatalf("update-authorized detail lost channel config: %s", detailResponse.ConfigJSON)
	}
}

func TestChannelDetailResponseRedactsWxWorkProtocolCredentials(t *testing.T) {
	item := &models.Channel{
		TenantID:    12,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConfigJSON:  `{"appKey":"must-not-leak","appSecret":"must-not-leak","callbackToken":"must-not-leak","baseUrl":"https://protocol.example.test"}`,
	}
	detail := buildChannelResponse(item)
	if strings.Contains(detail.ConfigJSON, "must-not-leak") {
		t.Fatalf("protocol channel detail exposed credentials: %s", detail.ConfigJSON)
	}
	if !strings.Contains(detail.ConfigJSON, "https://protocol.example.test") {
		t.Fatalf("protocol channel detail lost non-secret configuration: %s", detail.ConfigJSON)
	}
}
