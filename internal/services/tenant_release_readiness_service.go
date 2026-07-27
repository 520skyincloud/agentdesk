package services

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantReleaseReadinessService = newTenantReleaseReadinessService()

type tenantReleaseReadinessService struct{}

func newTenantReleaseReadinessService() *tenantReleaseReadinessService {
	return &tenantReleaseReadinessService{}
}

type TenantReleaseReadinessLevel string

const (
	TenantReleaseReadinessConfiguration TenantReleaseReadinessLevel = "configuration"
	TenantReleaseReadinessPilot         TenantReleaseReadinessLevel = "pilot"
	TenantReleaseReadinessTagGray       TenantReleaseReadinessLevel = "tag_gray"
)

type TenantReleaseReadinessOptions struct {
	TenantID      int64
	TenantCode    string
	StoreIDs      []int64
	Level         TenantReleaseReadinessLevel
	EvidenceStart *time.Time
	SampleLimit   int
	Now           time.Time
}

type TenantReleaseReadinessTenant struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	IndustryID int64  `json:"industryId"`
}

type TenantReleaseReadinessCheck struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Required int    `json:"required"`
	Passed   int    `json:"passed"`
}

type TenantReleaseReadinessViolation struct {
	Code           string  `json:"code"`
	Scope          string  `json:"scope"`
	Count          int     `json:"count"`
	SampleStoreIDs []int64 `json:"sampleStoreIds"`
	Message        string  `json:"message"`
}

type TenantReleaseReadinessCursorSnapshot struct {
	MessageMaxID          int64 `json:"messageMaxId"`
	MessageCount          int64 `json:"messageCount"`
	OutboxMaxID           int64 `json:"outboxMaxId"`
	OutboxCount           int64 `json:"outboxCount"`
	UnsettledOutboxCount  int64 `json:"unsettledOutboxCount"`
	AssignmentMaxID       int64 `json:"assignmentMaxId"`
	AssignmentCount       int64 `json:"assignmentCount"`
	ActiveAssignmentCount int64 `json:"activeAssignmentCount"`
}

type TenantReleaseReadinessReport struct {
	Status             string                               `json:"status"`
	GeneratedAt        time.Time                            `json:"generatedAt"`
	Level              TenantReleaseReadinessLevel          `json:"level"`
	EvidenceStart      *time.Time                           `json:"evidenceStart,omitempty"`
	Tenant             TenantReleaseReadinessTenant         `json:"tenant"`
	SelectedStoreCount int                                  `json:"selectedStoreCount"`
	SelectedStoreIDs   []int64                              `json:"selectedStoreIds"`
	RequiredCheckCount int                                  `json:"requiredCheckCount"`
	PassedCheckCount   int                                  `json:"passedCheckCount"`
	ReleaseCursor      TenantReleaseReadinessCursorSnapshot `json:"releaseCursor"`
	Checks             []TenantReleaseReadinessCheck        `json:"checks"`
	Violations         []TenantReleaseReadinessViolation    `json:"violations"`
}

func ParseTenantReleaseReadinessLevel(value string) (TenantReleaseReadinessLevel, error) {
	level := TenantReleaseReadinessLevel(strings.ToLower(strings.TrimSpace(value)))
	switch level {
	case TenantReleaseReadinessConfiguration, TenantReleaseReadinessPilot, TenantReleaseReadinessTagGray:
		return level, nil
	default:
		return "", fmt.Errorf("readiness level must be configuration, pilot, or tag_gray")
	}
}

func (r *TenantReleaseReadinessReport) HasViolations() bool {
	return r != nil && len(r.Violations) > 0
}

func (s *tenantReleaseReadinessService) Audit(
	db *gorm.DB,
	options TenantReleaseReadinessOptions,
) (*TenantReleaseReadinessReport, error) {
	if db == nil {
		return nil, fmt.Errorf("tenant release readiness audit requires a database")
	}
	options, err := normalizeTenantReleaseReadinessOptions(options)
	if err != nil {
		return nil, err
	}
	cursor, err := repositories.TenantReleaseReadinessRepository.FindCursorSnapshot(db)
	if err != nil {
		return nil, fmt.Errorf("capture release cursor failed: %w", err)
	}
	report := &TenantReleaseReadinessReport{
		Status:        "passed",
		GeneratedAt:   options.Now.UTC(),
		Level:         options.Level,
		EvidenceStart: cloneReleaseReadinessTime(options.EvidenceStart),
		Tenant: TenantReleaseReadinessTenant{
			ID: options.TenantID, Code: options.TenantCode,
		},
		SelectedStoreIDs: []int64{},
		ReleaseCursor: TenantReleaseReadinessCursorSnapshot{
			MessageMaxID: cursor.MessageMaxID, MessageCount: cursor.MessageCount,
			OutboxMaxID: cursor.OutboxMaxID, OutboxCount: cursor.OutboxCount,
			UnsettledOutboxCount: cursor.UnsettledOutboxCount,
			AssignmentMaxID:      cursor.AssignmentMaxID, AssignmentCount: cursor.AssignmentCount,
			ActiveAssignmentCount: cursor.ActiveAssignmentCount,
		},
		Checks: []TenantReleaseReadinessCheck{}, Violations: []TenantReleaseReadinessViolation{},
	}

	tenant := s.findTenant(db, options)
	if tenant == nil {
		report.addCheck("tenant.exists", 1, 0, "tenant", nil, options.SampleLimit, "指定接入公司不存在")
		report.finalize()
		return report, nil
	}
	report.Tenant = TenantReleaseReadinessTenant{
		ID: tenant.ID, Code: tenant.TenantCode,
		Name:       firstNonBlank(tenant.ShortName, tenant.LegalName),
		IndustryID: tenant.IntentProfileID,
	}
	report.addBooleanCheck("tenant.enabled", tenant.Status == enums.StatusOk, "tenant", nil, options.SampleLimit, "接入公司未启用")
	report.addBooleanCheck(
		"tenant.verified",
		tenant.VerificationStatus == enums.TenantVerificationStatusVerified,
		"tenant",
		nil,
		options.SampleLimit,
		"接入公司尚未通过权威信息核验",
	)

	industryProfile, industryErr := TenantIndustryService.ResolveTenantProfileDB(db, tenant.ID)
	industryReady := industryErr == nil && industryProfile != nil
	report.addBooleanCheck(
		"tenant.industry_profile",
		industryReady,
		"tenant",
		nil,
		options.SampleLimit,
		"接入公司未绑定已发布且分类、Prompt、Schema、行业标签目录完整的行业 Profile",
	)
	tagPolicyReady := false
	tagCatalogReady := false
	if industryReady {
		tagPolicyReady = tenantReleaseTagPolicyReady(db, tenant.ID, industryProfile.ID)
		tagCatalogReady, err = tenantReleaseTagCatalogReady(db, tenant.ID, industryProfile.ID)
		if err != nil {
			return nil, err
		}
	}
	report.addBooleanCheck(
		"tenant.tag_policy",
		tagPolicyReady,
		"tenant",
		nil,
		options.SampleLimit,
		"租户客户标签策略未匹配当前行业，或租户默认灰度开关没有保持关闭",
	)
	report.addBooleanCheck(
		"tenant.tag_catalog",
		tagCatalogReady,
		"tenant",
		nil,
		options.SampleLimit,
		"租户固定标签目录与当前行业定义不一致",
	)

	stores, invalidStoreIDs := tenantReleaseSelectedStores(db, tenant.ID, options.StoreIDs)
	report.SelectedStoreCount = len(stores)
	if len(options.StoreIDs) > 0 {
		report.SelectedStoreCount += len(invalidStoreIDs)
		report.addCheck(
			"store.selection",
			len(options.StoreIDs),
			len(stores),
			"store",
			invalidStoreIDs,
			options.SampleLimit,
			"所选门店不存在、不属于当前接入公司或未启用",
		)
	} else {
		report.addBooleanCheck(
			"store.selection",
			len(stores) > 0,
			"tenant",
			nil,
			options.SampleLimit,
			"接入公司没有可用于发布验收的启用门店",
		)
	}
	if len(stores) == 0 {
		report.finalize()
		return report, nil
	}

	storeIDs := make([]int64, 0, len(stores))
	knowledgeBaseIDs := make([]int64, 0, len(stores))
	storeByID := make(map[int64]models.Store, len(stores))
	for i := range stores {
		storeIDs = append(storeIDs, stores[i].ID)
		storeByID[stores[i].ID] = stores[i]
		if stores[i].KnowledgeBaseID > 0 {
			knowledgeBaseIDs = append(knowledgeBaseIDs, stores[i].KnowledgeBaseID)
		}
	}
	report.SelectedStoreIDs = append(report.SelectedStoreIDs, storeIDs...)

	accountStates, err := repositories.TenantReleaseReadinessRepository.FindStoreAccountStates(db, tenant.ID, storeIDs)
	if err != nil {
		return nil, fmt.Errorf("read Store account readiness failed: %w", err)
	}
	accountByStore := make(map[int64]repositories.TenantReleaseReadinessStoreAccountState, len(accountStates))
	for _, state := range accountStates {
		accountByStore[state.StoreID] = state
	}
	accountFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		state := accountByStore[storeID]
		return state.ActiveBindingCount == 1 && state.ReadyAccountCount == 1
	})
	report.addStoreCheck(
		"store.system_account",
		storeIDs,
		accountFailures,
		options.SampleLimit,
		"门店必须且只能绑定一个已审核、已启用并已分配客服组的系统门店员工账号",
	)

	wxWorkProtocolStates, err := repositories.TenantReleaseReadinessRepository.FindWxWorkProtocolStates(
		db,
		tenant.ID,
		storeIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("read Store WxWork protocol readiness failed: %w", err)
	}
	wxWorkProtocolByStore := make(
		map[int64]repositories.TenantReleaseReadinessWxWorkProtocolState,
		len(wxWorkProtocolStates),
	)
	for _, state := range wxWorkProtocolStates {
		wxWorkProtocolByStore[state.StoreID] = state
	}
	wxWorkProtocolFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		state := wxWorkProtocolByStore[storeID]
		return state.ActiveCount == 1 && state.ReadyChannelCount == 1
	})
	report.addStoreCheck(
		"store.wxwork_protocol",
		storeIDs,
		wxWorkProtocolFailures,
		options.SampleLimit,
		"门店必须且只能有一个当前启用的企微员工号实例，并与当前门店员工绑定及启用的企微协议渠道一致",
	)

	assignments := repositories.StoreModelProfileAssignmentRepository.FindByTenant(db, tenant.ID)
	assignmentByStore := make(map[int64]models.StoreModelProfileAssignment, len(assignments))
	for _, assignment := range assignments {
		if _, selected := storeByID[assignment.StoreID]; selected {
			assignmentByStore[assignment.StoreID] = assignment
		}
	}
	assignmentFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		assignment, exists := assignmentByStore[storeID]
		return exists &&
			assignment.Status == enums.StoreModelAssignmentStatusReady &&
			strings.EqualFold(strings.TrimSpace(assignment.ReadinessStatus), "ready") &&
			assignment.TemplateID > 0 &&
			assignment.TemplateRevision > 0 &&
			assignment.LastReadyAt != nil
	})
	report.addStoreCheck(
		"store.model_assignment",
		storeIDs,
		assignmentFailures,
		options.SampleLimit,
		"门店缺少已完成就绪切换的 active Model Profile Assignment",
	)

	profileReadyByStore := make(map[int64]bool, len(storeIDs))
	profileReadyCache := make(map[string]bool)
	for _, storeID := range storeIDs {
		assignment, exists := assignmentByStore[storeID]
		if !exists || assignment.TemplateID <= 0 || assignment.TemplateRevision <= 0 {
			continue
		}
		cacheKey := fmt.Sprintf("%d:%d", assignment.TemplateID, assignment.TemplateRevision)
		ready, cached := profileReadyCache[cacheKey]
		if !cached {
			template := repositories.ModelProfileTemplateRepository.Get(db, assignment.TemplateID)
			slots := repositories.ModelProfileSlotRepository.FindByTemplateID(db, assignment.TemplateID)
			ready = template != nil &&
				template.Status == enums.ModelProfileStatusActive &&
				template.Revision == assignment.TemplateRevision &&
				template.PublishedAt != nil &&
				len(ValidateModelProfileForPublication(template, slots)) == 0
			profileReadyCache[cacheKey] = ready
		}
		profileReadyByStore[storeID] = ready
	}
	profileFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		return profileReadyByStore[storeID]
	})
	report.addStoreCheck(
		"store.model_profile",
		storeIDs,
		profileFailures,
		options.SampleLimit,
		"门店 active Model Profile 未发布、revision 不匹配或九个强制用途槽未全部通过校验",
	)

	credentialStates, err := repositories.TenantReleaseReadinessRepository.FindCredentialStates(db, tenant.ID, storeIDs)
	if err != nil {
		return nil, fmt.Errorf("read Store credential readiness failed: %w", err)
	}
	credentialByStore := make(map[int64]repositories.TenantReleaseReadinessCredentialState, len(credentialStates))
	for _, state := range credentialStates {
		credentialByStore[state.StoreID] = state
	}
	credentialFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		state, exists := credentialByStore[storeID]
		return exists &&
			state.Status == enums.StoreCredentialStatusActive &&
			state.CredentialRevision > 0 &&
			state.HasActiveEncryptedKey == 1 &&
			strings.EqualFold(strings.TrimSpace(state.LastTestStatus), "passed") &&
			state.LastTestedAt != nil &&
			strings.EqualFold(strings.TrimSpace(state.LastFastGPTSyncStatus), storeCredentialFastGPTStatusReady) &&
			state.LastFastGPTSyncedAt != nil
	})
	report.addStoreCheck(
		"store.credential",
		storeIDs,
		credentialFailures,
		options.SampleLimit,
		"门店缺少已测试、已同步 FastGPT 且已激活的加密 NewAPI Credential",
	)
	profileTestFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		assignment, assignmentExists := assignmentByStore[storeID]
		credential, credentialExists := credentialByStore[storeID]
		if !assignmentExists || !credentialExists ||
			assignment.TemplateID <= 0 ||
			assignment.TemplateRevision <= 0 ||
			credential.CredentialRevision <= 0 {
			return false
		}
		template := repositories.ModelProfileTemplateRepository.Get(db, assignment.TemplateID)
		slots := repositories.ModelProfileSlotRepository.FindByTemplateID(db, assignment.TemplateID)
		if template == nil || template.Revision != assignment.TemplateRevision {
			return false
		}
		digest := modelProfileConfigurationDigest(template, slots)
		return repositories.ModelProfileTestRunRepository.FindLatestPassedForStore(
			db,
			template.ID,
			template.Revision,
			tenant.ID,
			storeID,
			credential.CredentialRevision,
			digest,
		) != nil
	})
	report.addStoreCheck(
		"store.model_profile_test_evidence",
		storeIDs,
		profileTestFailures,
		options.SampleLimit,
		"门店当前 Profile 配置摘要与 Credential revision 缺少不可变九槽通过证据",
	)
	if options.Level == TenantReleaseReadinessPilot || options.Level == TenantReleaseReadinessTagGray {
		policies := repositories.StoreCredentialPolicyRepository.FindByTenant(db, tenant.ID)
		policyByStore := make(map[int64]models.StoreCredentialPolicy, len(policies))
		for _, policy := range policies {
			if _, selected := storeByID[policy.StoreID]; selected {
				policyByStore[policy.StoreID] = policy
			}
		}
		policyFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
			policy, exists := policyByStore[storeID]
			return exists &&
				policy.Status == enums.StatusOk &&
				policy.AllowCredentialSelfService &&
				policy.RequireSupervisorApproval
		})
		report.addStoreCheck(
			"store.credential_self_service_policy",
			storeIDs,
			policyFailures,
			options.SampleLimit,
			"真实灰度门店必须允许唯一门店员工自助录入，并强制公司主管异人审批",
		)

		approvalAudit, auditErr := repositories.TenantReleaseReadinessRepository.FindCredentialApprovalAuditStates(db, tenant.ID, storeIDs)
		if auditErr != nil {
			return nil, fmt.Errorf("read Store credential approval evidence failed: %w", auditErr)
		}
		approvalReady := tenantReleaseCredentialApprovalReadiness(approvalAudit, accountByStore)
		approvalFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
			credential, exists := credentialByStore[storeID]
			if !exists || credential.CredentialRevision <= 0 {
				return false
			}
			return approvalReady[tenantReleaseCredentialRevisionKey{
				StoreID:  storeID,
				Revision: credential.CredentialRevision,
			}]
		})
		report.addStoreCheck(
			"evidence.credential_supervisor_approval",
			storeIDs,
			approvalFailures,
			options.SampleLimit,
			"当前 active Credential revision 缺少门店员工提交及异人公司主管审批的不可变审计证据",
		)
	}

	fastGPTStates, err := repositories.TenantReleaseReadinessRepository.FindFastGPTStates(db, tenant.ID, storeIDs)
	if err != nil {
		return nil, fmt.Errorf("read FastGPT Store readiness failed: %w", err)
	}
	fastGPTByStore := make(map[int64]repositories.TenantReleaseReadinessFastGPTState, len(fastGPTStates))
	for _, state := range fastGPTStates {
		fastGPTByStore[state.StoreID] = state
	}
	knowledgeStates, err := repositories.TenantReleaseReadinessRepository.FindKnowledgeStates(
		db,
		tenant.ID,
		uniqueTenantReleaseIDs(knowledgeBaseIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("read FastGPT Dataset readiness failed: %w", err)
	}
	knowledgeByID := make(map[int64]repositories.TenantReleaseReadinessKnowledgeState, len(knowledgeStates))
	for _, state := range knowledgeStates {
		knowledgeByID[state.KnowledgeBaseID] = state
	}
	fastGPTFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		store := storeByID[storeID]
		assignment, assignmentExists := assignmentByStore[storeID]
		credential, credentialExists := credentialByStore[storeID]
		fastGPT, fastGPTExists := fastGPTByStore[storeID]
		knowledge, knowledgeExists := knowledgeByID[store.KnowledgeBaseID]
		return assignmentExists &&
			credentialExists &&
			fastGPTExists &&
			knowledgeExists &&
			store.KnowledgeBaseID > 0 &&
			knowledge.StoreID == storeID &&
			knowledge.Status == enums.StatusOk &&
			knowledge.DatasetReady == 1 &&
			strings.TrimSpace(knowledge.ConnectionID) == fastgptapi.ManagedConnectionID &&
			knowledge.FastGPTProfileReady == 1 &&
			knowledge.FastGPTAppliedProfileID == assignment.TemplateID &&
			knowledge.FastGPTAppliedProfileRevision == assignment.TemplateRevision &&
			knowledge.FastGPTAppliedCredentialRevision == credential.CredentialRevision &&
			fastGPT.HasTenantTeam == 1 &&
			strings.EqualFold(strings.TrimSpace(fastGPT.Status), "active") &&
			strings.EqualFold(strings.TrimSpace(fastGPT.ReadinessStatus), "ready") &&
			fastGPT.LastSyncedAt != nil &&
			fastGPT.TargetProfileID == assignment.TemplateID &&
			fastGPT.TargetProfileRevision == assignment.TemplateRevision &&
			fastGPT.AppliedProfileID == assignment.TemplateID &&
			fastGPT.AppliedProfileRevision == assignment.TemplateRevision &&
			fastGPT.TargetCredentialRevision == credential.CredentialRevision &&
			fastGPT.AppliedCredentialRevision == credential.CredentialRevision
	})
	report.addStoreCheck(
		"store.fastgpt",
		storeIDs,
		fastGPTFailures,
		options.SampleLimit,
		"门店 FastGPT Team、Dataset 或已应用 Profile/Credential revision 未达到 ready",
	)

	runtimePolicies, err := repositories.StoreCustomerTagRuntimePolicyRepository.FindByStores(db, tenant.ID, storeIDs)
	if err != nil {
		return nil, fmt.Errorf("read Store tag runtime policy failed: %w", err)
	}
	runtimePolicyByStore := make(map[int64]models.StoreCustomerTagRuntimePolicy, len(runtimePolicies))
	for _, policy := range runtimePolicies {
		runtimePolicyByStore[policy.StoreID] = policy
	}
	tagSwitchesExpectedEnabled := options.Level == TenantReleaseReadinessTagGray
	tagSwitchFailures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		policy, exists := runtimePolicyByStore[storeID]
		return exists &&
			policy.Status == enums.StatusOk &&
			policy.CustomerTagEvolutionEnabled == tagSwitchesExpectedEnabled &&
			policy.ReplyTagContextEnabled == tagSwitchesExpectedEnabled
	})
	tagSwitchCode := "store.tag_switches_off"
	tagSwitchMessage := "配置和真实试点阶段必须保持客户标签演化与回复标签上下文关闭"
	if tagSwitchesExpectedEnabled {
		tagSwitchCode = "store.tag_switches_gray"
		tagSwitchMessage = "标签灰度门禁要求所选门店同时启用客户标签演化与回复标签上下文"
	}
	report.addStoreCheck(tagSwitchCode, storeIDs, tagSwitchFailures, options.SampleLimit, tagSwitchMessage)

	if options.Level == TenantReleaseReadinessPilot || options.Level == TenantReleaseReadinessTagGray {
		evidence, err := repositories.TenantReleaseReadinessRepository.FindEvidence(
			db,
			tenant.ID,
			storeIDs,
			*options.EvidenceStart,
			repositories.TenantReleaseReadinessEvidenceFilter{
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
			},
		)
		if err != nil {
			return nil, fmt.Errorf("read release evidence failed: %w", err)
		}
		report.addEvidenceStoreCheck(
			"evidence.wxwork_protocol_inbound",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool {
				return item.WxWorkProtocolInboundCount > 0
			},
			options.SampleLimit,
			"所选门店在证据窗口内没有由真实企微员工号回调写入的客户消息",
		)
		report.addEvidenceStoreCheck(
			"evidence.wxwork_protocol_outbound",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool {
				return item.WxWorkProtocolOutboundCount > 0
			},
			options.SampleLimit,
			"所选门店在证据窗口内没有经企微协议 Outbox 成功投递并落消息映射的 AI 回复",
		)
		report.addEvidenceStoreCheck(
			"evidence.newapi_call",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.SuccessfulNewAPICallCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有当前 Profile/Credential revision 的成功 NewAPI 调用",
		)
		report.addEvidenceStoreCheck(
			"evidence.fastgpt_retrieval",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.FastGPTRetrievalCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有当前 Profile/Credential revision 的真实 FastGPT 会话检索与命中证据",
		)
		report.addEvidenceStoreCheck(
			"evidence.customer_ai_reply",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.CustomerAIReplyCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有客户消息之后成功发送的真实 AI 回复",
		)
		report.addEvidenceStoreCheck(
			"evidence.ai_handoff",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.AIHandoffCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有 AI 进入现有人工任务池的转人工事件",
		)
		report.addEvidenceStoreCheck(
			"evidence.rule_assignment",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.RuleAssignmentCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有承接 AI 转人工会话的确定性规则派单",
		)
		report.addEvidenceStoreCheck(
			"evidence.billing_reconciled",
			storeIDs,
			evidence,
			func(item repositories.TenantReleaseReadinessEvidence) bool { return item.ReconciledBillingCount > 0 },
			options.SampleLimit,
			"所选门店在证据窗口内没有按 Request ID 精确完成的 NewAPI 人民币账单归因对账",
		)
		if options.Level == TenantReleaseReadinessTagGray {
			report.addEvidenceStoreCheck(
				"evidence.ai_customer_tag_change",
				storeIDs,
				evidence,
				func(item repositories.TenantReleaseReadinessEvidence) bool { return item.AICustomerTagChangeCount > 0 },
				options.SampleLimit,
				"所选门店在证据窗口内没有 AI 客户标签变更的追加式审计证据",
			)
		}
	}

	report.finalize()
	return report, nil
}

type tenantReleaseCredentialRevisionKey struct {
	StoreID  int64
	Revision int64
}

type tenantReleaseCredentialSubmission struct {
	AuditID    int64
	OperatorID int64
}

func tenantReleaseCredentialApprovalReadiness(
	items []repositories.TenantReleaseReadinessCredentialAuditState,
	accountByStore map[int64]repositories.TenantReleaseReadinessStoreAccountState,
) map[tenantReleaseCredentialRevisionKey]bool {
	submissions := make(map[tenantReleaseCredentialRevisionKey][]tenantReleaseCredentialSubmission)
	ret := make(map[tenantReleaseCredentialRevisionKey]bool)
	for _, item := range items {
		if item.StoreID <= 0 || item.ToRevision <= 0 {
			continue
		}
		key := tenantReleaseCredentialRevisionKey{StoreID: item.StoreID, Revision: item.ToRevision}
		switch item.Action {
		case enums.CredentialAuditActionSubmit:
			account, accountReady := accountByStore[item.StoreID]
			if !accountReady ||
				account.ActiveBindingCount != 1 ||
				account.ReadyAccountCount != 1 ||
				account.ActiveUserID <= 0 ||
				item.OperatorID != account.ActiveUserID ||
				!tenantReleaseRoleSnapshotContains(item.OperatorRole, constants.RoleCodeStoreStaff) ||
				(item.Result != enums.CredentialAuditResultPending && item.Result != enums.CredentialAuditResultSuccess) {
				continue
			}
			submissions[key] = append(submissions[key], tenantReleaseCredentialSubmission{
				AuditID: item.ID, OperatorID: item.OperatorID,
			})
		case enums.CredentialAuditActionApprove:
			if item.Result != enums.CredentialAuditResultSuccess ||
				item.OperatorID <= 0 ||
				item.ApproverID != item.OperatorID ||
				!tenantReleaseRoleSnapshotContains(item.OperatorRole, constants.RoleCodeTenantAdmin) {
				continue
			}
			for _, submission := range submissions[key] {
				if submission.AuditID < item.ID && submission.OperatorID != item.OperatorID {
					ret[key] = true
					break
				}
			}
		}
	}
	return ret
}

func tenantReleaseRoleSnapshotContains(snapshot, roleCode string) bool {
	for _, item := range strings.Split(snapshot, ",") {
		if strings.TrimSpace(item) == roleCode {
			return true
		}
	}
	return false
}

func normalizeTenantReleaseReadinessOptions(options TenantReleaseReadinessOptions) (TenantReleaseReadinessOptions, error) {
	options.TenantCode = strings.TrimSpace(options.TenantCode)
	if options.TenantID <= 0 && options.TenantCode == "" {
		return options, fmt.Errorf("readiness tenant id or tenant code is required")
	}
	if options.TenantID > 0 && options.TenantCode != "" {
		return options, fmt.Errorf("readiness tenant id and tenant code are mutually exclusive")
	}
	if options.Level == "" {
		options.Level = TenantReleaseReadinessConfiguration
	}
	level, err := ParseTenantReleaseReadinessLevel(string(options.Level))
	if err != nil {
		return options, err
	}
	options.Level = level
	for _, storeID := range options.StoreIDs {
		if storeID <= 0 {
			return options, fmt.Errorf("readiness Store IDs must be positive")
		}
	}
	options.StoreIDs = uniqueTenantReleaseIDs(options.StoreIDs)
	if options.SampleLimit <= 0 {
		options.SampleLimit = 20
	}
	if options.SampleLimit > 1000 {
		options.SampleLimit = 1000
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	if options.Level == TenantReleaseReadinessPilot || options.Level == TenantReleaseReadinessTagGray {
		if options.EvidenceStart == nil || options.EvidenceStart.IsZero() {
			return options, fmt.Errorf("evidence start time is required for pilot and tag_gray readiness")
		}
		if options.EvidenceStart.After(options.Now) {
			return options, fmt.Errorf("evidence start time cannot be in the future")
		}
	}
	return options, nil
}

func (s *tenantReleaseReadinessService) findTenant(db *gorm.DB, options TenantReleaseReadinessOptions) *models.Tenant {
	if options.TenantID > 0 {
		return repositories.TenantRepository.Get(db, options.TenantID)
	}
	return repositories.TenantRepository.GetByTenantCode(db, options.TenantCode)
}

func tenantReleaseTagPolicyReady(db *gorm.DB, tenantID, industryProfileID int64) bool {
	policy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(db, tenantID)
	return policy != nil &&
		policy.Status == enums.StatusOk &&
		policy.IntentProfileID == industryProfileID &&
		!policy.EvolutionDefaultEnabled &&
		!policy.ReplyTagContextDefaultEnabled
}

func tenantReleaseTagCatalogReady(db *gorm.DB, tenantID, industryProfileID int64) (bool, error) {
	definitions, err := repositories.IndustryTagDefinitionRepository.FindActiveByProfile(db, industryProfileID)
	if err != nil {
		return false, err
	}
	tags, err := repositories.TagRepository.FindByProfileInTenant(db, tenantID, industryProfileID)
	if err != nil {
		return false, err
	}
	activeTags := make([]models.Tag, 0, len(tags))
	for i := range tags {
		if tags[i].Status != enums.StatusDeleted {
			activeTags = append(activeTags, tags[i])
		}
	}
	if len(definitions) == 0 || len(activeTags) != len(definitions) {
		return false, nil
	}
	definitionByID := make(map[int64]models.IndustryTagDefinition, len(definitions))
	tagByDefinitionID := make(map[int64]models.Tag, len(activeTags))
	for _, definition := range definitions {
		definitionByID[definition.ID] = definition
	}
	for _, tag := range activeTags {
		if tag.TemplateDefinitionID == nil || *tag.TemplateDefinitionID <= 0 {
			return false, nil
		}
		if _, duplicated := tagByDefinitionID[*tag.TemplateDefinitionID]; duplicated {
			return false, nil
		}
		tagByDefinitionID[*tag.TemplateDefinitionID] = tag
	}
	for _, definition := range definitions {
		tag, exists := tagByDefinitionID[definition.ID]
		if !exists ||
			!tag.SystemDefined ||
			tag.IntentProfileID != industryProfileID ||
			tag.Name != definition.Name ||
			tag.SemanticKey != definition.SemanticKey ||
			tag.Aliases != definition.Aliases ||
			tag.ConflictGroup != definition.ConflictGroup ||
			tag.ApplicableScene != definition.ApplicableScene ||
			tag.AIEnabled != definition.AIEnabled ||
			tag.ReplyEnabled != definition.ReplyEnabled {
			return false, nil
		}
		if definition.ParentID == 0 {
			if tag.ParentID != 0 {
				return false, nil
			}
			continue
		}
		parentDefinition, parentDefinitionExists := definitionByID[definition.ParentID]
		parentTag, parentTagExists := tagByDefinitionID[parentDefinition.ID]
		if !parentDefinitionExists || !parentTagExists || tag.ParentID != parentTag.ID {
			return false, nil
		}
	}
	return true, nil
}

func tenantReleaseSelectedStores(db *gorm.DB, tenantID int64, requested []int64) ([]models.Store, []int64) {
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID)
	if len(requested) > 0 {
		cnd.In("id", requested)
	} else {
		cnd.Eq("status", enums.StatusOk)
	}
	stores := repositories.StoreRepository.Find(db, cnd.Asc("id"))
	if len(requested) == 0 {
		return stores, nil
	}
	valid := make([]models.Store, 0, len(stores))
	found := make(map[int64]bool, len(stores))
	for _, store := range stores {
		found[store.ID] = true
		if store.Status == enums.StatusOk {
			valid = append(valid, store)
		}
	}
	invalid := make([]int64, 0)
	for _, storeID := range requested {
		if !found[storeID] {
			invalid = append(invalid, storeID)
			continue
		}
		for _, store := range stores {
			if store.ID == storeID && store.Status != enums.StatusOk {
				invalid = append(invalid, storeID)
				break
			}
		}
	}
	return valid, invalid
}

func failedTenantReleaseStores(storeIDs []int64, ready func(int64) bool) []int64 {
	failures := make([]int64, 0)
	for _, storeID := range storeIDs {
		if !ready(storeID) {
			failures = append(failures, storeID)
		}
	}
	return failures
}

func uniqueTenantReleaseIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i] < ret[j] })
	return ret
}

func cloneReleaseReadinessTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func (r *TenantReleaseReadinessReport) addBooleanCheck(
	code string,
	passed bool,
	scope string,
	sampleStoreIDs []int64,
	sampleLimit int,
	message string,
) {
	passedCount := 0
	if passed {
		passedCount = 1
	}
	r.addCheck(code, 1, passedCount, scope, sampleStoreIDs, sampleLimit, message)
}

func (r *TenantReleaseReadinessReport) addStoreCheck(
	code string,
	storeIDs []int64,
	failures []int64,
	sampleLimit int,
	message string,
) {
	r.addCheck(code, len(storeIDs), len(storeIDs)-len(failures), "store", failures, sampleLimit, message)
}

func (r *TenantReleaseReadinessReport) addEvidenceStoreCheck(
	code string,
	storeIDs []int64,
	evidence map[int64]repositories.TenantReleaseReadinessEvidence,
	passed func(repositories.TenantReleaseReadinessEvidence) bool,
	sampleLimit int,
	message string,
) {
	failures := failedTenantReleaseStores(storeIDs, func(storeID int64) bool {
		item, exists := evidence[storeID]
		return exists && passed(item)
	})
	r.addStoreCheck(code, storeIDs, failures, sampleLimit, message)
}

func (r *TenantReleaseReadinessReport) addCheck(
	code string,
	required int,
	passed int,
	scope string,
	sampleStoreIDs []int64,
	sampleLimit int,
	message string,
) {
	if required < 0 {
		required = 0
	}
	if passed < 0 {
		passed = 0
	}
	if passed > required {
		passed = required
	}
	status := "passed"
	if passed != required {
		status = "failed"
	}
	r.Checks = append(r.Checks, TenantReleaseReadinessCheck{
		Code: code, Status: status, Required: required, Passed: passed,
	})
	if status == "passed" {
		return
	}
	samples := uniqueTenantReleaseIDs(sampleStoreIDs)
	if sampleLimit > 0 && len(samples) > sampleLimit {
		samples = samples[:sampleLimit]
	}
	r.Violations = append(r.Violations, TenantReleaseReadinessViolation{
		Code:  strings.ToUpper(strings.ReplaceAll(code, ".", "_")),
		Scope: scope, Count: required - passed, SampleStoreIDs: samples, Message: message,
	})
}

func (r *TenantReleaseReadinessReport) finalize() {
	r.RequiredCheckCount = len(r.Checks)
	r.PassedCheckCount = 0
	for _, check := range r.Checks {
		if check.Status == "passed" {
			r.PassedCheckCount++
		}
	}
	sort.SliceStable(r.Violations, func(i, j int) bool {
		return r.Violations[i].Code < r.Violations[j].Code
	})
	if len(r.Violations) > 0 {
		r.Status = "failed"
	}
}
