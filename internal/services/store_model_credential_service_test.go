package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type storeCredentialValidatorStub struct {
	mu      sync.Mutex
	err     error
	calls   int
	started chan struct{}
	release chan struct{}
}

func (s *storeCredentialValidatorStub) Validate(_ context.Context, _ *models.ModelProfileTemplate, _ []models.ModelProfileSlot, _ string) error {
	s.mu.Lock()
	s.calls++
	started := s.started
	release := s.release
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return s.err
}

func (s *storeCredentialValidatorStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type storeCredentialFastGPTStub struct {
	mu           sync.Mutex
	err          error
	errorsByCall map[int]error
	calls        int
	onFirst      func()
}

func (s *storeCredentialFastGPTStub) Sync(_ context.Context, _ storeCredentialActivationTarget, _ string, _ int64, _ string) (string, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	onFirst := s.onFirst
	err := s.err
	if callErr, exists := s.errorsByCall[call]; exists {
		err = callErr
	}
	s.mu.Unlock()
	if call == 1 && onFirst != nil {
		onFirst()
	}
	if err != nil {
		return storeCredentialFastGPTStatusFailed, err
	}
	return storeCredentialFastGPTStatusNotRequired, nil
}

func (s *storeCredentialFastGPTStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type storeCredentialFixture struct {
	db       *gorm.DB
	tenant   *models.Tenant
	store    *models.Store
	manager  *dto.AuthPrincipal
	approver *dto.AuthPrincipal
	staff    *dto.AuthPrincipal
	profile  *models.ModelProfileTemplate
	slots    []models.ModelProfileSlot
	password string
}

func TestStoreModelCredentialRequiresConfirmationPasswordAndTenantScope(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	validator := &storeCredentialValidatorStub{}
	fastGPT := &storeCredentialFastGPTStub{}
	service := &storeModelCredentialService{validator: validator, fastGPT: fastGPT}

	base := request.SubmitStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-store-first",
		CurrentPassword: fixture.password, Confirmed: true,
	}
	unconfirmed := base
	unconfirmed.Confirmed = false
	if _, err := service.SubmitManager(context.Background(), unconfirmed, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-unconfirmed"}); err == nil {
		t.Fatal("unconfirmed credential update must fail")
	}
	wrongPassword := base
	wrongPassword.CurrentPassword = "wrong-password"
	if _, err := service.SubmitManager(context.Background(), wrongPassword, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-wrong-password"}); err == nil {
		t.Fatal("credential update with a wrong password must fail")
	}
	crossTenant := base
	crossTenant.TenantID = fixture.tenant.ID + 100
	if _, err := service.SubmitManager(context.Background(), crossTenant, fixture.manager, StoreCredentialRequestMeta{}); err == nil {
		t.Fatal("tenant operator must not update a different tenant")
	}

	data, err := service.SubmitManager(context.Background(), base, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-activate", ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Credential == nil || data.Credential.Status != enums.StoreCredentialStatusActive || data.Credential.CredentialRevision != 1 {
		t.Fatalf("credential was not activated: %#v", data.Credential)
	}
	resolved, err := service.ResolveActive(fixture.tenant.ID, fixture.store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != base.APIKey || resolved.Revision != 1 {
		t.Fatalf("resolved credential=%#v", resolved)
	}
	if validator.callCount() != 1 || fastGPT.callCount() != 1 {
		t.Fatalf("validator calls=%d fastgpt calls=%d", validator.callCount(), fastGPT.callCount())
	}
	assertCredentialAuditContainsNoSecret(t, fixture.db, base.APIKey)
}

func TestStoreModelCredentialPolicyRequiresPasswordForSingleAndBatch(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		storeCount int
	}{
		{name: "single", storeCount: 1},
		{name: "batch", storeCount: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := setupStoreCredentialFixture(t)
			service := &storeModelCredentialService{validator: &storeCredentialValidatorStub{}, fastGPT: &storeCredentialFastGPTStub{}}
			storeIDs := []int64{fixture.store.ID}
			for index := 1; index < testCase.storeCount; index++ {
				store := &models.Store{
					TenantID:  fixture.tenant.ID,
					StoreCode: fmt.Sprintf("credential-store-%d", index+1),
					Name:      fmt.Sprintf("凭据测试门店 %d", index+1),
					Status:    enums.StatusOk,
				}
				if err := fixture.db.Create(store).Error; err != nil {
					t.Fatal(err)
				}
				if err := service.EnsureStoreRecordsDB(fixture.db, store, fixture.manager); err != nil {
					t.Fatal(err)
				}
				storeIDs = append(storeIDs, store.ID)
			}

			base := request.UpdateStoreCredentialPolicyRequest{
				TenantID: fixture.tenant.ID, StoreIDs: storeIDs,
				AllowCredentialSelfService: true, RequireSupervisorApproval: true,
				CurrentPassword: fixture.password, Confirmed: true,
			}
			unconfirmed := base
			unconfirmed.Confirmed = false
			if err := service.UpdatePolicy(unconfirmed, fixture.manager, StoreCredentialRequestMeta{RequestID: "policy-unconfirmed"}); err == nil {
				t.Fatal("unconfirmed credential policy update must fail")
			}
			wrongPassword := base
			wrongPassword.CurrentPassword = "wrong-password"
			if err := service.UpdatePolicy(wrongPassword, fixture.manager, StoreCredentialRequestMeta{RequestID: "policy-wrong-password"}); err == nil {
				t.Fatal("credential policy update with a wrong password must fail")
			}

			for _, storeID := range storeIDs {
				policy := repositories.StoreCredentialPolicyRepository.GetByStore(fixture.db, fixture.tenant.ID, storeID)
				if policy == nil || policy.AllowCredentialSelfService || policy.RequireSupervisorApproval {
					t.Fatalf("failed sensitive action changed store %d policy: %#v", storeID, policy)
				}
			}
			var failedAuditCount int64
			if err := fixture.db.Model(&models.StoreModelCredentialAuditLog{}).
				Where("action = ? AND result = ?", enums.CredentialAuditActionPolicyUpdate, enums.CredentialAuditResultFailure).
				Count(&failedAuditCount).Error; err != nil {
				t.Fatal(err)
			}
			if failedAuditCount != int64(len(storeIDs)*2) {
				t.Fatalf("failed policy audit count=%d want=%d", failedAuditCount, len(storeIDs)*2)
			}

			normalized := base
			normalized.AllowCredentialSelfService = false
			if err := service.UpdatePolicy(normalized, fixture.manager, StoreCredentialRequestMeta{RequestID: "policy-normalized"}); err != nil {
				t.Fatal(err)
			}
			for _, storeID := range storeIDs {
				policy := repositories.StoreCredentialPolicyRepository.GetByStore(fixture.db, fixture.tenant.ID, storeID)
				if policy == nil || policy.AllowCredentialSelfService || policy.RequireSupervisorApproval {
					t.Fatalf("disabled self-service must clear approval for store %d: %#v", storeID, policy)
				}
			}

			if err := service.UpdatePolicy(base, fixture.manager, StoreCredentialRequestMeta{RequestID: "policy-enabled"}); err != nil {
				t.Fatal(err)
			}
			for _, storeID := range storeIDs {
				policy := repositories.StoreCredentialPolicyRepository.GetByStore(fixture.db, fixture.tenant.ID, storeID)
				if policy == nil || !policy.AllowCredentialSelfService || !policy.RequireSupervisorApproval {
					t.Fatalf("enabled policy was not applied to store %d: %#v", storeID, policy)
				}
			}
		})
	}
}

func TestStoreModelCredentialSelfServiceApproval(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	validator := &storeCredentialValidatorStub{}
	service := &storeModelCredentialService{validator: validator, fastGPT: &storeCredentialFastGPTStub{}}
	req := request.SubmitStoreModelCredentialRequest{APIKey: "sk-self-service", CurrentPassword: fixture.password, Confirmed: true}
	if _, err := service.SubmitSelf(context.Background(), req, fixture.staff, StoreCredentialRequestMeta{}); err == nil {
		t.Fatal("self-service must be denied while the policy is disabled")
	}
	policy := repositories.StoreCredentialPolicyRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if policy == nil {
		t.Fatal("fixture policy is missing")
	}
	if err := repositories.StoreCredentialPolicyRepository.Updates(fixture.db, policy.ID, map[string]any{
		"allow_credential_self_service": true, "require_supervisor_approval": true,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := service.SubmitSelf(context.Background(), req, fixture.staff, StoreCredentialRequestMeta{RequestID: "req-self"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Credential == nil || data.Credential.CandidateStatus != enums.StoreCredentialStatusPendingApproval ||
		data.Credential.CandidateApprovalStatus != enums.CredentialApprovalStatusPending || validator.callCount() != 0 {
		t.Fatalf("unexpected approval state: %#v calls=%d", data.Credential, validator.callCount())
	}
	staffApprover := *fixture.staff
	staffApprover.Permissions = append(staffApprover.Permissions, constants.PermissionAIConfigUpdate.Code)
	if _, err := service.Approve(context.Background(), request.DecideStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		CandidateRevision: data.Credential.CandidateRevision, CurrentPassword: fixture.password, Confirmed: true,
	}, &staffApprover, StoreCredentialRequestMeta{}); err == nil {
		t.Fatal("credential submitter must not approve their own request")
	}

	approved, err := service.Approve(context.Background(), request.DecideStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		CandidateRevision: data.Credential.CandidateRevision, CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.approver, StoreCredentialRequestMeta{RequestID: "req-approve"})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Credential == nil || approved.Credential.Status != enums.StoreCredentialStatusActive || validator.callCount() != 1 {
		t.Fatalf("approved credential was not activated: %#v", approved.Credential)
	}
}

func TestStoreModelCredentialFailedCandidatePreservesActive(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedActiveStoreCredential(t, fixture, "sk-active", 3)
	validator := &storeCredentialValidatorStub{err: &storeCredentialValidationError{UsageCode: enums.ModelUsageSlotReplyLLM, Class: "credential_rejected"}}
	fastGPT := &storeCredentialFastGPTStub{}
	service := &storeModelCredentialService{validator: validator, fastGPT: fastGPT}

	_, err := service.SubmitManager(context.Background(), request.SubmitStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-invalid-candidate",
		CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-failed-candidate"})
	if err == nil {
		t.Fatal("invalid candidate must fail")
	}
	credential := repositories.StoreModelCredentialRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if credential == nil || credential.CredentialRevision != 3 || credential.Status != enums.StoreCredentialStatusActive ||
		credential.CandidateRevision != 4 || credential.CandidateStatus != enums.StoreCredentialStatusFailed {
		t.Fatalf("failed candidate replaced active state: %#v", credential)
	}
	resolved, resolveErr := service.ResolveActive(fixture.tenant.ID, fixture.store.ID)
	if resolveErr != nil || resolved.APIKey != "sk-active" {
		t.Fatalf("active credential was not preserved: %#v err=%v", resolved, resolveErr)
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if assignment == nil || assignment.Status != enums.StoreModelAssignmentStatusReady || assignment.ReadinessStatus != "ready" {
		t.Fatalf("active assignment was not preserved: %#v", assignment)
	}
	if fastGPT.callCount() != 0 {
		t.Fatalf("FastGPT must not run after model validation failure, calls=%d", fastGPT.callCount())
	}
}

func TestStoreModelCredentialCASConflictRestoresOldFastGPTRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	activeProfile := fixture.profile
	seedActiveStoreCredential(t, fixture, "sk-active", 1)
	pendingProfile, _ := createStoreCredentialProfile(t, fixture.db, "next", 2, enums.ModelProfileStatusCandidate)
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if err := repositories.StoreModelProfileAssignmentRepository.Updates(fixture.db, assignment.ID, map[string]any{
		"pending_template_id": pendingProfile.ID, "pending_template_revision": pendingProfile.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	fastGPT := &storeCredentialFastGPTStub{}
	fastGPT.onFirst = func() {
		_ = repositories.StoreModelProfileAssignmentRepository.Updates(fixture.db, assignment.ID, map[string]any{
			"pending_template_id": activeProfile.ID, "pending_template_revision": activeProfile.Revision + 100,
		})
	}
	service := &storeModelCredentialService{validator: &storeCredentialValidatorStub{}, fastGPT: fastGPT}
	_, err := service.SubmitManager(context.Background(), request.SubmitStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-cas-candidate",
		CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-cas"})
	if err == nil {
		t.Fatal("changed assignment must prevent activation")
	}
	credential := repositories.StoreModelCredentialRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if credential == nil || credential.CredentialRevision != 1 || credential.Status != enums.StoreCredentialStatusActive || credential.CandidateStatus != enums.StoreCredentialStatusFailed {
		t.Fatalf("CAS conflict damaged active credential: %#v", credential)
	}
	if fastGPT.callCount() != 2 {
		t.Fatalf("candidate sync and old revision restore calls=%d want=2", fastGPT.callCount())
	}
}

func TestStoreModelCredentialFastGPTFailureRestoresOldRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedActiveStoreCredential(t, fixture, "sk-active", 1)
	fastGPT := &storeCredentialFastGPTStub{errorsByCall: map[int]error{1: errors.New("candidate sync failed")}}
	service := &storeModelCredentialService{validator: &storeCredentialValidatorStub{}, fastGPT: fastGPT}

	_, err := service.SubmitManager(context.Background(), request.SubmitStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-next",
		CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-fastgpt-failure"})
	if err == nil {
		t.Fatal("FastGPT candidate failure must prevent activation")
	}
	if fastGPT.callCount() != 2 {
		t.Fatalf("candidate sync and old revision restore calls=%d want=2", fastGPT.callCount())
	}
	credential := repositories.StoreModelCredentialRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if credential == nil || credential.CredentialRevision != 1 || credential.Status != enums.StoreCredentialStatusActive || credential.CandidateStatus != enums.StoreCredentialStatusFailed {
		t.Fatalf("FastGPT failure damaged active credential: %#v", credential)
	}
	resolved, resolveErr := service.ResolveActive(fixture.tenant.ID, fixture.store.ID)
	if resolveErr != nil || resolved.APIKey != "sk-active" {
		t.Fatalf("old active credential was not preserved: %#v err=%v", resolved, resolveErr)
	}
}

func TestStoreModelCredentialConcurrentCandidateSubmissionAllowsOne(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	validator := &storeCredentialValidatorStub{started: make(chan struct{}, 1), release: make(chan struct{})}
	service := &storeModelCredentialService{validator: validator, fastGPT: &storeCredentialFastGPTStub{}}
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.SubmitManager(context.Background(), request.SubmitStoreModelCredentialRequest{
			TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-concurrent-first",
			CurrentPassword: fixture.password, Confirmed: true,
		}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-concurrent-first"})
		firstDone <- err
	}()
	select {
	case <-validator.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first candidate did not reach validation")
	}
	_, secondErr := service.SubmitManager(context.Background(), request.SubmitStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, APIKey: "sk-concurrent-second",
		CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-concurrent-second"})
	if secondErr == nil {
		t.Fatal("second live candidate must be rejected")
	}
	close(validator.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first candidate failed: %v", err)
	}
	if validator.callCount() != 1 {
		t.Fatalf("validator calls=%d want=1", validator.callCount())
	}
}

func TestStoreModelCredentialDisableBlocksAssignment(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedActiveStoreCredential(t, fixture, "sk-active", 2)
	service := &storeModelCredentialService{validator: &storeCredentialValidatorStub{}, fastGPT: &storeCredentialFastGPTStub{}}
	data, err := service.Disable(request.DecideStoreModelCredentialRequest{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		CurrentPassword: fixture.password, Confirmed: true,
	}, fixture.manager, StoreCredentialRequestMeta{RequestID: "req-disable"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Credential == nil || data.Credential.Status != enums.StoreCredentialStatusDisabled {
		t.Fatalf("credential was not disabled: %#v", data.Credential)
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if assignment == nil || assignment.Status != enums.StoreModelAssignmentStatusBlocked || assignment.ReadinessStatus != "blocked" || assignment.LastErrorClass != "credential_disabled" {
		t.Fatalf("assignment remained ready after credential disable: %#v", assignment)
	}
}

func TestNewAPIStoreCredentialValidatorExercisesAllNineSlots(t *testing.T) {
	var mu sync.Mutex
	modelsSeen := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := ""
		switch r.URL.Path {
		case "/v1/chat/completions":
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			model = payload.Model
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		case "/v1/audio/transcriptions":
			model = multipartModelName(r)
			_, _ = w.Write([]byte(`{"text":""}`))
		case "/v1/embeddings":
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			model = payload.Model
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1]}]}`))
		case "/v1/rerank":
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			model = payload.Model
			_, _ = w.Write([]byte(`{"results":[{"index":0,"score":1}]}`))
		default:
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-nine-slots" {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		modelsSeen[model]++
		mu.Unlock()
	}))
	defer server.Close()

	template, slots := completeStoreCredentialProfile("nine", 1, enums.ModelProfileStatusCandidate, server.URL+"/v1")
	if err := (&newAPIStoreCredentialValidator{}).Validate(context.Background(), template, slots, "sk-nine-slots"); err != nil {
		t.Fatal(err)
	}
	if len(modelsSeen) != len(enums.RequiredModelUsageSlots) {
		t.Fatalf("validated models=%#v want=%d distinct slots", modelsSeen, len(enums.RequiredModelUsageSlots))
	}
	for _, usage := range enums.RequiredModelUsageSlots {
		name := "model-" + string(usage)
		if modelsSeen[name] != 1 {
			t.Fatalf("slot %s validation count=%d", usage, modelsSeen[name])
		}
	}
}

func TestCredentialRuntimeTypesNeverSerializeSecretMaterial(t *testing.T) {
	secret := "sk-runtime-secret"
	fingerprint := securex.Fingerprint(secret)
	values := []any{
		models.StoreModelCredential{
			EncryptedKey: "active-ciphertext", KeyNonce: "active-nonce", KeyFingerprint: fingerprint,
			CipherVersion: securex.AESGCMCipherVersion, MasterKeyID: "master-key-id",
			CandidateEncryptedKey: "candidate-ciphertext", CandidateKeyNonce: "candidate-nonce",
			CandidateKeyFingerprint: fingerprint, CandidateCipherVersion: securex.AESGCMCipherVersion,
			CandidateMasterKeyID: "candidate-master-key-id",
		},
		models.FastGPTStoreTenant{AppliedKeyFingerprint: fingerprint},
		ModelCallConfig{
			GatewayBaseURL: "https://private-gateway.example.com/v1", PromptTemplate: "private prompt",
			JSONSchema: "private schema", APIKey: secret, KeyFingerprint: fingerprint,
		},
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, forbidden := range []string{
			secret, fingerprint, "active-ciphertext", "active-nonce", "candidate-ciphertext", "candidate-nonce",
			"master-key-id", "candidate-master-key-id", "private-gateway.example.com", "private prompt", "private schema",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("runtime JSON leaked %q from %T: %s", forbidden, value, body)
			}
		}
	}
}

func setupStoreCredentialFixture(t *testing.T) storeCredentialFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&models.Tenant{}, &models.User{}, &models.Store{}, &models.StoreStaffBinding{},
		&models.AgentTeam{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{},
		&models.ModelProfileTemplate{}, &models.ModelProfileSlot{}, &models.StoreModelProfileAssignment{},
		&models.StoreModelCredential{}, &models.StoreCredentialPolicy{}, &models.StoreModelCredentialAuditLog{},
		&models.FastGPTStoreTenant{},
	); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey, MasterKeyID: "test-key-v1"}})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
		sqls.SetDB(nil)
		if raw, openErr := db.DB(); openErr == nil {
			_ = raw.Close()
		}
	})

	tenant := &models.Tenant{
		TenantCode: "tenant-credential",
		LegalName:  "凭据测试公司",
		ShortName:  "测试公司",
		Status:     enums.StatusOk,
	}
	if err = db.Create(tenant).Error; err != nil {
		t.Fatal(err)
	}
	password := "Password-123!"
	managerUser := createStoreCredentialUser(t, db, tenant.ID, "credential-manager", password)
	approverUser := createStoreCredentialUser(t, db, tenant.ID, "credential-approver", password)
	staffUser := createStoreCredentialUser(t, db, tenant.ID, "credential-staff", password)
	store := &models.Store{TenantID: tenant.ID, StoreCode: "credential-store", Name: "凭据测试门店", Status: enums.StatusOk}
	if err = db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&models.StoreStaffBinding{TenantID: tenant.ID, UserID: staffUser.ID, StoreID: store.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatal(err)
	}
	profile, slots := createStoreCredentialProfile(t, db, "standard", 1, enums.ModelProfileStatusCandidate)
	now := time.Now()
	assignment := &models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID,
		PendingTemplateID: profile.ID, PendingTemplateRevision: profile.Revision,
		Status: enums.StoreModelAssignmentStatusAssigned, ReadinessStatus: "pending",
		AssignedAt: now,
	}
	if err = db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}
	service := newStoreModelCredentialService()
	manager := &dto.AuthPrincipal{
		UserID: managerUser.ID, TenantID: tenant.ID, ActiveTenantID: tenant.ID, Username: managerUser.Username,
		Roles: []string{constants.RoleCodeTenantAdmin}, Permissions: []string{constants.PermissionAIConfigView.Code, constants.PermissionAIConfigUpdate.Code},
	}
	if err = service.EnsureStoreRecordsDB(db, store, manager); err != nil {
		t.Fatal(err)
	}
	return storeCredentialFixture{
		db: db, tenant: tenant, store: store, profile: profile, slots: slots, password: password,
		manager: manager,
		approver: &dto.AuthPrincipal{
			UserID: approverUser.ID, TenantID: tenant.ID, ActiveTenantID: tenant.ID, Username: approverUser.Username,
			Roles: []string{constants.RoleCodeTenantAdmin}, Permissions: []string{constants.PermissionAIConfigView.Code, constants.PermissionAIConfigUpdate.Code},
		},
		staff: &dto.AuthPrincipal{
			UserID: staffUser.ID, TenantID: tenant.ID, ActiveTenantID: tenant.ID, Username: staffUser.Username,
			Roles: []string{constants.RoleCodeStoreStaff}, Permissions: []string{constants.PermissionStoreWorkbenchView.Code, constants.PermissionStoreWorkbenchUpdate.Code},
		},
	}
}

func createStoreCredentialUser(t *testing.T, db *gorm.DB, tenantID int64, username, password string) *models.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	user := &models.User{TenantID: tenantID, Username: username, Nickname: username, Password: string(hash), Status: enums.StatusOk}
	if err = db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func createStoreCredentialProfile(t *testing.T, db *gorm.DB, code string, revision int64, status enums.ModelProfileStatus) (*models.ModelProfileTemplate, []models.ModelProfileSlot) {
	t.Helper()
	template, slots := completeStoreCredentialProfile(code, revision, status, "https://newapi.example.com/v1")
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	for i := range slots {
		slots[i].TemplateID = template.ID
	}
	if err := db.Create(&slots).Error; err != nil {
		t.Fatal(err)
	}
	return template, slots
}

func completeStoreCredentialProfile(code string, revision int64, status enums.ModelProfileStatus, baseURL string) (*models.ModelProfileTemplate, []models.ModelProfileSlot) {
	template := &models.ModelProfileTemplate{Code: code, Name: code, Revision: revision, GatewayBaseURL: baseURL, Status: status}
	slots := make([]models.ModelProfileSlot, 0, len(RequiredModelUsageSlotSpecs()))
	for index, spec := range RequiredModelUsageSlotSpecs() {
		slot := models.ModelProfileSlot{
			UsageCode: spec.UsageCode, DisplayName: spec.DisplayName, ModelType: spec.ExpectedModelType,
			Provider: modelProfileProviderNewAPI, ModelName: "model-" + string(spec.UsageCode),
			APIMode: spec.DefaultAPIMode, TimeoutMS: 5000, Enabled: true, SortNo: index + 1,
		}
		if slot.ModelType == enums.AIModelTypeLLM || slot.ModelType == enums.AIModelTypeVision {
			slot.MaxContextTokens = 8192
			slot.MaxOutputTokens = 128
		}
		if slot.ModelType == enums.AIModelTypeEmbedding {
			slot.Dimension = 1536
		}
		if slot.UsageCode == enums.ModelUsageSlotCustomerTag {
			slot.SchemaVersion = "customer_tag_evolution.v1"
			slot.PromptTemplate = "Return valid JSON."
			slot.JSONSchema = `{"type":"object"}`
		}
		slots = append(slots, slot)
	}
	return template, slots
}

func seedActiveStoreCredential(t *testing.T, fixture storeCredentialFixture, apiKey string, revision int64) {
	t.Helper()
	cipher, err := securex.NewAESGCM(config.Current().StoreCredential.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt(apiKey, storeCredentialAAD(fixture.tenant.ID, fixture.store.ID, revision))
	if err != nil {
		t.Fatal(err)
	}
	credential := repositories.StoreModelCredentialRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if credential == nil {
		t.Fatal("credential is missing")
	}
	if err = repositories.StoreModelCredentialRepository.Updates(fixture.db, credential.ID, map[string]any{
		"encrypted_key": ciphertext, "key_nonce": nonce, "key_fingerprint": securex.Fingerprint(apiKey),
		"cipher_version": securex.AESGCMCipherVersion, "master_key_id": config.Current().StoreCredential.MasterKeyID,
		"credential_revision": revision, "status": enums.StoreCredentialStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err = repositories.ModelProfileTemplateRepository.Updates(fixture.db, fixture.profile.ID, map[string]any{"status": enums.ModelProfileStatusActive}); err != nil {
		t.Fatal(err)
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if err = repositories.StoreModelProfileAssignmentRepository.Updates(fixture.db, assignment.ID, map[string]any{
		"template_id": fixture.profile.ID, "template_revision": fixture.profile.Revision,
		"pending_template_id": 0, "pending_template_revision": 0,
		"status": enums.StoreModelAssignmentStatusReady, "readiness_status": "ready",
	}); err != nil {
		t.Fatal(err)
	}
	fixture.profile.Status = enums.ModelProfileStatusActive
}

func assertCredentialAuditContainsNoSecret(t *testing.T, db *gorm.DB, secret string) {
	t.Helper()
	var logs []models.StoreModelCredentialAuditLog
	if err := db.Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("credential audit log is empty")
	}
	raw, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, secret) || strings.Contains(body, securex.Fingerprint(secret)) {
		t.Fatalf("credential audit leaked secret material: %s", body)
	}
}

func multipartModelName(r *http.Request) string {
	reader, err := r.MultipartReader()
	if err != nil {
		return ""
	}
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, multipart.ErrMessageTooLarge) || partErr != nil {
			return ""
		}
		if part.FormName() == "model" {
			raw, _ := io.ReadAll(part)
			return strings.TrimSpace(string(raw))
		}
	}
}

func Example_storeCredentialAAD() {
	fmt.Println(string(storeCredentialAAD(7, 9, 2)))
	// Output: tenant:7:store:9:revision:2
}
