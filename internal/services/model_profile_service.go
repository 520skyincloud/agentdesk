package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const modelProfileProviderNewAPI = "newapi"

var modelProfileCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,79}$`)

type ModelUsageSlotSpec struct {
	UsageCode         enums.ModelUsageSlot
	DisplayName       string
	ExpectedModelType enums.AIModelType
	DefaultAPIMode    string
	Optional          bool
}

type ModelProfileWithSlots struct {
	Template     models.ModelProfileTemplate
	Slots        []models.ModelProfileSlot
	ConfigDigest string
	LatestTest   *models.ModelProfileTestRun
}

type ModelProfileCatalogData struct {
	Profiles     []ModelProfileWithSlots
	TestTargets  []ModelProfileTestTarget
	TestRequired bool
}

type ModelProfileValidationIssue struct {
	UsageCode enums.ModelUsageSlot
	Message   string
}

type ModelProfileValidationData struct {
	Template     models.ModelProfileTemplate
	ConfigDigest string
	Issues       []ModelProfileValidationIssue
	TestRun      *models.ModelProfileTestRun
}

type ModelProfileTestTarget struct {
	Tenant                 models.Tenant
	Store                  models.Store
	StoreStaffBindingID    int64
	StoreStaffAccountName  string
	CredentialRevision     int64
	ActiveTemplateID       int64
	ActiveTemplateName     string
	ActiveTemplateRevision int64
}

type StoreModelProfileAssignmentItem struct {
	Store              models.Store
	Assignment         *models.StoreModelProfileAssignment
	CredentialBindings []StoreModelCredentialBinding
}

type StoreModelCredentialBinding struct {
	ID          int64
	UserID      int64
	AccountName string
}

type StoreModelProfileAssignmentsData struct {
	TenantID  int64
	Profiles  []ModelProfileWithSlots
	Stores    []StoreModelProfileAssignmentItem
	Templates map[int64]models.ModelProfileTemplate
}

var ModelProfileService = newModelProfileService()
var StoreModelProfileAssignmentService = &storeModelProfileAssignmentService{}

type modelProfileService struct {
	validator storeCredentialSlotValidator
}
type storeModelProfileAssignmentService struct{}

func newModelProfileService() *modelProfileService {
	return &modelProfileService{validator: &newAPIStoreCredentialValidator{}}
}

func RequiredModelUsageSlotSpecs() []ModelUsageSlotSpec {
	return []ModelUsageSlotSpec{
		{UsageCode: enums.ModelUsageSlotReplyLLM, DisplayName: "回复生成", ExpectedModelType: enums.AIModelTypeLLM, DefaultAPIMode: "chat_completions"},
		{UsageCode: enums.ModelUsageSlotIntentDetectLLM, DisplayName: "意图识别", ExpectedModelType: enums.AIModelTypeLLM, DefaultAPIMode: "chat_completions"},
		{UsageCode: enums.ModelUsageSlotMemorySummary, DisplayName: "会话摘要", ExpectedModelType: enums.AIModelTypeLLM, DefaultAPIMode: "chat_completions"},
		{UsageCode: enums.ModelUsageSlotCustomerTag, DisplayName: "客户标签", ExpectedModelType: enums.AIModelTypeLLM, DefaultAPIMode: "chat_completions"},
		{UsageCode: enums.ModelUsageSlotVision, DisplayName: "视觉理解", ExpectedModelType: enums.AIModelTypeVision, DefaultAPIMode: "chat_completions"},
		{UsageCode: enums.ModelUsageSlotASR, DisplayName: "语音识别", ExpectedModelType: enums.AIModelTypeASR, DefaultAPIMode: "audio_transcriptions", Optional: true},
		{UsageCode: enums.ModelUsageSlotEmbedding, DisplayName: "向量检索", ExpectedModelType: enums.AIModelTypeEmbedding, DefaultAPIMode: "embeddings"},
		{UsageCode: enums.ModelUsageSlotRerank, DisplayName: "结果重排", ExpectedModelType: enums.AIModelTypeRerank, DefaultAPIMode: "rerank"},
		{UsageCode: enums.ModelUsageSlotDocumentParser, DisplayName: "文档解析", ExpectedModelType: enums.AIModelTypeLLM, DefaultAPIMode: "chat_completions"},
	}
}

func modelUsageSlotSpecByCode(code enums.ModelUsageSlot) (ModelUsageSlotSpec, bool) {
	for _, spec := range RequiredModelUsageSlotSpecs() {
		if spec.UsageCode == code {
			return spec, true
		}
	}
	return ModelUsageSlotSpec{}, false
}

func (s *modelProfileService) GetCatalog(req request.GetModelProfileCatalogRequest, operator *dto.AuthPrincipal) (*ModelProfileCatalogData, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	cnd := sqls.NewCnd().Asc("code").Desc("revision").Desc("id")
	if req.ID > 0 {
		cnd.Eq("id", req.ID)
	}
	if code := normalizeModelProfileCode(req.Code); code != "" {
		cnd.Eq("code", code)
	}
	items := repositories.ModelProfileTemplateRepository.Find(sqls.DB(), cnd)
	testRequired, err := repositories.StoreModelCredentialRepository.HasActive(sqls.DB())
	if err != nil {
		return nil, errorsx.BusinessError(3001, "无法读取模型方案真实测试门槛")
	}
	result := &ModelProfileCatalogData{
		Profiles:     make([]ModelProfileWithSlots, 0, len(items)),
		TestTargets:  s.findTestTargets(200),
		TestRequired: testRequired,
	}
	for i := range items {
		slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), items[i].ID)
		digest := modelProfileConfigurationDigest(&items[i], slots)
		result.Profiles = append(result.Profiles, ModelProfileWithSlots{
			Template:     items[i],
			Slots:        slots,
			ConfigDigest: digest,
			LatestTest:   repositories.ModelProfileTestRunRepository.FindLatestByDigest(sqls.DB(), items[i].ID, items[i].Revision, digest),
		})
	}
	return result, nil
}

func (s *modelProfileService) Create(req request.CreateModelProfileRequest, operator *dto.AuthPrincipal) (*ModelProfileWithSlots, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	now := time.Now()
	code := normalizeModelProfileCode(req.Code)
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	gatewayBaseURL := normalizeGatewayBaseURL(req.GatewayBaseURL)
	inputSlots := req.Slots
	revision := int64(1)

	if req.SourceTemplateID > 0 {
		source := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), req.SourceTemplateID)
		if source == nil {
			return nil, errorsx.InvalidParam("来源模型方案不存在")
		}
		if code != "" && code != source.Code {
			return nil, errorsx.InvalidParam("新 revision 不能修改模型方案编码")
		}
		code = source.Code
		if name == "" {
			name = source.Name
		}
		if description == "" {
			description = source.Description
		}
		if gatewayBaseURL == "" {
			gatewayBaseURL = source.GatewayBaseURL
		}
		latest := repositories.ModelProfileTemplateRepository.GetLatestByCode(sqls.DB(), source.Code)
		if latest != nil {
			revision = latest.Revision + 1
		}
		if len(inputSlots) == 0 {
			inputSlots = modelSlotRequestsFromModels(repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), source.ID))
		}
	} else {
		if !modelProfileCodePattern.MatchString(code) {
			return nil, errorsx.InvalidParam("模型方案编码必须以小写字母开头，仅包含小写字母、数字、下划线或连字符")
		}
		if repositories.ModelProfileTemplateRepository.GetLatestByCode(sqls.DB(), code) != nil {
			return nil, errorsx.InvalidParam("模型方案编码已存在，请从现有方案创建新 revision")
		}
		if len(inputSlots) == 0 {
			inputSlots = defaultModelProfileSlotRequests()
		}
	}
	if name == "" {
		return nil, errorsx.InvalidParam("模型方案名称不能为空")
	}
	if gatewayBaseURL == "" {
		gatewayBaseURL = constants.UnifiedNewAPIGatewayBaseURL
	}
	template := &models.ModelProfileTemplate{
		Code: code, Name: name, Description: description, Revision: revision,
		GatewayBaseURL: gatewayBaseURL, Status: enums.ModelProfileStatusDraft,
		AuditFields: modelProfileAuditFields(operator, now),
	}
	slots := buildModelProfileSlots(inputSlots, 0, operator, now)
	if issues := validateModelProfileDraft(template, slots); len(issues) > 0 {
		return nil, errorsx.InvalidParam(issues[0].Message)
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ModelProfileTemplateRepository.Create(ctx.Tx, template); err != nil {
			return err
		}
		for i := range slots {
			slots[i].TemplateID = template.ID
		}
		return repositories.ModelProfileSlotRepository.ReplaceByTemplateID(ctx.Tx, template.ID, slots)
	}); err != nil {
		return nil, err
	}
	WsService.PublishStoreModelProfileChanged(
		0, 0, template.ID, template.Revision, string(template.Status), template.UpdatedAt,
	)
	return &ModelProfileWithSlots{Template: *template, Slots: slots}, nil
}

func (s *modelProfileService) Update(req request.UpdateModelProfileRequest, operator *dto.AuthPrincipal) (*ModelProfileWithSlots, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("模型方案名称不能为空")
	}
	now := time.Now()
	var updated models.ModelProfileTemplate
	var slots []models.ModelProfileSlot
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ModelProfileTemplateRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("模型方案 revision 不存在")
		}
		if req.ConfirmRevision <= 0 || req.ConfirmRevision != current.Revision {
			return errorsx.InvalidParam("二次确认的 revision 与当前模型方案不一致")
		}
		target := current
		switch current.Status {
		case enums.ModelProfileStatusDraft:
		case enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive:
			target, err = repositories.ModelProfileTemplateRepository.FindDraftByCodeForUpdate(ctx.Tx, current.Code)
			if err != nil {
				return err
			}
			if target == nil {
				latest, latestErr := repositories.ModelProfileTemplateRepository.GetLatestByCodeForUpdate(ctx.Tx, current.Code)
				if latestErr != nil {
					return latestErr
				}
				nextRevision := current.Revision + 1
				if latest != nil && latest.Revision >= nextRevision {
					nextRevision = latest.Revision + 1
				}
				target = &models.ModelProfileTemplate{
					Code: current.Code, Name: current.Name, Description: current.Description,
					Revision: nextRevision, GatewayBaseURL: current.GatewayBaseURL,
					Status: enums.ModelProfileStatusDraft, AuditFields: modelProfileAuditFields(operator, now),
				}
				if err := repositories.ModelProfileTemplateRepository.Create(ctx.Tx, target); err != nil {
					return err
				}
			}
		default:
			return errorsx.InvalidParam("当前模型方案状态不允许编辑")
		}
		updated = *target
		updated.Name = name
		updated.Description = strings.TrimSpace(req.Description)
		updated.GatewayBaseURL = normalizeGatewayBaseURL(req.GatewayBaseURL)
		slots = buildModelProfileSlots(req.Slots, target.ID, operator, now)
		if issues := validateModelProfileDraft(&updated, slots); len(issues) > 0 {
			return errorsx.InvalidParam(issues[0].Message)
		}
		if err := repositories.ModelProfileTemplateRepository.Updates(ctx.Tx, target.ID, map[string]any{
			"name": updated.Name, "description": updated.Description, "gateway_base_url": updated.GatewayBaseURL,
			"update_user_id": operator.UserID, "update_user_name": operator.Username, "updated_at": now,
		}); err != nil {
			return err
		}
		return repositories.ModelProfileSlotRepository.ReplaceByTemplateID(ctx.Tx, target.ID, slots)
	}); err != nil {
		return nil, err
	}
	updated.UpdatedAt = now
	updated.UpdateUserID = operator.UserID
	updated.UpdateUserName = operator.Username
	WsService.PublishStoreModelProfileChanged(
		0, 0, updated.ID, updated.Revision, string(updated.Status), updated.UpdatedAt,
	)
	return &ModelProfileWithSlots{Template: updated, Slots: slots}, nil
}

func (s *modelProfileService) Validate(req request.ModelProfileRevisionActionRequest, operator *dto.AuthPrincipal) (*ModelProfileValidationData, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	item := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), req.ID)
	if item == nil {
		return nil, errorsx.InvalidParam("模型方案 revision 不存在")
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), item.ID)
	return &ModelProfileValidationData{
		Template:     *item,
		ConfigDigest: modelProfileConfigurationDigest(item, slots),
		Issues:       ValidateModelProfileForPublication(item, slots),
	}, nil
}

func (s *modelProfileService) Test(
	ctx context.Context,
	req request.TestModelProfileRequest,
	operator *dto.AuthPrincipal,
	meta StoreCredentialRequestMeta,
) (*ModelProfileValidationData, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	item := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), req.ID)
	if item == nil {
		return nil, errorsx.InvalidParam("模型方案 revision 不存在")
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), item.ID)
	data := &ModelProfileValidationData{
		Template:     *item,
		ConfigDigest: modelProfileConfigurationDigest(item, slots),
		Issues:       ValidateModelProfileForPublication(item, slots),
	}
	if len(data.Issues) > 0 {
		return data, nil
	}
	target, credential, err := s.loadTestTarget(req.TenantID, req.StoreID, req.StoreStaffBindingID, item)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	testErr := s.validator.Validate(ctx, item, slots, credential.APIKey)
	run, recordErr := recordModelProfileTestRun(
		item,
		slots,
		&target.Tenant,
		&target.Store,
		target.StoreStaffBindingID,
		credential.Revision,
		enums.ModelProfileTestCredentialSourceActive,
		testErr,
		startedAt,
		operator,
		meta,
	)
	if recordErr != nil {
		return nil, recordErr
	}
	data.TestRun = run
	return data, nil
}

func (s *modelProfileService) Publish(req request.ModelProfileRevisionActionRequest, operator *dto.AuthPrincipal) (*ModelProfileWithSlots, error) {
	if err := requirePlatformModelProfileAccess(operator); err != nil {
		return nil, err
	}
	now := time.Now()
	var item models.ModelProfileTemplate
	var slots []models.ModelProfileSlot
	var rollouts []models.StoreModelProfileAssignment
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ModelProfileTemplateRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("模型方案 revision 不存在")
		}
		if current.Status != enums.ModelProfileStatusDraft {
			return errorsx.InvalidParam("只有 draft revision 可以提交候选发布")
		}
		if req.ConfirmRevision <= 0 || req.ConfirmRevision != current.Revision {
			return errorsx.InvalidParam("二次确认的 revision 与当前方案不一致")
		}
		slots = repositories.ModelProfileSlotRepository.FindByTemplateID(ctx.Tx, current.ID)
		if issues := ValidateModelProfileForPublication(current, slots); len(issues) > 0 {
			return errorsx.InvalidParam(issues[0].Message)
		}
		digest := modelProfileConfigurationDigest(current, slots)
		testRequired, err := repositories.StoreModelCredentialRepository.HasActive(ctx.Tx)
		if err != nil {
			return errorsx.BusinessError(3001, "无法确认模型方案真实测试门槛")
		}
		if testRequired &&
			repositories.ModelProfileTestRunRepository.FindLatestPassedByDigest(
				ctx.Tx,
				current.ID,
				current.Revision,
				digest,
			) == nil {
			return errorsx.InvalidParam("当前配置尚未通过受控门店的真实启用槽测试")
		}
		if err := repositories.ModelProfileTemplateRepository.Updates(ctx.Tx, current.ID, map[string]any{
			"status": enums.ModelProfileStatusCandidate, "published_at": now,
			"published_by": operator.UserID, "published_by_name": operator.Username,
			"update_user_id": operator.UserID, "update_user_name": operator.Username, "updated_at": now,
		}); err != nil {
			return err
		}
		rollouts, err = ModelProfileRolloutService.ScheduleFollowersDB(ctx.Tx, current, now)
		if err != nil {
			return err
		}
		item = *current
		return nil
	}); err != nil {
		return nil, err
	}
	item.Status = enums.ModelProfileStatusCandidate
	item.PublishedAt = &now
	item.PublishedBy = operator.UserID
	item.PublishedByName = operator.Username
	item.UpdatedAt = now
	WsService.PublishStoreModelProfileChanged(
		0, 0, item.ID, item.Revision, string(item.Status), item.UpdatedAt,
	)
	for i := range rollouts {
		WsService.PublishStoreModelProfileChanged(
			rollouts[i].TenantID,
			rollouts[i].StoreID,
			item.ID,
			item.Revision,
			"pending",
			now,
		)
	}
	if len(rollouts) > 0 {
		go ModelProfileRolloutService.ProcessDue(len(rollouts))
	}
	return &ModelProfileWithSlots{Template: item, Slots: slots}, nil
}

func ValidateModelProfileForPublication(template *models.ModelProfileTemplate, slots []models.ModelProfileSlot) []ModelProfileValidationIssue {
	issues := validateModelProfileDraft(template, slots)
	if template == nil {
		return issues
	}
	if !validHTTPURL(template.GatewayBaseURL) {
		issues = append(issues, ModelProfileValidationIssue{Message: "统一 NewAPI 网关地址不能为空且必须是有效的 HTTP(S) 地址"})
	}
	for _, slot := range slots {
		spec, known := modelUsageSlotSpecByCode(slot.UsageCode)
		if !known {
			continue
		}
		if !slot.Enabled {
			if !spec.Optional {
				issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "槽不能停用"})
			}
			continue
		}
		if strings.TrimSpace(slot.ModelName) == "" {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "模型名不能为空"})
		}
		if strings.TrimSpace(slot.APIMode) == "" {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + " API 模式不能为空"})
		} else if !modelProfileAPIModeCompatible(spec, slot.APIMode) {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + " API 模式与模型用途不匹配"})
		}
		if slot.TimeoutMS <= 0 {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "超时时间必须大于 0"})
		}
		if slot.MaxRetryCount < 0 || slot.MaxRetryCount > 10 {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "重试次数必须在 0 到 10 之间"})
		}
		if slot.Temperature < 0 || slot.Temperature > 2 {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + " Temperature 必须在 0 到 2 之间"})
		}
		if slot.ModelType == enums.AIModelTypeEmbedding && slot.Dimension <= 0 {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: "向量模型维度必须大于 0"})
		}
		if slot.ModelType == enums.AIModelTypeLLM || slot.ModelType == enums.AIModelTypeVision {
			if slot.MaxContextTokens <= 0 || slot.MaxOutputTokens <= 0 {
				issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "必须配置上下文和输出 Token 上限"})
			}
		}
		if slot.UsageCode == enums.ModelUsageSlotCustomerTag {
			if strings.TrimSpace(slot.SchemaVersion) == "" || strings.TrimSpace(slot.PromptTemplate) == "" || !validJSONDocument(slot.JSONSchema) {
				issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: "客户标签槽必须配置 Schema 版本、Prompt 和合法 JSON Schema"})
			}
		}
		if slot.JSONSchema != "" && !validJSONDocument(slot.JSONSchema) {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + " JSON Schema 格式不合法"})
		}
	}
	return deduplicateModelProfileIssues(issues)
}

func validateModelProfileDraft(template *models.ModelProfileTemplate, slots []models.ModelProfileSlot) []ModelProfileValidationIssue {
	issues := make([]ModelProfileValidationIssue, 0)
	if template == nil {
		return append(issues, ModelProfileValidationIssue{Message: "模型方案不能为空"})
	}
	if !modelProfileCodePattern.MatchString(template.Code) {
		issues = append(issues, ModelProfileValidationIssue{Message: "模型方案编码不合法"})
	}
	if strings.TrimSpace(template.Name) == "" {
		issues = append(issues, ModelProfileValidationIssue{Message: "模型方案名称不能为空"})
	}
	if template.GatewayBaseURL != "" && !validHTTPURL(template.GatewayBaseURL) {
		issues = append(issues, ModelProfileValidationIssue{Message: "统一 NewAPI 网关地址格式不正确"})
	}
	seen := make(map[enums.ModelUsageSlot]struct{}, len(slots))
	for _, slot := range slots {
		spec, known := modelUsageSlotSpecByCode(slot.UsageCode)
		if !known {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: "存在未定义的模型用途槽: " + string(slot.UsageCode)})
			continue
		}
		if _, exists := seen[slot.UsageCode]; exists {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "槽重复"})
			continue
		}
		seen[slot.UsageCode] = struct{}{}
		if slot.ModelType != spec.ExpectedModelType {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: fmt.Sprintf("%s槽必须使用 %s 类型", spec.DisplayName, spec.ExpectedModelType)})
		}
		if !strings.EqualFold(strings.TrimSpace(slot.Provider), modelProfileProviderNewAPI) {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: slot.UsageCode, Message: spec.DisplayName + "槽只允许统一 NewAPI Provider"})
		}
	}
	for _, spec := range RequiredModelUsageSlotSpecs() {
		if _, exists := seen[spec.UsageCode]; !exists {
			issues = append(issues, ModelProfileValidationIssue{UsageCode: spec.UsageCode, Message: "缺少必需模型槽: " + spec.DisplayName})
		}
	}
	return deduplicateModelProfileIssues(issues)
}

func (s *storeModelProfileAssignmentService) List(req request.GetStoreModelProfileAssignmentsRequest, operator *dto.AuthPrincipal) (*StoreModelProfileAssignmentsData, error) {
	tenantID, err := resolveStoreModelTenantScope(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted).Asc("name").Asc("id"))
	assignments := repositories.StoreModelProfileAssignmentRepository.FindByTenant(sqls.DB(), tenantID)
	assignmentByStore := make(map[int64]models.StoreModelProfileAssignment, len(assignments))
	for _, item := range assignments {
		assignmentByStore[item.StoreID] = item
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.StatusOk).
		Where("active_user_id IS NOT NULL").
		Asc("store_id").Asc("id"))
	userIDs := make([]int64, 0, len(bindings))
	seenUserIDs := make(map[int64]struct{}, len(bindings))
	for i := range bindings {
		if bindings[i].UserID <= 0 {
			continue
		}
		if _, exists := seenUserIDs[bindings[i].UserID]; exists {
			continue
		}
		seenUserIDs[bindings[i].UserID] = struct{}{}
		userIDs = append(userIDs, bindings[i].UserID)
	}
	usersByID := make(map[int64]models.User, len(userIDs))
	for _, user := range repositories.UserRepository.FindByIdsInTenant(sqls.DB(), userIDs, tenantID) {
		usersByID[user.ID] = user
	}
	bindingsByStore := make(map[int64][]StoreModelCredentialBinding)
	for i := range bindings {
		user, exists := usersByID[bindings[i].UserID]
		if !exists || user.Status != enums.StatusOk {
			continue
		}
		accountName := strings.TrimSpace(user.Nickname)
		if accountName == "" {
			accountName = strings.TrimSpace(user.Username)
		}
		bindingsByStore[bindings[i].StoreID] = append(bindingsByStore[bindings[i].StoreID], StoreModelCredentialBinding{
			ID: bindings[i].ID, UserID: user.ID, AccountName: accountName,
		})
	}
	profiles := repositories.ModelProfileTemplateRepository.Find(sqls.DB(), sqls.NewCnd().In("status", []enums.ModelProfileStatus{
		enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive,
	}).Asc("code").Desc("revision").Desc("id"))
	result := &StoreModelProfileAssignmentsData{
		TenantID: tenantID, Profiles: make([]ModelProfileWithSlots, 0, len(profiles)),
		Stores: make([]StoreModelProfileAssignmentItem, 0, len(stores)), Templates: make(map[int64]models.ModelProfileTemplate),
	}
	seenProfileCodes := make(map[string]struct{}, len(profiles))
	for i := range profiles {
		if _, exists := seenProfileCodes[profiles[i].Code]; exists {
			continue
		}
		seenProfileCodes[profiles[i].Code] = struct{}{}
		slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), profiles[i].ID)
		if len(ValidateModelProfileForPublication(&profiles[i], slots)) > 0 {
			continue
		}
		result.Profiles = append(result.Profiles, ModelProfileWithSlots{Template: profiles[i], Slots: slots})
		result.Templates[profiles[i].ID] = profiles[i]
	}
	for i := range stores {
		entry := StoreModelProfileAssignmentItem{
			Store: stores[i], CredentialBindings: bindingsByStore[stores[i].ID],
		}
		if assignment, exists := assignmentByStore[stores[i].ID]; exists {
			copy := assignment
			entry.Assignment = &copy
			for _, templateID := range []int64{copy.TemplateID, copy.PendingTemplateID} {
				if templateID <= 0 {
					continue
				}
				if _, exists := result.Templates[templateID]; !exists {
					if template := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), templateID); template != nil {
						result.Templates[templateID] = *template
					}
				}
			}
		}
		result.Stores = append(result.Stores, entry)
	}
	return result, nil
}

func (s *storeModelProfileAssignmentService) Assign(req request.AssignStoreModelProfileRequest, operator *dto.AuthPrincipal) error {
	return s.assignBatch(req.TenantID, []int64{req.StoreID}, req.TemplateID, req.ConfirmRevision, operator)
}

func (s *storeModelProfileAssignmentService) BatchAssign(req request.BatchAssignStoreModelProfileRequest, operator *dto.AuthPrincipal) error {
	return s.assignBatch(req.TenantID, req.StoreIDs, req.TemplateID, req.ConfirmRevision, operator)
}

func (s *storeModelProfileAssignmentService) assignBatch(tenantID int64, storeIDs []int64, templateID, confirmRevision int64, operator *dto.AuthPrincipal) error {
	tenantID, err := resolveStoreModelTenantScope(operator, tenantID)
	if err != nil {
		return err
	}
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), templateID)
	if template == nil || !slices.Contains([]enums.ModelProfileStatus{enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive}, template.Status) {
		return errorsx.InvalidParam("只能指派已发布的 candidate 或 active 模型方案")
	}
	if confirmRevision <= 0 || confirmRevision != template.Revision {
		return errorsx.InvalidParam("二次确认的 revision 与目标模型方案不一致")
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), template.ID)
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) > 0 {
		return errorsx.InvalidParam(issues[0].Message)
	}
	storeIDs = normalizePositiveIDs(storeIDs)
	if len(storeIDs) == 0 {
		return errorsx.InvalidParam("至少选择一个门店")
	}
	stores := make([]models.Store, 0, len(storeIDs))
	for _, storeID := range storeIDs {
		store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
		if store == nil || store.Status == enums.StatusDeleted {
			return errorsx.InvalidParam(fmt.Sprintf("门店 %d 不存在或不属于当前接入公司", storeID))
		}
		stores = append(stores, *store)
	}
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, store := range stores {
			current, err := repositories.StoreModelProfileAssignmentRepository.GetForUpdateByStore(ctx.Tx, tenantID, store.ID)
			if err != nil {
				return err
			}
			if current == nil {
				item := &models.StoreModelProfileAssignment{
					TenantID: tenantID, StoreID: store.ID,
					PendingTemplateID: template.ID, PendingTemplateRevision: template.Revision,
					PendingRequestedAt: &now, PendingRequestedBy: operator.UserID, PendingRequestedByName: operator.Username,
					Status: enums.StoreModelAssignmentStatusAssigned, ReadinessStatus: "pending",
					AssignedAt: now, AssignedBy: operator.UserID, AssignedByName: operator.Username,
					AuditFields: modelProfileAuditFields(operator, now),
				}
				if err := repositories.StoreModelProfileAssignmentRepository.Create(ctx.Tx, item); err != nil {
					return err
				}
				continue
			}
			if current.TemplateID == template.ID && current.TemplateRevision == template.Revision {
				updates := map[string]any{
					"pending_template_id": 0, "pending_template_revision": 0, "pending_requested_at": nil,
					"pending_requested_by": 0, "pending_requested_by_name": "", "updated_at": now,
					"update_user_id": operator.UserID, "update_user_name": operator.Username,
				}
				if current.Status == enums.StoreModelAssignmentStatusReady {
					updates["readiness_status"] = "ready"
					updates["last_error_class"] = ""
					updates["last_error_message"] = ""
				}
				if err := repositories.StoreModelProfileAssignmentRepository.Updates(ctx.Tx, current.ID, updates); err != nil {
					return err
				}
				continue
			}
			status := current.Status
			readiness := current.ReadinessStatus
			if status == "" {
				status = enums.StoreModelAssignmentStatusAssigned
			}
			if readiness == "" {
				readiness = "pending"
			}
			updates := map[string]any{
				"pending_template_id": template.ID, "pending_template_revision": template.Revision,
				"pending_requested_at": now, "pending_requested_by": operator.UserID,
				"pending_requested_by_name": operator.Username, "status": status, "readiness_status": readiness,
				"assigned_at": now,
				"assigned_by": operator.UserID, "assigned_by_name": operator.Username,
				"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
			}
			if status != enums.StoreModelAssignmentStatusBlocked {
				updates["last_error_class"] = ""
				updates["last_error_message"] = ""
			}
			if err := repositories.StoreModelProfileAssignmentRepository.Updates(ctx.Tx, current.ID, updates); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, store := range stores {
		WsService.PublishStoreModelProfileChanged(
			tenantID,
			store.ID,
			template.ID,
			template.Revision,
			"pending",
			now,
		)
	}
	return nil
}

func (s *modelProfileService) findTestTargets(limit int) []ModelProfileTestTarget {
	return s.findTestTargetsDB(sqls.DB(), limit)
}

func (s *modelProfileService) findTestTargetsDB(db *gorm.DB, limit int) []ModelProfileTestTarget {
	targets, err := repositories.StoreModelCredentialRepository.FindUsableProfileTestTargets(db, limit)
	if err != nil {
		return nil
	}
	result := make([]ModelProfileTestTarget, 0, len(targets))
	for i := range targets {
		target := &targets[i]
		result = append(result, ModelProfileTestTarget{
			Tenant: models.Tenant{
				ID:        target.TenantID,
				ShortName: target.TenantShortName,
				LegalName: target.TenantLegalName,
			},
			Store: models.Store{
				ID:        target.StoreID,
				TenantID:  target.TenantID,
				StoreCode: target.StoreCode,
				Name:      target.StoreName,
			},
			StoreStaffBindingID:    target.StoreStaffBindingID,
			StoreStaffAccountName:  modelProfileTestBindingAccountName(db, target.StoreStaffBindingID),
			CredentialRevision:     target.CredentialRevision,
			ActiveTemplateID:       target.ActiveTemplateID,
			ActiveTemplateName:     target.ActiveTemplateName,
			ActiveTemplateRevision: target.ActiveTemplateRevision,
		})
	}
	return result
}

func (s *modelProfileService) buildTestTargetDB(
	db *gorm.DB,
	credential *repositories.ActiveStoreModelCredentialMetadata,
) *ModelProfileTestTarget {
	if db == nil || credential == nil {
		return nil
	}
	store := repositories.StoreRepository.GetInTenant(db, credential.StoreID, credential.TenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil
	}
	tenant := repositories.TenantRepository.Get(db, credential.TenantID)
	if tenant == nil || tenant.Status != enums.StatusOk {
		return nil
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(db, credential.TenantID, credential.StoreID)
	if assignment == nil ||
		assignment.Status != enums.StoreModelAssignmentStatusReady ||
		!strings.EqualFold(strings.TrimSpace(assignment.ReadinessStatus), "ready") ||
		assignment.TemplateID <= 0 ||
		assignment.TemplateRevision <= 0 {
		return nil
	}
	template := repositories.ModelProfileTemplateRepository.Get(db, assignment.TemplateID)
	if template == nil ||
		template.Status != enums.ModelProfileStatusActive ||
		template.Revision != assignment.TemplateRevision {
		return nil
	}
	return &ModelProfileTestTarget{
		Tenant:                 *tenant,
		Store:                  *store,
		StoreStaffBindingID:    credential.StoreStaffBindingID,
		StoreStaffAccountName:  modelProfileTestBindingAccountName(db, credential.StoreStaffBindingID),
		CredentialRevision:     credential.CredentialRevision,
		ActiveTemplateID:       template.ID,
		ActiveTemplateName:     template.Name,
		ActiveTemplateRevision: template.Revision,
	}
}

func (s *modelProfileService) loadTestTarget(
	tenantID, storeID, storeStaffBindingID int64,
	template *models.ModelProfileTemplate,
) (*ModelProfileTestTarget, *resolvedStoreModelCredential, error) {
	if tenantID <= 0 || storeID <= 0 || storeStaffBindingID <= 0 {
		return nil, nil, errorsx.InvalidParam("请选择一个已有 active 凭据的受控测试门店")
	}
	credentialMetadata := repositories.StoreModelCredentialRepository.FindActiveMetadataByBinding(sqls.DB(), tenantID, storeID, storeStaffBindingID)
	selected := s.buildTestTargetDB(sqls.DB(), credentialMetadata)
	if selected == nil {
		return nil, nil, errorsx.InvalidParam("测试门店没有可用的 active 凭据与模型方案")
	}
	activeTemplate := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), selected.ActiveTemplateID)
	if activeTemplate == nil ||
		normalizeGatewayBaseURL(activeTemplate.GatewayBaseURL) != normalizeGatewayBaseURL(template.GatewayBaseURL) {
		return nil, nil, errorsx.InvalidParam("测试门店当前方案不使用相同的统一 NewAPI 网关，禁止发送门店凭据")
	}
	credential, err := StoreModelCredentialService.ResolveActiveForBinding(tenantID, storeID, storeStaffBindingID)
	if err != nil {
		return nil, nil, err
	}
	if credential.Revision != selected.CredentialRevision {
		return nil, nil, errorsx.InvalidParam("测试门店凭据 revision 已变化，请刷新后重试")
	}
	return selected, credential, nil
}

func modelProfileTestBindingAccountName(db *gorm.DB, bindingID int64) string {
	fallback := fmt.Sprintf("门店员工号 #%d", bindingID)
	binding := repositories.StoreStaffBindingRepository.Get(db, bindingID)
	if binding == nil {
		return fallback
	}
	user := repositories.UserRepository.Get(db, binding.UserID)
	if user == nil {
		return fallback
	}
	return firstNonBlank(strings.TrimSpace(user.Nickname), strings.TrimSpace(user.Username), fallback)
}

func recordModelProfileTestRun(
	template *models.ModelProfileTemplate,
	slots []models.ModelProfileSlot,
	tenant *models.Tenant,
	store *models.Store,
	storeStaffBindingID int64,
	credentialRevision int64,
	credentialSource enums.ModelProfileTestCredentialSource,
	testErr error,
	startedAt time.Time,
	operator *dto.AuthPrincipal,
	meta StoreCredentialRequestMeta,
) (*models.ModelProfileTestRun, error) {
	if template == nil || tenant == nil || store == nil || storeStaffBindingID <= 0 || credentialRevision <= 0 || operator == nil {
		return nil, errorsx.BusinessError(3001, "模型方案测试证据上下文不完整")
	}
	completedAt := time.Now()
	status := enums.ModelProfileTestStatusPassed
	failedUsage := enums.ModelUsageSlot("")
	errorClass := ""
	errorMessage := ""
	if testErr != nil {
		status = enums.ModelProfileTestStatusFailed
		errorClass, errorMessage = publicCredentialValidationFailure(testErr, slots)
		var validationErr *storeCredentialValidationError
		if errors.As(testErr, &validationErr) {
			failedUsage = validationErr.UsageCode
		}
	}
	item := &models.ModelProfileTestRun{
		TemplateID: template.ID, TemplateRevision: template.Revision,
		ConfigDigest: modelProfileConfigurationDigest(template, slots),
		TenantID:     tenant.ID, StoreID: store.ID, StoreStaffBindingID: storeStaffBindingID,
		TenantName: firstNonBlank(tenant.ShortName, tenant.LegalName), StoreName: store.Name,
		CredentialRevision: credentialRevision, CredentialSource: credentialSource,
		Status: status, FailedUsageCode: failedUsage,
		ErrorClass: errorClass, ErrorMessage: errorMessage,
		RequestID:  trimCredentialAuditValue(meta.RequestID, 128),
		ClientIP:   trimCredentialAuditValue(meta.ClientIP, 64),
		LatencyMS:  completedAt.Sub(startedAt).Milliseconds(),
		OperatorID: operator.UserID, OperatorName: trimCredentialAuditValue(operator.Username, 100),
		CreatedAt: completedAt,
	}
	if err := repositories.ModelProfileTestRunRepository.Create(sqls.DB(), item); err != nil {
		return nil, errorsx.BusinessError(3001, "模型方案测试证据写入失败")
	}
	return item, nil
}

func modelProfileConfigurationDigest(template *models.ModelProfileTemplate, slots []models.ModelProfileSlot) string {
	if template == nil {
		return ""
	}
	sortedSlots := append([]models.ModelProfileSlot(nil), slots...)
	slices.SortFunc(sortedSlots, func(left, right models.ModelProfileSlot) int {
		if left.UsageCode < right.UsageCode {
			return -1
		}
		if left.UsageCode > right.UsageCode {
			return 1
		}
		return left.SortNo - right.SortNo
	})
	payload := struct {
		Code           string                            `json:"code"`
		Name           string                            `json:"name"`
		Description    string                            `json:"description"`
		Revision       int64                             `json:"revision"`
		GatewayBaseURL string                            `json:"gatewayBaseUrl"`
		Slots          []request.ModelProfileSlotRequest `json:"slots"`
	}{
		Code: normalizeModelProfileCode(template.Code), Name: strings.TrimSpace(template.Name),
		Description: strings.TrimSpace(template.Description), Revision: template.Revision,
		GatewayBaseURL: normalizeGatewayBaseURL(template.GatewayBaseURL),
		Slots:          modelSlotRequestsFromModels(sortedSlots),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

func requirePlatformModelProfileAccess(operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !operator.IsPlatformAccount {
		return errorsx.Forbidden("只有平台账号可以查看或管理模型方案内部配置")
	}
	return nil
}

func resolveStoreModelTenantScope(operator *dto.AuthPrincipal, requestedTenantID int64) (int64, error) {
	if operator == nil {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	if operator.IsPlatformAccount {
		if requestedTenantID <= 0 || repositories.TenantRepository.Get(sqls.DB(), requestedTenantID) == nil {
			return 0, errorsx.InvalidParam("接入公司不存在")
		}
		return requestedTenantID, nil
	}
	tenantID := operator.ActiveTenantID
	if tenantID <= 0 {
		tenantID = operator.TenantID
	}
	if tenantID <= 0 || requestedTenantID != tenantID {
		return 0, errorsx.Forbidden("只能管理当前接入公司的门店模型")
	}
	return tenantID, nil
}

func modelProfileAuditFields(operator *dto.AuthPrincipal, now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, UpdatedAt: now,
		CreateUserID: operator.UserID, CreateUserName: operator.Username,
		UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
	}
}

func normalizeModelProfileCode(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeGatewayBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func validJSONDocument(value string) bool {
	var document any
	return strings.TrimSpace(value) != "" && json.Unmarshal([]byte(value), &document) == nil
}

func modelProfileAPIModeCompatible(spec ModelUsageSlotSpec, apiMode string) bool {
	mode := strings.TrimSpace(apiMode)
	switch spec.ExpectedModelType {
	case enums.AIModelTypeLLM, enums.AIModelTypeVision:
		return mode == "chat_completions" || mode == "responses"
	case enums.AIModelTypeASR:
		return mode == "audio_transcriptions"
	case enums.AIModelTypeEmbedding:
		return mode == "embeddings"
	case enums.AIModelTypeRerank:
		return mode == "rerank"
	default:
		return false
	}
}

func defaultModelProfileSlotRequests() []request.ModelProfileSlotRequest {
	result := make([]request.ModelProfileSlotRequest, 0, len(RequiredModelUsageSlotSpecs()))
	for index, spec := range RequiredModelUsageSlotSpecs() {
		result = append(result, request.ModelProfileSlotRequest{
			UsageCode: string(spec.UsageCode), DisplayName: spec.DisplayName,
			ModelType: string(spec.ExpectedModelType), Provider: modelProfileProviderNewAPI,
			APIMode: spec.DefaultAPIMode, TimeoutMS: 30000, MaxRetryCount: 2, Enabled: true, SortNo: index + 1,
		})
	}
	return result
}

func buildModelProfileSlots(inputs []request.ModelProfileSlotRequest, templateID int64, operator *dto.AuthPrincipal, now time.Time) []models.ModelProfileSlot {
	result := make([]models.ModelProfileSlot, 0, len(inputs))
	for index, input := range inputs {
		usage := enums.ModelUsageSlot(strings.TrimSpace(input.UsageCode))
		spec, known := modelUsageSlotSpecByCode(usage)
		displayName := strings.TrimSpace(input.DisplayName)
		modelType := enums.AIModelType(strings.TrimSpace(input.ModelType))
		apiMode := strings.TrimSpace(input.APIMode)
		if known {
			if displayName == "" {
				displayName = spec.DisplayName
			}
			if modelType == "" {
				modelType = spec.ExpectedModelType
			}
			if apiMode == "" {
				apiMode = spec.DefaultAPIMode
			}
		}
		provider := strings.ToLower(strings.TrimSpace(input.Provider))
		if provider == "" {
			provider = modelProfileProviderNewAPI
		}
		timeoutMS := input.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 30000
		}
		sortNo := input.SortNo
		if sortNo <= 0 {
			sortNo = index + 1
		}
		result = append(result, models.ModelProfileSlot{
			TemplateID: templateID, UsageCode: usage, DisplayName: displayName,
			ModelType: modelType, Provider: provider, ModelName: strings.TrimSpace(input.ModelName), APIMode: apiMode,
			Dimension: input.Dimension, MaxContextTokens: input.MaxContextTokens, MaxOutputTokens: input.MaxOutputTokens,
			TimeoutMS: timeoutMS, MaxRetryCount: input.MaxRetryCount, Temperature: input.Temperature,
			SchemaVersion: strings.TrimSpace(input.SchemaVersion), PromptTemplate: strings.TrimSpace(input.PromptTemplate),
			JSONSchema: strings.TrimSpace(input.JSONSchema), Enabled: input.Enabled, SortNo: sortNo,
			AuditFields: modelProfileAuditFields(operator, now),
		})
	}
	return result
}

func modelSlotRequestsFromModels(items []models.ModelProfileSlot) []request.ModelProfileSlotRequest {
	result := make([]request.ModelProfileSlotRequest, 0, len(items))
	for _, item := range items {
		result = append(result, request.ModelProfileSlotRequest{
			UsageCode: string(item.UsageCode), DisplayName: item.DisplayName, ModelType: string(item.ModelType),
			Provider: item.Provider, ModelName: item.ModelName, APIMode: item.APIMode, Dimension: item.Dimension,
			MaxContextTokens: item.MaxContextTokens, MaxOutputTokens: item.MaxOutputTokens,
			TimeoutMS: item.TimeoutMS, MaxRetryCount: item.MaxRetryCount, Temperature: item.Temperature,
			SchemaVersion: item.SchemaVersion, PromptTemplate: item.PromptTemplate, JSONSchema: item.JSONSchema,
			Enabled: item.Enabled, SortNo: item.SortNo,
		})
	}
	return result
}

func normalizePositiveIDs(values []int64) []int64 {
	result := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func deduplicateModelProfileIssues(items []ModelProfileValidationIssue) []ModelProfileValidationIssue {
	result := make([]ModelProfileValidationIssue, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := string(item.UsageCode) + "\x00" + item.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}
