package services

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/pkg/enums"
)

func TestBuildPillowProductMessageUsesControlledShopProduct(t *testing.T) {
	resource, err := WxWorkProtocolShopProductResourceService.BuildPillowProductMessage()
	if err != nil {
		t.Fatalf("build pillow product: %v", err)
	}
	if resource.MessageType != enums.IMMessageTypeShopProduct || resource.Content != pillowProductTitle || resource.Lead != pillowProductLead {
		t.Fatalf("unexpected pillow product resource: %#v", resource)
	}
	var body struct {
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal([]byte(resource.Payload), &body); err != nil {
		t.Fatalf("unmarshal pillow payload: %v", err)
	}
	if body.Content["product_id"] != pillowProductID || body.Content["product_appid"] != pillowProductAppID {
		t.Fatalf("unexpected controlled product identity: %#v", body.Content)
	}
	shopInfo, ok := body.Content["shop_info"].(map[string]any)
	if !ok || strings.TrimSpace(shopInfo["url"].(string)) == "" || strings.TrimSpace(shopInfo["extra_info"].(string)) == "" {
		t.Fatalf("missing original shop info: %#v", body.Content["shop_info"])
	}
}

func TestBuildPillowProductMessageRejectsInvalidBoundResource(t *testing.T) {
	original := pillowProductPayload
	t.Cleanup(func() { pillowProductPayload = original })
	for _, payload := range []string{"{}", `{"content":{"product_id":"other"}}`} {
		pillowProductPayload = payload
		if _, err := WxWorkProtocolShopProductResourceService.BuildPillowProductMessage(); err == nil {
			t.Fatal("invalid resource must not be prepared")
		}
	}
}
