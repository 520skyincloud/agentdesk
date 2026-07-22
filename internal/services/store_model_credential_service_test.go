package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/newapi"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestStoreModelCredentialSafeResponseOmitsSecretsAndBaseURLs(t *testing.T) {
	db := setupStoreModelCredentialTestDB(t)
	store := createStoreModelCredentialTestStore(t, db, "南七店")
	template := completeStoreModelCredentialTemplate("https://private-gateway.example.com/v1")
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FastGPTStoreTenant{
		StoreID: store.ID, Status: "active",
		ProfileTemplateSyncStatus: "ready", ProfileTemplateRevision: template.Revision,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		StoreID: store.ID, CompanyID: store.CompanyID,
		Status: storeModelCredentialStatusActive, CredentialRevision: 2,
		KeyFingerprint: securex.Fingerprint("sk-secret-value"),
	}).Error; err != nil {
		t.Fatal(err)
	}

	ret, err := newStoreModelCredentialService().Get(
		request.StoreModelCredentialRequest{StoreID: store.ID},
		&dto.AuthPrincipal{UserID: 1, Roles: []string{constants.RoleCodeSuperAdmin}},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ret)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"profileStatus":"ready"`) {
		t.Fatalf("safe profile metadata is incomplete: %s", body)
	}
	for _, forbidden := range []string{
		"sk-secret-value", "private-gateway.example.com", "baseUrl",
		"apiKey", "encryptedKey", "keyFingerprint", "chat-model",
		"vision-model", "asr-model", "embedding-model", "rerank-model",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("safe credential DTO leaked %q: %s", forbidden, body)
		}
	}
}

func TestCandidateCredentialValidationDoesNotGateOnASR(t *testing.T) {
	tests := candidateCredentialTests(completeStoreModelCredentialTemplate("https://gateway.example.com/v1"), "sk-test")
	codes := make([]string, 0, len(tests))
	for _, item := range tests {
		codes = append(codes, item.code)
	}
	if slices.Contains(codes, "asr") {
		t.Fatalf("ASR must remain configurable without blocking credential activation: %#v", codes)
	}
	for _, required := range []string{"chat", "vision", "embedding", "rerank", "document_parser"} {
		if !slices.Contains(codes, required) {
			t.Fatalf("required credential validation slot %q is missing: %#v", required, codes)
		}
	}
}

func TestStoreModelCredentialScopeAndSuperAdminStoreList(t *testing.T) {
	db := setupStoreModelCredentialTestDB(t)
	first := createStoreModelCredentialTestStore(t, db, "南七店")
	second := createStoreModelCredentialTestStore(t, db, "高铁南站店")
	if err := db.Create(&models.StoreStaffBinding{
		UserID: 77, CompanyID: first.CompanyID, StoreID: first.ID, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := newStoreModelCredentialService()
	storeStaff := &dto.AuthPrincipal{
		UserID: 77, Username: "store-user", Roles: []string{constants.RoleCodeStoreStaff},
	}
	if _, err := service.Get(request.StoreModelCredentialRequest{StoreID: first.ID}, storeStaff); err != nil {
		t.Fatalf("store staff should read the bound store: %v", err)
	}
	if _, err := service.Get(request.StoreModelCredentialRequest{StoreID: second.ID}, storeStaff); err == nil {
		t.Fatal("store staff must not read another store")
	}
	staffStores, err := service.ListStores(storeStaff)
	if err != nil {
		t.Fatal(err)
	}
	if len(staffStores) != 1 || staffStores[0].StoreID != first.ID {
		t.Fatalf("unexpected store staff scope: %#v", staffStores)
	}

	superStores, err := service.ListStores(&dto.AuthPrincipal{
		UserID: 1, Username: "root", Roles: []string{constants.RoleCodeSuperAdmin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(superStores) != 2 {
		t.Fatalf("super admin should list both stores, got %#v", superStores)
	}
}

func TestStoreModelCredentialFailedCandidatePreservesActiveRevision(t *testing.T) {
	db := setupStoreModelCredentialTestDB(t)
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey}})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "candidate rejected", http.StatusUnauthorized)
	}))
	defer server.Close()

	store := createStoreModelCredentialTestStore(t, db, "失败切换测试店")
	template := completeStoreModelCredentialTemplate(server.URL + "/v1")
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	activeKey := "sk-active-secret"
	encryptedKey, nonce, err := cipher.Encrypt(activeKey, credentialAAD(store.ID, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		CompanyID: store.CompanyID, StoreID: store.ID,
		EncryptedKey: encryptedKey, KeyNonce: nonce,
		KeyFingerprint:     securex.Fingerprint(activeKey),
		CredentialRevision: 3, Status: storeModelCredentialStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	_, err = newStoreModelCredentialService().Update(
		context.Background(),
		request.UpdateStoreModelCredentialRequest{StoreID: store.ID, APIKey: "sk-candidate-secret"},
		&dto.AuthPrincipal{
			UserID: 1, Username: "root", Roles: []string{constants.RoleCodeSuperAdmin},
		},
	)
	if err == nil {
		t.Fatal("invalid candidate should fail validation")
	}
	current := repositories.StoreModelCredentialRepository.GetByStoreID(db, store.ID)
	if current == nil {
		t.Fatal("credential record disappeared")
	}
	if current.CredentialRevision != 3 || current.Status != storeModelCredentialStatusActive {
		t.Fatalf("active credential changed after failed candidate: %#v", current)
	}
	if current.CandidateRevision != 4 || current.CandidateStatus != storeModelCandidateStatusFailed {
		t.Fatalf("failed candidate audit state was not retained: %#v", current)
	}
	resolved, err := newStoreModelCredentialService().resolveRecord(current)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != activeKey {
		t.Fatal("failed candidate replaced the active plaintext credential")
	}
}

func TestVisionConnectionTestImageMeetsModelSizeRequirements(t *testing.T) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(visionConnectionTestImage, prefix) {
		t.Fatal("vision connection test image must be an inline PNG")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(visionConnectionTestImage, prefix))
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if image.Width <= 10 || image.Height <= 10 {
		t.Fatalf("vision connection test image is too small: %dx%d", image.Width, image.Height)
	}
}

func TestParseBillingDateRangeUsesShanghaiCalendarDays(t *testing.T) {
	dateRange, err := parseBillingDateRange("2026-07-20", "2026-07-21")
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	expectedStart := time.Date(2026, 7, 20, 0, 0, 0, 0, location).Unix()
	expectedEnd := time.Date(2026, 7, 22, 0, 0, 0, 0, location).Unix()
	if dateRange.StartTimestamp != expectedStart || dateRange.EndExclusiveTimestamp != expectedEnd {
		t.Fatalf("range=%#v expectedStart=%d expectedEnd=%d", dateRange, expectedStart, expectedEnd)
	}
	if !dateRange.Contains(expectedStart) || !dateRange.Contains(expectedEnd-1) || dateRange.Contains(expectedEnd) {
		t.Fatalf("date range boundaries are incorrect: %#v", dateRange)
	}
}

func TestParseBillingDateRangeRejectsInvalidRange(t *testing.T) {
	if _, err := parseBillingDateRange("2026-07-21", "2026-07-20"); err == nil {
		t.Fatal("end date before start date must be rejected")
	}
	if _, err := parseBillingDateRange("2026-07-20", ""); err == nil {
		t.Fatal("partial date range must be rejected")
	}
}

func TestQueryBillingUsesDateRangeAndReturnsDirectCNYAmounts(t *testing.T) {
	db := setupStoreModelCredentialTestDB(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	startTimestamp := time.Date(2026, 7, 20, 0, 0, 0, 0, location).Unix()
	endTimestamp := time.Date(2026, 7, 21, 23, 59, 59, 0, location).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_display_type":"CNY","quota_per_unit":500000,"usd_exchange_rate":7.3,"price":7.3}}`))
		case "/api/usage/token/":
			_, _ = w.Write([]byte(`{"code":true,"data":{"name":"store-token","total_granted":1000000,"total_used":250000,"total_available":750000,"expires_at":0}}`))
		case "/api/log/token":
			if r.URL.Query().Get("start_timestamp") != fmt.Sprintf("%d", startTimestamp) ||
				r.URL.Query().Get("end_timestamp") != fmt.Sprintf("%d", endTimestamp) {
				http.Error(w, "unexpected date range", http.StatusBadRequest)
				return
			}
			insideTimestamp := startTimestamp + 3600
			outsideTimestamp := startTimestamp - 1
			_, _ = fmt.Fprintf(
				w,
				`{"success":true,"data":[{"id":1,"created_at":%d,"type":2,"model_name":"chat","quota":250000,"prompt_tokens":8,"completion_tokens":4,"use_time":1,"request_id":"req-in"},{"id":2,"created_at":%d,"type":2,"model_name":"chat","quota":500000,"prompt_tokens":10,"completion_tokens":5,"use_time":1,"request_id":"req-out"}]}`,
				insideTimestamp,
				outsideTimestamp,
			)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{
		NewAPIUsage:     config.NewAPIUsageConfig{BaseURL: server.URL, TimeoutMS: 5000},
		StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey},
	})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
	})

	store := createStoreModelCredentialTestStore(t, db, "日期计费测试店")
	template := completeStoreModelCredentialTemplate(server.URL + "/v1")
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	encryptedKey, nonce, err := cipher.Encrypt("sk-billing-test", credentialAAD(store.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		CompanyID: store.CompanyID, StoreID: store.ID,
		EncryptedKey: encryptedKey, KeyNonce: nonce,
		KeyFingerprint:     securex.Fingerprint("sk-billing-test"),
		CredentialRevision: 1, Status: storeModelCredentialStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := newStoreModelCredentialService().QueryBilling(
		context.Background(),
		request.BillingQueryRequest{
			StoreID: store.ID, StartDate: "2026-07-20", EndDate: "2026-07-21",
		},
		&dto.AuthPrincipal{UserID: 1, Roles: []string{constants.RoleCodeSuperAdmin}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Logs) != 1 || result.Logs[0].RequestID != "req-in" {
		t.Fatalf("logs=%#v", result.Logs)
	}
	if result.Summary.GrantedCNY != 14.6 || result.Summary.UsedCNY != 3.65 ||
		result.Summary.AvailableCNY != 10.95 || result.PeriodCostCNY != 3.65 {
		t.Fatalf("unexpected CNY amounts: %#v", result)
	}
	if result.PeriodPromptTokens != 8 || result.PeriodOutputTokens != 4 {
		t.Fatalf("unexpected period tokens: %#v", result)
	}
}

func TestQuotaCNYUsesNewAPIBillingUnitDirectly(t *testing.T) {
	settings := &newapi.TokenBillingSettings{QuotaPerUnit: 500000, USDExchangeRate: 7.3}
	if got := quotaCNY(500000, settings); got != 7.3 {
		t.Fatalf("quotaCNY=%v", got)
	}
}

func setupStoreModelCredentialTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Store{},
		&models.StoreStaffBinding{},
		&models.WxWorkProtocolInstance{},
		&models.KnowledgeBase{},
		&models.StoreModelCredential{},
		&models.FastGPTStoreTenant{},
		&models.FastGPTProfileTemplate{},
		&models.AIUsageEvent{},
		&models.AIUsageGatewayCall{},
	); err != nil {
		t.Fatalf("migrate store credential tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}

func createStoreModelCredentialTestStore(t *testing.T, db *gorm.DB, name string) *models.Store {
	t.Helper()
	now := time.Now()
	store := &models.Store{
		StoreCode: fmt.Sprintf("store-%d", now.UnixNano()),
		Name:      name, CompanyID: 10, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	return store
}

func completeStoreModelCredentialTemplate(baseURL string) *models.FastGPTProfileTemplate {
	now := time.Now()
	return &models.FastGPTProfileTemplate{
		ID: 1, Name: "门店统一模型模板", Revision: 6, Status: fastGPTProfileTemplateStatusActive,
		ChatProvider: "OpenAI", ChatBaseURL: baseURL, ChatModel: "chat-model", ChatAPIMode: "chat_completions",
		VisionProvider: "OpenAI", VisionBaseURL: baseURL, VisionModel: "vision-model",
		ASRProvider: "OpenAI", ASRBaseURL: baseURL, ASRModel: "asr-model",
		EmbeddingProvider: "OpenAI", EmbeddingBaseURL: baseURL, EmbeddingModel: "embedding-model",
		RerankProvider: "OpenAI", RerankBaseURL: baseURL, RerankModel: "rerank-model",
		DocumentParserProvider: "OpenAI", DocumentParserBaseURL: baseURL, DocumentParserModel: "document-model",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
}
