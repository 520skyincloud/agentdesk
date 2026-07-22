package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestCustomerTagModelUsesCurrentStoreCredentialAndUnifiedUsageRecorder(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	const storeID int64 = 71
	const storeKey = "sk-store-current"
	var requestCount atomic.Int64
	var authorization string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected model path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(usagex.NewAPIRequestIDHeader, "new-api-tag-request-1")
		_, _ = fmt.Fprint(w, `{
			"id":"chatcmpl-tag-1",
			"object":"chat.completion",
			"created":1,
			"model":"tag-model",
			"choices":[{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"{\"schemaVersion\":\"customer_tag_evolution.v1\",\"operations\":[{\"op\":\"add\",\"tagId\":7,\"replaces\":[],\"confidence\":0.96,\"persistence\":\"long_term\",\"evidenceMessageIds\":[101],\"reasonCode\":\"explicit_preference\"}]}"
				},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}
		}`)
	}))
	defer server.Close()

	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })

	now := time.Now()
	if err := db.Create(&models.ModelProfileTemplate{
		ID: 1, Name: "平台模型模板", Revision: 8, GatewayBaseURL: server.URL + "/v1",
		CustomerTagEvolutionEnabled: true, Status: "active",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ModelProfileSlot{
		TemplateID: 1, UsageCode: ModelProfileUsageCustomerTag, DisplayName: "客户标签模型",
		ModelType: enums.AIModelTypeLLM, Provider: "openai", ModelName: "tag-model",
		APIMode: "chat_completions", TimeoutMS: 5000, Enabled: true,
		SchemaVersion: "customer_tag_evolution.v1",
		AuditFields:   models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	encryptedKey, nonce, err := cipher.Encrypt(storeKey, credentialAAD(storeID, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		CompanyID: 11, StoreID: storeID, EncryptedKey: encryptedKey, KeyNonce: nonce,
		KeyFingerprint: securex.Fingerprint(storeKey), CredentialRevision: 4,
		Status:      storeModelCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := ModelProfileTemplateService.ResolveSlot(storeID, ModelProfileUsageCustomerTag)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.APIKey != storeKey || resolved.CredentialRevision != 4 {
		t.Fatalf("tag slot did not resolve the existing store credential: %#v", resolved)
	}

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	records := make([]ai.ModelUsageRecord, 0, 1)
	scopes := make([]usagex.Scope, 0, 1)
	ai.RecordModelUsageForContext = func(ctx context.Context, record ai.ModelUsageRecord) {
		records = append(records, record)
		scopes = append(scopes, usagex.ScopeFromContext(ctx))
	}

	run := &models.ConversationEvolutionRun{
		ID: 91, CompanyID: 11, StoreID: storeID, ConversationID: 81, EndMessageID: 101,
	}
	operations, err := newConversationEvolutionService().callTagModel(
		context.Background(),
		run,
		resolved,
		1,
		customerTagInput{
			SchemaVersion: "customer_tag_input.v1",
			AllowedTags:   []customerTagInputAllowed{{ID: 7, Name: "喜静", SemanticKey: "room.quiet"}},
			Messages:      []customerTagInputMessage{{ID: 101, Role: "customer", Content: "我每次都喜欢安静一点的房间"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].TagID != 7 {
		t.Fatalf("operations=%#v", operations)
	}
	if requestCount.Load() != 1 || authorization != "Bearer "+storeKey {
		t.Fatalf("requestCount=%d authorization=%q", requestCount.Load(), authorization)
	}
	if len(records) != 1 || len(scopes) != 1 {
		t.Fatalf("expected one unified usage record, records=%d scopes=%d", len(records), len(scopes))
	}
	if records[0].Stage != "customer_tag_evolution" ||
		records[0].OperationType != "customer_tag_extract" ||
		records[0].ExternalEventKey != "tag-evolution:91:chunk:1:attempt:1" {
		t.Fatalf("usage record=%#v", records[0])
	}
	if scopes[0].StoreID != storeID ||
		scopes[0].CredentialRevision != 4 ||
		scopes[0].ModelSource != "store_credential_tag_template" {
		t.Fatalf("usage scope=%#v", scopes[0])
	}
}

func TestCustomerTagModelRepairsInvalidSchemaAndRecordsEachCall(t *testing.T) {
	var requestCount atomic.Int64
	var secondRequestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := requestCount.Add(1)
		rawBody, _ := io.ReadAll(r.Body)
		if current == 2 {
			secondRequestBody = string(rawBody)
		}
		content := `{"operations":[{"action":"add","id":7}]}`
		if current == 2 {
			content = `{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":7,"replaces":[],"confidence":0.96,"persistence":"long_term","evidenceMessageIds":[101],"reasonCode":"explicit_preference"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(usagex.NewAPIRequestIDHeader, fmt.Sprintf("new-api-tag-repair-%d", current))
		_, _ = fmt.Fprintf(w, `{
			"id":"chatcmpl-tag-repair-%d",
			"object":"chat.completion",
			"created":1,
			"model":"tag-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}
		}`, current, content)
	}))
	defer server.Close()

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	records := make([]ai.ModelUsageRecord, 0, 2)
	ai.RecordModelUsageForContext = func(_ context.Context, record ai.ModelUsageRecord) {
		records = append(records, record)
	}

	run := &models.ConversationEvolutionRun{
		ID: 92, CompanyID: 11, StoreID: 71, ConversationID: 82, EndMessageID: 101,
	}
	resolved := &ResolvedModelProfileSlot{
		Template: models.ModelProfileTemplate{Revision: 8},
		Slot: models.ModelProfileSlot{
			UsageCode: ModelProfileUsageCustomerTag, MaxRetryCount: 0,
		},
		Config: models.AIConfig{
			Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "sk-store-current",
			ModelType: enums.AIModelTypeLLM, ModelName: "tag-model", APIMode: "chat_completions",
			TimeoutMS: 5000, MaxRetryCount: 0,
		},
		CredentialRevision: 4,
	}
	operations, err := newConversationEvolutionService().callTagModel(
		context.Background(),
		run,
		resolved,
		1,
		customerTagInput{
			SchemaVersion: "customer_tag_input.v1",
			AllowedTags:   []customerTagInputAllowed{{ID: 7, Name: "喜静", SemanticKey: "room.quiet"}},
			Messages:      []customerTagInputMessage{{ID: 101, Role: "customer", Content: "我每次都喜欢安静一点的房间"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].TagID != 7 {
		t.Fatalf("operations=%#v", operations)
	}
	if requestCount.Load() != 2 {
		t.Fatalf("requestCount=%d", requestCount.Load())
	}
	if !strings.Contains(secondRequestBody, "上一次输出未通过严格 Schema 校验") {
		t.Fatalf("second request did not contain repair instructions: %s", secondRequestBody)
	}
	if len(records) != 2 {
		t.Fatalf("expected two usage records, got %d", len(records))
	}
	if records[0].Status != "invalid_schema" ||
		records[0].ExternalEventKey != "tag-evolution:92:chunk:1:attempt:1" {
		t.Fatalf("first usage record=%#v", records[0])
	}
	if records[1].Status != "completed" ||
		records[1].ExternalEventKey != "tag-evolution:92:chunk:1:attempt:2" {
		t.Fatalf("second usage record=%#v", records[1])
	}
}

func TestConversationEvolutionObservationResetsInactivityWithoutModelCall(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	store := &models.Store{
		StoreCode: "evolution-store", Name: "测试门店", CompanyID: 21, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		CustomerID: 31, Status: enums.IMConversationStatusAIServing,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID: conversation.ID, StoreID: store.ID, SessionNo: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	modelCallCount := 0
	ai.RecordModelUsageForContext = func(context.Context, ai.ModelUsageRecord) {
		modelCallCount++
	}

	firstAt := now.Add(-2 * time.Hour)
	ConversationEvolutionService.ObserveCommittedMessage(conversation, &models.Message{
		ID: 10, SessionNo: 1, SentAt: &firstAt,
	})
	state := repositories.ConversationEvolutionStateRepository.GetByConversationSession(db, conversation.ID, 1)
	if state == nil || state.NextEvolutionAt == nil {
		t.Fatalf("state=%#v", state)
	}
	if state.LastObservedMessageID != 10 || !state.NextEvolutionAt.Equal(firstAt.Add(24*time.Hour)) {
		t.Fatalf("first observation state=%#v", state)
	}

	secondAt := now
	ConversationEvolutionService.ObserveCommittedMessage(conversation, &models.Message{
		ID: 11, SessionNo: 1, SentAt: &secondAt,
	})
	state = repositories.ConversationEvolutionStateRepository.GetByConversationSession(db, conversation.ID, 1)
	if state == nil || state.NextEvolutionAt == nil {
		t.Fatalf("state=%#v", state)
	}
	if state.LastObservedMessageID != 11 || !state.NextEvolutionAt.Equal(secondAt.Add(24*time.Hour)) {
		t.Fatalf("second observation state=%#v", state)
	}
	if modelCallCount != 0 {
		t.Fatalf("observation must not call or bill a model, got %d calls", modelCallCount)
	}
}

func TestConversationEvolutionSkipsWithoutIncrementalMessages(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	if err := db.Create(&models.ModelProfileTemplate{
		ID: 1, Name: "平台模型模板", Revision: 1, GatewayBaseURL: "https://gateway.invalid/v1",
		CustomerTagEvolutionEnabled: true, Status: "active",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Hour)
	if err := db.Create(&models.ConversationEvolutionState{
		ConversationID: 1, SessionNo: 1, LastObservedMessageID: 9, LastEvolvedMessageID: 9,
		NextEvolutionAt: &past, LastStatus: conversationEvolutionStatusWaiting, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	modelCallCount := 0
	ai.RecordModelUsageForContext = func(context.Context, ai.ModelUsageRecord) {
		modelCallCount++
	}
	if processed := ConversationEvolutionService.ProcessDue(20); processed != 0 {
		t.Fatalf("processed=%d", processed)
	}
	if modelCallCount != 0 {
		t.Fatalf("no incremental messages must produce zero model usage, got %d", modelCallCount)
	}
}

func TestConversationEvolutionEmptyStoreAllowlistSkipsDueStateWithoutModelCall(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	if err := db.Create(&models.ModelProfileTemplate{
		ID: 1, Name: "平台模型模板", Revision: 1, GatewayBaseURL: "https://gateway.invalid/v1",
		CustomerTagEvolutionEnabled: true, CustomerTagEvolutionStoreIDs: "[]", Status: "active",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Hour)
	state := &models.ConversationEvolutionState{
		ConversationID: 1, SessionNo: 1, StoreID: 71,
		LastObservedMessageID: 10, LastEvolvedMessageID: 0,
		NextEvolutionAt: &past, LastStatus: conversationEvolutionStatusWaiting, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(state).Error; err != nil {
		t.Fatal(err)
	}

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	modelCallCount := 0
	ai.RecordModelUsageForContext = func(context.Context, ai.ModelUsageRecord) {
		modelCallCount++
	}

	if processed := ConversationEvolutionService.ProcessDue(20); processed != 0 {
		t.Fatalf("processed=%d", processed)
	}
	current := repositories.ConversationEvolutionStateRepository.GetByConversationSession(db, 1, 1)
	if current == nil || current.LastStatus != conversationEvolutionStatusWaiting {
		t.Fatalf("empty allowlist must leave due state untouched: %#v", current)
	}
	if modelCallCount != 0 {
		t.Fatalf("empty allowlist must produce zero model usage, got %d", modelCallCount)
	}
}

func TestConversationEvolutionProcessesOnlyAllowlistedStore(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	if err := db.Create(&models.ModelProfileTemplate{
		ID: 1, Name: "平台模型模板", Revision: 1, GatewayBaseURL: "https://gateway.invalid/v1",
		CustomerTagEvolutionEnabled: true, CustomerTagEvolutionStoreIDs: "[71]", Status: "active",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Hour)
	for _, item := range []models.ConversationEvolutionState{
		{
			ConversationID: 1, SessionNo: 1, StoreID: 71,
			LastObservedMessageID: 10, NextEvolutionAt: &past,
			LastStatus: conversationEvolutionStatusWaiting, Status: enums.StatusOk,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		},
		{
			ConversationID: 2, SessionNo: 1, StoreID: 72,
			LastObservedMessageID: 20, NextEvolutionAt: &past,
			LastStatus: conversationEvolutionStatusWaiting, Status: enums.StatusOk,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		},
	} {
		state := item
		if err := db.Create(&state).Error; err != nil {
			t.Fatal(err)
		}
	}

	previousRecorder := ai.RecordModelUsageForContext
	t.Cleanup(func() { ai.RecordModelUsageForContext = previousRecorder })
	modelCallCount := 0
	ai.RecordModelUsageForContext = func(context.Context, ai.ModelUsageRecord) {
		modelCallCount++
	}

	if processed := ConversationEvolutionService.ProcessDue(20); processed != 1 {
		t.Fatalf("processed=%d", processed)
	}
	selected := repositories.ConversationEvolutionStateRepository.GetByConversationSession(db, 1, 1)
	if selected == nil || selected.LastStatus != conversationEvolutionStatusFailed {
		t.Fatalf("selected store state was not processed: %#v", selected)
	}
	notSelected := repositories.ConversationEvolutionStateRepository.GetByConversationSession(db, 2, 1)
	if notSelected == nil || notSelected.LastStatus != conversationEvolutionStatusWaiting {
		t.Fatalf("non-allowlisted store state changed: %#v", notSelected)
	}
	if modelCallCount != 0 {
		t.Fatalf("missing conversation path must not call a model, got %d", modelCallCount)
	}
}

func TestModelProfileTemplateValidationRequiresEvolutionStore(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	store := &models.Store{
		StoreCode: "tag-shadow-store", Name: "标签影子门店", CompanyID: 21, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	req := request.UpdateModelProfileTemplateRequest{
		Name: "平台模型模板", GatewayBaseURL: "https://gateway.example.com/v1",
		CustomerTagEvolutionEnabled: true,
		Slots: []request.ModelProfileSlotRequest{{
			UsageCode: ModelProfileUsageCustomerTag, DisplayName: "客户标签模型",
			ModelType: "llm", Provider: "openai", ModelName: "tag-model",
			APIMode: "chat_completions", SchemaVersion: "customer_tag_evolution.v1",
			JSONSchema: `{}`, Enabled: true,
		}},
	}
	normalizeModelProfileTemplateRequest(&req)
	if err := validateModelProfileTemplateRequest(req); err == nil {
		t.Fatal("enabled evolution without a store allowlist must be rejected")
	}
	req.CustomerTagEvolutionStoreIDs = []int64{store.ID}
	if err := validateModelProfileTemplateRequest(req); err != nil {
		t.Fatalf("valid store allowlist rejected: %v", err)
	}
}

func TestCustomerTagOutputValidationIsStrictAndAppliesConfidenceThresholds(t *testing.T) {
	input := customerTagInput{
		AllowedTags: []customerTagInputAllowed{
			{ID: 1, Name: "喜静", SemanticKey: "room.quiet"},
			{ID: 2, Name: "高楼层", SemanticKey: "room.high_floor"},
		},
		Messages: []customerTagInputMessage{{ID: 10, Role: "customer", Content: "我喜欢安静"}},
	}

	operations, err := validateCustomerTagModelOutput(`{
		"schemaVersion":"customer_tag_evolution.v1",
		"operations":[
			{"op":"add","tagId":1,"replaces":[],"confidence":0.91,"persistence":"long_term","evidenceMessageIds":[10],"reasonCode":"explicit_preference"},
			{"op":"refresh","tagId":2,"replaces":[],"confidence":0.85,"persistence":"long_term","evidenceMessageIds":[10],"reasonCode":"repeated_preference"}
		]
	}`, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Op != "refresh" || operations[0].TagID != 2 {
		t.Fatalf("threshold filtering operations=%#v", operations)
	}

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "unknown field",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":[],"explanation":"no"}`,
		},
		{
			name:    "unknown tag",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":99,"replaces":[],"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[10],"reasonCode":"explicit_preference"}]}`,
		},
		{
			name:    "non customer evidence",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":1,"replaces":[],"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[11],"reasonCode":"explicit_preference"}]}`,
		},
		{
			name:    "null replaces",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":1,"replaces":null,"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[10],"reasonCode":"explicit_preference"}]}`,
		},
		{
			name:    "missing required field",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":1,"replaces":[],"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[10]}]}`,
		},
		{
			name:    "null operations",
			content: `{"schemaVersion":"customer_tag_evolution.v1","operations":null}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateCustomerTagModelOutput(test.content, input); err == nil {
				t.Fatalf("expected validation failure for %s", test.name)
			}
		})
	}

	fenced, err := validateCustomerTagModelOutput("```json\n"+
		`{"schemaVersion":"customer_tag_evolution.v1","operations":[]}`+
		"\n```", input)
	if err != nil {
		t.Fatalf("pure JSON code fence should be accepted: %v", err)
	}
	if len(fenced) != 0 {
		t.Fatalf("unexpected fenced operations: %#v", fenced)
	}
	if _, err := validateCustomerTagModelOutput(
		"提取结果如下：\n```json\n"+
			`{"schemaVersion":"customer_tag_evolution.v1","operations":[]}`+
			"\n```", input,
	); err == nil {
		t.Fatal("explanatory text outside the JSON fence must be rejected")
	}
}

func TestConversationEvolutionInputBudgetUsesTokenEstimate(t *testing.T) {
	if exceedsConversationEvolutionInputBudget(strings.Repeat("中", 6500)) {
		t.Fatal("6500 Chinese characters plus prompt reserve should remain within the 4000-token budget")
	}
	if !exceedsConversationEvolutionInputBudget(strings.Repeat("中", 7000)) {
		t.Fatal("7000 Chinese characters plus prompt reserve should exceed the 4000-token budget")
	}
}

func TestAllowedCustomerTagsExcludeCategoryNodes(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	parent := &models.Tag{
		Name: "房间偏好", SemanticKey: "category.hotel", AIEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	child := &models.Tag{
		ParentID: parent.ID, Name: "喜静", SemanticKey: "room.quiet",
		AIEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(child).Error; err != nil {
		t.Fatal(err)
	}

	allowed := CustomerTagService.ListAllowedTags(0)
	if len(allowed) != 1 || allowed[0].ID != child.ID {
		t.Fatalf("allowed tags=%#v", allowed)
	}
}

func TestCustomerTagAIRespectsManualProtection(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	oldTag := &models.Tag{
		Name: "喜静", SemanticKey: "room.quiet", ConflictGroup: "room.noise",
		AIEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	newTag := &models.Tag{
		Name: "喜热闹", SemanticKey: "room.lively", ConflictGroup: "room.noise",
		AIEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(oldTag).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(newTag).Error; err != nil {
		t.Fatal(err)
	}
	relation := &models.StoreCustomerRelation{
		CustomerID: 31, StoreID: 41, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatal(err)
	}
	protected := &models.CustomerTagRelation{
		CompanyID: 21, StoreID: 41, CustomerID: 31,
		StoreCustomerRelationID: relation.ID, TagID: oldTag.ID,
		Source: customerTagSourceManual, RelationStatus: customerTagRelationActive,
		Confidence: 1, EvidenceCount: 1, ManualProtected: true,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(protected).Error; err != nil {
		t.Fatal(err)
	}
	scope := &customerTagScope{
		Conversation: &models.Conversation{ID: 51, CustomerID: 31},
		Relation:     relation, CompanyID: 21, StoreID: 41,
	}

	changed, err := newCustomerTagService().applyAIOperation(db, scope, 61, CustomerTagOperation{
		Op: "replace", TagID: newTag.ID, Replaces: []int64{oldTag.ID},
		Confidence: 0.99, EvidenceMessageIDs: []int64{71},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("AI must not replace a manually protected tag")
	}
	current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, relation.ID, oldTag.ID)
	if current == nil || current.RelationStatus != customerTagRelationActive || !current.ManualProtected {
		t.Fatalf("protected relation changed: %#v", current)
	}
	if next := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, relation.ID, newTag.ID); next != nil {
		t.Fatalf("replacement tag should not be created: %#v", next)
	}
}

func setupConversationEvolutionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Store{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.StoreCustomerRelation{},
		&models.Message{},
		&models.ConversationSessionSummary{},
		&models.ConversationEvolutionState{},
		&models.ConversationEvolutionRun{},
		&models.Tag{},
		&models.CustomerTagRelation{},
		&models.CustomerTagChangeLog{},
		&models.ModelProfileTemplate{},
		&models.ModelProfileSlot{},
		&models.StoreModelCredential{},
		&models.FastGPTProfileTemplate{},
	); err != nil {
		t.Fatal(err)
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
