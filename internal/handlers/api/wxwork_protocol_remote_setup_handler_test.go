package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildRemoteLoginQRCodeResponseDoesNotExposeProviderFields(t *testing.T) {
	raw := `{"err_code":0,"data":{"qrcode":"base64-image","qrcode_content":"https://wx.work.weixin.qq.com/login","key":"must-not-return"}}`
	result, err := buildRemoteLoginQRCodeResponse(42, raw)
	if err != nil {
		t.Fatalf("build response: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"rawResponse", "must-not-return", `"key"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("remote login response leaked %q: %s", forbidden, body)
		}
	}
}

func TestBuildRemoteLoginQRCodeResponseRejectsMissingImage(t *testing.T) {
	if _, err := buildRemoteLoginQRCodeResponse(42, `{"err_code":0,"data":{"status":0}}`); err == nil {
		t.Fatal("missing qrcode should be rejected")
	}
}
