package services

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestFastGPTReadinessRequiresActiveTenant(t *testing.T) {
	operator := &dto.AuthPrincipal{
		UserID: 1,
		Permissions: []string{
			constants.PermissionAIConfigUpdate.Code,
		},
	}
	_, err := FastGPTDatasetService.GetReadiness(1, operator)
	if err == nil || !strings.Contains(err.Error(), "接入公司") {
		t.Fatalf("FastGPT readiness without active tenant must fail, err=%v", err)
	}
}

func TestFastGPTFailedDatasetJobCanBeRetried(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	db := fixture.db
	tenantID := fixture.tenant.ID
	store := fixture.store
	completedAt := time.Now()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", tenantID, store.ID, strings.ToLower(store.Name))))
	failed := &models.FastGPTDatasetJob{
		TenantID: tenantID, TaskKey: fmt.Sprintf("fastgpt-create-tenant-%d-store-%d-%x", tenantID, store.ID, sum[:6]), StoreID: store.ID,
		Action: fastGPTJobActionCreateDataset, Status: fastGPTJobStatusFailed,
		AttemptCount: 5, CompletedAt: &completedAt, LastError: "platform unavailable",
		CreatedAt: completedAt, UpdatedAt: completedAt,
	}
	if err := db.Create(failed).Error; err != nil {
		t.Fatalf("create failed job: %v", err)
	}

	retried, err := FastGPTDatasetService.enqueueDefaultDataset(store, store.Name)
	if err != nil {
		t.Fatalf("retry failed job: %v", err)
	}
	if retried.ID != failed.ID || retried.Status != fastGPTJobStatusPending || retried.AttemptCount != 0 || retried.CompletedAt != nil || retried.LastError != "" {
		t.Fatalf("unexpected retried job: %#v", retried)
	}
}

func TestFastGPTDatasetJobsAllowMultipleKnowledgeBasesForOneStore(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	db := fixture.db
	store := fixture.store
	first, err := FastGPTDatasetService.enqueueDefaultDataset(store, "前厅资料")
	if err != nil {
		t.Fatalf("enqueue first knowledge base: %v", err)
	}
	second, err := FastGPTDatasetService.enqueueDefaultDataset(store, "房间设施资料")
	if err != nil {
		t.Fatalf("enqueue second knowledge base: %v", err)
	}
	if first.ID == second.ID || first.TaskKey == second.TaskKey {
		t.Fatalf("different knowledge-base names must create independent jobs: %#v %#v", first, second)
	}
	var count int64
	if err := db.Model(&models.FastGPTDatasetJob{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("expected two jobs, count=%d err=%v", count, err)
	}
}

func TestFastGPTDatasetJobLeasePreventsDoubleClaim(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.FastGPTDatasetJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{TenantID: 101, StoreID: 201, TaskKey: "lease-job", Action: fastGPTJobActionSyncProfile, Status: fastGPTJobStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	first, err := repositories.FastGPTDatasetJobRepository.ClaimDue(db, []string{fastGPTJobStatusPending}, now, now.Add(time.Minute), "worker-a", 1)
	if err != nil || len(first) != 1 || first[0].LeaseOwner != "worker-a" {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	second, err := repositories.FastGPTDatasetJobRepository.ClaimDue(db, []string{fastGPTJobStatusPending}, now, now.Add(time.Minute), "worker-b", 1)
	if err != nil || len(second) != 0 {
		t.Fatalf("live lease was claimed twice: %#v err=%v", second, err)
	}
	afterExpiry, err := repositories.FastGPTDatasetJobRepository.ClaimDue(db, []string{fastGPTJobStatusPending}, now.Add(2*time.Minute), now.Add(3*time.Minute), "worker-b", 1)
	if err != nil || len(afterExpiry) != 1 || afterExpiry[0].LeaseOwner != "worker-b" {
		t.Fatalf("expired lease was not recoverable: %#v err=%v", afterExpiry, err)
	}
}

func TestFastGPTDatasetJobRejectsStaleTargetRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, TaskKey: "stale-target-job",
		Action: fastGPTJobActionSyncProfile, Status: fastGPTJobStatusPending, LeaseOwner: "worker-a",
		TargetProfileID: fixture.profile.ID, TargetProfileRevision: fixture.profile.Revision + 1,
		TargetCredentialRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(job).Error; err != nil {
		t.Fatalf("create stale job: %v", err)
	}
	if _, _, err := FastGPTDatasetService.resolveClaimedJobTarget(job); !errors.Is(err, errFastGPTJobTargetChanged) {
		t.Fatalf("stale target error=%v", err)
	}
	FastGPTDatasetService.failOrRetry(job, errFastGPTJobTargetChanged)
	updated := repositories.FastGPTDatasetJobRepository.GetInTenant(fixture.db, job.ID, fixture.tenant.ID)
	if updated == nil || updated.Status != fastGPTJobStatusFailed || updated.LastErrorClass != "target_revision_changed" || updated.LeaseOwner != "" || updated.CompletedAt == nil {
		t.Fatalf("stale target was not failed closed: %#v", updated)
	}
}

func TestFastGPTPollingFailuresReachTerminalState(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, TaskKey: "poll-failure-job",
		Action: fastGPTJobActionUploadFile, Status: fastGPTJobStatusIndexing, CollectionID: "collection-1",
		LeaseOwner: "worker-1", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		job.LeaseOwner = fmt.Sprintf("worker-%d", attempt)
		if err := fixture.db.Model(job).Updates(map[string]any{"lease_owner": job.LeaseOwner, "lease_expires_at": now.Add(time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
		FastGPTDatasetService.failOrRetry(job, errors.New("temporary polling failure"))
		updated := repositories.FastGPTDatasetJobRepository.GetInTenant(fixture.db, job.ID, fixture.tenant.ID)
		if updated == nil || updated.AttemptCount != attempt {
			t.Fatalf("attempt %d state=%#v", attempt, updated)
		}
		if attempt < 5 && (updated.Status != fastGPTJobStatusIndexing || updated.CompletedAt != nil) {
			t.Fatalf("attempt %d became terminal too early: %#v", attempt, updated)
		}
		if attempt == 5 && (updated.Status != fastGPTJobStatusFailed || updated.CompletedAt == nil) {
			t.Fatalf("polling failures did not become terminal: %#v", updated)
		}
		job = updated
	}
}

func TestFastGPTProfileFailurePreservesPreviousAppliedRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(fixture.db, binding.ID, fixture.tenant.ID, map[string]any{
		"target_profile_revision": fixture.profile.Revision + 1, "target_credential_revision": 2, "readiness_status": "syncing",
	}); err != nil {
		t.Fatal(err)
	}
	(&managedStoreCredentialFastGPTSynchronizer{}).markFailed(binding, fixture.tenant.ID, "profile_test_failed")
	updated := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if updated == nil || updated.AppliedProfileID != fixture.profile.ID || updated.AppliedProfileRevision != fixture.profile.Revision ||
		updated.AppliedCredentialRevision != 1 || updated.ReadinessStatus != "ready" || updated.LastError != "profile_test_failed" {
		t.Fatalf("failed synchronization damaged previous applied revision: %#v", updated)
	}
}

func TestFastGPTProfileCASCannotCrossTenant(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	updated, err := repositories.FastGPTStoreTenantRepository.ApplyTargetRevisions(
		fixture.db, fixture.tenant.ID+999, fixture.store.ID, fixture.profile.ID, fixture.profile.Revision, 1,
		map[string]any{"applied_profile_revision": fixture.profile.Revision + 99},
	)
	if err != nil || updated {
		t.Fatalf("cross-tenant CAS updated=%t err=%v", updated, err)
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if binding == nil || binding.AppliedProfileRevision != fixture.profile.Revision {
		t.Fatalf("cross-tenant CAS changed binding: %#v", binding)
	}
}

func TestFastGPTStoreBindingCreateIfAbsentPreservesAppliedRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(fixture.db, binding.ID, fixture.tenant.ID, map[string]any{
		"applied_profile_revision": 77, "applied_credential_revision": 66, "applied_key_fingerprint": "keep-me",
	}); err != nil {
		t.Fatal(err)
	}
	created, err := repositories.FastGPTStoreTenantRepository.CreateIfAbsent(fixture.db, &models.FastGPTStoreTenant{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, TenantTeamID: binding.TenantTeamID,
		TargetProfileID: fixture.profile.ID, TargetProfileRevision: fixture.profile.Revision,
		TargetCredentialRevision: 1, ReadinessStatus: "syncing",
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	})
	if err != nil || created {
		t.Fatalf("duplicate binding created=%t err=%v", created, err)
	}
	binding = repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if binding.AppliedProfileRevision != 77 || binding.AppliedCredentialRevision != 66 || binding.AppliedKeyFingerprint != "keep-me" || binding.ReadinessStatus != "ready" {
		t.Fatalf("duplicate create changed applied binding: %#v", binding)
	}
}

func TestFastGPTCommitTargetRecheckRejectsCredentialRace(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	target, credential, err := FastGPTDatasetService.resolveJobTarget(fixture.tenant.ID, fixture.store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentFastGPTJobTargetDB(fixture.db, *target, credential); err != nil {
		t.Fatalf("current target rejected: %v", err)
	}
	current := repositories.StoreModelCredentialRepository.GetByStore(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if err := repositories.StoreModelCredentialRepository.Updates(fixture.db, current.ID, map[string]any{
		"credential_revision": current.CredentialRevision + 1, "key_fingerprint": "rotated",
	}); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentFastGPTJobTargetDB(fixture.db, *target, credential); !errors.Is(err, errFastGPTJobTargetChanged) {
		t.Fatalf("credential race error=%v", err)
	}
}

func TestFastGPTKnowledgeBaseActivationProjectsToWholeStore(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	db := fixture.db
	tenantID := fixture.tenant.ID
	store := fixture.store
	active := &models.KnowledgeBase{
		TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-active", Name: "当前库",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileStatus: "ready",
		FastGPTAppliedProfileID: fixture.profile.ID, FastGPTAppliedProfileRevision: fixture.profile.Revision,
		FastGPTAppliedCredentialRevision: 1, Status: enums.StatusOk,
	}
	candidate := &models.KnowledgeBase{
		TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-next", Name: "新库",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileStatus: "ready",
		FastGPTAppliedProfileID: fixture.profile.ID, FastGPTAppliedProfileRevision: fixture.profile.Revision,
		FastGPTAppliedCredentialRevision: 1, Status: enums.StatusOk,
	}
	if err := db.Create(active).Error; err != nil {
		t.Fatalf("create active knowledge base: %v", err)
	}
	if err := db.Create(candidate).Error; err != nil {
		t.Fatalf("create candidate knowledge base: %v", err)
	}
	if err := db.Model(store).Update("knowledge_base_id", active.ID).Error; err != nil {
		t.Fatalf("set store default: %v", err)
	}
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	first := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "gateway-instance-1", StoreID: store.ID, KnowledgeBaseID: active.ID, Status: enums.StatusOk}
	second := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "gateway-instance-2", StoreID: store.ID, KnowledgeBaseID: active.ID, Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first instance: %v", err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second instance: %v", err)
	}

	if err := FastGPTDatasetService.ActivateKnowledgeBase(store.ID, candidate.ID, fixture.manager); err != nil {
		t.Fatalf("activate knowledge base: %v", err)
	}
	updatedFirst := WxWorkProtocolInstanceService.Get(first.ID)
	updatedSecond := WxWorkProtocolInstanceService.Get(second.ID)
	updatedStore := StoreService.Get(store.ID)
	if updatedFirst == nil || updatedFirst.KnowledgeBaseID != candidate.ID {
		t.Fatalf("first instance did not switch: %#v", updatedFirst)
	}
	if updatedSecond == nil || updatedSecond.KnowledgeBaseID != candidate.ID {
		t.Fatalf("second instance did not receive Store projection: %#v", updatedSecond)
	}
	if updatedStore == nil || updatedStore.KnowledgeBaseID != candidate.ID {
		t.Fatalf("Store authoritative knowledge base did not switch: %#v", updatedStore)
	}
	if err := db.First(active, active.ID).Error; err != nil || active.Status != enums.StatusOk {
		t.Fatalf("previous knowledge base should remain an explicit managed resource: %#v err=%v", active, err)
	}
}

func TestFastGPTProfileCommitUpdatesOnlySelectedKnowledgeBase(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	db := fixture.db
	first := &models.KnowledgeBase{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, DatasetID: "dataset-first", Name: "当前库",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileStatus: "pending", Status: enums.StatusOk,
	}
	second := &models.KnowledgeBase{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, DatasetID: "dataset-second", Name: "候选库",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileStatus: "pending", Status: enums.StatusOk,
	}
	if err := db.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(fixture.store).Update("knowledge_base_id", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	fixture.store.KnowledgeBaseID = first.ID
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	target, err := StoreModelCredentialService.loadActiveTargetDB(db, fixture.tenant.ID, fixture.store.ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := StoreModelCredentialService.ResolveActive(fixture.tenant.ID, fixture.store.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteProfile := &FastGPTModelProfile{ID: "remote-profile-first", Name: "门店模型", Revision: 7}
	if err := commitManagedStoreFastGPTProfile(*target, credential.Revision, credential.Fingerprint, remoteProfile, first.ID, fixture.manager); err != nil {
		t.Fatal(err)
	}
	if err := db.First(first, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(second, second.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.FastGPTProfileStatus != "ready" || first.FastGPTAppliedProfileID != fixture.profile.ID ||
		first.FastGPTAppliedProfileRevision != fixture.profile.Revision || first.FastGPTAppliedCredentialRevision != credential.Revision {
		t.Fatalf("selected knowledge base was not committed: %#v", first)
	}
	if second.FastGPTProfileStatus != "pending" || second.FastGPTAppliedProfileID != 0 ||
		second.FastGPTAppliedProfileRevision != 0 || second.FastGPTAppliedCredentialRevision != 0 {
		t.Fatalf("unselected knowledge base was falsely marked ready: %#v", second)
	}
}

func TestFastGPTCandidateProfileCommitPreservesActiveStoreRevision(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedFastGPTDatasetRuntime(t, fixture)
	candidate := &models.KnowledgeBase{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, DatasetID: "dataset-candidate", Name: "候选库",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileStatus: "pending", Status: enums.StatusOk,
	}
	if err := fixture.db.Create(candidate).Error; err != nil {
		t.Fatal(err)
	}
	seedReadyFastGPTStoreBinding(t, fixture, 1)
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(fixture.db, binding.ID, fixture.tenant.ID, map[string]any{
		"applied_profile_id": fixture.profile.ID + 100, "applied_profile_revision": fixture.profile.Revision + 100,
		"applied_credential_revision": 1, "applied_key_fingerprint": "active-old",
	}); err != nil {
		t.Fatal(err)
	}
	target, err := StoreModelCredentialService.loadActiveTargetDB(fixture.db, fixture.tenant.ID, fixture.store.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := &FastGPTModelProfile{ID: "candidate-profile", Name: "候选模型", Revision: 8}
	if err := commitManagedKnowledgeBaseFastGPTProfileDB(fixture.db, *target, 1, profile, candidate.ID, fixture.manager, time.Now()); err != nil {
		t.Fatal(err)
	}
	binding = repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if binding.AppliedProfileID != fixture.profile.ID+100 || binding.AppliedProfileRevision != fixture.profile.Revision+100 || binding.AppliedKeyFingerprint != "active-old" {
		t.Fatalf("candidate profile changed active binding: %#v", binding)
	}
	if err := fixture.db.First(candidate, candidate.ID).Error; err != nil {
		t.Fatal(err)
	}
	if candidate.FastGPTProfileStatus != "ready" || candidate.FastGPTAppliedProfileID != fixture.profile.ID ||
		candidate.FastGPTAppliedProfileRevision != fixture.profile.Revision || candidate.FastGPTAppliedCredentialRevision != 1 {
		t.Fatalf("candidate snapshot=%#v", candidate)
	}
}

func seedFastGPTDatasetRuntime(t *testing.T, fixture storeCredentialFixture) {
	t.Helper()
	if err := fixture.db.AutoMigrate(
		&models.ReplyIntentProfile{}, &models.ReplyIntentConfig{}, &models.IndustryTagDefinition{},
		&models.FastGPTDatasetJob{}, &models.ConversationRouteState{},
	); err != nil {
		t.Fatalf("migrate FastGPT runtime fixture: %v", err)
	}
	now := time.Now()
	profile := &models.ReplyIntentProfile{
		Code: "fastgpt-test-industry", Name: "FastGPT 测试行业", IndustryCode: "fastgpt_test",
		IntentDetectPrompt: "Classify the message.", IntentJSONSchema: `{"type":"object"}`,
		Revision: 1, PublishedAt: &now, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(profile).Error; err != nil {
		t.Fatalf("create industry profile: %v", err)
	}
	if err := fixture.db.Create(&models.ReplyIntentConfig{
		Code: "general", Name: "通用咨询", IntentProfileID: profile.ID, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create industry intent: %v", err)
	}
	parent := &models.IndustryTagDefinition{
		IntentProfileID: profile.ID, Name: "偏好", SemanticKey: "preference", DefinitionRevision: 1, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(parent).Error; err != nil {
		t.Fatalf("create industry tag parent: %v", err)
	}
	if err := fixture.db.Create(&models.IndustryTagDefinition{
		IntentProfileID: profile.ID, ParentID: parent.ID, Name: "安静", SemanticKey: "preference.quiet",
		DefinitionRevision: 1, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create industry tag leaf: %v", err)
	}
	if err := repositories.TenantRepository.Updates(fixture.db, fixture.tenant.ID, map[string]any{"intent_profile_id": profile.ID}); err != nil {
		t.Fatalf("bind tenant industry: %v", err)
	}
	fixture.tenant.IntentProfileID = profile.ID
	seedActiveStoreCredential(t, fixture, "sk-fastgpt-test", 1)
}

func seedReadyFastGPTStoreBinding(t *testing.T, fixture storeCredentialFixture, credentialRevision int64) {
	t.Helper()
	now := time.Now()
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(fixture.db, fixture.store.ID, fixture.tenant.ID)
	if binding == nil {
		binding = &models.FastGPTStoreTenant{
			TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, TenantTeamID: fmt.Sprintf("team-%d", fixture.store.ID),
			TenantTeamName: fixture.store.Name, Status: "active", ReadinessStatus: "ready",
			TargetProfileID: fixture.profile.ID, TargetProfileRevision: fixture.profile.Revision,
			AppliedProfileID: fixture.profile.ID, AppliedProfileRevision: fixture.profile.Revision,
			TargetCredentialRevision: credentialRevision, AppliedCredentialRevision: credentialRevision,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := repositories.FastGPTStoreTenantRepository.Save(fixture.db, binding); err != nil {
			t.Fatalf("create FastGPT Store binding: %v", err)
		}
		return
	}
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(fixture.db, binding.ID, fixture.tenant.ID, map[string]any{
		"status": "active", "readiness_status": "ready",
		"target_profile_id": fixture.profile.ID, "target_profile_revision": fixture.profile.Revision,
		"applied_profile_id": fixture.profile.ID, "applied_profile_revision": fixture.profile.Revision,
		"target_credential_revision": credentialRevision, "applied_credential_revision": credentialRevision,
		"updated_at": now,
	}); err != nil {
		t.Fatalf("update FastGPT Store binding: %v", err)
	}
}

func TestFastGPTDatasetJobsAreScopedToAuthorizedKnowledgeBase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.FastGPTDatasetJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	const tenantID int64 = 101
	store := &models.Store{TenantID: tenantID, Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	current := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-current", Name: "当前库", Status: enums.StatusOk}
	other := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-other", Name: "其他库", Status: enums.StatusOk}
	if err := db.Create(current).Error; err != nil {
		t.Fatalf("create current knowledge base: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other knowledge base: %v", err)
	}
	now := time.Now()
	if err := db.Create(&models.FastGPTDatasetJob{TenantID: tenantID, TaskKey: "job-current", StoreID: store.ID, KnowledgeBaseID: current.ID, Action: fastGPTJobActionUploadFile, Status: fastGPTJobStatusReady, Filename: "前厅资料.pdf", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create current job: %v", err)
	}
	if err := db.Create(&models.FastGPTDatasetJob{TenantID: tenantID, TaskKey: "job-other", StoreID: store.ID, KnowledgeBaseID: other.ID, Action: fastGPTJobActionUploadFile, Status: fastGPTJobStatusReady, Filename: "其他资料.pdf", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create other job: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeAdmin}}
	jobs, err := FastGPTDatasetService.ListJobs(current.ID, operator)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].KnowledgeBaseID != current.ID || jobs[0].Filename != "前厅资料.pdf" {
		t.Fatalf("jobs leaked across knowledge bases: %#v", jobs)
	}
}

func TestFastGPTDatasetDeletionRequiresExactKnowledgeBaseName(t *testing.T) {
	knowledgeBase := &models.KnowledgeBase{Name: "南七店前厅资料"}
	if err := validateDatasetDeletionConfirmation(knowledgeBase, "南七店前厅资料"); err != nil {
		t.Fatalf("exact confirmation should pass: %v", err)
	}
	if err := validateDatasetDeletionConfirmation(knowledgeBase, "南七店资料"); err == nil {
		t.Fatal("different confirmation name must be rejected")
	}
}

func TestFinalizeFastGPTDatasetDeletionClearsOnlyBoundInstances(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.WxWorkProtocolInstance{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	const tenantID int64 = 101
	store := &models.Store{TenantID: tenantID, Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	deleting := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-delete", Name: "待删除资料", Status: enums.StatusOk}
	other := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-keep", Name: "保留资料", Status: enums.StatusOk}
	if err := db.Create(deleting).Error; err != nil {
		t.Fatalf("create deleting knowledge base: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create retained knowledge base: %v", err)
	}
	if err := db.Model(store).Update("knowledge_base_id", deleting.ID).Error; err != nil {
		t.Fatalf("set store knowledge base: %v", err)
	}
	bound := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "instance-bound", StoreID: store.ID, KnowledgeBaseID: deleting.ID, Status: enums.StatusOk}
	retained := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "instance-retained", StoreID: store.ID, KnowledgeBaseID: other.ID, Status: enums.StatusOk}
	if err := db.Create(bound).Error; err != nil {
		t.Fatalf("create bound instance: %v", err)
	}
	if err := db.Create(retained).Error; err != nil {
		t.Fatalf("create retained instance: %v", err)
	}
	boundRoute := &models.ConversationRouteState{TenantID: tenantID, ConversationID: 101, WxWorkInstanceID: bound.ID, StoreID: store.ID, KnowledgeBaseID: deleting.ID}
	retainedRoute := &models.ConversationRouteState{TenantID: tenantID, ConversationID: 102, WxWorkInstanceID: retained.ID, StoreID: store.ID, KnowledgeBaseID: other.ID}
	if err := db.Create(boundRoute).Error; err != nil {
		t.Fatalf("create bound route: %v", err)
	}
	if err := db.Create(retainedRoute).Error; err != nil {
		t.Fatalf("create retained route: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeAdmin}}
	if err := FastGPTDatasetService.finalizeDatasetDeletion(deleting, operator); err != nil {
		t.Fatalf("finalize dataset deletion: %v", err)
	}

	updatedKnowledgeBase := KnowledgeBaseService.Get(deleting.ID)
	updatedStore := StoreService.Get(store.ID)
	updatedBound := WxWorkProtocolInstanceService.Get(bound.ID)
	updatedRetained := WxWorkProtocolInstanceService.Get(retained.ID)
	if updatedKnowledgeBase == nil || updatedKnowledgeBase.Status != enums.StatusDeleted {
		t.Fatalf("knowledge base was not marked deleted: %#v", updatedKnowledgeBase)
	}
	if updatedStore == nil || updatedStore.KnowledgeBaseID != 0 {
		t.Fatalf("store retained deleted knowledge base: %#v", updatedStore)
	}
	if updatedBound == nil || updatedBound.KnowledgeBaseID != 0 {
		t.Fatalf("bound instance retained deleted knowledge base: %#v", updatedBound)
	}
	if updatedRetained == nil || updatedRetained.KnowledgeBaseID != 0 {
		t.Fatalf("Store projection retained a conflicting knowledge base: %#v", updatedRetained)
	}
	updatedBoundRoute := repositories.ConversationRouteStateRepository.Get(db, boundRoute.ID)
	updatedRetainedRoute := repositories.ConversationRouteStateRepository.Get(db, retainedRoute.ID)
	if updatedBoundRoute == nil || updatedBoundRoute.KnowledgeBaseID != 0 || updatedRetainedRoute == nil || updatedRetainedRoute.KnowledgeBaseID != 0 {
		t.Fatalf("conversation route projections were not cleared: bound=%#v retained=%#v", updatedBoundRoute, updatedRetainedRoute)
	}
}
