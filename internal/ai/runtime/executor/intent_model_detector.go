package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/channelbreaker"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/strictjson"
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
	modes := resolveRuntimeFeatureModes(req)
	if modes.IntentContract == runtimeIntentContractV1 {
		return detectRuntimeIntentLegacy(ctx, req, history, configs)
	}
	// Strict V3 只保留给显式实验开关 AI_RUNTIME_MULTIMODAL_V3_STRICT；
	// 生产旧变量不能再切换这条链路。
	if modes.IntentContract == runtimeIntentContractV3 {
		return detectRuntimeIntentV3(ctx, req, history, configs)
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
	if open, retryAt := channelbreaker.IsOpen("intent_detect", resolved.ModelName, time.Now()); open {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent channel breaker open until %s", retryAt.Format(time.RFC3339))
	}
	firstCallCtx, firstCallCancel := context.WithTimeout(intentCtx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
	result, err := chatModel.Generate(firstCallCtx, compiled.Messages)
	firstCallCancel()
	if err != nil {
		channelbreaker.RecordFailure("intent_detect", resolved.ModelName, time.Now())
		recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptsSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), err)
		return callbacks.IntentTraceData{}, err
	}
	channelbreaker.RecordSuccess("intent_detect", resolved.ModelName)
	recordIntentModelUsage(req, intentConfig, resolved, result, gatewayReceiptsSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), nil)
	parsed, derived, err := parseRuntimeIntentTasksV2(result.Content, runtimeSchema, configs)
	if err != nil && runtimeProtocolRepairAllowed(err) {
		firstProtocolErr := err
		compileInput.RepairInstruction = buildRuntimeProtocolRepairInstruction(
			contracts.SchemaIntentTasksV2,
			err,
			schemaCatalog,
			runtimeUserMessageText(req.UserMessage),
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
	text := runtimeUserMessageText(req.UserMessage)
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
		intent = ensureCheckinProcessKnowledgeTask(intent, req)
		intent = ensureCheckinProcessMiniProgramTaskIfConfigured(intent, req, configs)
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
		"办理入住", "办入住", "怎么入住", "咋入住", "怎么办入住", "入住怎么办", "入住怎么弄",
		"如何入住", "入住流程", "入住步骤",
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
		"你是酒店无人化客服系统的 IntentDetect 阶段。只判断当前客户表达与上一轮的关系，并按客户原始顺序拆分可独立处理的语义任务；不要回复客户。",
		"模型只输出语义字段 sequence、intent、subIntent、text、requestMode、confidence。不得输出 taskKey、needsKnowledge、needsResource、needsTool、needsHumanRoute、resourceAction 或任何执行结果。",
		"当前消息中的每个有效问题、资源请求、人工诉求或社交表达都必须覆盖；不得把跨主题问题压成一个任务，也不得从无关历史补出当前未问的任务。",
		"历史只用于解析紧邻指代、追问、重复、纠正、确认和取消。新主题必须与旧主题分开。",
		"只允许 5 个顶层 intent：hotel_info、hotel_variable、service_request、human_complaint_risk、interaction。",
		"【人工/投诉/风险边界·最关键】只有当前消息明确要求人工，或明确表达投诉升级、赔付退款、订单/价格严重争议、安全事件，才能归 human_complaint_risk。单纯骂人、吐槽、说你不满、说“来点优惠/续住能便宜点/床单能换不/空调不制冷怎么办/你们客服几点上班”，只要没有明确人工/投诉/赔付/安全诉求，一律不得归 human_complaint_risk：优惠/续住/权益归 hotel_info（subIntent=discount 或续住），设备/用品/流程问题归 hotel_info，单纯不满归 interaction。",
		"【问信息 vs 要动作边界】判断标准是客户想不想要现状发生改变、或想不想要真人来做一件事：只是问“是什么/能不能/怎么弄/在哪/多久/几点/多少钱/流程是什么”，现状不变，都是要信息，归 hotel_info；“换/改/调/取消/退/升/降/送/修/加/订/办/打扫/叫醒/搬/拿/联系人来”等想改变现状或要真人介入的诉求，才是要动作，归 service_request。例：“空调不制冷怎么办”是 hotel_info，“叫人来看看空调”是 service_request。",
		"【定位对象判断·关键】客户明确要其他地点（菜市场/超市/厕所/游乐场/银行/机场/景点/商场等）的定位或导航时，绝不能归 hotel_variable：在问酒店周边/前文推荐地点归 hotel_info（subIntent=surrounding_facilities），其他外部地点归 interaction（subIntent=clarify）。只有明确索要当前酒店位置（“酒店在哪/你们店定位发我/门店地址发我/导航到酒店”）才是 hotel_variable + subIntent=location。",
		"【多地点歧义】定位/地址/导航先判断对象。当前消息点名的外部地点，或最近一轮仍在讨论的外部地点所指代的“那里/那个地方/它”，都以该外部地点为准，不能输出 provide_location。若同时有多个地点且无法唯一判断，归 interaction + clarify，只追问要哪个地点，不取变量、不发定位。",
		"【纠错 vs 业务边界】纠错语气本身不是独立业务任务。客户只是指出系统看错、听错、理解错，且没有要求继续回答业务问题时，归 interaction + correction。客户一边纠正一边明确要回答酒店问题时，必须按被纠正后的业务目标分类，不能因为“不是、别串了、我问的是”这类纠错语气归 interaction。例：“我没给你发语音大哥”是 interaction/correction；“我问的是停车，不是早餐，停车入口在哪”是 hotel_info/parking。",
		"【紧邻追问继承】若紧邻的上一条 AI 客服消息正在追问某业务问题的偏好、条件、范围或选项，客户当前的短回答就是该业务的连续补充，不是独立闲聊。必须继承该业务 intent/subIntent，并把 text 写成“上一轮业务主题 + 当前补充条件”的完整检索问题。例：AI 问附近餐饮口味、客户答“麻辣口味的”，应输出 hotel_info/surrounding_facilities。没有紧邻业务追问时，独立短语不得从更早历史强行继承旧主题。",
		"【hotel_info 与 hotel_variable 边界】WiFi/停车/早餐/发票/电视/空调/用品/入住退房流程都是 hotel_info，不是 hotel_variable。电话/定位/小程序才 hotel_variable。例：“停车在哪里”是 hotel_info+parking，“定位发我/酒店地址发我”才是 hotel_variable+location。",
		"【地址文字 vs 定位卡片·关键】客户要「地址文字」（外卖地址填哪里、收货地址填哪、酒店地址是多少、地址给我一个）是 hotel_info + subIntent=address，走知识库回答文字，绝不发定位；客户要「定位/导航」（定位发我、导航到酒店、酒店在哪、怎么去）才是 hotel_variable + location，发定位卡片。不要把“要地址文字”误判成 hotel_variable 去发定位，也不要把“要定位”误判成 hotel_info 走知识库。",
		"【多任务】当前消息有多个问题或动作时，intentTasks 必须逐项拆分、按用户原顺序排列；不能只输出主意图或最后一句。",
		"【并列对象也要拆】同一句里出现多个可独立回答的对象时也必须逐项拆分，例如“咖啡和草稿纸有没有”要拆成咖啡、草稿纸两个任务；时间+地点等属于同一对象的不同维度可以保留为一个任务。",
		"【task.text 纪律】intentTasks[].text 必须只写该任务对应的原话子句（如“怎么把门打开”“附近有什么好玩的”），禁止把整句原文复制到每个任务；整句包含多个主题时，必须按主题拆成多个 text，每个 text 只承载一个主题，否则知识检索会错配到别的主题。",
		"【subIntent 纪律】subIntent 写具体业务子意图，不要空泛写 store_knowledge。hotel_info 常用：network_wifi、parking、breakfast、invoice、checkin_process、checkout_process、tv_cast、air_conditioner、supplies_self_help、laundry、surrounding_facilities、discount。human_complaint_risk 常用：explicit_handoff、complaint_escalation、refund_compensation、order_price_dispute、emergency_safety。",
		"【办入住·金标】客户说“我要办入住/我想办入住/怎么入住/入住怎么弄/给我办入住”时，只输出 hotel_info/checkin_process，完整回答办理流程。服务器会按当前门店真实配置决定是否另发入住小程序，模型不得默认补出资源任务。只有客户明确只索要“入住小程序/办理入住入口”且没有询问步骤时，才输出 hotel_variable/mini_program。禁止把办入住整体归 service_request 或 human_complaint_risk。",
		"【顶层聚合·金标】primaryIntent 按以下优先级确定：存在 human_complaint_risk 任务→human_complaint_risk；入住流程任务→hotel_info；存在客户明确索要的 hotel_variable 任务→hotel_variable；否则按用户原顺序第一个业务任务；没有业务任务→interaction。忽略只表达语气的 interaction 任务。",
		"【资源动作纪律·金标】本轮资源动作只来自客户明确索要的 hotel_variable 任务（电话/定位/小程序），禁止默认补齐任何变量，禁止把电话、定位、小程序当兜底一起输出。",
		"【interaction 否定项】interaction 任务不查知识、不取变量、不转人工；不明确时只追问一个关键点。",
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
	// 关键：文本生成路径也必须 hydrate，把 Store 表里的权威事实（地址/电话/导航名/定位）
	// 补到 instance 上，与发定位卡片的路径保持一致。否则 Generate 阶段门店地址为空，
	// 模型会从最近上下文（如客户图片 OCR 文本）里抓“看着像地址”的内容来补，导致编造。
	return services.StoreService.HydrateRuntimeInstanceDB(sqls.DB(), instance)
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
	currentText := runtimeUserMessageText(req.UserMessage)
	currentDisplayText := currentText
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
	intent = normalizeStoreIdentityQuestionIntent(intent, req)
	intent = deriveModelIntentFromTasks(intent)
	intent = normalizeStructuredSocialIntent(intent)
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
		// 天气查询能力已下线（后续作为技能重新接入）。天气诉求按普通互动自然接话，
		// 不再触发 NeedsTool / 工具调用链路。
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
	currentText := runtimeUserMessageText(req.UserMessage)
	if explicitCheckinKnowledgeRequest(currentText) && intent.PrimaryIntent != "human_complaint_risk" &&
		runtimeIntentConfigEnabled(configs, "hotel_info") {
		intent.PrimaryIntent = "hotel_info"
		intent.MatchedIntentCode = "hotel_info"
		intent.SubIntent = "checkin_process"
		intent.NeedsClarification = false
		intent.NeedsKnowledge = true
		intent.Reason = appendIntentReason(intent.Reason, "deterministic checkin rule")
		intent = ensureCheckinProcessKnowledgeTask(intent, req)
		intent = ensureCheckinProcessMiniProgramTaskIfConfigured(intent, req, configs)
	}
	if shouldAttachCheckinMiniProgramTask(intent) {
		intent = ensureCheckinProcessKnowledgeTask(intent, req)
		intent = ensureCheckinProcessMiniProgramTaskIfConfigured(intent, req, configs)
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
	intent = suppressNonHotelLocationResource(intent, currentText)
	intent = applyDeterministicHotelDirectResources(intent, req, configs)
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

// normalizeStoreIdentityQuestionIntent 纠正一类稳定的分类偏差：客户是在确认
// “当前是哪家酒店/公寓”，不是在陈述一个门店事实，也不是普通闲聊。该类任务
// 统一归 hotel_info/store_identity，后续只读取 Store 权威名称，不依赖客户口述
// 或历史 AI 回复。规则按问题类型判断，不绑定任何具体门店名。
func normalizeStoreIdentityQuestionIntent(intent callbacks.IntentTraceData, req RunInput) callbacks.IntentTraceData {
	currentText := runtimeUserMessageText(req.UserMessage)
	if !explicitStoreIdentityQuestion(currentText) {
		return intent
	}
	matched := false
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		if !explicitStoreIdentityQuestion(task.Text) && !(len(intent.IntentTasks) == 1 && !hasMixedCustomerRequests(currentText)) {
			continue
		}
		task.Intent = "hotel_info"
		task.SubIntent = "store_identity"
		task.NeedsKnowledge = true
		task.NeedsResource = false
		task.NeedsTool = false
		task.NeedsHumanRoute = false
		task.ResourceAction = ""
		if strings.TrimSpace(task.Text) == "" {
			task.Text = currentText
		}
		if strings.TrimSpace(task.RequestMode) == "" {
			task.RequestMode = "question"
		}
		task.Reason = appendIntentReason(task.Reason, "store identity question uses authoritative store fact")
		matched = true
	}
	if !matched && !hasMixedCustomerRequests(currentText) {
		intent.IntentTasks = []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: "hotel_info", SubIntent: "store_identity", Text: currentText,
			RequestMode: "question", Confidence: max(intent.IntentConfidence, 0.8), NeedsKnowledge: true,
			Reason: "store identity question uses authoritative store fact",
		}}
	}
	intent.NeedsClarification = false
	intent.Reason = appendIntentReason(intent.Reason, "store identity normalized from current question")
	return intent
}

func explicitStoreIdentityQuestion(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" {
		return false
	}
	if containsAny(compact, []string{
		"酒店叫什么", "酒店名字", "酒店名称", "门店叫什么", "门店名字", "门店名称",
		"店名是什么", "这里叫什么", "这是什么酒店", "你们叫什么", "你们酒店名",
	}) {
		return true
	}
	if !containsAny(compact, []string{"酒店", "公寓", "宾馆", "旅店", "民宿"}) {
		return false
	}
	if strings.HasSuffix(compact, "吗") || strings.HasSuffix(compact, "么") || strings.Contains(compact, "是不是") {
		return true
	}
	return strings.ContainsAny(text, "?？") && containsAny(compact, []string{
		"这里是", "这是", "你们是", "本店是", "我订的是", "名字是", "名称是", "叫",
	})
}

// hasMixedCustomerRequests 判别一句话里是否叠加了多个诉求（定位+小程序+问答等）。
// 直发规则只接管“单一聚焦请求”，混合轮次必须保留完整任务分解。
func hasMixedCustomerRequests(rawText string) bool {
	return strings.ContainsAny(rawText, "，,；;") || containsAny(compactRuntimeProtocolText(rawText), []string{"还要", "再发", "再帮", "顺便", "还有", "也发", "也帮"})
}

// applyDeterministicHotelDirectResources 在既有归一化之后兜底当前酒店定位卡片。
// 入住流程始终进入知识任务；是否附加小程序由真实门店配置在后续服务器规则中决定。
func applyDeterministicHotelDirectResources(intent callbacks.IntentTraceData, req RunInput, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	if intent.PrimaryIntent == "human_complaint_risk" || !runtimeIntentConfigEnabled(configs, "hotel_variable") {
		return intent
	}
	// 模型已明确选择资源类型时尊重模型（既有契约：关键词不得覆盖模型资源选择），
	// 只在模型把明确卡片请求归成纯知识/互动时兜底。
	if intent.PrimaryIntent == "hotel_variable" && strings.TrimSpace(intent.ResourceAction) != "" {
		return intent
	}
	text := runtimeUserMessageText(req.UserMessage)
	if explicitHotelLocationCardRequest(text) {
		return convertHotelDirectResourceIntent(intent, text, "location", "provide_location", "hotel location request routed to location card direct")
	}
	return intent
}

func convertHotelDirectResourceIntent(intent callbacks.IntentTraceData, text, resourceType, action, reason string) callbacks.IntentTraceData {
	intent.PrimaryIntent = "hotel_variable"
	intent.MatchedIntentCode = "hotel_variable"
	intent.DetectedIntent = "hotel_variable"
	intent.SubIntent = resourceType
	intent.IntentTasks = []callbacks.IntentTaskTraceData{{
		Sequence: 1, Intent: "hotel_variable", SubIntent: resourceType, Text: strings.TrimSpace(text),
		RequestMode: "request_action", Confidence: intent.IntentConfidence, NeedsResource: true,
		ResourceAction: action, Reason: reason,
	}}
	intent.NeedsKnowledge = false
	intent.NeedsTool = false
	intent.NeedsHumanRoute = false
	intent.HumanRoutePolicy = ""
	intent.NeedsClarification = false
	intent.ShouldReply = true
	applyRuntimeProtocolFallbackResource(&intent, action)
	intent.Reason = appendIntentReason(intent.Reason, reason)
	return intent
}

// explicitHotelLocationCardRequest 识别对"当前酒店"的位置/地址/定位请求。
// 外部地点（商场/车站等）与"附近"类探索性请求继续走知识或澄清，不发本店卡片。
func explicitHotelLocationCardRequest(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" {
		return false
	}
	if hasMixedCustomerRequests(text) {
		return false
	}
	if containsAny(compact, []string{
		"小区", "商场", "车站", "机场", "餐厅", "饭店", "景点", "医院", "学校", "公司", "写字楼", "地铁", "高铁站", "火车站", "超市", "附近",
	}) {
		return false
	}
	// 只接“明确索要卡片”的措辞；“酒店在哪/在哪里”等描述性提问仍按既有契约
	// 由模型决定知识回答（知识库含地理位置答案行），不用关键词强行覆盖。
	return containsAny(compact, []string{
		"发定位", "发个定位", "发一下定位", "定位发我", "位置发我", "地址发我",
		"发我定位", "发我地址", "发下定位", "定位发一下", "发个地址", "酒店定位", "门店定位",
	})
}

func ensureCheckinProcessMiniProgramTaskIfConfigured(intent callbacks.IntentTraceData, req RunInput, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	if !runtimeIntentConfigEnabled(configs, "hotel_variable") || !runtimeCheckinMiniProgramAvailable(req) {
		return removeCheckinMiniProgramTask(intent)
	}
	return ensureCheckinProcessMiniProgramTask(intent)
}

func runtimeCheckinMiniProgramAvailable(req RunInput) bool {
	instance := findRuntimeWxWorkInstance(req)
	if instance == nil || strings.TrimSpace(instance.DefaultMiniProgramPayload) == "" {
		return false
	}
	_, _, err := services.WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(instance)
	return err == nil
}

func removeCheckinMiniProgramTask(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	tasks := make([]callbacks.IntentTaskTraceData, 0, len(intent.IntentTasks))
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_variable" && strings.TrimSpace(task.ResourceAction) == "provide_mini_program" {
			continue
		}
		tasks = append(tasks, task)
	}
	intent.IntentTasks = tasks
	resources := make([]string, 0, len(intent.ResourceActions))
	for _, action := range intent.ResourceActions {
		if strings.TrimSpace(action) != "provide_mini_program" {
			resources = append(resources, action)
		}
	}
	intent.ResourceActions = resources
	if strings.TrimSpace(intent.ResourceAction) == "provide_mini_program" {
		intent.ResourceAction = ""
		intent.ResourceType = ""
		if len(resources) > 0 {
			intent.ResourceAction = resources[0]
			intent.ResourceType = hotelVariableResourceTypeFromAction(resources[0])
		}
	}
	intent.NeedsResource = len(resources) > 0
	return intent
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

func ensureCheckinProcessKnowledgeTask(intent callbacks.IntentTraceData, req RunInput) callbacks.IntentTraceData {
	intent.NeedsKnowledge = true
	if strings.TrimSpace(intent.SubIntent) == "" || intent.SubIntent == "check_in" || intent.SubIntent == "checkin" {
		intent.SubIntent = "checkin_process"
	}
	currentText := runtimeUserMessageText(req.UserMessage)
	if currentText == "" {
		currentText = "办理入住"
	}
	hasKnowledgeTask := false
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent == "hotel_info" && isCheckinProcessSubIntent(intent.IntentTasks[i].SubIntent) {
			intent.IntentTasks[i].SubIntent = "checkin_process"
			intent.IntentTasks[i].NeedsKnowledge = true
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = currentText
			}
			hasKnowledgeTask = true
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
	intent.Reason = appendIntentReason(intent.Reason, "checkin_process requires knowledge tutorial")
	return intent
}

func ensureCheckinProcessMiniProgramTask(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	intent.NeedsResource = true
	intent.ResourceActions = normalizeHotelVariableResourceActions(append(intent.ResourceActions, "provide_mini_program"), intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
	if strings.TrimSpace(intent.ResourceAction) == "" {
		intent.ResourceAction = "provide_mini_program"
	}
	hasMiniProgramTask := false
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent != "hotel_variable" || strings.TrimSpace(intent.IntentTasks[i].ResourceAction) != "provide_mini_program" {
			continue
		}
		intent.IntentTasks[i].SubIntent = "mini_program"
		intent.IntentTasks[i].NeedsResource = true
		if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
			intent.IntentTasks[i].Text = "发送入住小程序入口"
		}
		hasMiniProgramTask = true
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
		if task.Intent == "interaction" {
			// The model may copy a knowledge flag from nearby business context.
			// Interaction tasks start as text-only; the conditional knowledge
			// router can promote a concrete, unresolved service question later.
			task.NeedsKnowledge = false
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

func normalizeStructuredSocialIntent(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if !isStructuredSocialInteractionIntent(intent) {
		return intent
	}
	intent.PrimaryIntent = "interaction"
	intent.MatchedIntentCode = "interaction"
	intent.DetectedIntent = "interaction"
	intent.ShouldReply = true
	intent.NeedsKnowledge = false
	intent.NeedsTool = false
	intent.NeedsResource = false
	intent.NeedsHumanRoute = false
	intent.NeedsClarification = false
	intent.ToolCodes = nil
	intent.ResourceActions = nil
	intent.ResourceAction = ""
	intent.ResourceType = ""
	intent.HumanRoutePolicy = ""
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		task.NeedsKnowledge = false
		task.NeedsTool = false
		task.NeedsResource = false
		task.NeedsHumanRoute = false
		task.ResourceAction = ""
		if strings.TrimSpace(intent.SubIntent) == "" || intent.SubIntent == "clarify" {
			intent.SubIntent = strings.TrimSpace(task.SubIntent)
		}
	}
	if strings.TrimSpace(intent.SubIntent) == "" {
		intent.SubIntent = "social"
	}
	return intent
}

func isStructuredSocialInteractionIntent(intent callbacks.IntentTraceData) bool {
	if len(intent.IntentTasks) == 0 {
		return intent.PrimaryIntent == "interaction" && isStructuredSocialSubIntent(intent.SubIntent)
	}
	for _, task := range intent.IntentTasks {
		if canonicalIntentCode(task.Intent) != "interaction" || !isStructuredSocialInteractionTask(task) {
			return false
		}
	}
	return true
}

func isStructuredSocialInteractionTask(task callbacks.IntentTaskTraceData) bool {
	switch strings.TrimSpace(task.RequestMode) {
	case "social", "ack", "thanks", "greeting":
		return true
	}
	return isStructuredSocialSubIntent(task.SubIntent)
}

func isStructuredSocialSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "social", "greeting", "thanks", "thank_you", "ack", "acknowledgment", "acknowledgement", "farewell", "goodbye", "emoji", "smalltalk", "small_talk":
		return true
	default:
		return false
	}
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

// suppressNonHotelLocationResource 在最终归一化阶段兜底：如果用户要的是非酒店地标定位，
// 即使上游把意图判成了 hotel_variable/location，也移除 provide_location，避免错误发送酒店定位。
// 这是确定性兜底，覆盖意图模型和协议恢复两条路径。
func suppressNonHotelLocationResource(intent callbacks.IntentTraceData, currentText string) callbacks.IntentTraceData {
	if !looksLikeNonHotelLocation(currentText) {
		return intent
	}
	// 只有纯定位诉求才需要抑制；若同时问了酒店电话/小程序等其它变量，不整体清空。
	if intentHasHotelVariableTask(intent) {
		intent.ResourceActions = removeString(intent.ResourceActions, "provide_location")
	}
	if strings.TrimSpace(intent.ResourceAction) == "provide_location" {
		intent.ResourceAction = ""
		intent.ResourceType = ""
	}
	// 清理任务账本里对应 location 任务。
	kept := make([]callbacks.IntentTaskTraceData, 0, len(intent.IntentTasks))
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_variable" && strings.TrimSpace(task.ResourceAction) == "provide_location" {
			continue
		}
		kept = append(kept, task)
	}
	intent.IntentTasks = kept
	if !intentHasHotelVariableTask(intent) && len(intent.ResourceActions) == 0 {
		intent.NeedsResource = false
		if intent.PrimaryIntent == "hotel_variable" {
			intent.PrimaryIntent = "interaction"
			intent.MatchedIntentCode = "interaction"
			intent.SubIntent = "clarify"
			intent.NeedsClarification = true
		}
	}
	return intent
}

func looksLikeNonHotelLocation(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" || !containsAny(compact, []string{"定位", "地址", "导航", "怎么去", "在哪里", "在哪儿"}) {
		return false
	}
	return containsAny(compact, nonHotelLocationMarkers())
}

func nonHotelLocationMarkers() []string {
	return []string{
		"菜市场", "菜场", "市场", "超市", "商场", "商店", "便利店",
		"银行", "药房", "药店", "医院", "诊所", "学校", "幼儿园",
		"小区", "公寓", "写字楼", "公司", "工厂", "地铁", "地铁站",
		"高铁站", "火车站", "机场", "车站", "汽车站", "公交站",
		"餐厅", "饭店", "小吃", "火锅", "烧烤", "咖啡馆", "奶茶",
		"景点", "公园", "游乐园", "博物馆", "图书馆", "政府", "派出所",
		"健身房", "影院", "电影院", "ktv", "酒吧", "网咖", "网吧",
	}
}

func removeString(values []string, target string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != target {
			ret = append(ret, value)
		}
	}
	return ret
}
