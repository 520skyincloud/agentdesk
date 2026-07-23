package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/pkg/usagex"

	"gorm.io/gorm"
)

func TestRuntimeAuxiliaryModelCallsUseFinalSlotsAndAttribution(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		attempt := callCount.Add(1)
		content := `{"decision":"confirm","confidence":0.96,"reason":"客户明确同意"}`
		if attempt == 2 {
			content = "客人反馈房间空调无法制冷，需要门店同事尽快处理。"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(usagex.NewAPIRequestIDHeader, fmt.Sprintf("newapi-%d", attempt))
		w.Header().Set(usagex.NewAPIUpstreamIDHeader, fmt.Sprintf("upstream-%d", attempt))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-runtime-attribution", "object": "chat.completion", "created": time.Now().Unix(),
			"model": "runtime-test", "choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": content},
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer server.Close()

	db := setupModelProfileTestDB(t)
	if err := db.AutoMigrate(&models.Message{}, &models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatalf("migrate runtime attribution tables: %v", err)
	}
	tenant, store, profile, credential, conversation, message := seedRuntimeModelAttributionFixture(t, db, server.URL+"/v1")

	vision, err := ModelCallResolverService.ResolveForConversation(conversation.ID, enums.ModelUsageSlotVision)
	if err != nil {
		t.Fatalf("resolve vision slot: %v", err)
	}
	asr, err := ModelCallResolverService.ResolveForConversation(conversation.ID, enums.ModelUsageSlotASR)
	if err != nil {
		t.Fatalf("resolve ASR slot: %v", err)
	}
	confirmation, ok := resolveHandoffConfirmationModelCall(conversation)
	if !ok {
		t.Fatal("resolve handoff confirmation slot failed")
	}
	summary, ok := newConversationHumanDispatchService().resolveHandoffSummaryModelCall(conversation)
	if !ok {
		t.Fatal("resolve handoff summary slot failed")
	}

	assertRuntimeModelCallSlot(t, vision, enums.ModelUsageSlotVision, "vision-model", tenant.ID, store.ID, profile, credential)
	assertRuntimeModelCallSlot(t, asr, enums.ModelUsageSlotASR, "asr-model", tenant.ID, store.ID, profile, credential)
	assertRuntimeModelCallSlot(t, confirmation, enums.ModelUsageSlotIntentDetectLLM, "intent_detect_llm-model", tenant.ID, store.ID, profile, credential)
	assertRuntimeModelCallSlot(t, summary, enums.ModelUsageSlotReplyLLM, "reply_llm-model", tenant.ID, store.ID, profile, credential)

	now := time.Now()
	receipt := &usagex.Receipt{
		Gateway: usagex.GatewayNewAPI, RequestID: "newapi-media-vision", UpstreamRequestID: "upstream-media-vision",
		StartedAt: now.Add(-25 * time.Millisecond), FinishedAt: now, StatusCode: http.StatusOK,
	}
	MediaUnderstandingService.recordMediaModelUsage(message, vision, "vision", &upstreamModelUsage{
		RequestID: "provider-media-vision", PromptTokens: 9, CompletionTokens: 3,
	}, receipt, 25, nil)
	MediaUnderstandingService.recordMediaModelUsage(message, asr, "asr", nil, nil, 30, errors.New("upstream unavailable"))

	classified := classifyHumanHandoffConfirmationWithModel(context.Background(), conversation, message, handoffConfirmationPayload{Reason: "客户要求人工"}, "好的，帮我转人工")
	if classified.Decision != humanHandoffConfirmationConfirm || classified.Source != "model" {
		t.Fatalf("unexpected handoff confirmation result: %#v", classified)
	}
	handoffSummary := newConversationHumanDispatchService().buildAIHandoffConversationSummary(conversation, "空调不制冷", []handoffSummaryItem{{Speaker: "客人", Text: "房间空调不制冷"}})
	if !strings.Contains(handoffSummary, "空调无法制冷") {
		t.Fatalf("unexpected handoff summary: %q", handoffSummary)
	}
	if callCount.Load() != 2 {
		t.Fatalf("auxiliary model calls=%d want 2", callCount.Load())
	}

	wantEvents := map[string]enums.ModelUsageSlot{
		"media_vision":     enums.ModelUsageSlotVision,
		"media_asr":        enums.ModelUsageSlotASR,
		"handoff_classify": enums.ModelUsageSlotIntentDetectLLM,
		"handoff_summary":  enums.ModelUsageSlotReplyLLM,
	}
	for stage, usageSlot := range wantEvents {
		var event models.AIUsageEvent
		if err := db.Where("stage = ?", stage).Take(&event).Error; err != nil {
			t.Fatalf("load %s usage: %v", stage, err)
		}
		if event.TenantID != tenant.ID || event.StoreID != store.ID || event.ModelProfileID != profile.ID || event.ModelProfileRevision != profile.Revision {
			t.Fatalf("%s profile attribution mismatch: %#v", stage, event)
		}
		if event.UsageSlot != string(usageSlot) || event.CredentialRevision != credential.CredentialRevision || event.KeyFingerprint != credential.KeyFingerprint {
			t.Fatalf("%s credential attribution mismatch: %#v", stage, event)
		}
		if event.ModelSource != AIModelSourceStoreProfile {
			t.Fatalf("%s used an unexpected model source: %#v", stage, event)
		}
	}
	var failedASR models.AIUsageEvent
	if err := db.Where("stage = ?", "media_asr").Take(&failedASR).Error; err != nil {
		t.Fatal(err)
	}
	if failedASR.Status != "failed" || failedASR.ErrorClass != "model_call_failed" || failedASR.ErrorMessage != "" {
		t.Fatalf("ASR failure leaked provider details or lost stable class: %#v", failedASR)
	}

	var gatewayCalls []models.AIUsageGatewayCall
	if err := db.Order("id ASC").Find(&gatewayCalls).Error; err != nil {
		t.Fatal(err)
	}
	if len(gatewayCalls) != 3 {
		t.Fatalf("gateway call evidence=%d want 3: %#v", len(gatewayCalls), gatewayCalls)
	}
	for _, item := range gatewayCalls {
		if item.TenantID != tenant.ID || item.StoreID != store.ID || item.ModelProfileID != profile.ID || item.CredentialRevision != credential.CredentialRevision {
			t.Fatalf("gateway call attribution mismatch: %#v", item)
		}
	}
}

func assertRuntimeModelCallSlot(t *testing.T, resolved *ModelCallConfig, usageSlot enums.ModelUsageSlot, modelName string, tenantID, storeID int64, profile *models.ModelProfileTemplate, credential *models.StoreModelCredential) {
	t.Helper()
	if resolved == nil {
		t.Fatal("resolved model call is nil")
	}
	if resolved.UsageCode != usageSlot || resolved.ModelName != modelName || resolved.TenantID != tenantID || resolved.StoreID != storeID {
		t.Fatalf("unexpected %s slot: %#v", usageSlot, resolved)
	}
	if resolved.ProfileID != profile.ID || resolved.ProfileRevision != profile.Revision || resolved.CredentialRevision != credential.CredentialRevision || resolved.KeyFingerprint != credential.KeyFingerprint {
		t.Fatalf("unexpected %s immutable attribution: %#v", usageSlot, resolved)
	}
}

func seedRuntimeModelAttributionFixture(t *testing.T, db *gorm.DB, gatewayURL string) (*models.Tenant, *models.Store, *models.ModelProfileTemplate, *models.StoreModelCredential, *models.Conversation, *models.Message) {
	t.Helper()
	tenant, store := createModelProfileTenantAndStore(t, db)
	profile := createPersistedModelProfileForTest(t, db, "runtime-attribution", 7, enums.ModelProfileStatusActive)
	if err := db.Model(profile).Update("gateway_base_url", gatewayURL).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	assignment := &models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: profile.ID, TemplateRevision: profile.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}

	const apiKey = "runtime-attribution-secret"
	const credentialRevision int64 = 9
	cipher, err := securex.NewAESGCM(config.Current().StoreCredential.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt(apiKey, storeCredentialAAD(tenant.ID, store.ID, credentialRevision))
	if err != nil {
		t.Fatal(err)
	}
	credential := &models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID, EncryptedKey: ciphertext, KeyNonce: nonce,
		KeyFingerprint: securex.Fingerprint(apiKey), CipherVersion: securex.AESGCMCipherVersion,
		MasterKeyID: config.Current().StoreCredential.MasterKeyID, CredentialRevision: credentialRevision,
		Status:      enums.StoreCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}

	conversation := &models.Conversation{
		TenantID: tenant.ID, Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst,
		LastMessageAt: now, LastActiveAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: tenant.ID, ConversationID: conversation.ID, StoreID: store.ID,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID: tenant.ID, ConversationID: conversation.ID, SessionNo: 1, RequestID: "runtime-attribution-request",
		ClientMsgID: "runtime-attribution-message", SenderType: enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText, Content: "帮我转人工", SeqNo: 1, SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	conversation.LastMessageID = message.ID
	if err := db.Model(conversation).Update("last_message_id", message.ID).Error; err != nil {
		t.Fatal(err)
	}
	return tenant, store, profile, credential, conversation, message
}
