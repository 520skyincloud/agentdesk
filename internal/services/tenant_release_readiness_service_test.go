package services

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestTenantReleaseReadinessConfigurationPilotAndTagGray(t *testing.T) {
	assertTenantReleaseReadinessStages(t, newTenantReleaseReadinessFixture(t))
}

func TestTenantReleaseReadinessMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("AGENT_DESK_RELEASE_READINESS_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("AGENT_DESK_RELEASE_READINESS_TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), tenantReleaseReadinessGORMConfig())
	if err != nil {
		t.Fatalf("open MySQL readiness database: %v", err)
	}
	testModels := tenantReleaseReadinessModels()
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
		t.Fatalf("disable MySQL foreign key checks: %v", err)
	}
	for i := len(testModels) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(testModels[i]); err != nil {
			t.Fatalf("reset MySQL readiness table: %v", err)
		}
	}
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil {
		t.Fatalf("enable MySQL foreign key checks: %v", err)
	}
	assertTenantReleaseReadinessStages(t, seedTenantReleaseReadinessFixture(t, db))
}

func assertTenantReleaseReadinessStages(t *testing.T, fixture *tenantReleaseReadinessFixture) {
	t.Helper()

	configuration := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	assertTenantReleaseReadinessPassed(t, configuration)
	if len(configuration.SelectedStoreIDs) != 1 ||
		configuration.SelectedStoreIDs[0] != fixture.store.ID {
		t.Fatalf("readiness report did not bind the selected Store: %#v", configuration.SelectedStoreIDs)
	}

	evidenceStart := fixture.now.Add(-10 * time.Minute)
	pilotWithoutEvidence := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	for _, code := range []string{
		"EVIDENCE_WXWORK_PROTOCOL_INBOUND",
		"EVIDENCE_WXWORK_PROTOCOL_OUTBOUND",
		"EVIDENCE_NEWAPI_CALL",
		"EVIDENCE_FASTGPT_RETRIEVAL",
		"EVIDENCE_CUSTOMER_AI_REPLY",
		"EVIDENCE_AI_HANDOFF",
		"EVIDENCE_RULE_ASSIGNMENT",
		"EVIDENCE_BILLING_RECONCILED",
	} {
		if !tenantReleaseReadinessHasViolation(pilotWithoutEvidence, code) {
			t.Fatalf("pilot report missing %s: %#v", code, pilotWithoutEvidence.Violations)
		}
	}

	fixture.seedPilotEvidence(t, evidenceStart)
	pilot := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	assertTenantReleaseReadinessPassed(t, pilot)

	if err := fixture.db.Model(&models.StoreCustomerTagRuntimePolicy{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Updates(map[string]any{
			"customer_tag_evolution_enabled": true,
			"reply_tag_context_enabled":      true,
			"updated_at":                     fixture.now,
		}).Error; err != nil {
		t.Fatalf("enable Store tag gray switches: %v", err)
	}
	if err := fixture.db.Create(&models.CustomerTagChangeLog{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID,
		CustomerID: 99, StoreCustomerRelationID: 100, ConversationID: fixture.conversation.ID,
		Action: "add", NewTagID: fixture.leafTag.ID, Source: customerTagSourceAI,
		Confidence: 0.95, OperatorType: "system", OperatorName: "system",
		CreatedAt: fixture.now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed AI tag change evidence: %v", err)
	}

	tagGray := fixture.audit(t, TenantReleaseReadinessTagGray, &evidenceStart)
	assertTenantReleaseReadinessPassed(t, tagGray)
}

func TestTenantReleaseReadinessReportNeverContainsSecretOrCustomerEvidence(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	report := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	assertTenantReleaseReadinessPassed(t, report)

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal readiness report: %v", err)
	}
	output := string(raw)
	for _, forbidden := range []string{
		"ciphertext-DO-NOT-PRINT",
		"nonce-DO-NOT-PRINT",
		"fingerprint-DO-NOT-PRINT",
		"intent-prompt-DO-NOT-PRINT",
		`"encryptedKey"`,
		`"keyNonce"`,
		`"keyFingerprint"`,
		`"promptTemplate"`,
		`"jsonSchema"`,
		`"customerId"`,
		`"conversationId"`,
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("readiness report leaked forbidden value or field %q: %s", forbidden, output)
		}
	}
}

func TestTenantReleaseReadinessRequiresCurrentModelProfileTestEvidence(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	if err := fixture.db.Where(
		"tenant_id = ? AND store_id = ?",
		fixture.tenant.ID,
		fixture.store.ID,
	).Delete(&models.ModelProfileTestRun{}).Error; err != nil {
		t.Fatalf("delete Model Profile test evidence: %v", err)
	}
	report := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(report, "STORE_MODEL_PROFILE_TEST_EVIDENCE") {
		t.Fatalf("readiness passed without current Model Profile test evidence: %#v", report.Violations)
	}
}

func TestTenantReleaseReadinessIgnoresPendingReplacementDraft(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	draft := models.WxWorkProtocolInstance{
		TenantID: fixture.tenant.ID, Guid: "release-readiness-pending-replacement",
		ChannelID: fixture.channel.ID, AgentTeamID: fixture.binding.AgentTeamID,
		StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID,
		EmployeeUserID: "pending-replacement", EmployeeName: "Pending replacement",
		ReplacesInstanceID: fixture.wxWork.ID, HealthStatus: "online", Status: enums.StatusOk,
		AuditFields: tenantReleaseReadinessAuditFields(fixture.now),
	}
	if err := fixture.db.Create(&draft).Error; err != nil {
		t.Fatalf("create pending replacement draft: %v", err)
	}

	report := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	assertTenantReleaseReadinessPassed(t, report)
	instanceCounts := repositories.StoreRepository.CountCurrentInstancesByStoreIDs(
		fixture.db,
		fixture.tenant.ID,
		[]int64{fixture.store.ID},
	)
	if instanceCounts[fixture.store.ID] != 1 {
		t.Fatalf("pending replacement counted as current instance: %v", instanceCounts)
	}
}

func TestTenantReleaseReadinessRejectsFutureOrMissingPilotEvidenceWindow(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	_, err := TenantReleaseReadinessService.Audit(fixture.db, TenantReleaseReadinessOptions{
		TenantID: fixture.tenant.ID, StoreIDs: []int64{fixture.store.ID},
		Level: TenantReleaseReadinessPilot, Now: fixture.now,
	})
	if err == nil || !strings.Contains(err.Error(), "evidence start time is required") {
		t.Fatalf("missing evidence start error=%v", err)
	}

	future := fixture.now.Add(time.Minute)
	_, err = TenantReleaseReadinessService.Audit(fixture.db, TenantReleaseReadinessOptions{
		TenantID: fixture.tenant.ID, StoreIDs: []int64{fixture.store.ID},
		Level: TenantReleaseReadinessPilot, EvidenceStart: &future, Now: fixture.now,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be in the future") {
		t.Fatalf("future evidence start error=%v", err)
	}
}

func TestTenantReleaseReadinessRequiresReadyStoreBindingAndSupervisorApprovalEvidence(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	evidenceStart := fixture.now.Add(-10 * time.Minute)
	fixture.seedPilotEvidence(t, evidenceStart)

	if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
		Where("id = ? AND tenant_id = ?", fixture.wxWork.ID, fixture.tenant.ID).
		Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable WxWork protocol instance: %v", err)
	}
	wxWorkReport := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(wxWorkReport, "STORE_WXWORK_PROTOCOL") {
		t.Fatalf("configuration without an active WxWork protocol instance must fail: %#v", wxWorkReport.Violations)
	}
	if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
		Where("id = ? AND tenant_id = ?", fixture.wxWork.ID, fixture.tenant.ID).
		Update("status", enums.StatusOk).Error; err != nil {
		t.Fatalf("restore WxWork protocol instance: %v", err)
	}

	if err := fixture.db.Model(&models.StoreStaffBinding{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("active_user_id", nil).Error; err != nil {
		t.Fatalf("clear active Store account ownership: %v", err)
	}
	accountReport := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(accountReport, "STORE_SYSTEM_ACCOUNT") {
		t.Fatalf("missing active account ownership marker must fail readiness: %#v", accountReport.Violations)
	}
	if err := fixture.db.Model(&models.StoreStaffBinding{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("active_user_id", fixture.account.ID).Error; err != nil {
		t.Fatalf("restore active Store account ownership: %v", err)
	}

	if err := fixture.db.Model(&models.StoreCredentialPolicy{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("require_supervisor_approval", false).Error; err != nil {
		t.Fatalf("disable supervisor approval policy: %v", err)
	}
	policyReport := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(policyReport, "STORE_CREDENTIAL_SELF_SERVICE_POLICY") {
		t.Fatalf("pilot without mandatory supervisor policy must fail: %#v", policyReport.Violations)
	}
	if err := fixture.db.Model(&models.StoreCredentialPolicy{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("require_supervisor_approval", true).Error; err != nil {
		t.Fatalf("restore supervisor approval policy: %v", err)
	}

	if err := fixture.db.Model(&models.StoreModelCredentialAuditLog{}).
		Where(
			"tenant_id = ? AND store_id = ? AND action = ?",
			fixture.tenant.ID,
			fixture.store.ID,
			enums.CredentialAuditActionSubmit,
		).
		Update("operator_id", fixture.account.ID+2000).Error; err != nil {
		t.Fatalf("replace Credential submitter: %v", err)
	}
	submitterReport := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(submitterReport, "EVIDENCE_CREDENTIAL_SUPERVISOR_APPROVAL") {
		t.Fatalf("pilot Credential not submitted by the active Store account must fail: %#v", submitterReport.Violations)
	}
	if err := fixture.db.Model(&models.StoreModelCredentialAuditLog{}).
		Where(
			"tenant_id = ? AND store_id = ? AND action = ?",
			fixture.tenant.ID,
			fixture.store.ID,
			enums.CredentialAuditActionSubmit,
		).
		Update("operator_id", fixture.account.ID).Error; err != nil {
		t.Fatalf("restore Credential submitter: %v", err)
	}

	if err := fixture.db.Model(&models.StoreModelCredentialAuditLog{}).
		Where(
			"tenant_id = ? AND store_id = ? AND action = ?",
			fixture.tenant.ID,
			fixture.store.ID,
			enums.CredentialAuditActionApprove,
		).
		Update("operator_role", constants.RoleCodeAdmin).Error; err != nil {
		t.Fatalf("replace supervisor role snapshot: %v", err)
	}
	approvalReport := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(approvalReport, "EVIDENCE_CREDENTIAL_SUPERVISOR_APPROVAL") {
		t.Fatalf("pilot without company-supervisor audit evidence must fail: %#v", approvalReport.Violations)
	}
}

func TestTenantReleaseReadinessSupportsMultipleReadyStoreBindings(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	second := fixture.seedReadyStoreBinding(t, "second")

	assertTenantReleaseReadinessPassed(t, fixture.audit(t, TenantReleaseReadinessConfiguration, nil))

	if err := fixture.db.Where("store_staff_binding_id = ?", second.binding.ID).
		Delete(&models.ModelProfileTestRun{}).Error; err != nil {
		t.Fatalf("delete second binding Profile evidence: %v", err)
	}
	missingEvidence := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(missingEvidence, "STORE_MODEL_PROFILE_TEST_EVIDENCE") {
		t.Fatalf("every active binding must have its own Profile evidence: %#v", missingEvidence.Violations)
	}
	second.seedProfileEvidence(t, fixture)

	duplicate := second.instance
	duplicate.ID = 0
	duplicate.Guid = "readiness-wxwork-guid-second-duplicate"
	duplicate.EmployeeUserID = "168-readiness-second-duplicate"
	duplicate.ReplacesInstanceID = 0
	duplicate.ReplacedByInstanceID = 0
	if err := fixture.db.Create(&duplicate).Error; err != nil {
		t.Fatalf("create duplicate current WxWork instance: %v", err)
	}
	duplicateReport := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(duplicateReport, "STORE_WXWORK_PROTOCOL") {
		t.Fatalf("two current instances for one binding must fail: %#v", duplicateReport.Violations)
	}
	if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", duplicate.ID).
		Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable duplicate current WxWork instance: %v", err)
	}

	orphan := second.instance
	orphan.ID = 0
	orphan.Guid = "readiness-wxwork-guid-orphan"
	orphan.EmployeeUserID = "168-readiness-orphan"
	orphan.StoreStaffBindingID = second.binding.ID + 100000
	orphan.ReplacesInstanceID = 0
	orphan.ReplacedByInstanceID = 0
	if err := fixture.db.Create(&orphan).Error; err != nil {
		t.Fatalf("create orphan current WxWork instance: %v", err)
	}
	orphanReport := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
	if !tenantReleaseReadinessHasViolation(orphanReport, "STORE_WXWORK_PROTOCOL") {
		t.Fatalf("orphan current instance must fail: %#v", orphanReport.Violations)
	}
}

func TestTenantReleaseReadinessUsesStoreOwnedRuntimeResources(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
		Where("id = ? AND tenant_id = ?", fixture.wxWork.ID, fixture.tenant.ID).
		Updates(map[string]any{
			"store_contact_phone":   "",
			"store_longitude":       "181",
			"store_latitude":        "91",
			"store_navigation_name": "stale instance navigation",
			"store_address":         "stale instance address",
		}).Error; err != nil {
		t.Fatalf("stale instance Store snapshots: %v", err)
	}

	assertTenantReleaseReadinessPassed(t, fixture.audit(t, TenantReleaseReadinessConfiguration, nil))
}

func TestTenantReleaseReadinessRequiresCompleteRuntimeResources(t *testing.T) {
	tests := []struct {
		name            string
		storeUpdates    map[string]any
		instanceUpdates map[string]any
		violation       string
	}{
		{
			name:         "phone",
			storeUpdates: map[string]any{"contact_phone": ""},
			violation:    "STORE_RESOURCE_PHONE",
		},
		{
			name:         "location",
			storeUpdates: map[string]any{"longitude": "181"},
			violation:    "STORE_RESOURCE_LOCATION",
		},
		{
			name:            "mini_program",
			instanceUpdates: map[string]any{"default_mini_program_payload": `{"title":"missing protocol fields"}`},
			violation:       "STORE_RESOURCE_MINI_PROGRAM",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTenantReleaseReadinessFixture(t)
			if test.storeUpdates != nil {
				if err := fixture.db.Model(&models.Store{}).
					Where("id = ? AND tenant_id = ?", fixture.store.ID, fixture.tenant.ID).
					Updates(test.storeUpdates).Error; err != nil {
					t.Fatalf("break %s Store resource: %v", test.name, err)
				}
			}
			if test.instanceUpdates != nil {
				if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
					Where("id = ? AND tenant_id = ?", fixture.wxWork.ID, fixture.tenant.ID).
					Updates(test.instanceUpdates).Error; err != nil {
					t.Fatalf("break %s instance resource: %v", test.name, err)
				}
			}
			report := fixture.audit(t, TenantReleaseReadinessConfiguration, nil)
			if !tenantReleaseReadinessHasViolation(report, test.violation) {
				t.Fatalf("readiness passed with invalid %s resource: %#v", test.name, report.Violations)
			}
		})
	}
}

func TestTenantReleaseReadinessEvidenceIsScopedToOneStoreStaffBinding(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	evidenceStart := fixture.now.Add(-10 * time.Minute)
	fixture.seedPilotEvidence(t, evidenceStart)
	second := fixture.seedReadyStoreBinding(t, "evidence-scope")

	evidence, err := repositories.TenantReleaseReadinessRepository.FindEvidence(
		fixture.db,
		fixture.tenant.ID,
		[]int64{fixture.store.ID},
		evidenceStart,
		tenantReleaseReadinessEvidenceFilterForTest(),
	)
	if err != nil {
		t.Fatalf("read binding-scoped release evidence: %v", err)
	}
	item := evidence[fixture.store.ID]
	if item.SuccessfulNewAPICallCount != 1 || item.FastGPTRetrievalCount != 1 || item.ReconciledBillingCount != 1 {
		t.Fatalf("second binding duplicated first binding evidence: %+v", item)
	}

	if err := fixture.db.Model(&models.AIUsageEvent{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("store_staff_binding_id", second.binding.ID).Error; err != nil {
		t.Fatalf("move usage evidence to mismatched binding: %v", err)
	}
	if err := fixture.db.Model(&models.AIUsageGatewayCall{}).
		Where("tenant_id = ? AND store_id = ?", fixture.tenant.ID, fixture.store.ID).
		Update("store_staff_binding_id", second.binding.ID).Error; err != nil {
		t.Fatalf("move billing evidence to mismatched binding: %v", err)
	}
	evidence, err = repositories.TenantReleaseReadinessRepository.FindEvidence(
		fixture.db,
		fixture.tenant.ID,
		[]int64{fixture.store.ID},
		evidenceStart,
		tenantReleaseReadinessEvidenceFilterForTest(),
	)
	if err != nil {
		t.Fatalf("read mismatched binding release evidence: %v", err)
	}
	item = evidence[fixture.store.ID]
	if item.SuccessfulNewAPICallCount != 0 || item.FastGPTRetrievalCount != 0 || item.ReconciledBillingCount != 0 {
		t.Fatalf("evidence borrowed a credential or instance from another binding: %+v", item)
	}
}

func tenantReleaseReadinessEvidenceFilterForTest() repositories.TenantReleaseReadinessEvidenceFilter {
	return repositories.TenantReleaseReadinessEvidenceFilter{
		NewAPIGateway:            AIUsageGatewayNewAPI,
		SuccessfulUsageStatuses:  []string{"completed", "success"},
		KnowledgeRetrieveStage:   "knowledge_retrieve",
		KnowledgeProvider:        enums.KnowledgeProviderFastGPT,
		KnowledgeOperation:       "knowledge_retrieve",
		KnowledgeStatus:          "completed",
		KnowledgeConnectionID:    fastgptapi.ManagedConnectionID,
		KnowledgeLogSourceType:   "fastgpt",
		KnowledgeChunkProvider:   string(enums.KnowledgeChunkProviderFastGPT),
		KnowledgeChannel:         string(enums.KnowledgeRetrieveChannelIM),
		KnowledgeScene:           string(enums.KnowledgeRetrieveSceneFirstResponse),
		KnowledgeAnswerStatus:    int(enums.KnowledgeAnswerStatusNormal),
		AIHandoffContent:         "AI转人工",
		ReconcileStatus:          AIUsageReconcileCompleted,
		ReconcileMatchStrategy:   AIUsageMatchStrategyRequestID,
		ReconcileMatchConfidence: AIUsageMatchConfidenceExact,
		AITagSource:              customerTagSourceAI,
		WxWorkProtocolSource:     "wxwork_protocol",
		WxWorkProtocolTarget:     "agentdesk",
	}
}

func TestTenantReleaseReadinessFastGPTEvidenceMustMatchRuntimeLogAndCurrentRevision(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	evidenceStart := fixture.now.Add(-10 * time.Minute)
	fixture.seedPilotEvidence(t, evidenceStart)

	if err := fixture.db.Where("tenant_id = ? AND request_id = ?", fixture.tenant.ID, "pilot-fastgpt-request").
		Delete(&models.KnowledgeRetrieveLog{}).Error; err != nil {
		t.Fatalf("delete correlated FastGPT retrieve log: %v", err)
	}
	withoutLog := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(withoutLog, "EVIDENCE_FASTGPT_RETRIEVAL") {
		t.Fatalf("runtime usage without correlated retrieve log must fail: %#v", withoutLog.Violations)
	}

	if err := fixture.db.Create(&models.KnowledgeRetrieveLog{
		TenantID: fixture.tenant.ID, KnowledgeBaseID: fixture.store.KnowledgeBaseID,
		SourceType: "fastgpt", Channel: string(enums.KnowledgeRetrieveChannelIM),
		Scene: string(enums.KnowledgeRetrieveSceneFirstResponse), ConversationID: fixture.conversation.ID,
		RequestID: "pilot-fastgpt-request", AnswerStatus: int(enums.KnowledgeAnswerStatusNormal),
		HitCount: 1, UsedChunkCount: 1, ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT),
		CreatedAt: fixture.now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("restore correlated FastGPT retrieve log: %v", err)
	}
	if err := fixture.db.Model(&models.AIUsageEvent{}).
		Where("tenant_id = ? AND event_key = ?", fixture.tenant.ID, "pilot-fastgpt-usage").
		Update("model_profile_revision", fixture.modelProfile.Revision+1).Error; err != nil {
		t.Fatalf("make FastGPT usage revision stale: %v", err)
	}
	withStaleRevision := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(withStaleRevision, "EVIDENCE_FASTGPT_RETRIEVAL") {
		t.Fatalf("stale FastGPT usage revision must fail: %#v", withStaleRevision.Violations)
	}

	if err := fixture.db.Model(&models.AIUsageEvent{}).
		Where("tenant_id = ? AND event_key = ?", fixture.tenant.ID, "pilot-fastgpt-usage").
		Update("model_profile_revision", fixture.modelProfile.Revision).Error; err != nil {
		t.Fatalf("restore FastGPT usage revision: %v", err)
	}
	assertTenantReleaseReadinessPassed(t, fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart))
}

func TestTenantReleaseReadinessRejectsDashboardSimulationEvidence(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	evidenceStart := fixture.now.Add(-10 * time.Minute)
	fixture.seedPilotEvidence(t, evidenceStart)
	assertTenantReleaseReadinessPassed(t, fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart))

	if err := fixture.db.Where(
		"tenant_id = ? AND conversation_id = ? AND direction = ?",
		fixture.tenant.ID,
		fixture.conversation.ID,
		enums.WxWorkKFMessageDirectionIn,
	).Delete(&models.WxWorkKFMessageRef{}).Error; err != nil {
		t.Fatalf("delete real WxWork inbound message reference: %v", err)
	}
	withoutInboundRef := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	if !tenantReleaseReadinessHasViolation(withoutInboundRef, "EVIDENCE_WXWORK_PROTOCOL_INBOUND") {
		t.Fatalf("sync log without inbound message reference must not satisfy real WxWork evidence: %#v", withoutInboundRef.Violations)
	}

	if err := fixture.db.Where(
		"tenant_id = ? AND conversation_id = ?",
		fixture.tenant.ID,
		fixture.conversation.ID,
	).Delete(&models.MessageSyncLog{}).Error; err != nil {
		t.Fatalf("delete real WxWork inbound evidence: %v", err)
	}
	if err := fixture.db.Where(
		"tenant_id = ? AND conversation_id = ?",
		fixture.tenant.ID,
		fixture.conversation.ID,
	).Delete(&models.ChannelMessageOutbox{}).Error; err != nil {
		t.Fatalf("delete real WxWork outbound evidence: %v", err)
	}
	if err := fixture.db.Where(
		"tenant_id = ? AND conversation_id = ?",
		fixture.tenant.ID,
		fixture.conversation.ID,
	).Delete(&models.WxWorkKFMessageRef{}).Error; err != nil {
		t.Fatalf("delete real WxWork message references: %v", err)
	}

	report := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	for _, code := range []string{
		"EVIDENCE_WXWORK_PROTOCOL_INBOUND",
		"EVIDENCE_WXWORK_PROTOCOL_OUTBOUND",
		"EVIDENCE_NEWAPI_CALL",
		"EVIDENCE_FASTGPT_RETRIEVAL",
		"EVIDENCE_CUSTOMER_AI_REPLY",
		"EVIDENCE_AI_HANDOFF",
		"EVIDENCE_RULE_ASSIGNMENT",
		"EVIDENCE_BILLING_RECONCILED",
	} {
		if !tenantReleaseReadinessHasViolation(report, code) {
			t.Fatalf("dashboard-only evidence must not satisfy %s: %#v", code, report.Violations)
		}
	}
}

func TestTenantReleaseReadinessCapturesReleaseCursorsWithoutPayloads(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	evidenceStart := fixture.now.Add(-10 * time.Minute)
	fixture.seedPilotEvidence(t, evidenceStart)
	if err := fixture.db.Create(&models.ChannelMessageOutbox{
		TenantID: fixture.tenant.ID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: fixture.conversation.ID, MessageID: 99,
		Payload:     "outbox-payload-DO-NOT-PRINT",
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: tenantReleaseReadinessAuditFields(fixture.now),
	}).Error; err != nil {
		t.Fatalf("create release cursor Outbox: %v", err)
	}

	report := fixture.audit(t, TenantReleaseReadinessPilot, &evidenceStart)
	assertTenantReleaseReadinessPassed(t, report)
	cursor := report.ReleaseCursor
	if cursor.MessageMaxID <= 0 || cursor.MessageCount != 2 ||
		cursor.OutboxMaxID <= 0 || cursor.OutboxCount != 2 || cursor.UnsettledOutboxCount != 1 ||
		cursor.AssignmentMaxID <= 0 || cursor.AssignmentCount != 1 || cursor.ActiveAssignmentCount != 1 {
		t.Fatalf("unexpected release cursor snapshot: %#v", cursor)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal release cursor report: %v", err)
	}
	if strings.Contains(string(raw), "outbox-payload-DO-NOT-PRINT") {
		t.Fatalf("release cursor report leaked Outbox payload: %s", raw)
	}
}

func TestTenantReleaseReadinessSamplesOnlyConfiguredNumberOfStoreIDs(t *testing.T) {
	fixture := newTenantReleaseReadinessFixture(t)
	report, err := TenantReleaseReadinessService.Audit(fixture.db, TenantReleaseReadinessOptions{
		TenantID: fixture.tenant.ID,
		StoreIDs: []int64{fixture.store.ID + 100, fixture.store.ID + 101, fixture.store.ID + 102},
		Level:    TenantReleaseReadinessConfiguration, SampleLimit: 2, Now: fixture.now,
	})
	if err != nil {
		t.Fatalf("audit invalid Store selection: %v", err)
	}
	if !report.HasViolations() {
		t.Fatal("unknown Stores must fail readiness")
	}
	for _, violation := range report.Violations {
		if violation.Code == "STORE_SELECTION" {
			if violation.Count != 3 || len(violation.SampleStoreIDs) != 2 {
				t.Fatalf("Store selection violation=%#v", violation)
			}
			return
		}
	}
	t.Fatalf("Store selection violation missing: %#v", report.Violations)
}

type tenantReleaseReadinessFixture struct {
	db           *gorm.DB
	now          time.Time
	tenant       models.Tenant
	store        models.Store
	account      models.User
	binding      models.StoreStaffBinding
	channel      models.Channel
	wxWork       models.WxWorkProtocolInstance
	modelProfile models.ModelProfileTemplate
	credential   models.StoreModelCredential
	conversation models.Conversation
	leafTag      models.Tag
}

const tenantReleaseReadinessMiniProgramPayload = `{
	"username":"gh_readiness@app",
	"title":"Readiness mini program",
	"page_path":"pages/index/index",
	"file_id":"readiness-cover-file",
	"aes_key":"readiness-cover-key",
	"md5":"readiness-cover-md5",
	"size":20810
}`

type tenantReleaseReadinessBindingFixture struct {
	account    models.User
	binding    models.StoreStaffBinding
	instance   models.WxWorkProtocolInstance
	credential models.StoreModelCredential
}

func (f *tenantReleaseReadinessFixture) seedReadyStoreBinding(
	t *testing.T,
	suffix string,
) *tenantReleaseReadinessBindingFixture {
	t.Helper()
	audit := tenantReleaseReadinessAuditFields(f.now)
	readyAt := f.now.Add(-time.Hour)
	account := models.User{
		TenantID: f.tenant.ID, Username: "release-readiness-" + suffix,
		Nickname: "Readiness " + suffix, Password: "hash",
		ApprovalStatus: enums.UserApprovalStatusApproved, ApprovedAt: &readyAt,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := f.db.Create(&account).Error; err != nil {
		t.Fatalf("create %s Store account: %v", suffix, err)
	}
	binding := models.StoreStaffBinding{
		TenantID: f.tenant.ID, UserID: account.ID, ActiveUserID: positiveInt64Pointer(account.ID),
		StoreID: f.store.ID, AgentTeamID: f.binding.AgentTeamID,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := f.db.Create(&binding).Error; err != nil {
		t.Fatalf("create %s Store binding: %v", suffix, err)
	}
	instance := models.WxWorkProtocolInstance{
		TenantID: f.tenant.ID, AgentTeamID: binding.AgentTeamID,
		Guid: "readiness-wxwork-guid-" + suffix, ChannelID: f.channel.ID,
		EmployeeUserID: "168-readiness-" + suffix, EmployeeName: "Readiness " + suffix,
		StoreID: f.store.ID, StoreStaffBindingID: binding.ID,
		DefaultMiniProgramPayload: tenantReleaseReadinessMiniProgramPayload,
		NotifyURL:                 "https://readiness.example.com/api/third/wxwork-protocol/callback",
		HealthStatus:              "online", LastHeartbeatAt: &readyAt,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := f.db.Create(&instance).Error; err != nil {
		t.Fatalf("create %s WxWork instance: %v", suffix, err)
	}
	credential := models.StoreModelCredential{
		TenantID: f.tenant.ID, StoreID: f.store.ID, StoreStaffBindingID: binding.ID,
		EncryptedKey: "ciphertext-DO-NOT-PRINT", KeyNonce: "nonce-DO-NOT-PRINT",
		KeyFingerprint: "fingerprint-DO-NOT-PRINT-" + suffix, CipherVersion: "aes-gcm-v1",
		MasterKeyID: "release-key", CredentialRevision: 1,
		Status:         enums.StoreCredentialStatusActive,
		LastTestStatus: "passed", LastTestedAt: &readyAt, AuditFields: audit,
	}
	if err := f.db.Create(&credential).Error; err != nil {
		t.Fatalf("create %s Credential: %v", suffix, err)
	}
	ret := &tenantReleaseReadinessBindingFixture{
		account: account, binding: binding, instance: instance, credential: credential,
	}
	ret.seedProfileEvidence(t, f)
	return ret
}

func (f *tenantReleaseReadinessBindingFixture) seedProfileEvidence(
	t *testing.T,
	fixture *tenantReleaseReadinessFixture,
) {
	t.Helper()
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(fixture.db, fixture.modelProfile.ID)
	run := models.ModelProfileTestRun{
		TemplateID: fixture.modelProfile.ID, TemplateRevision: fixture.modelProfile.Revision,
		ConfigDigest: modelProfileConfigurationDigest(&fixture.modelProfile, slots),
		TenantID:     fixture.tenant.ID, TenantName: fixture.tenant.ShortName,
		StoreID: fixture.store.ID, StoreStaffBindingID: f.binding.ID, StoreName: fixture.store.Name,
		CredentialRevision: f.credential.CredentialRevision,
		CredentialSource:   enums.ModelProfileTestCredentialSourceCandidate,
		Status:             enums.ModelProfileTestStatusPassed,
		OperatorID:         f.account.ID, OperatorName: f.account.Username,
		CreatedAt: fixture.now.Add(-time.Hour),
	}
	if err := fixture.db.Create(&run).Error; err != nil {
		t.Fatalf("create Profile evidence for binding %d: %v", f.binding.ID, err)
	}
}

func newTenantReleaseReadinessFixture(t *testing.T) *tenantReleaseReadinessFixture {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:tenant-release-readiness-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		tenantReleaseReadinessGORMConfig(),
	)
	if err != nil {
		t.Fatalf("open readiness fixture database: %v", err)
	}
	return seedTenantReleaseReadinessFixture(t, db)
}

func tenantReleaseReadinessGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	}
}

func tenantReleaseReadinessModels() []any {
	return []any{
		&models.Tenant{},
		&models.ReplyIntentProfile{},
		&models.ReplyIntentConfig{},
		&models.IndustryTagDefinition{},
		&models.TenantCustomerTagPolicy{},
		&models.Tag{},
		&models.User{},
		&models.Store{},
		&models.StoreStaffBinding{},
		&models.Channel{},
		&models.WxWorkProtocolInstance{},
		&models.StoreCustomerTagRuntimePolicy{},
		&models.ModelProfileTemplate{},
		&models.ModelProfileSlot{},
		&models.ModelProfileTestRun{},
		&models.StoreModelProfileAssignment{},
		&models.StoreModelCredential{},
		&models.StoreCredentialPolicy{},
		&models.StoreModelCredentialAuditLog{},
		&models.FastGPTStoreTenant{},
		&models.KnowledgeBase{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.Message{},
		&models.MessageSyncLog{},
		&models.WxWorkKFMessageRef{},
		&models.ChannelMessageOutbox{},
		&models.KnowledgeRetrieveLog{},
		&models.ConversationEventLog{},
		&models.ConversationAssignment{},
		&models.AIUsageEvent{},
		&models.AIUsageGatewayCall{},
		&models.CustomerTagChangeLog{},
	}
}

func seedTenantReleaseReadinessFixture(t *testing.T, db *gorm.DB) *tenantReleaseReadinessFixture {
	t.Helper()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	if err := db.AutoMigrate(tenantReleaseReadinessModels()...); err != nil {
		t.Fatalf("migrate readiness fixture: %v", err)
	}
	audit := tenantReleaseReadinessAuditFields(now)
	publishedAt := now.Add(-time.Hour)
	industry := models.ReplyIntentProfile{
		Code: "readiness-hotel", Name: "Readiness hotel",
		IndustryCode: "readiness_hotel", IntentDetectPrompt: "intent-prompt-DO-NOT-PRINT",
		IntentJSONSchema: `{"type":"object"}`, Revision: 1,
		PublishedAt: &publishedAt, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&industry).Error; err != nil {
		t.Fatalf("create readiness industry: %v", err)
	}
	if err := db.Create(&models.ReplyIntentConfig{
		Code: "service_request", Name: "Service request", IntentProfileID: industry.ID,
		MatchMode: "hybrid", Status: enums.StatusOk, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness intent config: %v", err)
	}
	parentDefinition := models.IndustryTagDefinition{
		IntentProfileID: industry.ID, Name: "Preference", SemanticKey: "preference",
		DefinitionRevision: 1, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&parentDefinition).Error; err != nil {
		t.Fatalf("create parent tag definition: %v", err)
	}
	leafDefinition := models.IndustryTagDefinition{
		IntentProfileID: industry.ID, ParentID: parentDefinition.ID,
		Name: "Quiet", SemanticKey: "preference.quiet", ConflictGroup: "room_preference",
		ApplicableScene: "room", AIEnabled: true, ReplyEnabled: true,
		DefinitionRevision: 1, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&leafDefinition).Error; err != nil {
		t.Fatalf("create leaf tag definition: %v", err)
	}
	tenant := models.Tenant{
		IntentProfileID: industry.ID, TenantCode: "release-readiness",
		LegalName: "Release Readiness Tenant", ShortName: "Readiness",
		RegistrationType: "test", RegistrationNo: "release-readiness",
		VerificationStatus: enums.TenantVerificationStatusVerified,
		VerifiedAt:         &publishedAt, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create readiness Tenant: %v", err)
	}
	if err := db.Create(&models.TenantCustomerTagPolicy{
		TenantID: tenant.ID, IntentProfileID: industry.ID,
		QuietPeriodMinutes: 1440, MinimumConfidence: 0.8, MaxOperationsPerRun: 6,
		Status: enums.StatusOk, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness Tenant tag policy: %v", err)
	}
	parentDefinitionID := parentDefinition.ID
	parentTag := models.Tag{
		TenantID: tenant.ID, IntentProfileID: industry.ID, TemplateDefinitionID: &parentDefinitionID,
		Name: parentDefinition.Name, SemanticKey: parentDefinition.SemanticKey,
		SystemDefined: true, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&parentTag).Error; err != nil {
		t.Fatalf("create readiness parent Tag: %v", err)
	}
	leafDefinitionID := leafDefinition.ID
	leafTag := models.Tag{
		TenantID: tenant.ID, IntentProfileID: industry.ID, TemplateDefinitionID: &leafDefinitionID,
		ParentID: parentTag.ID, Name: leafDefinition.Name, SemanticKey: leafDefinition.SemanticKey,
		Aliases: leafDefinition.Aliases, ConflictGroup: leafDefinition.ConflictGroup,
		ApplicableScene: leafDefinition.ApplicableScene, AIEnabled: leafDefinition.AIEnabled,
		ReplyEnabled: leafDefinition.ReplyEnabled, SystemDefined: true,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&leafTag).Error; err != nil {
		t.Fatalf("create readiness leaf Tag: %v", err)
	}
	account := models.User{
		TenantID: tenant.ID, Username: "release-readiness-store",
		Nickname: "Readiness Store", Password: "hash",
		ApprovalStatus: enums.UserApprovalStatusApproved, ApprovedAt: &publishedAt,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create readiness Store account: %v", err)
	}
	store := models.Store{
		TenantID: tenant.ID, StoreCode: "READINESS-STORE", Name: "Readiness Store",
		Address: "Readiness Road 1", NavigationName: "Readiness Store",
		Longitude: "117.263908", Latitude: "31.824097", MapProvider: "tencent",
		ContactPhone: "0551-88886666",
		Status:       enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&store).Error; err != nil {
		t.Fatalf("create readiness Store: %v", err)
	}
	binding := models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: account.ID, ActiveUserID: positiveInt64Pointer(account.ID), StoreID: store.ID,
		AgentTeamID: 1, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create readiness Store binding: %v", err)
	}
	channel := models.Channel{
		TenantID: tenant.ID, Name: "Readiness WxWork protocol",
		ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "readiness-wxwork-protocol",
		ConfigJSON: `{}`, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create readiness WxWork protocol Channel: %v", err)
	}
	wxWork := models.WxWorkProtocolInstance{
		TenantID: tenant.ID, AgentTeamID: binding.AgentTeamID,
		Guid: "readiness-wxwork-guid", ChannelID: channel.ID,
		EmployeeUserID: "168-readiness", EmployeeName: "Readiness WxWork",
		StoreID: store.ID, StoreStaffBindingID: binding.ID,
		DefaultMiniProgramPayload: tenantReleaseReadinessMiniProgramPayload,
		NotifyURL:                 "https://readiness.example.com/api/third/wxwork-protocol/callback",
		HealthStatus:              "online", LastHeartbeatAt: &publishedAt,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&wxWork).Error; err != nil {
		t.Fatalf("create readiness WxWork protocol instance: %v", err)
	}
	modelProfile := models.ModelProfileTemplate{
		Code: "readiness-standard", Name: "Readiness standard",
		Revision: 1, GatewayBaseURL: "https://newapi.example.com/v1",
		Status: enums.ModelProfileStatusActive, PublishedAt: &publishedAt, AuditFields: audit,
	}
	if err := db.Create(&modelProfile).Error; err != nil {
		t.Fatalf("create readiness Model Profile: %v", err)
	}
	slots := completeModelProfileSlotsForTest(modelProfile.ID)
	if err := db.Create(&slots).Error; err != nil {
		t.Fatalf("create readiness Model Profile slots: %v", err)
	}
	if err := db.Create(&models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID,
		TemplateID: modelProfile.ID, TemplateRevision: modelProfile.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready",
		LastValidatedAt: &publishedAt, LastReadyAt: &publishedAt,
		AssignedAt: publishedAt, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness Model Profile assignment: %v", err)
	}
	credential := models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		EncryptedKey: "ciphertext-DO-NOT-PRINT", KeyNonce: "nonce-DO-NOT-PRINT",
		KeyFingerprint: "fingerprint-DO-NOT-PRINT", CipherVersion: "aes-gcm-v1",
		MasterKeyID: "release-key", CredentialRevision: 1,
		Status:         enums.StoreCredentialStatusActive,
		LastTestStatus: "passed", LastTestedAt: &publishedAt,
		LastFastGPTSyncStatus: storeCredentialFastGPTStatusReady,
		LastFastGPTSyncedAt:   &publishedAt, AuditFields: audit,
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create readiness Credential: %v", err)
	}
	if err := db.Create(&models.ModelProfileTestRun{
		TemplateID: modelProfile.ID, TemplateRevision: modelProfile.Revision,
		ConfigDigest: modelProfileConfigurationDigest(&modelProfile, slots),
		TenantID:     tenant.ID, TenantName: tenant.ShortName,
		StoreID: store.ID, StoreStaffBindingID: binding.ID, StoreName: store.Name,
		CredentialRevision: credential.CredentialRevision,
		CredentialSource:   enums.ModelProfileTestCredentialSourceCandidate,
		Status:             enums.ModelProfileTestStatusPassed,
		OperatorID:         account.ID, OperatorName: account.Username,
		CreatedAt: publishedAt,
	}).Error; err != nil {
		t.Fatalf("create readiness Model Profile test evidence: %v", err)
	}
	if err := db.Create(&models.StoreCredentialPolicy{
		TenantID: tenant.ID, StoreID: store.ID,
		AllowCredentialSelfService: true, RequireSupervisorApproval: true,
		Status: enums.StatusOk, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness Credential policy: %v", err)
	}
	if err := db.Create(&[]models.StoreModelCredentialAuditLog{
		{
			TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID, CredentialID: credential.ID,
			Action: enums.CredentialAuditActionSubmit, Result: enums.CredentialAuditResultPending,
			ToRevision: credential.CredentialRevision, OperatorID: account.ID,
			OperatorName: account.Username, OperatorRole: constants.RoleCodeStoreStaff,
			CreatedAt: publishedAt.Add(-2 * time.Minute),
		},
		{
			TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID, CredentialID: credential.ID,
			Action: enums.CredentialAuditActionApprove, Result: enums.CredentialAuditResultSuccess,
			ToRevision: credential.CredentialRevision, OperatorID: account.ID + 1000,
			OperatorName: "readiness-supervisor", OperatorRole: constants.RoleCodeTenantAdmin,
			ApproverID: account.ID + 1000, ApproverName: "readiness-supervisor",
			CreatedAt: publishedAt.Add(-time.Minute),
		},
	}).Error; err != nil {
		t.Fatalf("create readiness Credential approval audit: %v", err)
	}
	knowledge := models.KnowledgeBase{
		TenantID: tenant.ID, StoreID: store.ID,
		DatasetID: "dataset-ready", DatasetName: "Ready dataset",
		ConnectionID:     fastgptapi.ManagedConnectionID,
		FastGPTProfileID: "profile-ready", FastGPTProfileRevision: "1",
		FastGPTProfileStatus: "ready", FastGPTProfileSyncedAt: &publishedAt,
		FastGPTAppliedProfileID:           modelProfile.ID,
		FastGPTAppliedProfileRevision:     modelProfile.Revision,
		FastGPTAppliedStoreStaffBindingID: binding.ID,
		FastGPTAppliedCredentialRevision:  credential.CredentialRevision,
		Name:                              "Readiness knowledge", KnowledgeType: "fastgpt_cloud",
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(&knowledge).Error; err != nil {
		t.Fatalf("create readiness Knowledge Base: %v", err)
	}
	if err := db.Model(&models.Store{}).Where("id = ?", store.ID).
		Update("knowledge_base_id", knowledge.ID).Error; err != nil {
		t.Fatalf("bind readiness Knowledge Base: %v", err)
	}
	store.KnowledgeBaseID = knowledge.ID
	if err := db.Create(&models.FastGPTStoreTenant{
		TenantID: tenant.ID, StoreID: store.ID,
		TenantTeamID: "team-ready", TenantTeamName: "Ready team", Status: "active",
		TargetProfileID: modelProfile.ID, TargetProfileRevision: modelProfile.Revision,
		AppliedProfileID: modelProfile.ID, AppliedProfileRevision: modelProfile.Revision,
		TargetStoreStaffBindingID: binding.ID, AppliedStoreStaffBindingID: binding.ID,
		TargetCredentialRevision:  credential.CredentialRevision,
		AppliedCredentialRevision: credential.CredentialRevision,
		AppliedKeyFingerprint:     "fastgpt-fingerprint-DO-NOT-PRINT",
		ReadinessStatus:           "ready", LastSyncedAt: &publishedAt, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness FastGPT binding: %v", err)
	}
	if err := db.Create(&models.StoreCustomerTagRuntimePolicy{
		TenantID: tenant.ID, StoreID: store.ID, Status: enums.StatusOk, AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create readiness Store tag policy: %v", err)
	}

	return &tenantReleaseReadinessFixture{
		db: db, now: now, tenant: tenant, store: store, account: account,
		binding: binding, channel: channel, wxWork: wxWork,
		modelProfile: modelProfile, credential: credential, leafTag: leafTag,
	}
}

func (f *tenantReleaseReadinessFixture) audit(
	t *testing.T,
	level TenantReleaseReadinessLevel,
	evidenceStart *time.Time,
) *TenantReleaseReadinessReport {
	t.Helper()
	report, err := TenantReleaseReadinessService.Audit(f.db, TenantReleaseReadinessOptions{
		TenantID: f.tenant.ID, StoreIDs: []int64{f.store.ID},
		Level: level, EvidenceStart: evidenceStart, SampleLimit: 10, Now: f.now,
	})
	if err != nil {
		t.Fatalf("audit %s readiness: %v", level, err)
	}
	return report
}

func (f *tenantReleaseReadinessFixture) seedPilotEvidence(t *testing.T, evidenceStart time.Time) {
	t.Helper()
	audit := tenantReleaseReadinessAuditFields(f.now)
	conversation := models.Conversation{
		TenantID: f.tenant.ID, StoreID: f.store.ID, StoreStaffBindingID: f.binding.ID,
		CustomerID: 99, CustomerName: "customer-content-DO-NOT-PRINT",
		Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst,
		LastMessageAt: f.now, LastActiveAt: f.now, AuditFields: audit,
	}
	threadKey := buildStoreConversationThreadKey(f.tenant.ID, f.store.ID, conversation.CustomerID, f.binding.ID)
	conversation.ThreadKey = &threadKey
	if err := f.db.Create(&conversation).Error; err != nil {
		t.Fatalf("create pilot Conversation: %v", err)
	}
	f.conversation = conversation
	if err := f.db.Create(&models.ConversationRouteState{
		TenantID: f.tenant.ID, ConversationID: conversation.ID, StoreID: f.store.ID,
		StoreStaffBindingID: f.binding.ID, KnowledgeBaseID: f.store.KnowledgeBaseID,
		WxWorkInstanceID: f.wxWork.ID, SessionNo: 1,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai",
		AuditFields: audit,
	}).Error; err != nil {
		t.Fatalf("create pilot route: %v", err)
	}
	customerAt := evidenceStart.Add(time.Minute)
	aiAt := customerAt.Add(time.Minute)
	customerAudit := tenantReleaseReadinessAuditFields(customerAt)
	aiAudit := tenantReleaseReadinessAuditFields(aiAt)
	messages := []models.Message{
		{
			TenantID: f.tenant.ID, ConversationID: conversation.ID, SessionNo: 1,
			ClientMsgID: "pilot-customer", SenderType: enums.IMSenderTypeCustomer,
			MessageType: enums.IMMessageTypeText, Content: "customer-content-DO-NOT-PRINT",
			SeqNo: 1, SendStatus: enums.IMMessageStatusSent, AuditFields: customerAudit,
		},
		{
			TenantID: f.tenant.ID, ConversationID: conversation.ID, SessionNo: 1,
			ClientMsgID: "pilot-ai", SenderType: enums.IMSenderTypeAI,
			MessageType: enums.IMMessageTypeText, Content: "ai-content-DO-NOT-PRINT",
			SeqNo: 2, SendStatus: enums.IMMessageStatusSent,
			OutboundChannelType: enums.ChannelTypeWxWorkProtocol, AuditFields: aiAudit,
		},
	}
	if err := f.db.Create(&messages).Error; err != nil {
		t.Fatalf("create pilot messages: %v", err)
	}
	if err := f.db.Create(&models.MessageSyncLog{
		TenantID: f.tenant.ID, ConversationID: conversation.ID, MessageID: messages[0].ID,
		Direction: enums.MessageSyncDirectionWecomToAgentDesk,
		Source:    "wxwork_protocol", Target: "agentdesk",
		ExternalMsgID: "pilot-wxwork-inbound", SyncStatus: enums.MessageSyncStatusSuccess,
		Payload: `{"notify_type":11010}`, AuditFields: customerAudit,
	}).Error; err != nil {
		t.Fatalf("create pilot WxWork inbound evidence: %v", err)
	}
	sentAt := aiAt
	if err := f.db.Create(&models.ChannelMessageOutbox{
		TenantID: f.tenant.ID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: messages[1].ID,
		SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &sentAt,
		AuditFields: aiAudit,
	}).Error; err != nil {
		t.Fatalf("create pilot WxWork outbound evidence: %v", err)
	}
	if err := f.db.Create(&[]models.WxWorkKFMessageRef{
		{
			TenantID: f.tenant.ID, ConversationID: conversation.ID, MessageID: messages[0].ID,
			WxMsgID:        "wx_protocol:" + f.wxWork.Guid + ":pilot-inbound",
			Direction:      string(enums.WxWorkKFMessageDirectionIn),
			OpenKfID:       "wx_protocol:" + f.wxWork.Guid,
			ExternalUserID: "788-readiness",
			SendStatus:     string(enums.WxWorkKFMessageSendStatusReceived),
			Status:         enums.StatusOk, AuditFields: customerAudit,
		},
		{
			TenantID: f.tenant.ID, ConversationID: conversation.ID, MessageID: messages[1].ID,
			WxMsgID:        "wx_protocol:" + f.wxWork.Guid + ":pilot-outbound",
			Direction:      string(enums.WxWorkKFMessageDirectionOut),
			OpenKfID:       "wx_protocol:" + f.wxWork.Guid,
			ExternalUserID: "788-readiness",
			SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
			Status:         enums.StatusOk, AuditFields: aiAudit,
		},
	}).Error; err != nil {
		t.Fatalf("create pilot WxWork message refs: %v", err)
	}
	if err := f.db.Create(&models.AIUsageEvent{
		TenantID: f.tenant.ID, EventKey: "pilot-usage", StoreID: f.store.ID,
		StoreStaffBindingID: f.binding.ID, WxWorkInstanceID: f.wxWork.ID, ConversationID: conversation.ID,
		RequestID: "local-pilot-request",
		Stage:     "generate", Model: "reply-model", ModelProfileID: f.modelProfile.ID,
		ModelProfileRevision: f.modelProfile.Revision, UsageSlot: string(enums.ModelUsageSlotReplyLLM),
		CredentialRevision: f.credential.CredentialRevision,
		Gateway:            AIUsageGatewayNewAPI, GatewayRequestID: "gateway-pilot-request",
		Status: "completed", CreatedAt: aiAt,
	}).Error; err != nil {
		t.Fatalf("create pilot NewAPI usage: %v", err)
	}
	retrieveRequestID := "pilot-fastgpt-request"
	if err := f.db.Create(&models.KnowledgeRetrieveLog{
		TenantID: f.tenant.ID, KnowledgeBaseID: f.store.KnowledgeBaseID,
		SourceType: "fastgpt", Channel: string(enums.KnowledgeRetrieveChannelIM),
		Scene: string(enums.KnowledgeRetrieveSceneFirstResponse), ConversationID: conversation.ID,
		RequestID: retrieveRequestID, AnswerStatus: int(enums.KnowledgeAnswerStatusNormal),
		HitCount: 1, UsedChunkCount: 1, ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT),
		CreatedAt: aiAt,
	}).Error; err != nil {
		t.Fatalf("create pilot FastGPT retrieve log: %v", err)
	}
	if err := f.db.Create(&models.AIUsageEvent{
		TenantID: f.tenant.ID, EventKey: "pilot-fastgpt-usage", StoreID: f.store.ID,
		StoreStaffBindingID: f.binding.ID, WxWorkInstanceID: f.wxWork.ID, ConversationID: conversation.ID,
		KnowledgeBaseID: f.store.KnowledgeBaseID,
		RequestID:       retrieveRequestID, Stage: "knowledge_retrieve", Provider: "fastgpt",
		OperationType: "knowledge_retrieve", RequestCount: 1,
		ModelProfileID: f.modelProfile.ID, ModelProfileRevision: f.modelProfile.Revision,
		CredentialRevision: f.credential.CredentialRevision,
		Status:             "completed", CreatedAt: aiAt,
	}).Error; err != nil {
		t.Fatalf("create pilot FastGPT usage: %v", err)
	}
	handoffAt := aiAt.Add(time.Minute)
	if err := f.db.Create(&models.ConversationEventLog{
		TenantID: f.tenant.ID, ConversationID: conversation.ID,
		RequestID: "pilot-handoff", EventType: enums.IMEventTypeTransfer,
		OperatorType: enums.IMSenderTypeAI, OperatorID: 1, Content: "AI转人工",
		CreatedAt: handoffAt,
	}).Error; err != nil {
		t.Fatalf("create pilot AI handoff: %v", err)
	}
	if err := f.db.Create(&models.ConversationAssignment{
		TenantID: f.tenant.ID, ConversationID: conversation.ID, SessionNo: 1,
		ToUserID: f.account.ID, AssignType: string(enums.IMAssignmentTypeAssign),
		DispatchMode: enums.AgentTeamDispatchModeRule, WorkloadWeight: 1,
		Status: enums.IMAssignmentStatusActive, CreatedAt: handoffAt.Add(time.Minute),
	}).Error; err != nil {
		t.Fatalf("create pilot rule assignment: %v", err)
	}
	reconciledAt := aiAt.Add(2 * time.Minute)
	externalCreatedAt := aiAt
	if err := f.db.Create(&models.AIUsageGatewayCall{
		TenantID: f.tenant.ID, CallKey: "pilot-gateway-call", EventKey: "pilot-usage",
		StoreID: f.store.ID, StoreStaffBindingID: f.binding.ID,
		WxWorkInstanceID: f.wxWork.ID, ConversationID: conversation.ID,
		LocalRequestID: "local-pilot-request", Stage: "generate",
		ModelProfileID: f.modelProfile.ID, ModelProfileRevision: f.modelProfile.Revision,
		UsageSlot:          string(enums.ModelUsageSlotReplyLLM),
		CredentialRevision: f.credential.CredentialRevision,
		Gateway:            AIUsageGatewayNewAPI, GatewayRequestID: "gateway-pilot-request",
		StartedAt: aiAt, FinishedAt: aiAt.Add(time.Second),
		ReconcileStatus: AIUsageReconcileCompleted,
		MatchStrategy:   AIUsageMatchStrategyRequestID,
		MatchConfidence: AIUsageMatchConfidenceExact,
		ExternalModel:   "reply-model", ExternalCreatedAt: &externalCreatedAt,
		ReconciledAt: &reconciledAt, CreatedAt: aiAt, UpdatedAt: reconciledAt,
	}).Error; err != nil {
		t.Fatalf("create pilot reconciled billing: %v", err)
	}
}

func assertTenantReleaseReadinessPassed(t *testing.T, report *TenantReleaseReadinessReport) {
	t.Helper()
	if report == nil || report.HasViolations() || report.Status != "passed" {
		raw, _ := json.Marshal(report)
		t.Fatalf("readiness did not pass: %s", raw)
	}
	if report.RequiredCheckCount == 0 || report.RequiredCheckCount != report.PassedCheckCount {
		t.Fatalf("readiness check counts=%d/%d", report.PassedCheckCount, report.RequiredCheckCount)
	}
}

func tenantReleaseReadinessHasViolation(report *TenantReleaseReadinessReport, code string) bool {
	if report == nil {
		return false
	}
	for _, violation := range report.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

func tenantReleaseReadinessAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, CreateUserName: "readiness-test",
		UpdatedAt: now, UpdateUserName: "readiness-test",
	}
}
