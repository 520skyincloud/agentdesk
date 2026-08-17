package utils

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestBuildIMMessageAssetPayloadForResponseAddsSignedURL(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default:               enums.AssetProviderLocal,
			AssetURLSigningSecret: "message-test-signing-secret",
		},
	})
	createTestAsset(t, &models.Asset{
		TenantID: 101, AssetID: "asset_1", Provider: enums.AssetProviderLocal,
		StorageKey: "attachments/demo.png", Filename: "demo.png", Status: enums.AssetStatusSuccess,
	})

	payload := `{"assetId":"asset_1","provider":"local","storageKey":"attachments/demo.png","filename":"demo.png"}`
	got := buildIMMessageAssetPayloadForResponse(payload, 101)

	if !strings.Contains(got, `"provider":"local"`) {
		t.Fatalf("expected provider in payload, got: %s", got)
	}
	if strings.Contains(got, `"storageKey"`) {
		t.Fatalf("expected storageKey hidden from response, got: %s", got)
	}
	if !strings.Contains(got, `"url":"/api/asset/file/asset_1?`) || !strings.Contains(got, `tenantId=101`) {
		t.Fatalf("expected signed url in payload, got: %s", got)
	}
}

func TestSanitizeMessageHTMLStripsStoredSrcForManagedImages(t *testing.T) {
	html := `<p><img src="https://files.example.com/demo.png" data-provider="local" data-storage-key="attachments/demo.png" alt="demo"></p>`

	got := SanitizeMessageHTML(html)

	if strings.Contains(got, `src=`) {
		t.Fatalf("expected src removed from stored html, got: %s", got)
	}
	if !strings.Contains(got, `data-provider="local"`) {
		t.Fatalf("expected data-provider kept, got: %s", got)
	}
	if !strings.Contains(got, `data-storage-key="attachments/demo.png"`) {
		t.Fatalf("expected data-storage-key kept, got: %s", got)
	}
}

func TestBuildMessageHTMLForResponseAddsSignedURL(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default:               enums.AssetProviderLocal,
			AssetURLSigningSecret: "message-test-signing-secret",
		},
	})
	createTestAsset(t, &models.Asset{
		TenantID: 101, AssetID: "asset-html-1", Provider: enums.AssetProviderLocal,
		StorageKey: "attachments/html-demo.png", Filename: "html-demo.png", Status: enums.AssetStatusSuccess,
	})

	html := `<p><img data-asset-id="asset-html-1" data-provider="local" data-storage-key="attachments/html-demo.png" alt="demo"></p>`
	got := BuildMessageHTMLForResponse(html, 101)

	if !strings.Contains(got, `src="/api/asset/file/asset-html-1?`) || !strings.Contains(got, `tenantId=101`) {
		t.Fatalf("expected signed src in response html, got: %s", got)
	}
	if strings.Contains(got, "data-storage-key") || strings.Contains(got, "data-provider") {
		t.Fatalf("expected storage metadata hidden from response html, got: %s", got)
	}
}

func TestBuildRuntimeMessageTextForHTML(t *testing.T) {
	got := BuildRuntimeMessageText(enums.IMMessageTypeHTML, `<p>你好</p><p><img data-provider="local" data-storage-key="images/demo.png" alt="demo"></p>`)
	if got != "你好 [图片]" {
		t.Fatalf("expected html converted to plain text summary, got: %q", got)
	}
}

func TestBuildRuntimeMessageTextForAssetMessages(t *testing.T) {
	if got := BuildRuntimeMessageText(enums.IMMessageTypeImage, "demo.png"); got != "[图片] demo.png" {
		t.Fatalf("unexpected image runtime text: %q", got)
	}
	if got := BuildRuntimeMessageText(enums.IMMessageTypeAttachment, "spec.pdf"); got != "[附件] spec.pdf" {
		t.Fatalf("unexpected attachment runtime text: %q", got)
	}
}

func TestBuildRuntimeMessageTextWithPayloadKeepsMediaUnderstandingInContext(t *testing.T) {
	payload := `{"filename":"room.jpg","mediaText":"图片里是一间酒店客房，床边有一瓶矿泉水。","mediaSummary":"客房照片","mediaUnderstandingStatus":"understood"}`
	got := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeImage, "room.jpg", payload)
	if got != "[图片] room.jpg\n图片内容是：图片里是一间酒店客房，床边有一瓶矿泉水。" {
		t.Fatalf("unexpected image runtime context: %q", got)
	}

	voicePayload := `{"mediaText":"确认，麻烦帮我送两瓶水。","mediaUnderstandingStatus":"understood"}`
	voiceGot := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeVoice, "", voicePayload)
	if voiceGot != "确认，麻烦帮我送两瓶水。" {
		t.Fatalf("unexpected voice runtime context: %q", voiceGot)
	}
	textGot := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeText, "确认，麻烦帮我送两瓶水。", "")
	if voiceGot != textGot {
		t.Fatalf("voice and equivalent text must share canonical runtime text: voice=%q text=%q", voiceGot, textGot)
	}

	summaryPayload := `{"mediaSummary":"账单截图，包含房费金额。","mediaUnderstandingStatus":"understood"}`
	summaryGot := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeImage, "bill.png", summaryPayload)
	if summaryGot != "[图片] bill.png\n图片摘要是：账单截图，包含房费金额。" {
		t.Fatalf("unexpected summary runtime context: %q", summaryGot)
	}
}

func TestBuildRuntimeMessageTextWithPayloadUsesVoiceSummaryOnlyWhenTranscriptMissing(t *testing.T) {
	payload := `{"mediaSummary":"客户询问停车信息。","mediaUnderstandingStatus":"understood"}`
	got := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeVoice, "wx_protocol_1003228.mp3", payload)
	if got != "客户询问停车信息。" {
		t.Fatalf("expected summary as canonical fallback, got: %q", got)
	}
}

func TestBuildRuntimeMessageTextWithPayloadDropsUntranscribedVoice(t *testing.T) {
	payload := `{"filename":"wx_protocol_1003228.mp3","mediaUnderstandingStatus":"failed"}`
	got := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeVoice, "wx_protocol_1003228.mp3", payload)
	if got != "" {
		t.Fatalf("expected failed voice to be hidden from runtime context, got: %q", got)
	}
	withStaleText := `{"mediaText":"这条语音我没听清，请打字补充。","mediaUnderstandingStatus":"failed"}`
	if got := BuildRuntimeMessageTextWithPayload(enums.IMMessageTypeVoice, "wx_protocol_1003228.mp3", withStaleText); got != "" {
		t.Fatalf("failed ASR text must not enter semantic context, got: %q", got)
	}
}

func TestBuildRenderableMessageTransformsPayloadAndHTML(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default:               enums.AssetProviderLocal,
			AssetURLSigningSecret: "message-test-signing-secret",
		},
	})
	createTestAsset(t, &models.Asset{
		TenantID: 101, AssetID: "asset-render-1", Provider: enums.AssetProviderLocal,
		StorageKey: "attachments/render.png", Filename: "render.png", Status: enums.AssetStatusSuccess,
	})

	image := &models.Message{
		TenantID:    101,
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"assetId":"asset-render-1","provider":"local","storageKey":"attachments/render.png","filename":"render.png"}`,
	}
	_, imagePayload := BuildRenderableMessage(image)
	if !strings.Contains(imagePayload, `"url":"/api/asset/file/asset-render-1?`) {
		t.Fatalf("expected image payload signed url, got: %s", imagePayload)
	}

	htmlMsg := &models.Message{
		TenantID:    101,
		MessageType: enums.IMMessageTypeHTML,
		Content:     `<p><img data-asset-id="asset-render-1" data-provider="local" data-storage-key="attachments/render.png"></p>`,
	}
	htmlContent, _ := BuildRenderableMessage(htmlMsg)
	if !strings.Contains(htmlContent, `src="/api/asset/file/asset-render-1?`) {
		t.Fatalf("expected html content signed src, got: %s", htmlContent)
	}
}

func TestNormalizeMessageHTMLAssetsKeepsValidAttrsAndRemovesSrc(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default: enums.AssetProviderLocal,
			Local: config.LocalStorageConfig{
				BaseURL: "https://files.example.com",
			},
		},
	})
	createTestAsset(t, &models.Asset{
		AssetID:    "asset_local_1",
		Provider:   enums.AssetProviderLocal,
		StorageKey: "images/demo.png",
		Filename:   "demo.png",
		FileSize:   123,
		MimeType:   "image/png",
		Status:     enums.AssetStatusSuccess,
	})

	got, err := NormalizeMessageHTMLAssets(`<p><img src="https://files.example.com/images/demo.png" data-asset-id="asset_local_1" data-provider="local" data-storage-key="images/demo.png" alt="demo"></p>`)
	if err != nil {
		t.Fatalf("expected normalization success, got error: %v", err)
	}

	if !strings.Contains(got, `data-asset-id="asset_local_1"`) {
		t.Fatalf("expected data-asset-id added, got: %s", got)
	}
	if strings.Contains(got, `data-provider=`) {
		t.Fatalf("expected data-provider removed, got: %s", got)
	}
	if strings.Contains(got, `data-storage-key=`) {
		t.Fatalf("expected data-storage-key removed, got: %s", got)
	}
	if strings.Contains(got, `src=`) {
		t.Fatalf("expected src removed after asset binding, got: %s", got)
	}
}

func TestNormalizeMessageHTMLAssetsAcceptsAssetIDOnly(t *testing.T) {
	setupMessageTestDB(t)
	createTestAsset(t, &models.Asset{
		TenantID: 101, AssetID: "asset_id_only", Provider: enums.AssetProviderLocal,
		StorageKey: "images/id-only.png", Filename: "id-only.png", Status: enums.AssetStatusSuccess,
	})

	got, err := NormalizeMessageHTMLAssetsInTenant(`<p><img data-asset-id="asset_id_only" alt="demo"></p>`, 101)
	if err != nil {
		t.Fatalf("normalize assetId-only image: %v", err)
	}
	if !strings.Contains(got, `data-asset-id="asset_id_only"`) || strings.Contains(got, "data-storage-key") || strings.Contains(got, "src=") {
		t.Fatalf("unexpected normalized html: %s", got)
	}
}

func TestNormalizeMessageHTMLAssetsRejectsMissingAssetMetadata(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default: enums.AssetProviderLocal,
			Local: config.LocalStorageConfig{
				BaseURL: "https://files.example.com",
			},
		},
	})

	_, err := NormalizeMessageHTMLAssets(`<p><img src="https://unknown.example.com/demo.png" alt="demo"></p>`)
	if err == nil {
		t.Fatalf("expected missing image asset metadata rejected")
	}
}

func TestNormalizeMessageHTMLAssetsRejectsIncompleteAttrs(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default: enums.AssetProviderLocal,
			Local: config.LocalStorageConfig{
				BaseURL: "https://files.example.com",
			},
		},
	})

	_, err := NormalizeMessageHTMLAssets(`<p><img data-asset-id="asset1" data-provider="local" alt="demo"></p>`)
	if err == nil {
		t.Fatalf("expected incomplete asset attrs rejected")
	}
}

func TestNormalizeMessageHTMLAssetsRejectsMismatchedAttrs(t *testing.T) {
	setupMessageTestDB(t)
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Default: enums.AssetProviderLocal,
			Local: config.LocalStorageConfig{
				BaseURL: "https://files.example.com",
			},
		},
	})
	createTestAsset(t, &models.Asset{
		AssetID:    "asset_local_2",
		Provider:   enums.AssetProviderLocal,
		StorageKey: "images/real.png",
		Filename:   "real.png",
		FileSize:   456,
		MimeType:   "image/png",
		Status:     enums.AssetStatusSuccess,
	})

	_, err := NormalizeMessageHTMLAssets(`<p><img data-asset-id="asset_local_2" data-provider="local" data-storage-key="images/wrong.png" alt="demo"></p>`)
	if err == nil {
		t.Fatalf("expected mismatched asset attrs rejected")
	}
}

func setupMessageTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatalf("auto migrate asset failed: %v", err)
	}
	sqls.SetDB(db)
}

func createTestAsset(t *testing.T, item *models.Asset) {
	t.Helper()
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	if err := sqls.DB().Create(item).Error; err != nil {
		t.Fatalf("create asset failed: %v", err)
	}
}
