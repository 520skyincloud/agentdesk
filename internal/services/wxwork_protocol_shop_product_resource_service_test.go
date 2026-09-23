package services

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/pkg/enums"
)

func TestBuildPillowProductMessageUsesControlledShopProduct(t *testing.T) {
	resource, err := WxWorkProtocolShopProductResourceService.BuildPillowProductMessage("你们酒店同款枕头怎么买，有购买链接吗")
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

func TestBuildPillowProductMessageRequiresExplicitPurchaseIntent(t *testing.T) {
	for _, text := range []string{"你们家枕头好舒服", "这个枕头看起来不错"} {
		if _, err := WxWorkProtocolShopProductResourceService.BuildPillowProductMessage(text); err == nil {
			t.Fatalf("expected no product card for compliment %q", text)
		}
	}
}

func TestBuildPillowProductMessageRejectsRoomServiceRequests(t *testing.T) {
	for _, text := range []string{"送两个枕头到房间", "帮我换个枕头", "枕头脏了", "这个枕头不舒服"} {
		if _, err := WxWorkProtocolShopProductResourceService.BuildPillowProductMessage(text); err == nil || !strings.Contains(err.Error(), "客房枕头服务请求") {
			t.Fatalf("expected room-service rejection for %q, got %v", text, err)
		}
	}
}
