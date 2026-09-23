package request

import (
	"encoding/json"
	"testing"
)

func TestWxProtocolChatMsgUnmarshalKeepsStringContent(t *testing.T) {
	var msg WxProtocolChatMsg
	if err := json.Unmarshal([]byte(`{"content":"hello world","content_type":2}`), &msg); err != nil {
		t.Fatalf("unmarshal string content: %v", err)
	}
	if msg.Content != "hello world" {
		t.Fatalf("unexpected string content: %q", msg.Content)
	}
}

func TestWxProtocolChatMsgUnmarshalCompactsObjectContent(t *testing.T) {
	var msg WxProtocolChatMsg
	if err := json.Unmarshal([]byte(`{
		"content_type":597,
		"content":{"product_id":"10001004185008","shop_info":{"url":"https://example.com"}}
	}`), &msg); err != nil {
		t.Fatalf("unmarshal object content: %v", err)
	}
	if msg.Content != `{"product_id":"10001004185008","shop_info":{"url":"https://example.com"}}` {
		t.Fatalf("unexpected compact object content: %s", msg.Content)
	}
}
