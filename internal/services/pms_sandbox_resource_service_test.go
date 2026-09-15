package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPMSSandboxCardDoesNotChangeProductTargetOrInjectCheckinParams(t *testing.T) {
	input := `{"wxPayload":{"guid":"old-guid","conversation_id":"S:old","username":"gh_product@app","appid":"wx-product","title":"枕头","page_path":"product/view?id=123","appicon":"https://example.test/pillow.png","store_query_params":{"hotelId":"secret"},"authorization":"private","_internal":"drop"}}`
	got, err := SanitizePMSSandboxCard("mini_program", input)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got), &body); err != nil {
		t.Fatal(err)
	}
	if body["page_path"] != "product/view?id=123" || body["username"] != "gh_product@app" {
		t.Fatal("product target changed")
	}
	for _, key := range []string{"guid", "conversation_id", "store_query_params", "authorization", "_internal"} {
		if _, ok := body[key]; ok {
			t.Fatalf("unexpected field retained: %s", key)
		}
	}
}

func TestPMSSandboxTextTokenIsNotAnAcceptedCard(t *testing.T) {
	for _, input := range []struct{ kind, payload string }{
		{"text", `{"content":"#微信小店://丽斯严选/NxS0zhyvZJmEDEe"}`},
		{"mini_program", `{"title":"枕头","appid":"wx-placeholder"}`},
		{"shop_product", `{"content":{"product_title":"枕头"}}`},
		{"mini_program", `{} {}`},
	} {
		if _, err := SanitizePMSSandboxCard(input.kind, input.payload); err == nil {
			t.Fatal("incomplete card accepted")
		}
	}
}

func TestPMSSandboxProductCardPreservesLongIDs(t *testing.T) {
	raw := `{"content":{"finder_live_id":"","finder_username":"","finder_object_id":"","finder_nonce_id":"","product_appid":"wx-test","product_page_path":"pillow?id=9007199254740993","product_id":9007199254740993,"product_cover_url":"https://example.test/pillow.png","product_title":"测试枕头","product_desc":"","product_price":"99","platform_headimg":"","platform_nickname":"测试小店","shop_info":{"shop_id":"123","url":"","extra_info":""},"privateToken":"remove"}}`
	got, err := SanitizePMSSandboxCard("shop_product", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"product_id":"9007199254740993"`) || strings.Contains(got, "privateToken") {
		t.Fatalf("product ID or whitelist incorrect: %s", got)
	}
}
