package services

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestAssetRuntimeTenantIsolation(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	if err := fixture.db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatalf("migrate asset table: %v", err)
	}

	customerA := &models.Customer{TenantID: 101, Name: "asset-customer-a", Status: enums.StatusOk}
	customerB := &models.Customer{TenantID: 202, Name: "asset-customer-b", Status: enums.StatusOk}
	if err := fixture.db.Create(customerA).Error; err != nil {
		t.Fatalf("create tenant A customer: %v", err)
	}
	if err := fixture.db.Create(customerB).Error; err != nil {
		t.Fatalf("create tenant B customer: %v", err)
	}
	now := time.Now()
	conversationA := &models.Conversation{TenantID: 101, ChannelID: fixture.channelA.ID, CustomerID: customerA.ID, Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now}
	conversationB := &models.Conversation{TenantID: 202, ChannelID: fixture.channelB.ID, CustomerID: customerB.ID, Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}

	assetA := createAssetTenantFixture(t, fixture.db, 101, "runtime-asset-a")
	assetB := createAssetTenantFixture(t, fixture.db, 202, "runtime-asset-b")

	listA, _ := AssetService.FindPageByCndInTenant(sqls.NewCnd().Page(1, 20), 101)
	if len(listA) != 1 || listA[0].ID != assetA.ID {
		t.Fatalf("tenant A asset list=%+v want asset %d", listA, assetA.ID)
	}
	if AssetService.GetInTenant(assetB.ID, 101) != nil || AssetService.GetByAssetIDInTenant(assetB.AssetID, 101) != nil {
		t.Fatal("tenant A must not read tenant B asset")
	}
	if err := AssetService.DeleteAsset(assetB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B asset")
	}
	if got := AssetService.GetInTenant(assetB.ID, 202); got == nil || got.Status != enums.AssetStatusSuccess {
		t.Fatalf("tenant B asset changed after foreign delete: %+v", got)
	}

	foreignPayload := fmt.Sprintf(`{"assetId":%q}`, assetB.AssetID)
	if _, _, _, err := MessageService.normalizeMessageContent(conversationA.ID, enums.IMMessageTypeImage, "", foreignPayload); err == nil {
		t.Fatal("tenant A conversation must reject tenant B image")
	}
	if _, _, _, err := MessageService.normalizeMessageContent(conversationA.ID, enums.IMMessageTypeGIF, "", foreignPayload); err == nil {
		t.Fatal("tenant A conversation must reject tenant B gif")
	}

	localPayload := fmt.Sprintf(`{"assetId":%q}`, assetA.AssetID)
	content, payload, summary, err := MessageService.normalizeMessageContent(conversationA.ID, enums.IMMessageTypeGIF, "", localPayload)
	if err != nil {
		t.Fatalf("normalize same-tenant gif: %v", err)
	}
	if content != assetA.Filename || !strings.Contains(payload, assetA.AssetID) || !strings.Contains(summary, "动画") {
		t.Fatalf("unexpected gif normalization content=%q payload=%q summary=%q", content, payload, summary)
	}

	foreignHTML := fmt.Sprintf(`<p><img data-asset-id=%q data-provider=%q data-storage-key=%q></p>`, assetB.AssetID, assetB.Provider, assetB.StorageKey)
	if _, _, _, err := MessageService.normalizeMessageContent(conversationA.ID, enums.IMMessageTypeHTML, foreignHTML, ""); err == nil {
		t.Fatal("tenant A conversation must reject tenant B HTML image")
	}
	localHTML := fmt.Sprintf(`<p><img src="https://example.invalid/image.png" data-asset-id=%q data-provider=%q data-storage-key=%q></p>`, assetA.AssetID, assetA.Provider, assetA.StorageKey)
	normalizedHTML, _, _, err := MessageService.normalizeMessageContent(conversationA.ID, enums.IMMessageTypeHTML, localHTML, "")
	if err != nil {
		t.Fatalf("normalize same-tenant HTML image: %v", err)
	}
	if strings.Contains(normalizedHTML, "src=") || !strings.Contains(normalizedHTML, assetA.AssetID) {
		t.Fatalf("unexpected normalized HTML: %s", normalizedHTML)
	}

	messageB := &models.Message{
		TenantID:       202,
		ConversationID: conversationB.ID,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Payload:        `{"assetId":"runtime-asset-b","mediaUnderstandingStatus":"pending"}`,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
	}
	if err := fixture.db.Create(messageB).Error; err != nil {
		t.Fatalf("create tenant B media message: %v", err)
	}
	if err := MediaUnderstandingService.updateMessagePayload(messageB.ID, 101, &messageMediaPayload{AssetID: assetA.AssetID, MediaStatus: "understood"}); err != nil {
		t.Fatalf("foreign media update returned database error: %v", err)
	}
	unchanged := repositories.MessageRepository.GetInTenant(fixture.db, messageB.ID, 202)
	if unchanged == nil || !strings.Contains(unchanged.Payload, "runtime-asset-b") || strings.Contains(unchanged.Payload, "understood") {
		t.Fatalf("tenant B media payload changed through tenant A condition: %+v", unchanged)
	}
}

func TestAssetStoragePrefixIncludesTenant(t *testing.T) {
	prefix := tenantStorageObjectPrefix("images", 101)
	if !strings.Contains(prefix, "tenants/101/images") {
		t.Fatalf("tenant storage prefix=%q", prefix)
	}
}

func TestAssetAccessURLRefreshPreservesTenantBoundary(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	if err := fixture.db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatalf("migrate asset table: %v", err)
	}
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		AssetURLSigningSecret: "asset-refresh-signing-secret",
		Local:                 config.LocalStorageConfig{BaseURL: "/uploads"},
	}})
	assetA := createAssetTenantFixture(t, fixture.db, 101, "refresh-asset-a")
	assetB := createAssetTenantFixture(t, fixture.db, 202, "refresh-asset-b")

	legacyURL := "/uploads/" + assetA.StorageKey
	refreshed := AssetService.RefreshAccessURL(legacyURL, 101, assetaccess.PurposeInline)
	if !strings.HasPrefix(refreshed, "/api/asset/file/"+assetA.AssetID+"?") || !strings.Contains(refreshed, "tenantId=101") {
		t.Fatalf("refreshed legacy URL=%q", refreshed)
	}
	refreshedAgain := AssetService.RefreshAccessURL(refreshed, 101, assetaccess.PurposeInline)
	if !strings.HasPrefix(refreshedAgain, "/api/asset/file/"+assetA.AssetID+"?") {
		t.Fatalf("refreshed signed URL=%q", refreshedAgain)
	}
	if got := AssetService.RefreshAccessURL("/uploads/"+assetB.StorageKey, 101, assetaccess.PurposeInline); got != "" {
		t.Fatalf("foreign tenant legacy URL refreshed as %q", got)
	}
	if got := AssetService.RefreshAccessURL("https://avatars.example.com/external.png", 101, assetaccess.PurposeInline); got != "https://avatars.example.com/external.png" {
		t.Fatalf("external avatar URL changed to %q", got)
	}
}

func createAssetTenantFixture(t *testing.T, db *gorm.DB, tenantID int64, assetID string) *models.Asset {
	t.Helper()
	now := time.Now()
	item := &models.Asset{
		TenantID:   tenantID,
		AssetID:    assetID,
		Provider:   enums.AssetProviderLocal,
		StorageKey: fmt.Sprintf("tenants/%d/images/%s.png", tenantID, assetID),
		Filename:   assetID + ".gif",
		MimeType:   "image/gif",
		Status:     enums.AssetStatusSuccess,
		AuditFields: models.AuditFields{
			CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test",
		},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create asset %s: %v", assetID, err)
	}
	return item
}
