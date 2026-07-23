package services

import (
	"encoding/base64"
	"encoding/json"
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
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type conversationEvolutionFixture struct {
	db           *gorm.DB
	tenant       *models.Tenant
	profile      *models.ReplyIntentProfile
	store        *models.Store
	customer     *models.Customer
	conversation *models.Conversation
	route        *models.ConversationRouteState
	relation     *models.StoreCustomerRelation
	tag          *models.Tag
	policy       *models.TenantCustomerTagPolicy
	storePolicy  *models.StoreCustomerTagRuntimePolicy
}

func TestConversationEvolutionObservationIsMonotonicAndPolicyDriven(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	fixture.policy.QuietPeriodMinutes = 90
	if err := fixture.db.Model(fixture.policy).Update("quiet_period_minutes", 90).Error; err != nil {
		t.Fatal(err)
	}
	olderAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	newerAt := olderAt.Add(time.Hour)
	older := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我喜欢安静", olderAt)
	newer := createEvolutionMessage(t, fixture, enums.IMSenderTypeAI, "已经记录", newerAt)

	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, newer)
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, older)

	state, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.LastObservedMessageID != newer.ID {
		t.Fatalf("out-of-order observation moved cursor backwards: %#v", state)
	}
	wantNext := newerAt.Add(90 * time.Minute)
	if state.NextEvolutionAt == nil || !state.NextEvolutionAt.Equal(wantNext) {
		t.Fatalf("next evolution=%v want %v", state.NextEvolutionAt, wantNext)
	}
	if state.TenantID != fixture.tenant.ID || state.StoreID != fixture.store.ID || state.StoreCustomerRelationID != fixture.relation.ID {
		t.Fatalf("observation lost Tenant/Store scope: %#v", state)
	}
	var runCount int64
	if err := fixture.db.Model(&models.ConversationEvolutionRun{}).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if runCount != 0 {
		t.Fatalf("observation must not create model runs, got %d", runCount)
	}
}

func TestConversationEvolutionDueAndLeaseAreStoreGatedAndCASProtected(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	message := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都要安静房间", time.Now().Add(-2*time.Hour))
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, message)

	due, err := repositories.ConversationEvolutionStateRepository.FindDue(fixture.db, time.Now(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due states=%d want 1", len(due))
	}
	now := time.Now()
	first, err := repositories.ConversationEvolutionStateRepository.Claim(fixture.db, due[0].ID, fixture.tenant.ID, "worker-a", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repositories.ConversationEvolutionStateRepository.Claim(fixture.db, due[0].ID, fixture.tenant.ID, "worker-b", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("lease CAS first=%v second=%v", first, second)
	}

	if err := fixture.db.Model(fixture.storePolicy).Update("customer_tag_evolution_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.ConversationEvolutionStateRepository.UpdatesInTenant(fixture.db, due[0].ID, fixture.tenant.ID, map[string]any{
		"lease_owner": "", "lease_expires_at": nil,
	}); err != nil {
		t.Fatal(err)
	}
	due, err = repositories.ConversationEvolutionStateRepository.FindDue(fixture.db, time.Now(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("disabled Store must not produce due states: %#v", due)
	}
}

func TestConversationEvolutionFinalizeSupersedesOnNewCommittedMessage(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	oldMessage := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都喜欢安静房间", time.Now().Add(-2*time.Hour))
	state, owner, policy, run := claimEvolutionRun(t, fixture, oldMessage)

	newMessageAt := time.Now().UTC().Truncate(time.Second)
	newMessage := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "这次想住热闹一点", newMessageAt)
	result, err := ConversationEvolutionService.finalizeRun(
		state, owner, run, policy, "completed", "completed", "completed", true,
		[]CustomerTagOperation{{
			Op: "add", TagID: fixture.tag.ID, Confidence: 0.99,
			EvidenceMessageIDs: []int64{oldMessage.ID},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.superseded || result.changed {
		t.Fatalf("new-message race result=%#v", result)
	}
	assertNoEvolutionTagMutation(t, fixture)

	currentRun := &models.ConversationEvolutionRun{}
	if err := fixture.db.First(currentRun, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentRun.RunStatus != conversationEvolutionStatusSuperseded || currentRun.TagStatus != conversationEvolutionStatusSuperseded {
		t.Fatalf("run was not superseded: %#v", currentRun)
	}
	currentState, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if currentState.LastObservedMessageID != newMessage.ID || currentState.LastEvolvedMessageID != 0 || currentState.LeaseOwner != "" {
		t.Fatalf("superseded state=%#v", currentState)
	}
	wantNext := newMessageAt.Add(policy.QuietPeriod)
	if currentState.NextEvolutionAt == nil || !currentState.NextEvolutionAt.Equal(wantNext) {
		t.Fatalf("superseded deadline=%v want %v", currentState.NextEvolutionAt, wantNext)
	}
}

func TestConversationEvolutionRescheduleCannotOverwriteNewerObservation(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	oldMessage := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我喜欢安静", time.Now().Add(-3*time.Hour))
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, oldMessage)
	state, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil || state == nil {
		t.Fatalf("load state: %#v err=%v", state, err)
	}
	owner := "worker-reschedule-race"
	now := time.Now()
	claimed, err := repositories.ConversationEvolutionStateRepository.Claim(
		fixture.db, state.ID, state.TenantID, owner, now, now.Add(time.Minute),
	)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}

	intermediate := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "中间消息", time.Now().Add(-2*time.Hour))
	newestAt := time.Now().UTC().Truncate(time.Second)
	newest := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "最新消息", newestAt)
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, newest)

	ConversationEvolutionService.rescheduleClaim(state, owner, intermediate, time.Minute)
	current, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.LastObservedMessageID != newest.ID || current.LeaseOwner != "" || current.AttemptCount != 0 {
		t.Fatalf("newer observation was overwritten: %#v", current)
	}
	wantNext := newestAt.Add(time.Minute)
	if current.NextEvolutionAt == nil || !current.NextEvolutionAt.Equal(wantNext) {
		t.Fatalf("newer deadline=%v want %v", current.NextEvolutionAt, wantNext)
	}
}

func TestConversationEvolutionFailureCannotOverrideNewerObservation(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	oldMessage := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都喜欢安静房间", time.Now().Add(-2*time.Hour))
	state, owner, _, run := claimEvolutionRun(t, fixture, oldMessage)
	newestAt := time.Now().UTC().Truncate(time.Second)
	newest := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "这次需求变了", newestAt)
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, newest)

	ConversationEvolutionService.failClaim(state, owner, run, "customer_tag_model_failed", false, false)
	current, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.LastObservedMessageID != newest.ID || current.AttemptCount != 0 ||
		current.LastStatus != conversationEvolutionStatusWaiting || current.LeaseOwner != "" {
		t.Fatalf("failure overwrote newer observation: %#v", current)
	}
	wantNext := newestAt.Add(time.Minute)
	if current.NextEvolutionAt == nil || !current.NextEvolutionAt.Equal(wantNext) || current.NextRetryAt != nil {
		t.Fatalf("newer scheduling was overwritten: %#v", current)
	}
	currentRun := &models.ConversationEvolutionRun{}
	if err := fixture.db.First(currentRun, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentRun.RunStatus != conversationEvolutionStatusSuperseded || currentRun.TagStatus != conversationEvolutionStatusSuperseded {
		t.Fatalf("stale run was not superseded: %#v", currentRun)
	}
}

func TestConversationEvolutionFinalizeAppliesTagAndStateAtomically(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	message := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都喜欢安静房间", time.Now().Add(-2*time.Hour))
	state, owner, policy, run := claimEvolutionRun(t, fixture, message)

	result, err := ConversationEvolutionService.finalizeRun(
		state, owner, run, policy, "completed", "completed", "completed", true,
		[]CustomerTagOperation{{
			Op: "add", TagID: fixture.tag.ID, Confidence: 0.99,
			EvidenceMessageIDs: []int64{message.ID},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.changed || result.superseded {
		t.Fatalf("finalize result=%#v", result)
	}
	relation, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTag(
		fixture.db, fixture.tenant.ID, fixture.store.ID, fixture.relation.ID, fixture.tag.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if relation == nil || relation.RelationStatus != customerTagRelationActive || relation.LastEvolutionRunID != run.ID {
		t.Fatalf("tag relation=%#v", relation)
	}
	var changeLog models.CustomerTagChangeLog
	if err := fixture.db.Where("evolution_run_id = ?", run.ID).Take(&changeLog).Error; err != nil {
		t.Fatal(err)
	}
	if changeLog.TenantID != fixture.tenant.ID || changeLog.StoreID != fixture.store.ID || changeLog.EvidenceMessageIDs != fmt.Sprintf("[%d]", message.ID) {
		t.Fatalf("change log=%#v", changeLog)
	}
	currentState, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if currentState.LastEvolvedMessageID != message.ID || currentState.LastEvolutionRunID != run.ID ||
		currentState.LastStatus != conversationEvolutionStatusCompleted || currentState.LeaseOwner != "" || currentState.SummaryVersion != 1 {
		t.Fatalf("completed state=%#v", currentState)
	}
	currentRun := &models.ConversationEvolutionRun{}
	if err := fixture.db.First(currentRun, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentRun.RunStatus != conversationEvolutionStatusCompleted || currentRun.TagStatus != "completed" ||
		strings.Contains(currentRun.RedactedResult, "安静") {
		t.Fatalf("completed run=%#v", currentRun)
	}
}

func TestConversationEvolutionStopsAutomaticRetryAfterFifthFailure(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	message := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我喜欢安静", time.Now().Add(-2*time.Hour))
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, message)
	state, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.ConversationEvolutionStateRepository.UpdatesInTenant(fixture.db, state.ID, state.TenantID, map[string]any{
		"attempt_count": 4, "next_evolution_at": time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	claimed, err := repositories.ConversationEvolutionStateRepository.Claim(fixture.db, state.ID, state.TenantID, "worker-final-failure", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	state.AttemptCount = 4
	ConversationEvolutionService.failClaim(state, "worker-final-failure", nil, "customer_tag_model_failed", false, false)
	current, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.AttemptCount != 5 || current.NextRetryAt != nil || current.NextEvolutionAt != nil ||
		current.LastStatus != conversationEvolutionStatusFailed || current.LeaseOwner != "" {
		t.Fatalf("terminal failure state=%#v", current)
	}
}

func TestCustomerTagEvolutionOutputValidationIsStrict(t *testing.T) {
	input := customerTagInput{
		AllowedTags: []customerTagInputAllowed{{ID: 7, Name: "喜静", SemanticKey: "room.quiet"}},
		Messages:    []customerTagInputMessage{{ID: 101, Role: "customer", Content: "我每次都喜欢安静房间"}},
	}
	policy := &conversationEvolutionPolicy{MinimumConfidence: 0.8, MaxOperationsPerRun: 2}
	operations, err := validateCustomerTagModelOutput(`{
		"schemaVersion":"customer_tag_evolution.v1",
		"operations":[
			{"op":"add","tagId":7,"replaces":[],"confidence":0.91,"persistence":"long_term","evidenceMessageIds":[101],"reasonCode":"explicit_preference"},
			{"op":"refresh","tagId":7,"replaces":[],"confidence":0.86,"persistence":"long_term","evidenceMessageIds":[101],"reasonCode":"repeated_preference"}
		]
	}`, input, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Op != "refresh" {
		t.Fatalf("threshold-filtered operations=%#v", operations)
	}
	invalid := []string{
		`{"schemaVersion":"customer_tag_evolution.v1","operations":[],"explanation":"no"}`,
		`{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":99,"replaces":[],"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[101],"reasonCode":"explicit_preference"}]}`,
		`{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":7,"replaces":[],"confidence":0.99,"persistence":"long_term","evidenceMessageIds":[999],"reasonCode":"explicit_preference"}]}`,
		`{"schemaVersion":"customer_tag_evolution.v1","operations":null}`,
	}
	for i := range invalid {
		if _, err := validateCustomerTagModelOutput(invalid[i], input, policy); err == nil {
			t.Fatalf("invalid output %d was accepted", i)
		}
	}
}

func TestCustomerTagEvolutionModelCallUsesResolvedAttribution(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	message := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都喜欢安静房间", time.Now())
	var requestCount atomic.Int64
	authorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		authorization = request.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(usagex.NewAPIRequestIDHeader, "newapi-tag-1")
		w.Header().Set(usagex.NewAPIUpstreamIDHeader, "upstream-tag-1")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-tag-1", "object": "chat.completion", "created": time.Now().Unix(), "model": "tag-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": fmt.Sprintf(
					`{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":%d,"replaces":[],"confidence":0.97,"persistence":"long_term","evidenceMessageIds":[%d],"reasonCode":"explicit_preference"}]}`,
					fixture.tag.ID, message.ID,
				)},
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
		})
	}))
	defer server.Close()

	resolved := &ModelCallConfig{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		ProfileID: 88, ProfileRevision: 9, UsageCode: enums.ModelUsageSlotCustomerTag,
		Provider: modelProfileProviderNewAPI, GatewayBaseURL: server.URL + "/v1",
		APIMode: "chat_completions", ModelType: enums.AIModelTypeLLM, ModelName: "tag-model",
		MaxContextTokens: 8192, MaxOutputTokens: 256, TimeoutMS: 5000,
		SchemaVersion: "customer_tag_evolution.v1", PromptTemplate: "Return only valid JSON.",
		CredentialRevision: 4, APIKey: "sk-store-tag", KeyFingerprint: "fingerprint-tag",
	}
	run := &models.ConversationEvolutionRun{
		ID: 91, TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		ConversationID: fixture.conversation.ID, EndMessageID: message.ID,
	}
	input := customerTagInput{
		SchemaVersion: "customer_tag_input.v1",
		AllowedTags:   []customerTagInputAllowed{{ID: fixture.tag.ID, Name: fixture.tag.Name, SemanticKey: fixture.tag.SemanticKey}},
		Messages:      []customerTagInputMessage{{ID: message.ID, Role: "customer", Content: message.Content}},
	}
	operations, err := ConversationEvolutionService.callTagModel(run, resolved, fixturePolicy(fixture), 1, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].TagID != fixture.tag.ID || requestCount.Load() != 1 || authorization != "Bearer sk-store-tag" {
		t.Fatalf("operations=%#v requests=%d authorization=%q", operations, requestCount.Load(), authorization)
	}
	var event models.AIUsageEvent
	if err := fixture.db.Where("stage = ?", "customer_tag_evolution").Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.TenantID != fixture.tenant.ID || event.StoreID != fixture.store.ID ||
		event.ModelProfileID != resolved.ProfileID || event.ModelProfileRevision != resolved.ProfileRevision ||
		event.UsageSlot != string(enums.ModelUsageSlotCustomerTag) || event.CredentialRevision != resolved.CredentialRevision ||
		event.KeyFingerprint != resolved.KeyFingerprint || event.AIConfigID != 0 || event.ModelSource != AIModelSourceStoreProfile {
		t.Fatalf("usage attribution=%#v", event)
	}
	if event.GatewayRequestID != "newapi-tag-1" || event.GatewayUpstreamID != "upstream-tag-1" || event.ErrorMessage != "" {
		t.Fatalf("gateway evidence=%#v", event)
	}
}

func TestConversationEvolutionProcessDueUsesStoreResolverAndCompletes(t *testing.T) {
	fixture := setupConversationEvolutionFixture(t)
	message := createEvolutionMessage(t, fixture, enums.IMSenderTypeCustomer, "我每次都喜欢安静房间", time.Now().Add(-2*time.Hour))
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		if request.Header.Get("Authorization") != "Bearer sk-evolution-store" {
			t.Errorf("unexpected authorization %q", request.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(usagex.NewAPIRequestIDHeader, "newapi-process-due-1")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-process-due", "object": "chat.completion", "created": time.Now().Unix(), "model": "tag-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": fmt.Sprintf(
					`{"schemaVersion":"customer_tag_evolution.v1","operations":[{"op":"add","tagId":%d,"replaces":[],"confidence":0.97,"persistence":"long_term","evidenceMessageIds":[%d],"reasonCode":"explicit_preference"}]}`,
					fixture.tag.ID, message.ID,
				)},
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
		})
	}))
	defer server.Close()
	seedEvolutionStoreModelRuntime(t, fixture, server.URL+"/v1", "sk-evolution-store")
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, message)

	if processed := ConversationEvolutionService.ProcessDue(20); processed != 1 {
		t.Fatalf("processed=%d want 1", processed)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("model requests=%d want 1", requestCount.Load())
	}
	state, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.LastStatus != conversationEvolutionStatusCompleted || state.LastEvolvedMessageID != message.ID || state.LeaseOwner != "" {
		t.Fatalf("processed state=%#v", state)
	}
	relation, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTag(
		fixture.db, fixture.tenant.ID, fixture.store.ID, fixture.relation.ID, fixture.tag.ID,
	)
	if err != nil || relation == nil || relation.LastEvolutionRunID <= 0 {
		t.Fatalf("tag relation=%#v err=%v", relation, err)
	}
	var run models.ConversationEvolutionRun
	if err := fixture.db.First(&run, state.LastEvolutionRunID).Error; err != nil {
		t.Fatal(err)
	}
	if run.ModelProfileID <= 0 || run.ModelProfileRevision != 3 || run.CredentialRevision != 2 ||
		run.SummaryStatus != "completed" || run.KnowledgeStatus != "completed" || run.TagStatus != "completed" {
		t.Fatalf("processed run=%#v", run)
	}
}

func setupConversationEvolutionFixture(t *testing.T) *conversationEvolutionFixture {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.ReplyIntentProfile{}, &models.Store{}, &models.Customer{},
		&models.Conversation{}, &models.ConversationRouteState{}, &models.StoreCustomerRelation{}, &models.Message{},
		&models.ConversationSessionSummary{}, &models.TenantCustomerTagPolicy{}, &models.StoreCustomerTagRuntimePolicy{},
		&models.Tag{}, &models.CustomerTagRelation{}, &models.CustomerTagChangeLog{},
		&models.ConversationEvolutionState{}, &models.ConversationEvolutionRun{},
		&models.ModelProfileTemplate{}, &models.ModelProfileSlot{}, &models.StoreModelProfileAssignment{},
		&models.StoreModelCredential{}, &models.AIUsageEvent{}, &models.AIUsageGatewayCall{},
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
	now := time.Now()
	profile := &models.ReplyIntentProfile{
		Code: "hotel-" + dbName, Name: "酒店", IndustryCode: "hotel", Revision: 1,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatal(err)
	}
	tenant := &models.Tenant{
		IntentProfileID: profile.ID, TenantCode: "tenant-" + dbName, LegalName: "丽斯未来测试公司", ShortName: "丽斯未来",
		RegistrationType: "credit_code", RegistrationNo: "REG-" + dbName, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatal(err)
	}
	store := &models.Store{
		TenantID: tenant.ID, StoreCode: "store-1", Name: "未来酒店一店", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	customer := &models.Customer{
		TenantID: tenant.ID, Name: "测试客户", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customer).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		TenantID: tenant.ID, CustomerID: customer.ID, CustomerName: customer.Name,
		Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst,
		LastMessageAt: now, LastActiveAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	route := &models.ConversationRouteState{
		TenantID: tenant.ID, ConversationID: conversation.ID, StoreID: store.ID, SessionNo: 1,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	relation := &models.StoreCustomerRelation{
		TenantID: tenant.ID, CustomerID: customer.ID, StoreID: store.ID,
		LastConversationID: conversation.ID, LastActiveAt: &now, VisitCount: 1, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatal(err)
	}
	policy := &models.TenantCustomerTagPolicy{
		TenantID: tenant.ID, IntentProfileID: profile.ID, QuietPeriodMinutes: 1,
		MinimumConfidence: 0.8, MaxOperationsPerRun: 6, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(policy).Error; err != nil {
		t.Fatal(err)
	}
	storePolicy := &models.StoreCustomerTagRuntimePolicy{
		TenantID: tenant.ID, StoreID: store.ID, CustomerTagEvolutionEnabled: true,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(storePolicy).Error; err != nil {
		t.Fatal(err)
	}
	parentDefinitionID := int64(1001)
	parent := &models.Tag{
		TenantID: tenant.ID, IntentProfileID: profile.ID, TemplateDefinitionID: &parentDefinitionID,
		Name: "房间偏好", SemanticKey: "category.room", SystemDefined: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	tagDefinitionID := int64(1002)
	tag := &models.Tag{
		TenantID: tenant.ID, IntentProfileID: profile.ID, TemplateDefinitionID: &tagDefinitionID,
		ParentID: parent.ID, Name: "喜静", SemanticKey: "room.quiet", ConflictGroup: "room.noise",
		ApplicableScene: "accommodation", AIEnabled: true, ReplyEnabled: true, SystemDefined: true,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tag).Error; err != nil {
		t.Fatal(err)
	}
	return &conversationEvolutionFixture{
		db: db, tenant: tenant, profile: profile, store: store, customer: customer,
		conversation: conversation, route: route, relation: relation, tag: tag,
		policy: policy, storePolicy: storePolicy,
	}
}

func createEvolutionMessage(
	t *testing.T,
	fixture *conversationEvolutionFixture,
	senderType enums.IMSenderType,
	content string,
	sentAt time.Time,
) *models.Message {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.Message{}).Where("conversation_id = ?", fixture.conversation.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID: fixture.tenant.ID, ConversationID: fixture.conversation.ID, SessionNo: 1,
		RequestID: fmt.Sprintf("evolution-request-%d", count+1), ClientMsgID: fmt.Sprintf("evolution-message-%d", count+1),
		SenderType: senderType, MessageType: enums.IMMessageTypeText, Content: content,
		SeqNo: count + 1, SendStatus: enums.IMMessageStatusSent, SentAt: &sentAt,
		AuditFields: models.AuditFields{CreatedAt: sentAt, UpdatedAt: sentAt},
	}
	if err := fixture.db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	fixture.conversation.LastMessageID = message.ID
	fixture.conversation.LastMessageAt = sentAt
	if err := fixture.db.Model(fixture.conversation).Updates(map[string]any{
		"last_message_id": message.ID, "last_message_at": sentAt, "last_active_at": sentAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return message
}

func claimEvolutionRun(
	t *testing.T,
	fixture *conversationEvolutionFixture,
	message *models.Message,
) (*models.ConversationEvolutionState, string, *conversationEvolutionPolicy, *models.ConversationEvolutionRun) {
	t.Helper()
	ConversationEvolutionService.ObserveCommittedMessage(fixture.conversation, message)
	state, err := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		fixture.db, fixture.tenant.ID, fixture.conversation.ID, 1,
	)
	if err != nil || state == nil {
		t.Fatalf("load state: %#v err=%v", state, err)
	}
	owner := "worker-" + fmt.Sprint(message.ID)
	now := time.Now()
	claimed, err := repositories.ConversationEvolutionStateRepository.Claim(
		fixture.db, state.ID, state.TenantID, owner, now, now.Add(conversationEvolutionLeaseDuration),
	)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v state=%#v", claimed, err, state)
	}
	state.LeaseOwner = owner
	policy, class, err := ConversationEvolutionService.loadPolicy(fixture.db, state, true)
	if err != nil || policy == nil || class != "" {
		t.Fatalf("policy=%#v class=%q err=%v", policy, class, err)
	}
	run, completed, err := ConversationEvolutionService.beginRun(state, policy, message.ID)
	if err != nil || run == nil || completed {
		t.Fatalf("run=%#v completed=%v err=%v", run, completed, err)
	}
	return state, owner, policy, run
}

func fixturePolicy(fixture *conversationEvolutionFixture) *conversationEvolutionPolicy {
	return &conversationEvolutionPolicy{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, IntentProfileID: fixture.profile.ID,
		QuietPeriod: time.Minute, MinimumConfidence: 0.8, MaxOperationsPerRun: 6,
	}
}

func assertNoEvolutionTagMutation(t *testing.T, fixture *conversationEvolutionFixture) {
	t.Helper()
	var relationCount int64
	if err := fixture.db.Model(&models.CustomerTagRelation{}).Count(&relationCount).Error; err != nil {
		t.Fatal(err)
	}
	var logCount int64
	if err := fixture.db.Model(&models.CustomerTagChangeLog{}).Count(&logCount).Error; err != nil {
		t.Fatal(err)
	}
	if relationCount != 0 || logCount != 0 {
		t.Fatalf("superseded run mutated tags: relations=%d logs=%d", relationCount, logCount)
	}
}

func seedEvolutionStoreModelRuntime(
	t *testing.T,
	fixture *conversationEvolutionFixture,
	gatewayURL, apiKey string,
) {
	t.Helper()
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{
		MasterKey: masterKey, MasterKeyID: "evolution-test-master",
	}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })
	now := time.Now()
	template := &models.ModelProfileTemplate{
		Code: "evolution-profile", Name: "Evolution Profile", Revision: 3,
		GatewayBaseURL: gatewayURL, Status: enums.ModelProfileStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	slots := make([]models.ModelProfileSlot, 0, len(RequiredModelUsageSlotSpecs()))
	for index, spec := range RequiredModelUsageSlotSpecs() {
		slot := models.ModelProfileSlot{
			TemplateID: template.ID, UsageCode: spec.UsageCode, DisplayName: spec.DisplayName,
			ModelType: spec.ExpectedModelType, Provider: modelProfileProviderNewAPI,
			ModelName: "model-" + string(spec.UsageCode), APIMode: spec.DefaultAPIMode,
			TimeoutMS: 5000, Enabled: true, SortNo: index + 1,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if slot.ModelType == enums.AIModelTypeLLM || slot.ModelType == enums.AIModelTypeVision {
			slot.MaxContextTokens = 8192
			slot.MaxOutputTokens = 256
		}
		if slot.ModelType == enums.AIModelTypeEmbedding {
			slot.Dimension = 1536
		}
		if slot.UsageCode == enums.ModelUsageSlotCustomerTag {
			slot.ModelName = "tag-model"
			slot.SchemaVersion = "customer_tag_evolution.v1"
			slot.PromptTemplate = "Return only valid customer tag JSON."
			slot.JSONSchema = `{"type":"object"}`
		}
		slots = append(slots, slot)
	}
	if err := fixture.db.Create(&slots).Error; err != nil {
		t.Fatal(err)
	}
	assignment := &models.StoreModelProfileAssignment{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		TemplateID: template.ID, TemplateRevision: template.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	const credentialRevision int64 = 2
	encryptedKey, nonce, err := cipher.Encrypt(apiKey, storeCredentialAAD(fixture.tenant.ID, fixture.store.ID, credentialRevision))
	if err != nil {
		t.Fatal(err)
	}
	credential := &models.StoreModelCredential{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		EncryptedKey: encryptedKey, KeyNonce: nonce, KeyFingerprint: securex.Fingerprint(apiKey),
		CipherVersion: securex.AESGCMCipherVersion, MasterKeyID: "evolution-test-master",
		CredentialRevision: credentialRevision, Status: enums.StoreCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}
}
