package dashboard

import (
	"testing"

	"agent-desk/internal/models"
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
