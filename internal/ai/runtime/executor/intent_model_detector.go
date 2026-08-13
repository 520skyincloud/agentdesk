package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

type runtimeIntentModelDetector interface {
	DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error)
}

type llmRuntimeIntentDetector struct{}

func detectRuntimeIntentWithModelStrict(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, detector runtimeIntentModelDetector) (callbacks.IntentTraceData, callbacks.IntentPromptTraceData, bool, error) {
	if isMediaOnlyWithoutActionableIntent(req.UserMessage) && !hasAdjacentTextMediaQuestion(req, history) {
		intent := callbacks.IntentTraceData{DetectedIntent: "media_gate", MatchedIntentCode: "media_gate", SubIntent: "media_only_no_question", IntentConfidence: 0.9, ShouldReply: false, Reason: "media gate: media-only message has no actionable intent"}
		return intent, selectIntentPromptPack(intent), true, nil
	}
	configs := loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))
	if detector == nil {
		detector = llmRuntimeIntentDetector{}
	}
	intent, err := detector.DetectRuntimeIntent(ctx, req, history, configs)
	if err != nil {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false,
			services.NewAIReplyExecutionError(services.AIReplyExecutionErrorIntentDetectFailed, err)
	}
	intent = normalizeModelIntentTrace(intent, req, history, configs)
	if intent.PrimaryIntent == "" {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false,
			services.NewAIReplyExecutionError(services.AIReplyExecutionErrorIntentDetectFailed, fmt.Errorf("empty normalized intent"))
	}
	prompt := promptForModelDetectedIntent(intent, configs)
	return intent, prompt, true, nil
}

func (llmRuntimeIntentDetector) DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	if resolveRuntimeFeatureModes(req).IntentContract == runtimeIntentContractV1 {
		return detectRuntimeIntentLegacy(ctx, req, history, configs)
	}
	resolved, err := resolveRuntimeIntentDetectModelCall(req)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	runtimeSchema, schemaCatalog, err := contracts.BuildRuntimeIntentSchema(contracts.MustSchema(contracts.SchemaIntentTasksV2), configs)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	intentConfig, err := withRuntimeIntentStructuredOutputSchema(resolved.RuntimeConfig(), runtimeSchema)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	if strings.TrimSpace(intentConfig.ModelName) == "" || strings.TrimSpace(string(intentConfig.Provider)) == "" {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent model unavailable")
	}
	intentCtx, usageCapture := usagex.WithCapture(ctx)
	intentCtx = usagex.WithScope(intentCtx, services.ModelCallUsageScope(
		resolved,
		req.Conversation.ID,
		req.UserMessage.ID,
		req.UserMessage.RequestID,
	))
	chatModel, err := factory.NewChatModelFactory().Build(intentCtx, intentConfig)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	scope := resolveRuntimeIntentScope(req)
	profile := resolveRuntimeIntentProfile(scope)
	if profile == nil {
		return callbacks.IntentTraceData{}, fmt.Errorf("published tenant industry profile unavailable")
	}
	instance, err := resolveRuntimeCompilerInstance(req, resolved)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	dialogueState, err := services.ConversationDialogueStateService.Load(req.Conversation.TenantID, req.Conversation.ID, runtimeSessionNo(req))
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	compileInput := contextcompiler.CompileInput{
		Stage: contextcompiler.CompileStageIntent,
		Scope: runtimeCompilerScope(req, resolved, instance),
		Model: *resolved, Instance: *instance, Agent: req.AIAgent,
		CurrentMessages: []models.Message{req.UserMessage}, RecentHistory: history.RawItems,
		DialogueState:         dialogueState,
		IntentInstruction:     runtimeIntentDetectV2Instruction(profile, configs),
		IntentSchema:          runtimeSchema,
		IntentProfileRevision: profile.Revision,
	}
	compiled, err := contextcompiler.New(nil).Compile(intentCtx, compileInput)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	firstStartedAt := time.Now()
	firstReceiptOffset := len(usageCapture.Receipts())
	firstCallCtx, firstCallCancel := context.WithTimeout(intentCtx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
	result, err := chatModel.Generate(firstCallCtx, compiled.Messages)
	firstCallCancel()
	if err != nil {
		recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptsSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), err)
		return callbacks.IntentTraceData{}, err
	}
	recordIntentModelUsage(req, intentConfig, resolved, result, gatewayReceiptsSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), nil)
	parsed, derived, err := parseRuntimeIntentTasksV2(result.Content, runtimeSchema, configs)
	if err != nil && runtimeProtocolRepairAllowed(err) {
		firstProtocolErr := err
		compileInput.RepairInstruction = buildRuntimeProtocolRepairInstruction(
			contracts.SchemaIntentTasksV2,
			err,
			schemaCatalog,
			req.UserMessage.Content,
			result.Content,
		)
		repairContext, compileErr := contextcompiler.New(nil).Compile(intentCtx, compileInput)
		if compileErr != nil {
			return callbacks.IntentTraceData{}, compileErr
		}
		if repairContext.Fingerprint != compiled.Fingerprint {
			return callbacks.IntentTraceData{}, fmt.Errorf("intent repair context fingerprint changed")
		}
		retryStartedAt := time.Now()
		retryReceiptOffset := len(usageCapture.Receipts())
		repairCallCtx, repairCallCancel := context.WithTimeout(intentCtx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
		retry, retryErr := chatModel.Generate(repairCallCtx, repairContext.Messages)
		repairCallCancel()
		if retryErr != nil {
			recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptsSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), retryErr)
			if fallback, ok := buildRuntimeIntentProtocolFallback(req, configs, firstProtocolErr); ok {
				return fallback, nil
			}
			return callbacks.IntentTraceData{}, fmt.Errorf("%w; retry failed: %v", firstProtocolErr, retryErr)
		}
		recordIntentModelUsage(req, intentConfig, resolved, retry, gatewayReceiptsSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), nil)
		parsed, derived, err = parseRuntimeIntentTasksV2(retry.Content, runtimeSchema, configs)
		if err != nil {
			if fallback, ok := buildRuntimeIntentProtocolFallback(req, configs, err); ok {
				return fallback, nil
			}
			return callbacks.IntentTraceData{}, err
		}
	}
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	return AdaptIntentV2ToLegacyTrace(parsed, derived), nil
}

func buildRuntimeIntentProtocolFallback(req RunInput, configs []models.ReplyIntentConfig, protocolErr error) (callbacks.IntentTraceData, bool) {
	if _, ok := strictjson.CodeOf(protocolErr); !ok {
		return callbacks.IntentTraceData{}, false
	}
	text := strings.TrimSpace(currentTurnDisplayText(req.UserMessage.Content))
	if text == "" {
		return callbacks.IntentTraceData{}, false
	}
	intent := callbacks.IntentTraceData{
		ShouldReply: true, IntentConfidence: 0.55, MatchMode: "protocol_local_recovery",
		Reason: "intent protocol repair exhausted; deterministic narrow recovery",
	}
	setTask := func(code, subIntent, requestMode string) {
		intent.PrimaryIntent = code
		intent.MatchedIntentCode = code
		intent.DetectedIntent = code
		intent.SubIntent = subIntent
		intent.IntentTasks = []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: code, SubIntent: subIntent, Text: text,
			RequestMode: requestMode, Confidence: intent.IntentConfidence,
			Reason: "deterministic protocol recovery",
		}}
	}
	switch {
	case explicitRuntimeHumanRequest(text) && runtimeIntentConfigEnabled(configs, "human_complaint_risk"):
		setTask("human_complaint_risk", "explicit_handoff", "request_action")
		intent.NeedsHumanRoute = true
		intent.HumanRoutePolicy = "managed_mode"
		intent.IntentTasks[0].NeedsHumanRoute = true
	case explicitCheckinKnowledgeRequest(text) && runtimeIntentConfigEnabled(configs, "hotel_info"):
		setTask("hotel_info", "checkin_process", "question")
		intent.NeedsKnowledge = true
		if runtimeIntentConfigEnabled(configs, "hotel_variable") {
			intent = ensureCheckinProcessMiniProgramTask(intent, req)
		}
	case explicitCurrentHotelResourceRequest(text, "phone") && runtimeIntentConfigEnabled(configs, "hotel_variable"):
		setTask("hotel_variable", "phone", "request_action")
		applyRuntimeProtocolFallbackResource(&intent, "provide_phone")
	case explicitCurrentHotelResourceRequest(text, "location") && runtimeIntentConfigEnabled(configs, "hotel_variable"):
		setTask("hotel_variable", "location", "request_action")
		applyRuntimeProtocolFallbackResource(&intent, "provide_location")
	case explicitCurrentHotelResourceRequest(text, "mini_program") && runtimeIntentConfigEnabled(configs, "hotel_variable"):
		setTask("hotel_variable", "mini_program", "request_action")
		applyRuntimeProtocolFallbackResource(&intent, "provide_mini_program")
	default:
		setTask("interaction", "clarify", "clarify_previous")
		intent.NeedsClarification = true
	}
	return normalizeModelIntentTrace(intent, req, adapter.HistoryBuildResult{}, configs), true
}

func applyRuntimeProtocolFallbackResource(intent *callbacks.IntentTraceData, action string) {
	if intent == nil {
		return
	}
	intent.NeedsResource = true
	intent.ResourceAction = action
	intent.ResourceActions = []string{action}
	intent.ResourceType = hotelVariableResourceTypeFromAction(action)
	if len(intent.IntentTasks) > 0 {
		intent.IntentTasks[0].NeedsResource = true
		intent.IntentTasks[0].ResourceAction = action
	}
}

func runtimeIntentConfigEnabled(configs []models.ReplyIntentConfig, code string) bool {
	_, ok := findIntentConfigByCode(configs, code)
	return ok
}

func explicitRuntimeHumanRequest(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{"转人工", "人工客服", "找人工", "找客服", "真人客服", "找真人", "人工接待", "客服人员"})
}

func explicitCheckinKnowledgeRequest(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" || strings.Contains(compact, "小程序") || strings.Contains(compact, "入口") {
		return false
	}
	return containsAny(compact, []string{
		"办理入住", "怎么入住", "咋入住", "怎么办入住", "入住怎么办", "入住怎么弄",
		"如何入住", "入住流程", "入住步骤", "我想入住", "我要入住", "入住",
	})
}

func explicitCurrentHotelResourceRequest(text, resourceType string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" {
		return false
	}
	if resourceType == "location" && containsAny(compact, []string{
		"小区", "商场", "车站", "机场", "餐厅", "饭店", "景点", "医院", "学校", "公司", "写字楼", "地铁", "高铁站", "火车站", "超市", "附近",
	}) {
		return false
	}
	currentHotel := containsAny(compact, []string{"酒店", "门店", "你们店", "贵店", "这家店", "这家酒店", "本店", "前台"})
	switch resourceType {
	case "phone":
		return currentHotel && containsAny(compact, []string{"电话", "号码", "联系电话"})
	case "location":
		return currentHotel && containsAny(compact, []string{"定位", "地址", "导航", "在哪里", "在哪儿", "怎么去"})
	case "mini_program":
		return containsAny(compact, []string{"入住小程序", "酒店小程序", "门店小程序", "你们店小程序"})
	default:
		return false
	}
}

func compactRuntimeProtocolText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "", "：", "", ":", "", "；", "", ";", "",
	).Replace(value)
}

func parseRuntimeIntentTasksV2(content string, runtimeSchema []byte, configs []models.ReplyIntentConfig) (contracts.IntentTasksV2, []DerivedTaskCapabilities, error) {
	if len(runtimeSchema) == 0 {
		runtimeSchema = contracts.MustSchema(contracts.SchemaIntentTasksV2)
	}
	parsed, err := strictjson.DecodeObject[contracts.IntentTasksV2]([]byte(content), strictjson.DecodeOptions{
		MaxBytes: 32 * 1024,
		Schema:   runtimeSchema,
	})
	if err != nil {
		return contracts.IntentTasksV2{}, nil, err
	}
	derived, err := DeriveRuntimeIntentCapabilities(parsed, configs)
	if err != nil {
		path := "$.tasks"
		if invariant, ok := runtimeIntentInvariantDetails(err); ok && strings.TrimSpace(invariant.Path) != "" {
			path = strings.TrimSpace(invariant.Path)
		}
		return contracts.IntentTasksV2{}, nil, &strictjson.ProtocolError{Code: strictjson.ErrorJSONBusinessInvariant, Path: path, Message: err.Error(), Err: err}
	}
	return parsed, derived, nil
}

func runtimeProtocolRepairAllowed(err error) bool {
	code, ok := strictjson.CodeOf(err)
	if !ok {
		return false
	}
	switch code {
	case strictjson.ErrorJSONRootNotObject, strictjson.ErrorJSONSyntaxInvalid,
		strictjson.ErrorJSONDuplicateKey, strictjson.ErrorJSONUnknownField,
		strictjson.ErrorJSONTrailingContent, strictjson.ErrorJSONSchemaInvalid:
		return true
	case strictjson.ErrorJSONBusinessInvariant:
		invariant, ok := runtimeIntentInvariantDetails(err)
		return ok && invariant.Code != intentInvariantDuplicateTaskSequence
	default:
		return false
	}
}

func buildRuntimeProtocolRepairInstruction(schemaName string, protocolErr error, catalog contracts.RuntimeIntentSchemaCatalog, currentText, firstOutput string) string {
	code, _ := strictjson.CodeOf(protocolErr)
	path := "$"
	var typed *strictjson.ProtocolError
	if errors.As(protocolErr, &typed) && strings.TrimSpace(typed.Path) != "" {
		path = strings.TrimSpace(typed.Path)
	}
	parts := []string{
		"上一版输出存在可修复的 JSON 协议错误。只修复协议，不新增、删除或改写当前客户原文中的业务任务。",
		"schema=" + strings.TrimSpace(schemaName),
		"errorCode=" + strings.TrimSpace(code),
		"jsonPath=" + path,
		"当前客户原文：" + boundedRuntimeRepairText(currentText, 4096),
		"第一次输出：" + boundedRuntimeRepairText(firstOutput, 8*1024),
	}
	if invariant, ok := runtimeIntentInvariantDetails(protocolErr); ok {
		parts = append(parts, "businessErrorCode="+invariant.Code)
		if len(invariant.AllowedValues) > 0 {
			parts = append(parts, "allowedValues="+strings.Join(invariant.AllowedValues, ","))
		}
	} else if len(catalog.IntentCodes) > 0 {
		parts = append(parts, "allowedIntentCodes="+strings.Join(catalog.IntentCodes, ","))
	}
	parts = append(parts, "重新输出唯一一个严格 JSON Object；不要输出 Markdown、解释、注释或额外文本。")
	return strings.Join(parts, "\n")
}

func boundedRuntimeRepairText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

func runtimeIntentDetectV2Instruction(profile *models.ReplyIntentProfile, configs []models.ReplyIntentConfig) string {
	parts := []string{
		"你是 IntentDetect 阶段。只判断当前客户表达与上一轮的关系，并按客户原始顺序拆分可独立处理的语义任务；不要回复客户。",
		"模型只输出语义字段 sequence、intent、subIntent、text、requestMode、confidence。不得输出 taskKey、needsKnowledge、needsResource、needsTool、needsHumanRoute、resourceAction 或任何执行结果。",
		"当前消息中的每个有效问题、资源请求、人工诉求或社交表达都必须覆盖；不得把跨主题问题压成一个任务，也不得从无关历史补出当前未问的任务。",
		"历史只用于解析紧邻指代、追问、重复、纠正、确认和取消。新主题必须与旧主题分开。",
	}
	if profile != nil {
		if description := strings.TrimSpace(profile.Description); description != "" {
			parts = append(parts, "行业说明："+preview(description, 500))
		}
	}
	if len(configs) > 0 {
		var catalog strings.Builder
		catalog.WriteString("当前已发布意图目录：\n")
		for _, config := range normalizeIntentConfigs(configs) {
			if config.Status != enums.StatusOk || strings.TrimSpace(config.Code) == "" {
				continue
			}
			catalog.WriteString("- code=")
			catalog.WriteString(strings.TrimSpace(config.Code))
			catalog.WriteString(" name=")
			catalog.WriteString(preview(config.Name, 80))
			if description := strings.TrimSpace(config.Description); description != "" {
				catalog.WriteString(" definition=")
				catalog.WriteString(preview(description, 240))
			}
			if examples := strings.TrimSpace(config.PositiveExamples); examples != "" {
				catalog.WriteString(" positive=")
				catalog.WriteString(preview(examples, 240))
			}
			if examples := strings.TrimSpace(config.NegativeExamples); examples != "" {
				catalog.WriteString(" negative=")
				catalog.WriteString(preview(examples, 160))
			}
			catalog.WriteString("\n")
		}
		parts = append(parts, strings.TrimSpace(catalog.String()))
	}
	return strings.Join(parts, "\n\n")
}

func resolveRuntimeCompilerInstance(req RunInput, resolved *services.ModelCallConfig) (*models.WxWorkProtocolInstance, error) {
	if resolved == nil || resolved.TenantID <= 0 || resolved.StoreID <= 0 || resolved.StoreStaffBindingID <= 0 {
		return nil, fmt.Errorf("runtime model scope is incomplete")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), req.Conversation.ID, resolved.TenantID)
	if route == nil || route.WxWorkInstanceID <= 0 || route.StoreID != resolved.StoreID || route.StoreStaffBindingID != resolved.StoreStaffBindingID {
		return nil, fmt.Errorf("runtime conversation route is outside the resolved model scope")
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetActivatedCurrentInTenant(sqls.DB(), route.WxWorkInstanceID, resolved.TenantID)
	if instance == nil || instance.StoreID != resolved.StoreID || instance.StoreStaffBindingID != resolved.StoreStaffBindingID {
		return nil, fmt.Errorf("runtime protocol instance is outside the resolved model scope")
	}
	return instance, nil
}

func runtimeCompilerScope(req RunInput, resolved *services.ModelCallConfig, instance *models.WxWorkProtocolInstance) contextcompiler.RuntimeScope {
	scope := contextcompiler.RuntimeScope{
		TenantID: resolved.TenantID, StoreID: resolved.StoreID, ConversationID: req.Conversation.ID,
		SessionNo: runtimeSessionNo(req), WxWorkInstanceID: instance.ID,
		StoreStaffBindingID: resolved.StoreStaffBindingID, JobID: req.JobID,
	}
	job := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), req.JobID, resolved.TenantID)
	if job != nil && job.ConversationID == req.Conversation.ID {
		scope.TurnID = job.TurnID
		scope.TurnVersion = job.TurnVersion
	}
	return scope
}

func runtimeSessionNo(req RunInput) int {
	if req.UserMessage.SessionNo > 0 {
		return req.UserMessage.SessionNo
	}
	return 1
}

func runtimeIntentDetectTimeout(timeoutMS int) time.Duration {
	if timeoutMS > 0 {
		return time.Duration(timeoutMS) * time.Millisecond
	}
	return 60 * time.Second
}

func runtimeIntentModelInvocationTimeout(timeoutMS, maxRetryCount int) time.Duration {
	perAttempt := runtimeIntentDetectTimeout(timeoutMS)
	if maxRetryCount < 0 {
		maxRetryCount = 0
	}
	if maxRetryCount > 10 {
		maxRetryCount = 10
	}
	attempts := maxRetryCount + 1
	backoff := time.Duration(maxRetryCount*(maxRetryCount+1)/2) * 100 * time.Millisecond
	return time.Duration(attempts)*perAttempt + backoff + time.Second
}

func recordIntentModelUsage(req RunInput, modelConfig modelconfig.Config, resolved *services.ModelCallConfig, message *schema.Message, receipts []usagex.Receipt, attempt int, latencyMS int64, callErr error) {
	requestID := strings.TrimSpace(req.UserMessage.RequestID)
	if requestID == "" {
		return
	}
	status := "completed"
	errorMessage := ""
	if callErr != nil {
		status = "failed"
		errorMessage = modelconfig.InvocationErrorClass(callErr)
	}
	event := models.AIUsageEvent{
		EventKey:       fmt.Sprintf("%s:intent_detect:%d", requestID, attempt),
		ConversationID: req.Conversation.ID, MessageID: req.UserMessage.ID, RequestID: requestID,
		Stage: "intent_detect", Provider: string(modelConfig.Provider), Model: modelConfig.ModelName,
		MetricSource: services.AIUsageMetricSourceProviderOperation,
		LatencyMS:    latencyMS, Status: status, ErrorClass: errorMessage, ErrorMessage: errorMessage,
	}
	services.AIUsageEventService.ApplyModelCallAttribution(&event, resolved)
	if message != nil && message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		usage := message.ResponseMeta.Usage
		event.PromptTokens = int64(usage.PromptTokens)
		event.CompletionTokens = int64(usage.CompletionTokens)
		event.CachedPromptTokens = int64(usage.PromptTokenDetails.CachedTokens)
		event.ReasoningTokens = int64(usage.CompletionTokensDetails.ReasoningTokens)
		event.MetricSource = services.AIUsageMetricSourceUpstreamActual
	}
	applyGatewayReceiptToUsageEvent(&event, lastGatewayReceipt(receipts))
	_ = services.AIUsageEventService.RecordWithGatewayReceipts(event, receipts)
}

func gatewayReceiptsSince(capture *usagex.Capture, offset int) []usagex.Receipt {
	receipts := capture.Receipts()
	if offset < 0 || offset >= len(receipts) {
		return nil
	}
	return append([]usagex.Receipt(nil), receipts[offset:]...)
}

func lastGatewayReceipt(receipts []usagex.Receipt) *usagex.Receipt {
	if len(receipts) == 0 {
		return nil
	}
	return &receipts[len(receipts)-1]
}

func applyGatewayReceiptToUsageEvent(event *models.AIUsageEvent, receipt *usagex.Receipt) {
	if event == nil || receipt == nil {
		return
	}
	event.Gateway = receipt.Gateway
	event.GatewayRequestID = receipt.RequestID
	event.GatewayUpstreamID = receipt.UpstreamRequestID
	event.GatewayHTTPStatus = receipt.StatusCode
	event.CallStartedAt = &receipt.StartedAt
	event.CallFinishedAt = &receipt.FinishedAt
	if receipt.LatencyMS() > 0 {
		event.LatencyMS = receipt.LatencyMS()
	}
}

func resolveRuntimeIntentDetectModelCall(req RunInput) (*services.ModelCallConfig, error) {
	if req.Conversation.ID <= 0 {
		return nil, fmt.Errorf("conversation is required for intent model")
	}
	return services.ModelCallResolverService.ResolveForConversation(req.Conversation.ID, enums.ModelUsageSlotIntentDetectLLM)
}

func runtimeIntentDetectSystemPrompt() string {
	return legacyRuntimeIntentDetectSystemPromptForProfile(nil)
}

func runtimeIntentDetectSystemPromptForProfile(profile *models.ReplyIntentProfile) string {
	return legacyRuntimeIntentDetectSystemPromptForProfile(profile)
}

func buildRuntimeIntentDetectUserPrompt(req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) string {
	var b strings.Builder
	currentText := strings.TrimSpace(req.UserMessage.Content)
	currentDisplayText := currentTurnDisplayText(currentText)
	b.WriteString("必须分类的当前消息:\n")
	b.WriteString(currentDisplayText)
	b.WriteString("\n\n当前消息类型: ")
	b.WriteString(string(req.UserMessage.MessageType))
	if timeLabel := adapter.RuntimeMessageTimeLabel(&req.UserMessage); timeLabel != "" {
		b.WriteString("\n当前消息时间: ")
		b.WriteString(timeLabel)
	}
	b.WriteString("\n\n判别纪律：只给“当前消息”分类；最近原始消息、媒体理解和长期记忆只用于解释“这个/刚才/还/继续/那”等指代。")
	b.WriteString("如果当前消息已经有独立的新主题，禁止沿用上一轮早餐、停车、投诉、安全、转人工等历史主题。")
	b.WriteString("但若紧邻的上一条 AI 客服消息正在追问一个业务问题的偏好、条件、范围或选项，当前短回答属于该业务的连续补充：必须继承该业务 intent/subIntent，并将 intentTasks[].text 写成包含上一轮业务主题和当前补充条件的完整检索问题。")
	b.WriteString("例如 AI 问附近餐饮口味、客户答‘麻辣口味的’，应输出 hotel_info/surrounding_facilities 且 needsKnowledge=true，任务文本可写‘附近餐饮推荐，偏好麻辣口味’。没有紧邻业务追问时，独立短语不得从更早历史强行继承旧主题。")
	b.WriteString("历史消息使用[历史消息][说话人][时间]格式，必须分清客户、AI客服、人工客服分别说了什么。")
	b.WriteString("若当前消息包含按序编号的连续消息段，必须按顺序为每个仍有效的问题各生成一个 intentTasks 条目；不得只保留主问题、最后一句或把跨主题问题压成一个含糊意图。顶层字段只能汇总完整 intentTasks。")
	mediaContext := currentAndRecentMediaText(req, history)
	if mediaContext != "" {
		b.WriteString("\n\n上下文中的媒体理解:\n")
		b.WriteString(preview(mediaContext, 1200))
		b.WriteString("\n媒体解析结果只是上下文，不要输出单独的媒体类意图；是否使用它解释当前问题，由你根据当前消息语义判断。")
	}
	if len(history.RawItems) > 0 {
		b.WriteString("\n\n最近原始消息(低于当前消息优先级):\n")
		start := len(history.RawItems) - 8
		if start < 0 {
			start = 0
		}
		for _, item := range history.RawItems[start:] {
			text := adapter.RuntimeHistoryMessageContent(&item)
			if text != "" {
				b.WriteString("- ")
				b.WriteString(preview(text, 160))
				b.WriteString("\n")
			}
		}
	}
	if strings.TrimSpace(history.MemorySource) != "" {
		b.WriteString("\n长期记忆摘要(最低优先级，房号等一次性入住事实不能当当前事实):\n")
		b.WriteString(preview(history.MemorySource, 800))
	}
	if len(configs) > 0 {
		b.WriteString("\n\n启用的分类配置(用于理解分类含义，不要按关键词机械匹配):\n")
		for _, cfg := range normalizeIntentConfigs(configs) {
			b.WriteString("- code=")
			b.WriteString(canonicalIntentCode(cfg.Code))
			b.WriteString(" name=")
			b.WriteString(strings.TrimSpace(cfg.Name))
			if desc := strings.TrimSpace(cfg.Description); desc != "" {
				b.WriteString(" desc=")
				b.WriteString(preview(desc, 80))
			}
			b.WriteString("\n")
		}
	}
	if currentText != "" {
		b.WriteString("\n再次强调，最终 JSON 必须只分类这条当前消息：\n")
		b.WriteString(currentDisplayText)
		b.WriteString("\n")
	}
	b.WriteString("\n请输出严格 JSON。")
	return b.String()
}

func parseRuntimeIntentDetectJSON(content string) (contracts.IntentTasksV2, error) {
	return strictjson.DecodeObject[contracts.IntentTasksV2]([]byte(content), strictjson.DecodeOptions{
		MaxBytes: 32 * 1024,
		Schema:   contracts.MustSchema(contracts.SchemaIntentTasksV2),
	})
}

func normalizeModelIntentTrace(intent callbacks.IntentTraceData, req RunInput, _ adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	intent.PrimaryIntent = canonicalIntentCode(intent.PrimaryIntent)
	if intent.PrimaryIntent == "" {
		intent.PrimaryIntent = canonicalIntentCode(intent.MatchedIntentCode)
	}
	if intent.PrimaryIntent == "" || !isRuntimeTopLevelIntent(intent.PrimaryIntent) {
		intent.PrimaryIntent = "interaction"
		intent.MatchedIntentCode = "interaction"
		intent.SubIntent = "clarify"
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	intent.MatchedIntentCode = intent.PrimaryIntent
	intent.ShouldReply = true
	if intent.IntentConfidence <= 0 || intent.IntentConfidence > 1 {
		intent.IntentConfidence = 0.65
	}
	if intent.IntentConfidence < 0.45 && intent.PrimaryIntent != "human_complaint_risk" {
		intent.PrimaryIntent = "interaction"
		intent.MatchedIntentCode = "interaction"
		intent.SubIntent = "clarify"
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	intent.IntentTasks = normalizeRuntimeIntentTasks(intent.IntentTasks)
	intent = deriveModelIntentFromTasks(intent)
	if intentHasHotelVariableTask(intent) {
		intent.ResourceActions = normalizeHotelVariableResourceActions(intent.ResourceActions, intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
	}
	switch intent.PrimaryIntent {
	case "hotel_info":
		intent.NeedsKnowledge = true
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent.NeedsHumanRoute = false
		if strings.TrimSpace(intent.SubIntent) == "" {
			intent.SubIntent = "store_knowledge"
		}
	case "hotel_variable":
		intent.NeedsResource = true
		intent.NeedsHumanRoute = false
		intent.ResourceActions = normalizeHotelVariableResourceActions(intent.ResourceActions, intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
		resourceAction := ""
		if len(intent.ResourceActions) > 0 {
			resourceAction = intent.ResourceActions[0]
		}
		resourceType, resourceAction := normalizeHotelVariableResourceAction(resourceAction, intent.ResourceType, intent.SubIntent)
		intent.ResourceType = resourceType
		intent.ResourceAction = resourceAction
		if len(intent.ResourceActions) == 0 && resourceAction != "" {
			intent.ResourceActions = []string{resourceAction}
		}
		if resourceType != "" && resourceType != "store_variable" {
			intent.SubIntent = resourceType
		} else if intent.SubIntent == "" {
			intent.SubIntent = resourceType
		}
		if intentHasMixedHotelInfoTask(intent) {
			intent.NeedsKnowledge = true
		} else {
			intent.NeedsKnowledge = false
		}
	case "service_request":
		intent.NeedsKnowledge = true
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent = enforceHumanRouteFlagByIntentCategory(intent)
	case "interaction":
		intent.NeedsKnowledge = false
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent.NeedsHumanRoute = false
		if strings.TrimSpace(intent.SubIntent) == "" {
			intent.SubIntent = "chat"
		}
		if intent.NeedsClarification && strings.TrimSpace(intent.SubIntent) == "chat" {
			intent.SubIntent = "clarify"
		}
		if strings.TrimSpace(intent.SubIntent) == "weather_query" || strings.TrimSpace(intent.ResourceAction) == "get_weather" {
			intent.NeedsClarification = false
			intent.NeedsKnowledge = false
			intent.NeedsTool = true
			intent.NeedsResource = false
			intent.NeedsHumanRoute = false
			intent.ResourceAction = "get_weather"
			intent.ToolCodes = appendIfMissing(intent.ToolCodes, toolx.BuiltinWeather.Code)
		}
	case "human_complaint_risk":
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = true
		if intent.SubIntent == "emergency_safety" {
			intent.HumanRoutePolicy = "emergency_safety"
		} else {
			if strings.TrimSpace(intent.SubIntent) == "" {
				intent.SubIntent = "explicit_handoff"
			}
			intent.HumanRoutePolicy = "managed_mode"
		}
	}
	if explicitCheckinKnowledgeRequest(req.UserMessage.Content) && intent.PrimaryIntent != "human_complaint_risk" &&
		runtimeIntentConfigEnabled(configs, "hotel_info") {
		intent.PrimaryIntent = "hotel_info"
		intent.MatchedIntentCode = "hotel_info"
		intent.SubIntent = "checkin_process"
		intent.NeedsClarification = false
		intent.NeedsKnowledge = true
		intent.Reason = appendIntentReason(intent.Reason, "deterministic checkin rule")
		intent = ensureCheckinProcessMiniProgramTaskIfConfigured(intent, req, configs)
	}
	if shouldAttachCheckinMiniProgramTask(intent) {
		intent = ensureCheckinProcessMiniProgramTask(intent, req)
	}
	if len(intent.ResourceActions) > 0 && intent.PrimaryIntent != "human_complaint_risk" {
		intent.NeedsResource = true
		if strings.TrimSpace(intent.ResourceAction) == "" {
			intent.ResourceAction = intent.ResourceActions[0]
		}
		resourceType, resourceAction := normalizeHotelVariableResourceAction(intent.ResourceAction, intent.ResourceType, intent.SubIntent)
		intent.ResourceType = resourceType
		intent.ResourceAction = resourceAction
		if intent.PrimaryIntent == "hotel_variable" && resourceType != "" && resourceType != "store_variable" {
			intent.SubIntent = resourceType
		}
	}
	if intentHasMixedHotelInfoTask(intent) && intent.PrimaryIntent != "human_complaint_risk" {
		intent.NeedsKnowledge = true
	}
	intent = enforceHumanRouteFlagByIntentCategory(intent)
	if intent.DetectedIntent == "" {
		intent.DetectedIntent = intent.PrimaryIntent
	}
	if config, ok := findIntentConfigByCode(configs, intent.PrimaryIntent); ok {
		intent.MatchedConfigID = config.ID
		intent.MatchedConfig = strings.TrimSpace(config.Name)
		intent.MatchMode = "model"
	}
	return intent
}

func ensureCheckinProcessMiniProgramTaskIfConfigured(intent callbacks.IntentTraceData, req RunInput, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	if !runtimeIntentConfigEnabled(configs, "hotel_variable") {
		intent.NeedsResource = false
		intent.ResourceActions = nil
		intent.ResourceAction = ""
		intent.ResourceType = ""
		return intent
	}
	return ensureCheckinProcessMiniProgramTask(intent, req)
}

func deriveModelIntentFromTasks(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if len(intent.IntentTasks) == 0 {
		return intent
	}
	primary := ""
	hasHuman := false
	hasVariable := false
	hasCheckinKnowledge := false
	hasKnowledge := false
	hasResource := false
	resourceActions := make([]string, 0)
	for i := range intent.IntentTasks {
		task := &intent.IntentTasks[i]
		task.Intent = canonicalIntentCode(task.Intent)
		if task.Intent == "" || !isRuntimeTopLevelIntent(task.Intent) {
			task.Intent = "interaction"
		}
		switch task.Intent {
		case "human_complaint_risk":
			hasHuman = true
			task.NeedsHumanRoute = true
		case "hotel_variable":
			hasVariable = true
			hasResource = true
			task.NeedsResource = true
			_, task.ResourceAction = normalizeHotelVariableResourceAction(task.ResourceAction, "", task.SubIntent)
			if task.ResourceAction != "" && task.ResourceAction != "provide_store_variable" {
				resourceActions = appendIfMissing(resourceActions, task.ResourceAction)
			}
		case "hotel_info":
			hasKnowledge = true
			task.NeedsKnowledge = true
			if isCheckinProcessSubIntent(task.SubIntent) {
				hasCheckinKnowledge = true
			}
		}
		if task.NeedsKnowledge {
			hasKnowledge = true
		}
		if task.NeedsResource {
			hasResource = true
		}
	}
	if hasHuman {
		primary = "human_complaint_risk"
	} else if hasCheckinKnowledge {
		primary = "hotel_info"
	} else if hasVariable {
		primary = "hotel_variable"
	} else {
		for _, task := range intent.IntentTasks {
			if task.Intent != "interaction" {
				primary = task.Intent
				break
			}
		}
	}
	if primary == "" && len(intent.IntentTasks) > 0 {
		primary = intent.IntentTasks[0].Intent
	}
	if primary == "" {
		primary = "interaction"
	}
	secondary := make([]string, 0)
	for _, task := range intent.IntentTasks {
		if task.Intent != primary {
			secondary = appendIfMissing(secondary, task.Intent)
		}
	}
	if intent.PrimaryIntent != "" && intent.PrimaryIntent != primary {
		intent.Reason = appendIntentReason(intent.Reason, "primaryIntent derived from intentTasks")
	}
	intent.PrimaryIntent = primary
	intent.MatchedIntentCode = primary
	intent.SecondaryIntents = mergeStringLists(secondary, intent.SecondaryIntents)
	intent.SecondaryIntentCodes = mergeStringLists(secondary, intent.SecondaryIntentCodes)
	intent.NeedsKnowledge = hasKnowledge
	intent.NeedsResource = hasResource
	intent.NeedsHumanRoute = hasHuman
	if len(resourceActions) > 0 {
		intent.ResourceActions = resourceActions
		intent.ResourceAction = resourceActions[0]
		intent.ResourceType, intent.ResourceAction = normalizeHotelVariableResourceAction(intent.ResourceAction, intent.ResourceType, intent.SubIntent)
		if intent.ResourceType != "" && intent.PrimaryIntent == "hotel_variable" {
			intent.SubIntent = intent.ResourceType
		}
	}
	if strings.TrimSpace(intent.SubIntent) == "" || intent.PrimaryIntent == "hotel_variable" && intent.SubIntent == "store_variable" {
		if subIntent := firstTaskSubIntentForPrimary(intent.IntentTasks, intent.PrimaryIntent); subIntent != "" {
			intent.SubIntent = subIntent
		}
	}
	return intent
}

func firstTaskSubIntentForPrimary(tasks []callbacks.IntentTaskTraceData, primary string) string {
	for _, task := range tasks {
		if task.Intent == primary && strings.TrimSpace(task.SubIntent) != "" {
			return strings.TrimSpace(task.SubIntent)
		}
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.SubIntent) != "" {
			return strings.TrimSpace(task.SubIntent)
		}
	}
	return ""
}

func mergeStringLists(first []string, second []string) []string {
	ret := make([]string, 0, len(first)+len(second))
	for _, item := range first {
		ret = appendIfMissing(ret, strings.TrimSpace(item))
	}
	for _, item := range second {
		ret = appendIfMissing(ret, strings.TrimSpace(item))
	}
	return ret
}

func enforceHumanRouteFlagByIntentCategory(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if isHandoffIntentCategory(intent) {
		return intent
	}
	if intent.NeedsHumanRoute || strings.TrimSpace(intent.HumanRoutePolicy) != "" {
		intent.Reason = appendIntentReason(intent.Reason, "human route ignored: handoff confirmation only belongs to human_complaint_risk intent category")
	}
	intent.NeedsHumanRoute = false
	intent.HumanRoutePolicy = ""
	return intent
}

func appendIntentReason(current string, addition string) string {
	current = strings.TrimSpace(current)
	addition = strings.TrimSpace(addition)
	if current == "" {
		return addition
	}
	if addition == "" || strings.Contains(current, addition) {
		return current
	}
	return current + "; " + addition
}

func intentHasMixedHotelInfoTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" || task.NeedsKnowledge {
			return true
		}
	}
	return false
}

func shouldAttachCheckinMiniProgramTask(intent callbacks.IntentTraceData) bool {
	if intent.PrimaryIntent != "hotel_info" {
		return false
	}
	if isCheckinProcessSubIntent(intent.SubIntent) {
		return true
	}
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" && isCheckinProcessSubIntent(task.SubIntent) {
			return true
		}
	}
	return false
}

func isCheckinProcessSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "checkin_process", "check_in", "checkin", "check_in_process", "checkin_steps", "check_in_steps", "checkin_guide":
		return true
	default:
		return false
	}
}

func ensureCheckinProcessMiniProgramTask(intent callbacks.IntentTraceData, req RunInput) callbacks.IntentTraceData {
	intent.NeedsKnowledge = true
	intent.NeedsResource = true
	intent.ResourceActions = normalizeHotelVariableResourceActions(append(intent.ResourceActions, "provide_mini_program"), intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
	if strings.TrimSpace(intent.ResourceAction) == "" {
		intent.ResourceAction = "provide_mini_program"
	}
	if strings.TrimSpace(intent.SubIntent) == "" || intent.SubIntent == "check_in" || intent.SubIntent == "checkin" {
		intent.SubIntent = "checkin_process"
	}
	currentText := strings.TrimSpace(currentTurnDisplayText(req.UserMessage.Content))
	if currentText == "" {
		currentText = "办理入住"
	}
	hasKnowledgeTask := false
	hasMiniProgramTask := false
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent == "hotel_info" && isCheckinProcessSubIntent(intent.IntentTasks[i].SubIntent) {
			intent.IntentTasks[i].SubIntent = "checkin_process"
			intent.IntentTasks[i].NeedsKnowledge = true
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = currentText
			}
			hasKnowledgeTask = true
		}
		if intent.IntentTasks[i].Intent == "hotel_variable" && strings.TrimSpace(intent.IntentTasks[i].ResourceAction) == "provide_mini_program" {
			intent.IntentTasks[i].SubIntent = "mini_program"
			intent.IntentTasks[i].NeedsResource = true
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = "发送入住小程序入口"
			}
			hasMiniProgramTask = true
		}
	}
	if !hasKnowledgeTask {
		intent.IntentTasks = append([]callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "checkin_process",
			Text:           currentText,
			NeedsKnowledge: true,
			Reason:         "checkin process needs knowledge tutorial",
		}}, intent.IntentTasks...)
	}
	if !hasMiniProgramTask {
		intent.IntentTasks = append(intent.IntentTasks, callbacks.IntentTaskTraceData{
			Intent:         "hotel_variable",
			SubIntent:      "mini_program",
			Text:           "发送入住小程序入口",
			NeedsResource:  true,
			ResourceAction: "provide_mini_program",
			Reason:         "checkin process should also provide configured mini program entry",
		})
	}
	intent.Reason = appendIntentReason(intent.Reason, "checkin_process attached mini program resource action")
	return intent
}

func promptForModelDetectedIntent(intent callbacks.IntentTraceData, configs []models.ReplyIntentConfig) callbacks.IntentPromptTraceData {
	if config, ok := findIntentConfigByCode(configs, intent.PrimaryIntent); ok {
		return promptTraceFromConfig(config, intent)
	}
	return selectIntentPromptPack(intent)
}

func findIntentConfigByCode(configs []models.ReplyIntentConfig, code string) (models.ReplyIntentConfig, bool) {
	code = canonicalIntentCode(code)
	for _, item := range normalizeIntentConfigs(configs) {
		if canonicalIntentCode(item.Code) == code {
			return item, true
		}
	}
	return models.ReplyIntentConfig{}, false
}

func isRuntimeTopLevelIntent(code string) bool {
	switch code {
	case "hotel_info", "service_request", "human_complaint_risk", "interaction", "hotel_variable":
		return true
	default:
		return false
	}
}

func normalizeHotelVariableResourceAction(action string, resourceType string, subIntent string) (string, string) {
	action = strings.TrimSpace(action)
	switch action {
	case "provide_phone":
		return "phone", action
	case "provide_location":
		return "location", action
	case "send_miniprogram", "provide_mini_program":
		return "mini_program", "provide_mini_program"
	case "provide_store_group":
		return "store_group", action
	}
	resourceType = normalizeHotelVariableResourceType(resourceType)
	if resourceType == "" {
		resourceType = normalizeHotelVariableResourceType(subIntent)
	}
	switch resourceType {
	case "phone":
		return resourceType, "provide_phone"
	case "location":
		return resourceType, "provide_location"
	case "mini_program":
		return resourceType, "provide_mini_program"
	default:
		return "store_variable", "provide_store_variable"
	}
}

func normalizeHotelVariableResourceActions(actions []string, action string, resourceType string, subIntent string, tasks []callbacks.IntentTaskTraceData) []string {
	ret := make([]string, 0, len(actions)+1)
	add := func(value string) {
		_, normalized := normalizeHotelVariableResourceAction(value, "", "")
		if normalized == "provide_store_variable" {
			return
		}
		ret = appendIfMissing(ret, normalized)
	}
	for _, item := range actions {
		add(item)
	}
	for _, task := range tasks {
		if task.Intent == "hotel_variable" || task.NeedsResource {
			add(task.ResourceAction)
		}
	}
	add(action)
	if len(ret) == 0 {
		_, normalized := normalizeHotelVariableResourceAction("", resourceType, subIntent)
		if normalized != "provide_store_variable" {
			ret = appendIfMissing(ret, normalized)
		}
	}
	return ret
}

func normalizeRuntimeIntentTasks(tasks []callbacks.IntentTaskTraceData) []callbacks.IntentTaskTraceData {
	ret := make([]callbacks.IntentTaskTraceData, 0, len(tasks))
	for _, task := range tasks {
		task.Intent = canonicalIntentCode(task.Intent)
		task.SubIntent = strings.TrimSpace(task.SubIntent)
		task.Text = strings.TrimSpace(task.Text)
		task.ResourceAction = strings.TrimSpace(task.ResourceAction)
		task.Reason = strings.TrimSpace(task.Reason)
		if task.Intent == "" {
			continue
		}
		if task.Intent == "hotel_info" {
			task.NeedsKnowledge = true
		}
		if task.Intent == "hotel_variable" {
			task.NeedsResource = true
			_, task.ResourceAction = normalizeHotelVariableResourceAction(task.ResourceAction, "", task.SubIntent)
		}
		if task.Intent == "human_complaint_risk" {
			task.NeedsHumanRoute = true
		}
		ret = append(ret, task)
	}
	return ret
}

func intentHasHotelVariableTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_variable" || task.NeedsResource {
			return true
		}
	}
	return len(intent.ResourceActions) > 0 || strings.TrimSpace(intent.ResourceAction) != "" || normalizeHotelVariableResourceType(intent.ResourceType) != ""
}

func normalizeHotelVariableResourceType(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case "phone", "contact_phone", "store_phone":
		return "phone"
	case "location", "address", "navigation", "store_location":
		return "location"
	case "mini_program", "miniprogram", "miniProgram", "checkin_miniprogram", "send_miniprogram":
		return "mini_program"
	case "store_group", "room_group":
		return "store_group"
	default:
		return strings.TrimSpace(resourceType)
	}
}
