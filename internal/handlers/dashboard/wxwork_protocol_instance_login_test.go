package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildWxWorkProtocolLoginQRCodeResponse(t *testing.T) {
	raw := `{"err_code":0,"data":{"qrcode":"base64-image","qrcode_content":"https://wx.work.weixin.qq.com/login?key=opaque","key":"must-not-return"}}`

	result, err := buildWxWorkProtocolLoginQRCodeResponse(raw)
	if err != nil {
		t.Fatalf("build response: %v", err)
	}
	if result.QRCode != "base64-image" {
		t.Fatalf("unexpected qrcode: %q", result.QRCode)
	}
	if result.QRCodeContent != "https://wx.work.weixin.qq.com/login?key=opaque" {
		t.Fatalf("unexpected qrcode content: %q", result.QRCodeContent)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, "rawResponse") || strings.Contains(body, "must-not-return") {
		t.Fatalf("response leaked protocol-only fields: %s", body)
	}
}

func TestBuildWxWorkProtocolLoginQRCodeResponseRejectsMissingImage(t *testing.T) {
	if _, err := buildWxWorkProtocolLoginQRCodeResponse(`{"err_code":0,"data":{"status":0}}`); err == nil {
		t.Fatal("missing qrcode should be rejected")
	}
}
