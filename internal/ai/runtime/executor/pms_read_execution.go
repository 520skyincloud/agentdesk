package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	runtimetools "agent-desk/internal/ai/runtime/tools"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/pms"
)

var (
	runtimePMSExplicitDatePattern   = regexp.MustCompile(`(?P<year>20[0-9]{2})[-/.年](?P<month>1[0-2]|0?[1-9])[-/.月](?P<day>3[01]|[12][0-9]|0?[1-9])日?`)
	runtimePMSMonthDayPattern       = regexp.MustCompile(`(?P<month>1[0-2]|0?[1-9])月(?P<day>3[01]|[12][0-9]|0?[1-9])日?`)
	runtimePMSDayOnlyPattern        = regexp.MustCompile(`(?P<day>3[01]|[12][0-9]|0?[1-9])(?:日|号)`)
	runtimePMSTargetRoomPattern     = regexp.MustCompile(`(?:升级|升房|换房|换|改)(?:到|成|为|个)?\s*([^，。！？,.!?\n]{1,12})`)
	runtimePMSLateClockPattern      = regexp.MustCompile(`(?:延迟|延退|推迟)(?:退房)?(?:到|至)?\s*([01]?[0-9]|2[0-3])[:：]([0-5][0-9])`)
	runtimePMSLateChinesePattern    = regexp.MustCompile(`(?:延迟|延退|推迟)(?:退房)?(?:到|至)?\s*(?:(上午|下午|晚上|中午|凌晨)\s*)?([0-9零〇一二两三四五六七八九十]{1,3})(?:点|时)(半|[0-9零〇一二三四五六七八九十]{1,2}分?)?`)
	runtimePMSPeriodCheckoutPattern = regexp.MustCompile(`(上午|下午|晚上|中午|凌晨)\s*([0-9零〇一二两三四五六七八九十]{1,3})(?:点|时)(半|[0-9零〇一二三四五六七八九十]{1,2}分?)?\s*退房`)
	runtimePMSExtensionPattern      = regexp.MustCompile(`(?:多住|再住|续住|再续|续)([0-9零〇一二两三四五六七八九十]{1,3})(?:晚|天)`)
)

var (
	runtimePMSLabeledRoomPattern    = regexp.MustCompile(`(?i)(?:房间|房号|住在|入住|住|在)\s*([A-Za-z]?[0-9]{3,5})`)
	runtimePMSStandaloneRoomPattern = regexp.MustCompile(`(?:^|[^0-9])([A-Za-z]?[0-9]{3,5})(?:[^0-9]|$)`)
)

type runtimePMSReadInvoker interface {
	Invoke(context.Context, string, map[string]string) pmsReadStepResult
}

type runtimePMSReadToolInvoker struct {
	collector *callbacks.RuntimeTraceCollector
}

func (i runtimePMSReadToolInvoker) Invoke(ctx context.Context, action string, args map[string]string) pmsReadStepResult {
	payload := make(map[string]any, len(args)+1)
	payload["action"] = action
	for key, value := range args {
		if strings.TrimSpace(value) != "" {
			payload[key] = value
		}
	}
	arguments, err := json.Marshal(payload)
	if err != nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 查询参数无法编码"}
	}
	startedAt := time.Now()
	raw, invokeErr := runtimetools.NewPMSQueryTool().InvokableRun(ctx, string(arguments))
	latency := time.Since(startedAt).Milliseconds()
	result := parseRuntimePMSReadToolResult(raw, invokeErr)
	if i.collector != nil {
		traceStatus := string(result.Status)
		if traceStatus == "" {
			traceStatus = "unavailable"
		}
		i.collector.AddToolItem(callbacks.ToolTraceItem{
			ToolCode:     toolx.BuiltinPMSQuery.Code,
			ToolName:     toolx.BuiltinPMSQuery.Name,
			Arguments:    map[string]any{"action": action},
			Status:       traceStatus,
			LatencyMs:    latency,
			ErrorMessage: result.Message,
		})
	}
	return result
}

type runtimePMSCountingInvoker struct {
	delegate runtimePMSReadInvoker
	count    *int
	mu       sync.Mutex
}

type runtimePMSMemoizedResult struct {
	done   chan struct{}
	result pmsReadStepResult
}

type runtimePMSMemoizingInvoker struct {
	delegate runtimePMSReadInvoker
	mu       sync.Mutex
	results  map[string]*runtimePMSMemoizedResult
}

func (i *runtimePMSMemoizingInvoker) Invoke(ctx context.Context, action string, args map[string]string) pmsReadStepResult {
	if i == nil || i.delegate == nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 查询执行器不可用"}
	}
	key := runtimePMSReadCacheKey(action, args)
	i.mu.Lock()
	if i.results == nil {
		i.results = make(map[string]*runtimePMSMemoizedResult)
	}
	if cached, ok := i.results[key]; ok {
		i.mu.Unlock()
		select {
		case <-ctx.Done():
			return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 查询已取消"}
		case <-cached.done:
			return cached.result
		}
	}
	cached := &runtimePMSMemoizedResult{done: make(chan struct{})}
	i.results[key] = cached
	i.mu.Unlock()

	result := i.delegate.Invoke(ctx, action, args)
	i.mu.Lock()
	cached.result = result
	close(cached.done)
	i.mu.Unlock()
	return result
}

func runtimePMSReadCacheKey(action string, args map[string]string) string {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{strings.TrimSpace(action)}
	for _, key := range keys {
		parts = append(parts, key+"="+strings.TrimSpace(args[key]))
	}
	return strings.Join(parts, "|")
}

func (i *runtimePMSCountingInvoker) Invoke(ctx context.Context, action string, args map[string]string) pmsReadStepResult {
	if i.count != nil {
		i.mu.Lock()
		*i.count++
		i.mu.Unlock()
	}
	return i.delegate.Invoke(ctx, action, args)
}

func parseRuntimePMSReadToolResult(raw string, invokeErr error) pmsReadStepResult {
	if invokeErr != nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 查询暂时不可用"}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var envelope struct {
		Status  string `json:"status"`
		Data    any    `json:"data"`
		Message string `json:"message"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 查询返回无法解析"}
	}
	status := pmsReadStepStatus(strings.TrimSpace(envelope.Status))
	switch status {
	case pmsReadStepOK:
		if runtimePMSReadDataEmpty(envelope.Data) {
			status = pmsReadStepEmpty
		}
	case pmsReadStepPartial, pmsReadStepEmpty, pmsReadStepAmbiguous, pmsReadStepUnavailable, pmsReadStepUnsupported:
	default:
		status = pmsReadStepUnavailable
	}
	return pmsReadStepResult{Status: status, Data: envelope.Data, Message: strings.TrimSpace(envelope.Message)}
}

func runtimePMSReadDataEmpty(data any) bool {
	switch value := data.(type) {
	case nil:
		return true
	case []any:
		return len(value) == 0
	case map[string]any:
		if len(value) == 0 {
			return true
		}
		if rows, ok := value["rows"].([]any); ok {
			return len(rows) == 0
		}
	}
	return false
}

func applyRuntimePMSReadPlans(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData, summary *RunResult, collector *callbacks.RuntimeTraceCollector) (callbacks.IntentTraceData, callbacks.ReplyPlanTraceData, bool) {
	current := config.CurrentOrNil()
	if current == nil || !current.PMS.Enabled || strings.TrimSpace(current.PMS.BaseURL) == "" {
		return intent, replyPlan, false
	}
	return applyRuntimePMSReadPlansWithInvoker(ctx, req, history, intent, replyPlan, summary, collector, time.Now(), runtimePMSReadToolInvoker{collector: collector})
}

func applyRuntimePMSReadPlansWithInvoker(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData, summary *RunResult, collector *callbacks.RuntimeTraceCollector, now time.Time, delegate runtimePMSReadInvoker) (callbacks.IntentTraceData, callbacks.ReplyPlanTraceData, bool) {
	if delegate == nil {
		return intent, replyPlan, false
	}
	sessionLocator := runtimePMSSessionLocatorForRequest(req, history)
	callCount := 0
	invoker := &runtimePMSMemoizingInvoker{delegate: &runtimePMSCountingInvoker{delegate: delegate, count: &callCount}}
	executed := false
	processedTasks := make([]callbacks.ReplyTaskPlanTraceData, 0)
	for index := range replyPlan.TaskPlans {
		task := &replyPlan.TaskPlans[index]
		if !task.NeedsTool || !isPMSRuntimeSubIntent(task.SubIntent) {
			continue
		}
		input := runtimePMSReadPlanInputForTask(*task, sessionLocator, now)
		plan := buildPMSReadPlan(input)
		results, finalPlan := executeRuntimePMSReadPlan(ctx, plan, input, invoker)
		aggregated, err := aggregatePMSReadPlanResults(finalPlan, results)
		if err != nil {
			aggregated = pmsReadPlanResult{Status: pmsReadStepUnavailable, Unconfirmed: []string{"PMS 查询计划结果无效"}}
		}
		applyRuntimePMSReadResultToTask(task, finalPlan, aggregated, index)
		appendRuntimePMSResolvedOrderLocator(task, aggregated)
		resolveRuntimePMSKnowledgeHandoffForTask(req, task, replyPlan, finalPlan, aggregated, summary, collector)
		task.NeedsTool = false
		processedTasks = append(processedTasks, *task)
		executed = true
	}
	replyPlan.ActiveTaskCount = len(replyPlan.TaskPlans)
	replyPlan.ReplyRequiredTaskCount = countReplyRequiredTasks(replyPlan.TaskPlans)
	if !executed {
		return intent, replyPlan, false
	}
	usedIntentTasks := make(map[int]struct{}, len(processedTasks))
	processedBySubIntent := make(map[string]int)
	for _, processed := range processedTasks {
		processedBySubIntent[strings.TrimSpace(processed.SubIntent)]++
		if index := runtimePMSMatchingIntentTaskIndex(processed, intent.IntentTasks, usedIntentTasks); index >= 0 {
			intent.IntentTasks[index].NeedsTool = false
			usedIntentTasks[index] = struct{}{}
		}
	}
	pendingBySubIntent := make(map[string][]int)
	for index, task := range intent.IntentTasks {
		if task.NeedsTool && isPMSRuntimeSubIntent(task.SubIntent) {
			key := strings.TrimSpace(task.SubIntent)
			pendingBySubIntent[key] = append(pendingBySubIntent[key], index)
		}
	}
	for subIntent, indexes := range pendingBySubIntent {
		if processedBySubIntent[subIntent] < len(indexes) {
			continue
		}
		for _, index := range indexes {
			intent.IntentTasks[index].NeedsTool = false
		}
	}
	intent.NeedsTool = false
	hasPendingPMS := false
	for _, task := range intent.IntentTasks {
		if task.NeedsTool {
			intent.NeedsTool = true
			if isPMSRuntimeSubIntent(task.SubIntent) {
				hasPendingPMS = true
			}
		}
	}
	if !hasPendingPMS {
		intent.ToolCodes = removeRuntimeToolCode(intent.ToolCodes, toolx.BuiltinPMSQuery.Code)
	}
	if summary != nil {
		summary.ToolCodes = appendIfMissing(summary.ToolCodes, toolx.BuiltinPMSQuery.Code)
		if callCount > 0 {
			summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, toolx.BuiltinPMSQuery.Code)
			summary.ToolCallCount = len(summary.InvokedToolCodes)
		}
	}
	return intent, replyPlan, true
}

func resolveRuntimePMSKnowledgeHandoffForTask(req RunInput, task *callbacks.ReplyTaskPlanTraceData, replyPlan callbacks.ReplyPlanTraceData, pmsPlan pmsReadPlan, result pmsReadPlanResult, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if task == nil || collector == nil || strings.TrimSpace(task.TaskID) == "" || !isPMSRuntimeSubIntent(task.SubIntent) {
		return
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	traceIndex := -1
	for index := range trace.Tasks {
		if strings.TrimSpace(trace.Tasks[index].TaskID) == strings.TrimSpace(task.TaskID) {
			traceIndex = index
			break
		}
	}
	if traceIndex < 0 {
		return
	}
	taskTrace := &trace.Tasks[traceIndex]
	originalDisposition := strings.TrimSpace(taskTrace.Disposition)
	if originalDisposition != runtimeKnowledgeDispositionDirectHandoff && originalDisposition != runtimeKnowledgeDispositionAnswerThenHandoff {
		return
	}

	taskID := strings.TrimSpace(task.TaskID)
	hasPMSFacts := runtimeReplyTaskHasPMSFact(*task)
	if hasPMSFacts && runtimePMSReadResultCompleteForKnowledgePrecedence(*task, pmsPlan, result) {
		trace.DeferredTaskIDs = removePMSReadString(trace.DeferredTaskIDs, taskID)
		if len(trace.DeferredTaskIDs) == 0 {
			trace.DeferredHandoff = false
			trace.DeferredHandoffReason = ""
		}
		taskTrace.Disposition = runtimeKnowledgeDispositionAnswer
		taskTrace.DecisionSource = "pms_read_precedence"
		if originalDisposition == runtimeKnowledgeDispositionDirectHandoff {
			task.NeedsKnowledge = false
			task.Output = "text_reply"
			task.OutputKind = "text"
			task.ReplyRequired = true
			task.SelectedLayer = ""
			task.SelectedCandidateIDs = nil
			task.SupportedFacts = runtimeReplyTaskPMSFacts(*task)
			task.AnswerText = nil
			taskTrace.SelectedLayer = ""
			taskTrace.SelectedCandidateIDs = nil
			taskTrace.SupportedFacts = nil
			taskTrace.AnswerText = nil
		}
		collector.SetKnowledgeEvidenceJudge(trace)
		return
	}

	pending := runtimeKnowledgeQuestionDisposition{
		TaskID:         taskID,
		Query:          activeGenerationTaskText(*task),
		Disposition:    originalDisposition,
		HasAnswer:      hasPMSFacts || originalDisposition == runtimeKnowledgeDispositionAnswerThenHandoff,
		NeedsHandoff:   true,
		MissingAspects: append([]string(nil), taskTrace.MissingAspects...),
		HandoffHit:     rag.RetrieveResult{Content: "转人工"},
	}
	if utils.IsExplicitHumanHandoffRejection(currentRuntimeIntentSemanticText(req)) {
		pmsFacts := runtimeReplyTaskPMSFacts(*task)
		applyDeclinedKnowledgeHandoffReply(task, currentRuntimeIntentSemanticText(req))
		boundaryStatement := declinedKnowledgeHandoffReply
		if task.AnswerText != nil && strings.TrimSpace(*task.AnswerText) != "" {
			boundaryStatement = strings.TrimSpace(*task.AnswerText)
		}
		if len(pmsFacts) > 0 {
			task.SupportedFacts = append(pmsFacts, callbacks.KnowledgeEvidenceFactTraceData{
				FactID:    taskID + "FHandoffBoundary",
				Aspect:    "handoff_boundary",
				Statement: boundaryStatement,
			})
			task.AnswerText = nil
		}
		taskTrace.Disposition = runtimeKnowledgeDispositionAnswer
		taskTrace.DecisionSource = "customer_declined_handoff"
		trace.DeferredTaskIDs = removePMSReadString(trace.DeferredTaskIDs, taskID)
		if len(trace.DeferredTaskIDs) == 0 {
			trace.DeferredHandoff = false
			trace.DeferredHandoffReason = ""
		}
		collector.SetKnowledgeEvidenceJudge(trace)
		return
	}
	if !runtimeKnowledgeAutoHandoffEnabledForCollector(
		req.Conversation.ID,
		[]runtimeKnowledgeQuestionDisposition{pending},
		collector,
		currentRuntimeIntentSemanticText(req),
	) {
		return
	}
	previousCount := len(trace.DeferredTaskIDs)
	trace.DeferredTaskIDs = appendIfMissing(trace.DeferredTaskIDs, taskID)
	trace.DeferredHandoff = true
	if len(trace.DeferredTaskIDs) > previousCount {
		reason := deferredRuntimeKnowledgeHandoffReason([]runtimeKnowledgeQuestionDisposition{pending})
		if strings.TrimSpace(trace.DeferredHandoffReason) == "" {
			trace.DeferredHandoffReason = reason
		} else if strings.TrimSpace(reason) != "" {
			trace.DeferredHandoffReason += "；" + reason
		}
	}
	if originalDisposition == runtimeKnowledgeDispositionDirectHandoff && !hasPMSFacts {
		task.Output = runtimeKnowledgeDeferredHandoffOutput
		task.OutputKind = "handoff"
		task.ReplyRequired = false
		if summary != nil && !runtimePMSReplyPlanHasAnswerableSibling(replyPlan, taskID) {
			summary.handoffDirective = true
			summary.handoffDirectiveReason = deferredRuntimeKnowledgeHandoffReason([]runtimeKnowledgeQuestionDisposition{pending})
			summary.handoffDirectiveSource = "knowledge_top_answer"
		}
	} else {
		task.Output = "knowledge_text_reply"
		task.OutputKind = "text"
		task.ReplyRequired = true
		if originalDisposition == runtimeKnowledgeDispositionDirectHandoff {
			task.NeedsKnowledge = false
			task.SelectedLayer = ""
			task.SelectedCandidateIDs = nil
			task.SupportedFacts = runtimeReplyTaskPMSFacts(*task)
			task.AnswerText = nil
		}
	}
	collector.SetKnowledgeEvidenceJudge(trace)
}

func runtimePMSReadResultCompleteForKnowledgePrecedence(task callbacks.ReplyTaskPlanTraceData, plan pmsReadPlan, result pmsReadPlanResult) bool {
	if !runtimeReplyTaskHasPMSFact(task) || len(plan.Missing) > 0 {
		return false
	}
	byStep := make(map[string]pmsReadStepStatus, len(result.Steps))
	for _, step := range result.Steps {
		byStep[step.StepID] = step.Status
	}
	for _, step := range plan.Steps {
		if step.Required && byStep[step.ID] != pmsReadStepOK {
			return false
		}
	}

	text := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	if plan.Scenario == pmsReadScenarioRoomUpgrade || plan.Scenario == pmsReadScenarioRoomChange || plan.Scenario == pmsReadScenarioPrice {
		if containsAny(text, []string{"会员", "权益", "免差", "免费升"}) && byStep["member.benefits"] != pmsReadStepOK {
			return false
		}
		if containsAny(text, []string{"差价", "补多少", "多少钱", "费用", "价格"}) && byStep["price.difference"] != pmsReadStepOK {
			return false
		}
	}
	if plan.Scenario == pmsReadScenarioLateCheckout {
		if byStep["room.status"] != pmsReadStepOK && byStep["inventory.stay"] != pmsReadStepOK {
			return false
		}
		if containsAny(text, []string{"收费", "费用", "多少钱", "政策", "条件"}) {
			return false
		}
	}
	return true
}

func runtimePMSReplyPlanHasAnswerableSibling(plan callbacks.ReplyPlanTraceData, taskID string) bool {
	for _, candidate := range plan.TaskPlans {
		if strings.TrimSpace(candidate.TaskID) == strings.TrimSpace(taskID) {
			continue
		}
		if replyTaskRequiresText(candidate) || strings.TrimSpace(candidate.OutputKind) == "resource" || strings.TrimSpace(candidate.Output) == "structured_resource_commit" {
			return true
		}
	}
	return false
}

func runtimePMSMatchingIntentTaskIndex(replyTask callbacks.ReplyTaskPlanTraceData, intentTasks []callbacks.IntentTaskTraceData, used map[int]struct{}) int {
	bestIndex := -1
	bestScore := 0
	for index, task := range intentTasks {
		if _, exists := used[index]; exists || !task.NeedsTool || !isPMSRuntimeSubIntent(task.SubIntent) {
			continue
		}
		score := 0
		if strings.TrimSpace(task.SubIntent) == strings.TrimSpace(replyTask.SubIntent) {
			score += 4
		}
		if runtimePMSNormalizedTaskText(task.ResolvedText) != "" && runtimePMSNormalizedTaskText(task.ResolvedText) == runtimePMSNormalizedTaskText(replyTask.ResolvedText) {
			score += 3
		}
		if runtimePMSNormalizedTaskText(task.Text) != "" && runtimePMSNormalizedTaskText(task.Text) == runtimePMSNormalizedTaskText(firstNonEmptyReplyTaskText(replyTask.OriginalText, replyTask.Text)) {
			score += 2
		}
		if runtimePMSStringSlicesEqual(task.SourceRefs, replyTask.SourceRefs) {
			score++
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		} else if score == bestScore && score > 0 {
			bestIndex = -1
		}
	}
	return bestIndex
}

func runtimePMSNormalizedTaskText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}

func runtimePMSStringSlicesEqual(left, right []string) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}

func buildRuntimePMSResolvedInstruction(plan callbacks.ReplyPlanTraceData) string {
	hasPMSFacts := false
	for _, task := range plan.TaskPlans {
		for _, fact := range task.SupportedFacts {
			if strings.HasPrefix(strings.TrimSpace(fact.Aspect), "pms_") && strings.TrimSpace(fact.Statement) != "" {
				hasPMSFacts = true
				break
			}
		}
		if hasPMSFacts {
			break
		}
	}
	if !hasPMSFacts {
		return ""
	}
	return "PMS 只读事实已经由服务端查询并写入当前任务的已确认事实。Generate 要先理解客户此刻的目的，只选择能直接解决该目的的事实组织自然回复；不要复述全部库存、冲突检测、净脏房统计或内部评估过程。库存日期由订单派生时，必须按完整入住期间或具体日期范围表述；任一入住日无库存时，只能说明完整入住期间不能满足，不能擅自缩写成今天满房。客户要求推荐时，可在真实候选中推荐一个并说明已有依据；没有可区分依据时应如实说明候选目前看起来条件相同，再给出一个可选建议。不得再次调用 pms_query，不得补全尚未确认方面，也不得把可售、可选或评估结果说成已经锁房、换房、升房、续住、延退或完成收费。客户手机号只用于定位查询，不得在回复中原样复述完整手机号；内部预订单/接待单 ID 也不得在回复中原样复述。"
}

func appendRuntimePMSResolvedOrderLocator(task *callbacks.ReplyTaskPlanTraceData, result pmsReadPlanResult) {
	if task == nil {
		return
	}
	byStep := make(map[string]pmsReadStepResult, len(result.Steps))
	for _, step := range result.Steps {
		byStep[step.StepID] = step
	}
	candidate, status, _ := selectRuntimePMSStayCandidate(byStep, []string{"order.reserve", "order.recept"}, nil)
	if status != pmsReadStepOK {
		return
	}
	locators := make([]string, 0, 2)
	if candidate.reserveOrderID != "" {
		locators = append(locators, "预订单ID:"+candidate.reserveOrderID)
	}
	if candidate.receptOrderID != "" {
		locators = append(locators, "接待单ID:"+candidate.receptOrderID)
	}
	if len(locators) == 0 {
		return
	}
	setRuntimeIntentEntity(&task.Entities, runtimeIntentEntityOrderLocator, strings.Join(locators, " "))
}

func appendRuntimePMSLocatorMarkerToReplyTask(task *callbacks.ReplyTaskPlanTraceData, marker string) {
	if task == nil || strings.TrimSpace(marker) == "" || strings.Contains(task.ResolvedText, marker) {
		return
	}
	task.ResolvedText = strings.TrimSpace(task.ResolvedText) + "\n" + marker
}

func runtimePMSReadPlanInputForTask(task callbacks.ReplyTaskPlanTraceData, sessionLocator runtimePMSSessionLocator, now time.Time) pmsReadPlanInput {
	text := strings.TrimSpace(strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n"))
	phone := runtimePMSLastUsableCustomerPhone(runtimeIntentEntityValue(task.Entities, runtimeIntentEntityCustomerPhone))
	if phone == "" {
		phone = runtimePMSLastUsableCustomerPhone(text)
	}
	if phone == "" && !runtimePMSCurrentTextRejectsCustomerPhone(task.OriginalText) {
		phone = sessionLocator.Phone
	}
	input := pmsReadPlanInput{
		Scenario:           pmsReadScenarioForSubIntent(task.SubIntent),
		SubIntent:          task.SubIntent,
		Phone:              phone,
		RoomKeyword:        runtimePMSRoomKeyword(task),
		TargetRoomTypeText: runtimePMSTargetRoomTypeText(task),
	}
	entityLocator := runtimeIntentEntityValue(task.Entities, runtimeIntentEntityOrderLocator)
	input.ReserveOrderID, input.ReceptOrderID, input.CustomerNo = runtimePMSOrderLocators(entityLocator)
	if input.ReserveOrderID == "" && input.ReceptOrderID == "" && input.CustomerNo == "" {
		input.ReserveOrderID, input.ReceptOrderID, input.CustomerNo = runtimePMSOrderLocators(text)
	}
	if input.ReserveOrderID == "" && input.ReceptOrderID == "" && input.CustomerNo == "" {
		reserveID, receptID, customerNo := runtimePMSOrderLocators(sessionLocator.OrderLocator)
		input.ReserveOrderID = reserveID
		input.ReceptOrderID = receptID
		input.CustomerNo = customerNo
	}
	input.StartDate, input.EndDate = runtimePMSReadDates(task, input.Scenario, now)
	if input.Scenario == pmsReadScenarioRenewal && input.EndDate == "" {
		input.ExtensionDays = runtimePMSExtensionDays(task)
	}
	if input.Scenario == pmsReadScenarioLateCheckout {
		input.TargetCheckoutTime = runtimePMSLateCheckoutTargetTime(task)
	}
	return input
}

func runtimePMSLateCheckoutTargetTime(task callbacks.ReplyTaskPlanTraceData) string {
	text := strings.TrimSpace(strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n"))
	if matches := runtimePMSLateClockPattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		match := matches[len(matches)-1]
		return runtimePMSClockFromDigits(match[1], match[2])
	}
	for _, pattern := range []*regexp.Regexp{runtimePMSLateChinesePattern, runtimePMSPeriodCheckoutPattern} {
		if matches := pattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			return runtimePMSClockFromChineseMatch(matches[len(matches)-1])
		}
	}
	return ""
}

func runtimePMSExtensionDays(task callbacks.ReplyTaskPlanTraceData) int {
	text := compactRuntimePMSPhoneContext(strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n"))
	matches := runtimePMSExtensionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0
	}
	days, ok := runtimePMSChineseNumber(matches[len(matches)-1][1])
	if !ok || days <= 0 || days > 30 {
		return 0
	}
	return days
}

func runtimePMSClockFromChineseMatch(match []string) string {
	if len(match) < 4 {
		return ""
	}
	hour, ok := runtimePMSChineseNumber(match[2])
	if !ok {
		return ""
	}
	minute := 0
	minuteText := strings.TrimSuffix(strings.TrimSpace(match[3]), "分")
	if minuteText == "半" {
		minute = 30
	} else if minuteText != "" {
		var minuteOK bool
		minute, minuteOK = runtimePMSChineseNumber(minuteText)
		if !minuteOK {
			return ""
		}
	}
	switch match[1] {
	case "下午", "晚上":
		if hour < 12 {
			hour += 12
		}
	case "中午":
		if hour < 11 {
			hour += 12
		}
	case "上午", "凌晨":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func runtimePMSClockFromDigits(hourText, minuteText string) string {
	hour, hourErr := strconv.Atoi(hourText)
	minute, minuteErr := strconv.Atoi(minuteText)
	if hourErr != nil || minuteErr != nil || hour > 23 || minute > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func runtimePMSChineseNumber(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if number, err := strconv.Atoi(value); err == nil {
		return number, true
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(value)
	if len(runes) == 1 {
		if runes[0] == '十' {
			return 10, true
		}
		number, ok := digits[runes[0]]
		return number, ok
	}
	if len(runes) == 2 && runes[0] == '十' {
		ones, ok := digits[runes[1]]
		return 10 + ones, ok
	}
	if len(runes) == 2 && runes[1] == '十' {
		tens, ok := digits[runes[0]]
		return tens * 10, ok
	}
	if len(runes) == 3 && runes[1] == '十' {
		tens, tensOK := digits[runes[0]]
		ones, onesOK := digits[runes[2]]
		return tens*10 + ones, tensOK && onesOK
	}
	return 0, false
}

func runtimePMSOrderLocators(text string) (string, string, string) {
	reserveOrderID := ""
	receptOrderID := ""
	customerNo := ""
	for _, match := range runtimePMSCustomerLocatorPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(match[1]))
		value := strings.TrimSpace(match[2])
		switch {
		case strings.Contains(label, "reserveorderid") || strings.Contains(label, "预订单"):
			reserveOrderID = value
		case strings.Contains(label, "receptorderid") || strings.Contains(label, "接待单"):
			receptOrderID = value
		case strings.Contains(label, "会员") || strings.Contains(label, "协议公司"):
			customerNo = value
		}
	}
	return reserveOrderID, receptOrderID, customerNo
}

func runtimePMSRoomKeyword(task callbacks.ReplyTaskPlanTraceData) string {
	for _, entity := range task.Entities {
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		if strings.Contains(entityType, "room_number") || strings.Contains(entityType, "home_number") || strings.Contains(entityType, "房号") {
			return strings.TrimSpace(entity.Text)
		}
	}
	for _, text := range []string{task.OriginalText, task.Text, task.ResolvedText} {
		if match := runtimePMSLabeledRoomPattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	for _, text := range []string{task.OriginalText, task.Text, task.ResolvedText} {
		if match := runtimePMSStandaloneRoomPattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func runtimePMSTargetRoomTypeText(task callbacks.ReplyTaskPlanTraceData) string {
	roomEntities := make([]string, 0, 2)
	for _, entity := range task.Entities {
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		if strings.Contains(entityType, "room_type") || strings.Contains(entityType, "房型") {
			if text := strings.TrimSpace(entity.Text); text != "" {
				roomEntities = append(roomEntities, text)
			}
		}
	}
	if currentSelection := runtimePMSCurrentRoomTypeSelection(task); currentSelection != "" {
		return currentSelection
	}
	combined := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	for index := len(roomEntities) - 1; index >= 0; index-- {
		roomType := roomEntities[index]
		position := strings.LastIndex(combined, roomType)
		if position < 0 {
			continue
		}
		prefixStart := max(0, position-12)
		prefix := combined[prefixStart:position]
		if strings.Contains(prefix, "换") || strings.Contains(prefix, "升") || strings.Contains(prefix, "改") || strings.Contains(prefix, "目标") || strings.Contains(prefix, "想要") {
			return roomType
		}
	}
	if matches := runtimePMSTargetRoomPattern.FindAllStringSubmatch(combined, -1); len(matches) > 0 {
		match := matches[len(matches)-1]
		value := strings.TrimSpace(match[1])
		for _, suffix := range []string{"就行", "可以", "吧", "吗"} {
			value = strings.TrimSuffix(value, suffix)
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func runtimePMSCurrentRoomTypeSelection(task callbacks.ReplyTaskPlanTraceData) string {
	if strings.TrimSpace(task.DialogueAct) != "selection" && strings.TrimSpace(task.ReplyStrategy) != "confirm_selection_and_continue_goal" {
		return ""
	}
	current := strings.TrimSpace(task.OriginalText)
	if current == "" {
		return ""
	}
	if index := strings.IndexAny(current, "，。！？,.!?\n"); index >= 0 {
		current = strings.TrimSpace(current[:index])
	}
	for _, prefix := range []string{"那就选", "就选", "选", "要", "换成", "换到", "升到", "升级到"} {
		current = strings.TrimSpace(strings.TrimPrefix(current, prefix))
	}
	for _, suffix := range []string{"就行", "可以", "吧", "吗"} {
		current = strings.TrimSpace(strings.TrimSuffix(current, suffix))
	}
	if current == "" || len([]rune(current)) > 12 || runtimePMSGenericRoomChoice(normalizeRuntimePMSRoomTypeText(current)) {
		return ""
	}
	return current
}

type runtimePMSDateMention struct {
	start int
	end   int
	date  string
}

func runtimePMSReadDates(task callbacks.ReplyTaskPlanTraceData, scenario pmsReadScenario, now time.Time) (string, string) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		now = now.In(location)
	}
	text := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	dateText := text
	if correctedText := runtimePMSCorrectedDateText(text); correctedText != "" {
		dateText = correctedText
	}
	mentions := runtimePMSDateMentions(dateText, now)
	if len(mentions) >= 2 {
		return mentions[0].date, mentions[1].date
	}
	if len(mentions) == 1 {
		if scenario == pmsReadScenarioRenewal || scenario == pmsReadScenarioLateCheckout {
			return "", mentions[0].date
		}
		start, parseErr := time.ParseInLocation("2006-01-02", mentions[0].date, now.Location())
		if parseErr == nil {
			return mentions[0].date, start.AddDate(0, 0, 1).Format("2006-01-02")
		}
	}
	return "", ""
}

func runtimePMSCorrectedDateText(text string) string {
	boundary := -1
	for _, marker := range []string{"改成", "改为", "更正为", "调整为", "替换成", "替换为"} {
		if index := strings.LastIndex(text, marker); index >= 0 && index+len(marker) > boundary {
			boundary = index + len(marker)
		}
	}
	if boundary >= 0 {
		return strings.TrimSpace(text[boundary:])
	}

	if index := strings.LastIndex(text, "不是"); index >= 0 {
		replacement := text[index+len("不是"):]
		if isIndex := strings.Index(replacement, "是"); isIndex >= 0 {
			return strings.TrimSpace(replacement[isIndex+len("是"):])
		}
	}
	return ""
}

func runtimePMSDateMentions(text string, now time.Time) []runtimePMSDateMention {
	mentions := make([]runtimePMSDateMention, 0, 3)
	occupied := make([][2]int, 0, 3)
	add := func(start, end int, date string) {
		if normalizePMSReadDate(date) == "" {
			return
		}
		for _, span := range occupied {
			if start < span[1] && end > span[0] {
				return
			}
		}
		mentions = append(mentions, runtimePMSDateMention{start: start, end: end, date: date})
		occupied = append(occupied, [2]int{start, end})
	}
	for _, indexes := range runtimePMSExplicitDatePattern.FindAllStringSubmatchIndex(text, -1) {
		if len(indexes) < 8 {
			continue
		}
		year, yearErr := strconv.Atoi(text[indexes[2]:indexes[3]])
		month, monthErr := strconv.Atoi(text[indexes[4]:indexes[5]])
		day, dayErr := strconv.Atoi(text[indexes[6]:indexes[7]])
		if yearErr == nil && monthErr == nil && dayErr == nil {
			add(indexes[0], indexes[1], fmt.Sprintf("%04d-%02d-%02d", year, month, day))
		}
	}
	for _, indexes := range runtimePMSMonthDayPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(indexes) < 6 {
			continue
		}
		month, monthErr := strconv.Atoi(text[indexes[2]:indexes[3]])
		day, dayErr := strconv.Atoi(text[indexes[4]:indexes[5]])
		if monthErr == nil && dayErr == nil {
			add(indexes[0], indexes[1], fmt.Sprintf("%04d-%02d-%02d", now.Year(), month, day))
		}
	}
	for _, indexes := range runtimePMSDayOnlyPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(indexes) < 4 {
			continue
		}
		day, dayErr := strconv.Atoi(text[indexes[2]:indexes[3]])
		if dayErr == nil {
			add(indexes[0], indexes[1], fmt.Sprintf("%04d-%02d-%02d", now.Year(), int(now.Month()), day))
		}
	}
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for phrase, offset := range map[string]int{"今天": 0, "今晚": 0, "明天": 1, "明晚": 1, "后天": 2} {
		for searchFrom := 0; searchFrom < len(text); {
			index := strings.Index(text[searchFrom:], phrase)
			if index < 0 {
				break
			}
			start := searchFrom + index
			add(start, start+len(phrase), base.AddDate(0, 0, offset).Format("2006-01-02"))
			searchFrom = start + len(phrase)
		}
	}
	sort.SliceStable(mentions, func(i, j int) bool { return mentions[i].start < mentions[j].start })
	ret := make([]runtimePMSDateMention, 0, len(mentions))
	for _, mention := range mentions {
		if len(ret) > 0 && ret[len(ret)-1].date == mention.date {
			continue
		}
		ret = append(ret, mention)
	}
	return ret
}

func executeRuntimePMSReadPlan(ctx context.Context, plan pmsReadPlan, input pmsReadPlanInput, invoker runtimePMSReadInvoker) ([]pmsReadStepResult, pmsReadPlan) {
	byStep := make(map[string]pmsReadStepResult, len(plan.Steps)+1)
	executeRuntimePMSReadSteps(ctx, plan.Steps, byStep, invoker)

	if input.TargetRoomTypeID == "" && strings.TrimSpace(input.TargetRoomTypeText) != "" {
		if inventory, ok := byStep["inventory.stay"]; ok && (inventory.Status == pmsReadStepOK || inventory.Status == pmsReadStepPartial) {
			roomTypeID, roomTypeName, matchStatus := resolveRuntimePMSTargetRoomType(inventory.Data, input.TargetRoomTypeText)
			switch matchStatus {
			case pmsReadStepOK:
				input.TargetRoomTypeID = roomTypeID
				input.TargetRoomTypeText = roomTypeName
				plan.Missing = removePMSReadString(plan.Missing, "targetRoomTypeId")
				if plan.Scenario == pmsReadScenarioRoomUpgrade || plan.Scenario == pmsReadScenarioRoomChange || plan.Scenario == pmsReadScenarioPrice {
					previousCount := len(plan.Steps)
					appendPMSReadPriceStep(&plan, input, runtimePMSOrderStepIDs(plan), plan.Scenario == pmsReadScenarioPrice)
					if len(plan.Steps) > previousCount {
						executeRuntimePMSReadSteps(ctx, plan.Steps[previousCount:], byStep, invoker)
					}
				}
			case pmsReadStepAmbiguous:
				plan.Missing = append(plan.Missing, "targetRoomTypeAmbiguous")
			default:
				plan.Missing = append(plan.Missing, "targetRoomTypeUnmatched")
			}
		}
	}
	plan.Missing = uniquePMSReadStrings(plan.Missing)
	results := make([]pmsReadStepResult, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		if result, ok := byStep[step.ID]; ok {
			results = append(results, result)
		}
	}
	return results, plan
}

func executeRuntimePMSReadSteps(ctx context.Context, steps []pmsReadPlanStep, byStep map[string]pmsReadStepResult, invoker runtimePMSReadInvoker) {
	pending := append([]pmsReadPlanStep(nil), steps...)
	knownStepIDs := make(map[string]struct{}, len(steps)+len(byStep))
	for stepID := range byStep {
		knownStepIDs[stepID] = struct{}{}
	}
	for _, step := range steps {
		knownStepIDs[step.ID] = struct{}{}
	}

	for len(pending) > 0 {
		ready := make([]pmsReadPlanStep, 0, len(pending))
		remaining := make([]pmsReadPlanStep, 0, len(pending))
		for _, step := range pending {
			if runtimePMSReadStepDependenciesComplete(step, byStep, knownStepIDs) {
				ready = append(ready, step)
			} else {
				remaining = append(remaining, step)
			}
		}
		if len(ready) == 0 {
			for _, step := range remaining {
				byStep[step.ID] = pmsReadStepResult{
					StepID:  step.ID,
					Status:  pmsReadStepUnavailable,
					Message: "PMS 查询计划依赖无法解析",
					Args:    clonePMSReadArgs(step.Args),
				}
			}
			return
		}

		waveResults := make([]pmsReadStepResult, len(ready))
		var waitGroup sync.WaitGroup
		for index, step := range ready {
			args, status, message := resolveRuntimePMSReadStepArgs(step, byStep)
			if !isRuntimePMSReadOnlyAction(step.Action) {
				waveResults[index] = pmsReadStepResult{StepID: step.ID, Status: pmsReadStepUnsupported, Message: "PMS 只读链路拒绝非查询操作", Args: clonePMSReadArgs(args)}
				continue
			}
			if status != "" {
				waveResults[index] = pmsReadStepResult{StepID: step.ID, Status: status, Message: message, Args: args}
				continue
			}

			if step.Action == "price_difference" {
				result := assessRuntimePMSPriceDifference(byStep, args)
				result.StepID = step.ID
				result.Args = clonePMSReadArgs(args)
				waveResults[index] = result
				continue
			}
			if step.Action == "late_checkout_assessment" {
				result := assessRuntimePMSLateCheckout(args)
				result.StepID = step.ID
				result.Args = clonePMSReadArgs(args)
				waveResults[index] = result
				continue
			}

			waitGroup.Add(1)
			go func(index int, step pmsReadPlanStep, args map[string]string) {
				defer waitGroup.Done()
				result := invoker.Invoke(ctx, step.Action, args)
				result.StepID = step.ID
				result.Args = clonePMSReadArgs(args)
				if step.Action == "inventory" {
					result = validateRuntimePMSInventoryCoverage(result)
				}
				waveResults[index] = result
			}(index, step, clonePMSReadArgs(args))
		}
		waitGroup.Wait()
		for index, step := range ready {
			byStep[step.ID] = waveResults[index]
		}
		pending = remaining
	}
}

func runtimePMSReadStepDependenciesComplete(step pmsReadPlanStep, byStep map[string]pmsReadStepResult, knownStepIDs map[string]struct{}) bool {
	dependencies := make(map[string]struct{})
	for _, binding := range step.Bindings {
		for _, source := range binding.Sources {
			if _, known := knownStepIDs[source.StepID]; known {
				dependencies[source.StepID] = struct{}{}
			}
		}
	}
	if step.Action == "price_difference" {
		if _, known := knownStepIDs["inventory.stay"]; known {
			dependencies["inventory.stay"] = struct{}{}
		}
	}
	for stepID := range dependencies {
		if _, complete := byStep[stepID]; !complete {
			return false
		}
	}
	return true
}

func assessRuntimePMSPriceDifference(results map[string]pmsReadStepResult, args map[string]string) pmsReadStepResult {
	order, orderStatus, orderMessage := selectRuntimePMSStayCandidate(results, []string{"order.reserve", "order.recept"}, args)
	inventory, ok := results["inventory.stay"]
	if orderStatus != pmsReadStepOK {
		return pmsReadStepResult{Status: orderStatus, Message: orderMessage}
	}
	if order.data == nil || !ok || (inventory.Status != pmsReadStepOK && inventory.Status != pmsReadStepPartial) {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "订单或目标日期库存尚未确认，不能计算差价"}
	}
	startDate := firstNonEmpty(args["beginTime"], inventory.Args["beginTime"])
	endDate := firstNonEmpty(args["endTime"], inventory.Args["endTime"])
	assessment, err := pms.AssessPriceDifference(order.data, inventory.Data, args["roomTypeId"], startDate, endDate)
	if err != nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 差价评估暂时不可用"}
	}
	return pmsReadStepResult{Status: pmsReadStepOK, Data: map[string]any{"assessment": runtimePMSJSONValue(assessment)}}
}

func assessRuntimePMSLateCheckout(args map[string]string) pmsReadStepResult {
	target := normalizePMSReadClock(args["targetCheckoutTime"])
	if target == "" {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "缺少明确的目标退房时间"}
	}
	data := map[string]any{"targetCheckoutTime": target}
	if targetDate := normalizePMSReadDate(args["targetCheckoutDate"]); targetDate != "" {
		data["targetCheckoutDate"] = targetDate
	}
	if current := strings.TrimSpace(args["currentCheckoutTime"]); current != "" {
		data["currentCheckoutTime"] = current
		return pmsReadStepResult{Status: pmsReadStepOK, Data: data}
	}
	return pmsReadStepResult{Status: pmsReadStepPartial, Data: data, Message: "PMS 未返回当前订单的原退房时间"}
}

func runtimePMSJSONValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil
	}
	return decoded
}

func validateRuntimePMSInventoryCoverage(result pmsReadStepResult) pmsReadStepResult {
	if result.Status != pmsReadStepOK && result.Status != pmsReadStepPartial {
		return result
	}
	startDate := normalizePMSReadDate(result.Args["beginTime"])
	endDate := normalizePMSReadDate(result.Args["endTime"])
	if startDate == "" || endDate == "" {
		result.Status = pmsReadStepPartial
		result.Message = "目标入住和离店日期未完整确认"
		return result
	}
	expected, err := runtimePMSStayDates(startDate, endDate)
	if err != nil {
		result.Status = pmsReadStepPartial
		result.Message = "目标入住日期区间无效"
		return result
	}
	items, ok := result.Data.([]any)
	if !ok || len(items) == 0 {
		return result
	}
	targetRoomTypeID := strings.TrimSpace(result.Args["roomTypeId"])
	targetMatched := targetRoomTypeID == ""
	missingByRoom := make([]string, 0)
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if targetRoomTypeID != "" && firstRuntimePMSReadText(row, "roomTypeId", "productId", "roomId") != targetRoomTypeID {
			continue
		}
		targetMatched = true
		bookings, _ := row["bookings"].(map[string]any)
		missing := make([]string, 0)
		for _, date := range expected {
			day, exists := bookings[date].(map[string]any)
			if !exists || firstRuntimePMSReadText(day, "available") == "" {
				missing = append(missing, date)
			}
		}
		if len(missing) > 0 {
			roomName := firstRuntimePMSReadText(row, "roomTypeName", "productName", "roomName")
			if roomName == "" {
				roomName = "未命名房型"
			}
			missingByRoom = append(missingByRoom, roomName+"缺少"+strings.Join(missing, "、"))
		}
	}
	if !targetMatched {
		result.Status = pmsReadStepPartial
		result.Message = "PMS 未返回当前订单房型的库存"
		return result
	}
	if len(missingByRoom) > 0 {
		result.Status = pmsReadStepPartial
		result.Message = "PMS 未返回完整入住区间的逐日库存：" + strings.Join(missingByRoom, "；")
	}
	return result
}

func runtimePMSStayDates(startDate, endDate string) ([]string, error) {
	start, startErr := time.Parse("2006-01-02", startDate)
	end, endErr := time.Parse("2006-01-02", endDate)
	if startErr != nil || endErr != nil || !end.After(start) {
		return nil, fmt.Errorf("invalid stay range")
	}
	ret := make([]string, 0, int(end.Sub(start).Hours()/24))
	for date := start; date.Before(end); date = date.AddDate(0, 0, 1) {
		ret = append(ret, date.Format("2006-01-02"))
	}
	return ret, nil
}

func isRuntimePMSReadOnlyAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "reserve_order_detail", "reserve_order_by_phone",
		"recept_order_detail", "recept_order_by_phone",
		"renew_candidates", "room_status", "inventory", "stay_room_availability",
		"member_info_by_phone", "member_benefits_by_phone", "price_difference", "late_checkout_assessment":
		return true
	default:
		return false
	}
}

func resolveRuntimePMSReadStepArgs(step pmsReadPlanStep, results map[string]pmsReadStepResult) (map[string]string, pmsReadStepStatus, string) {
	args := clonePMSReadArgs(step.Args)
	orderSourceIDs := runtimePMSReadOrderBindingSourceIDs(step.Bindings)
	var orderCandidates []runtimePMSStayCandidate
	if len(orderSourceIDs) > 0 {
		candidates, status, message := runtimePMSStayCandidatesForArgs(results, orderSourceIDs, args)
		switch status {
		case pmsReadStepOK:
			if len(candidates) > 1 && !runtimePMSStayCandidatesShareReadScope(candidates) {
				return args, pmsReadStepAmbiguous, runtimePMSAmbiguousStayMessage(candidates)
			}
			orderCandidates = candidates
		case pmsReadStepEmpty:
			// Preserve the existing missing-argument behavior for an empty order response.
		default:
			return args, status, message
		}
	}
	for _, binding := range step.Bindings {
		values := make([]string, 0, 2)
		if len(orderCandidates) > 0 && runtimePMSReadBindingUsesOrderSource(binding) {
			fields := runtimePMSReadBindingFields(binding)
			for _, candidate := range orderCandidates {
				values = append(values, runtimePMSReadBindingSourceValues(candidate.data, fields, binding.Argument)...)
			}
		} else {
			for _, source := range binding.Sources {
				result, ok := results[source.StepID]
				if !ok || (result.Status != pmsReadStepOK && result.Status != pmsReadStepPartial) {
					continue
				}
				values = append(values, runtimePMSReadBindingSourceValues(result.Data, source.Fields, binding.Argument)...)
			}
		}
		values = normalizeRuntimePMSBindingValues(binding.Argument, values)
		if binding.DateOffsetDays != 0 {
			values = offsetRuntimePMSBindingDates(values, binding.DateOffsetDays)
		}
		switch len(values) {
		case 0:
			continue
		case 1:
			args[binding.Argument] = values[0]
		default:
			return args, pmsReadStepAmbiguous, "上游订单返回多个可用定位值，不能擅自选择"
		}
	}
	for _, key := range step.RequiredArgs {
		if strings.TrimSpace(args[key]) == "" {
			return args, pmsReadStepUnavailable, runtimePMSReadArgumentMissingMessage(key)
		}
	}
	if len(step.RequiredAnyArgs) > 0 {
		hasAny := false
		for _, key := range step.RequiredAnyArgs {
			if strings.TrimSpace(args[key]) != "" {
				hasAny = true
				break
			}
		}
		if !hasAny {
			return args, pmsReadStepUnavailable, "缺少必要查询定位信息"
		}
	}
	return args, "", ""
}

func offsetRuntimePMSBindingDates(values []string, days int) []string {
	if days == 0 {
		return values
	}
	ret := make([]string, 0, len(values))
	for _, value := range values {
		date := normalizePMSReadDate(value)
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		ret = append(ret, parsed.AddDate(0, 0, days).Format("2006-01-02"))
	}
	return uniquePMSReadStrings(ret)
}

type runtimePMSStayCandidate struct {
	data           map[string]any
	reserveOrderID string
	receptOrderID  string
	checkIn        string
	checkOut       string
	roomName       string
	homeName       string
	orderNumber    string
}

func selectRuntimePMSStayCandidate(results map[string]pmsReadStepResult, sourceIDs []string, args map[string]string) (runtimePMSStayCandidate, pmsReadStepStatus, string) {
	candidates, status, message := runtimePMSStayCandidatesForArgs(results, sourceIDs, args)
	if status != pmsReadStepOK {
		return runtimePMSStayCandidate{}, status, message
	}
	switch len(candidates) {
	case 1:
		return candidates[0], pmsReadStepOK, ""
	default:
		return runtimePMSStayCandidate{}, pmsReadStepAmbiguous, runtimePMSAmbiguousStayMessage(candidates)
	}
}

func runtimePMSStayCandidatesForArgs(results map[string]pmsReadStepResult, sourceIDs []string, args map[string]string) ([]runtimePMSStayCandidate, pmsReadStepStatus, string) {
	for _, stepID := range uniquePMSReadStrings(sourceIDs) {
		result, ok := results[stepID]
		if !ok || result.Status != pmsReadStepAmbiguous {
			continue
		}
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "查询到多个匹配住宿，请确认入住日期或订单号"
		}
		return nil, pmsReadStepAmbiguous, message
	}
	candidates := runtimePMSStayCandidates(results, sourceIDs)
	reserveOrderID := strings.TrimSpace(args["reserveOrderId"])
	receptOrderID := strings.TrimSpace(firstNonEmpty(args["receptOrderId"], args["currentReceptOrderId"]))
	if reserveOrderID != "" || receptOrderID != "" {
		matched := make([]runtimePMSStayCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if reserveOrderID != "" && candidate.reserveOrderID != reserveOrderID {
				continue
			}
			if receptOrderID != "" && candidate.receptOrderID != receptOrderID {
				continue
			}
			matched = append(matched, candidate)
		}
		candidates = matched
	}
	switch len(candidates) {
	case 0:
		return nil, pmsReadStepEmpty, "未查询到可唯一定位的当前住宿"
	default:
		return candidates, pmsReadStepOK, ""
	}
}

func runtimePMSStayCandidatesShareReadScope(candidates []runtimePMSStayCandidate) bool {
	if len(candidates) < 2 {
		return true
	}
	first := candidates[0]
	if first.reserveOrderID == "" || first.checkIn == "" || first.checkOut == "" {
		return false
	}
	firstRoomTypes := normalizeRuntimePMSBindingValues(
		"roomTypeId",
		runtimePMSReadBindingSourceValues(first.data, pmsReadRoomTypeFields, "roomTypeId"),
	)
	if len(firstRoomTypes) == 0 && first.roomName != "" {
		firstRoomTypes = []string{first.roomName}
	}
	if len(firstRoomTypes) != 1 {
		return false
	}
	for _, candidate := range candidates[1:] {
		if candidate.reserveOrderID != first.reserveOrderID || candidate.checkIn != first.checkIn || candidate.checkOut != first.checkOut {
			return false
		}
		roomTypes := normalizeRuntimePMSBindingValues(
			"roomTypeId",
			runtimePMSReadBindingSourceValues(candidate.data, pmsReadRoomTypeFields, "roomTypeId"),
		)
		if len(roomTypes) == 0 && candidate.roomName != "" {
			roomTypes = []string{candidate.roomName}
		}
		if len(roomTypes) != 1 || roomTypes[0] != firstRoomTypes[0] {
			return false
		}
	}
	return true
}

func runtimePMSStayCandidates(results map[string]pmsReadStepResult, sourceIDs []string) []runtimePMSStayCandidate {
	candidates := make([]runtimePMSStayCandidate, 0, 4)
	for _, stepID := range uniquePMSReadStrings(sourceIDs) {
		result, ok := results[stepID]
		if !ok || (result.Status != pmsReadStepOK && result.Status != pmsReadStepPartial) {
			continue
		}
		for _, data := range runtimePMSStayCandidateData(result.Data) {
			candidate := runtimePMSStayCandidateFromData(data)
			if candidate.data != nil {
				candidates = append(candidates, candidate)
			}
		}
	}
	for merged := true; merged; {
		merged = false
		for index := 0; index < len(candidates) && !merged; index++ {
			for other := index + 1; other < len(candidates); other++ {
				if !runtimePMSStayCandidatesMatch(candidates[index], candidates[other]) {
					continue
				}
				candidates[index] = mergeRuntimePMSStayCandidates(candidates[index], candidates[other])
				candidates = append(candidates[:other], candidates[other+1:]...)
				merged = true
				break
			}
		}
	}
	return candidates
}

func runtimePMSStayCandidateData(data any) []map[string]any {
	switch value := data.(type) {
	case []any:
		ret := make([]map[string]any, 0, len(value))
		for _, item := range value {
			ret = append(ret, runtimePMSStayCandidateData(item)...)
		}
		return ret
	case map[string]any:
		nested, _ := value["receptOrderList"].([]any)
		if len(nested) == 0 {
			return []map[string]any{cloneRuntimePMSReadData(value)}
		}
		ret := make([]map[string]any, 0, len(nested))
		for _, item := range nested {
			recept, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// The reserve root is authoritative for stay dates and price details;
			// the nested reception contributes its linked id and assigned room.
			ret = append(ret, mergeRuntimePMSReadData(recept, value))
		}
		if len(ret) > 0 {
			return ret
		}
		return []map[string]any{cloneRuntimePMSReadData(value)}
	default:
		return nil
	}
}

func runtimePMSStayCandidateFromData(data map[string]any) runtimePMSStayCandidate {
	if data == nil {
		return runtimePMSStayCandidate{}
	}
	return runtimePMSStayCandidate{
		data:           data,
		reserveOrderID: firstRuntimePMSReadText(data, "reserveOrderId"),
		receptOrderID:  firstRuntimePMSReadText(data, "receptOrderId"),
		checkIn:        normalizePMSReadDate(firstRuntimePMSReadText(data, "checkInTime", "checkInBusinessDate")),
		checkOut:       normalizePMSReadDate(firstRuntimePMSReadText(data, "checkOutTime", "checkOutBusinessDate")),
		roomName:       firstNonEmpty(firstRuntimePMSReadText(data, "roomName"), strings.Join(runtimePMSOrderRoomNames(data), "/")),
		homeName:       firstRuntimePMSReadText(data, "homeName"),
		orderNumber:    firstRuntimePMSReadText(data, "channelOrderNumber", "reserveOrderNo"),
	}
}

func runtimePMSStayCandidatesMatch(left, right runtimePMSStayCandidate) bool {
	if left.reserveOrderID != "" && left.reserveOrderID == right.reserveOrderID {
		return left.receptOrderID == "" || right.receptOrderID == "" || left.receptOrderID == right.receptOrderID
	}
	if left.receptOrderID != "" && left.receptOrderID == right.receptOrderID {
		return left.reserveOrderID == "" || right.reserveOrderID == "" || left.reserveOrderID == right.reserveOrderID
	}
	if left.checkIn == "" || left.checkOut == "" || left.checkIn != right.checkIn || left.checkOut != right.checkOut {
		return false
	}
	if left.reserveOrderID != "" && right.reserveOrderID != "" && left.reserveOrderID != right.reserveOrderID {
		return false
	}
	if left.receptOrderID != "" && right.receptOrderID != "" && left.receptOrderID != right.receptOrderID {
		return false
	}
	return true
}

func mergeRuntimePMSStayCandidates(left, right runtimePMSStayCandidate) runtimePMSStayCandidate {
	return runtimePMSStayCandidateFromData(mergeRuntimePMSReadData(right.data, left.data))
}

func mergeRuntimePMSReadData(base, preferred map[string]any) map[string]any {
	ret := cloneRuntimePMSReadData(base)
	for key, value := range preferred {
		if key == "receptOrderList" {
			continue
		}
		if runtimePMSReadText(value) == "" {
			if _, exists := ret[key]; exists {
				continue
			}
		}
		ret[key] = value
	}
	return ret
}

func cloneRuntimePMSReadData(source map[string]any) map[string]any {
	ret := make(map[string]any, len(source))
	for key, value := range source {
		if key != "receptOrderList" {
			ret[key] = value
		}
	}
	return ret
}

func runtimePMSAmbiguousStayMessage(candidates []runtimePMSStayCandidate) string {
	details := make([]string, 0, min(len(candidates), 3))
	for index, candidate := range candidates {
		fields := make([]string, 0, 4)
		if candidate.checkIn != "" || candidate.checkOut != "" {
			fields = append(fields, firstNonEmpty(candidate.checkIn, "日期未知")+"至"+firstNonEmpty(candidate.checkOut, "日期未知"))
		}
		if candidate.roomName != "" {
			fields = append(fields, candidate.roomName)
		}
		if candidate.homeName != "" {
			fields = append(fields, "房号"+candidate.homeName)
		}
		orderNumber := firstNonEmpty(candidate.orderNumber, candidate.reserveOrderID, candidate.receptOrderID)
		if orderNumber != "" {
			fields = append(fields, "订单"+orderNumber)
		}
		if len(fields) == 0 {
			fields = append(fields, fmt.Sprintf("第%d笔", index+1))
		}
		details = append(details, strings.Join(fields, "、"))
		if len(details) == 3 {
			break
		}
	}
	return "查询到多个匹配住宿（" + strings.Join(details, "；") + "），请确认入住日期、房型或订单号"
}

func runtimePMSReadOrderBindingSourceIDs(bindings []pmsReadPlanBinding) []string {
	ret := make([]string, 0, 2)
	for _, binding := range bindings {
		for _, source := range binding.Sources {
			if source.StepID == "order.reserve" || source.StepID == "order.recept" {
				ret = append(ret, source.StepID)
			}
		}
	}
	return uniquePMSReadStrings(ret)
}

func runtimePMSReadBindingUsesOrderSource(binding pmsReadPlanBinding) bool {
	for _, source := range binding.Sources {
		if source.StepID == "order.reserve" || source.StepID == "order.recept" {
			return true
		}
	}
	return false
}

func runtimePMSReadBindingFields(binding pmsReadPlanBinding) []string {
	ret := make([]string, 0, 4)
	for _, source := range binding.Sources {
		if source.StepID == "order.reserve" || source.StepID == "order.recept" {
			ret = append(ret, source.Fields...)
		}
	}
	return uniquePMSReadStrings(ret)
}

func runtimePMSReadBindingSourceValues(data any, fields []string, argument string) []string {
	if argument == "beginTime" || argument == "endTime" {
		for _, field := range fields {
			if values := runtimePMSReadPathStrings(data, field); len(values) > 0 {
				return values
			}
		}
		return nil
	}
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		values = append(values, runtimePMSReadPathStrings(data, field)...)
	}
	return values
}

func normalizeRuntimePMSBindingValues(argument string, values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch strings.TrimSpace(argument) {
		case "beginTime", "endTime":
			if date := normalizePMSReadDate(value); date != "" {
				value = date
			}
		}
		normalized = append(normalized, value)
	}
	return uniquePMSReadStrings(normalized)
}

func runtimePMSReadArgumentMissingMessage(key string) string {
	switch strings.TrimSpace(key) {
	case "beginTime", "endTime":
		return "缺少完整有效的入住和离店日期"
	case "roomTypeId":
		return "缺少已由 PMS 唯一匹配的目标房型"
	case "reserveOrderId", "receptOrderId", "currentReceptOrderId":
		return "缺少真实订单定位信息"
	case "phone":
		return "缺少用于查询当前客户信息的手机号"
	case "keyword":
		return "缺少用于查询当前房态的房号"
	default:
		return "缺少完成当前查询所需的必要信息"
	}
}

func runtimePMSReadPathStrings(data any, path string) []string {
	parts := strings.Split(path, ".")
	values := []any{data}
	for _, part := range parts {
		array := strings.HasSuffix(part, "[]")
		key := strings.TrimSuffix(part, "[]")
		next := make([]any, 0)
		for _, value := range values {
			objects := []any{value}
			if items, ok := value.([]any); ok {
				objects = items
			}
			for _, objectValue := range objects {
				object, ok := objectValue.(map[string]any)
				if !ok {
					continue
				}
				child, exists := object[key]
				if !exists {
					continue
				}
				if array {
					if items, ok := child.([]any); ok {
						next = append(next, items...)
					}
				} else {
					next = append(next, child)
				}
			}
		}
		values = next
	}
	ret := make([]string, 0, len(values))
	for _, value := range values {
		text := runtimePMSReadText(value)
		if text != "" {
			ret = append(ret, text)
		}
	}
	return uniquePMSReadStrings(ret)
}

func runtimePMSOrderStepIDs(plan pmsReadPlan) []string {
	ret := make([]string, 0, 2)
	for _, step := range plan.Steps {
		if step.ID == "order.reserve" || step.ID == "order.recept" {
			ret = append(ret, step.ID)
		}
	}
	return ret
}

func resolveRuntimePMSTargetRoomType(data any, targetText string) (string, string, pmsReadStepStatus) {
	items, ok := data.([]any)
	if !ok {
		return "", "", pmsReadStepUnavailable
	}
	normalizedTarget := normalizeRuntimePMSRoomTypeText(targetText)
	type candidate struct{ id, name string }
	exactCandidates := make([]candidate, 0)
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id := firstRuntimePMSReadText(row, "roomTypeId", "productId", "roomId")
		name := firstRuntimePMSReadText(row, "roomTypeName", "productName", "roomName")
		normalizedName := normalizeRuntimePMSRoomTypeText(name)
		if id == "" || normalizedName == "" {
			continue
		}
		if normalizedTarget == normalizedName {
			exactCandidates = append(exactCandidates, candidate{id: id, name: name})
		}
	}
	candidates := exactCandidates
	if len(candidates) == 0 {
		return "", "", pmsReadStepEmpty
	}
	unique := make(map[string]candidate)
	for _, item := range candidates {
		unique[item.id] = item
	}
	if len(unique) != 1 {
		return "", "", pmsReadStepAmbiguous
	}
	for _, item := range unique {
		return item.id, item.name, pmsReadStepOK
	}
	return "", "", pmsReadStepEmpty
}

func normalizeRuntimePMSRoomTypeText(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsSpace(r) || strings.ContainsRune("，。！？；：,.!?;:-_/（）()【】[]", r) {
			continue
		}
		b.WriteRune(r)
	}
	normalized := b.String()
	for _, suffix := range []string{"房间", "房型", "房"} {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	return normalized
}

func removePMSReadString(values []string, remove string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if value != remove {
			ret = append(ret, value)
		}
	}
	return ret
}

func applyRuntimePMSReadResultToTask(task *callbacks.ReplyTaskPlanTraceData, plan pmsReadPlan, result pmsReadPlanResult, taskIndex int) {
	if task == nil {
		return
	}
	hasUsablePriceFact := false
	for _, step := range result.Steps {
		if step.StepID == "price.difference" && (step.Status == pmsReadStepOK || step.Status == pmsReadStepPartial) {
			hasUsablePriceFact = true
			break
		}
	}
	factIndex := 0
	for _, step := range result.Steps {
		if step.Status != pmsReadStepOK && step.Status != pmsReadStepPartial {
			continue
		}
		if plan.Scenario == pmsReadScenarioPrice && step.StepID == "inventory.stay" && hasUsablePriceFact {
			continue
		}
		for _, fact := range runtimePMSReadFactsForTask(*task, plan, step) {
			if strings.TrimSpace(fact.Statement) == "" || runtimePMSReadTaskHasFact(*task, fact.Aspect, fact.Statement) {
				continue
			}
			factIndex++
			task.SupportedFacts = append(task.SupportedFacts, callbacks.KnowledgeEvidenceFactTraceData{
				FactID:         fmt.Sprintf("P%dF%d", taskIndex+1, factIndex),
				Aspect:         fact.Aspect,
				Statement:      fact.Statement,
				CriticalValues: append([]string(nil), fact.CriticalValues...),
			})
		}
	}
	for _, missing := range result.Unconfirmed {
		if step, ok := runtimePMSReadPlanStepByID(plan, missing); ok &&
			!step.Required && (missing == "order.reserve" || missing == "order.recept") &&
			runtimePMSReadHasOtherOrderFact(result, missing) {
			continue
		}
		if step, ok := runtimePMSReadPlanStepByID(plan, missing); ok && !step.Required && !runtimePMSOptionalStepRelevantToTask(plan, *task, missing) {
			continue
		}
		task.MissingAspects = appendIfMissing(task.MissingAspects, runtimePMSReadMissingAspect(missing))
	}
	for _, step := range result.Steps {
		if step.Status == pmsReadStepOK || step.Status == pmsReadStepPartial {
			continue
		}
		planned, plannedStep := runtimePMSReadPlanStepByID(plan, step.StepID)
		if plannedStep && !planned.Required &&
			(step.StepID == "order.reserve" || step.StepID == "order.recept") &&
			runtimePMSReadHasOtherOrderFact(result, step.StepID) {
			continue
		}
		if plannedStep && !planned.Required && !runtimePMSOptionalStepRelevantToTask(plan, *task, step.StepID) {
			continue
		}
		message := strings.TrimSpace(step.Message)
		if message == "" {
			message = runtimePMSReadMissingAspect(step.StepID)
		}
		task.MissingAspects = appendIfMissing(task.MissingAspects, message)
	}
	// Generate still owns the normal response. Keep one customer-safe projection
	// from the same structured result so an empty model response can recover the
	// active goal without dumping PMS facts or asking the customer to repeat it.
	if answer := strings.TrimSpace(runtimePMSCustomerAnswer(*task, plan, result)); answer != "" {
		task.AnswerText = &answer
	}
}

type runtimePMSReadTaskFact struct {
	Aspect         string
	Statement      string
	CriticalValues []string
}

func runtimePMSReadFactsForTask(task callbacks.ReplyTaskPlanTraceData, plan pmsReadPlan, step pmsReadStepResult) []runtimePMSReadTaskFact {
	if plan.Scenario == pmsReadScenarioOrder && (step.StepID == "order.reserve" || step.StepID == "order.recept") {
		if fact, requested, ok := runtimePMSFocusedOrderFact(task, step.Data); ok {
			return []runtimePMSReadTaskFact{fact}
		} else if requested {
			return nil
		}
	}
	statement := runtimePMSReadFactStatement(plan, step)
	if statement == "" {
		return nil
	}
	return []runtimePMSReadTaskFact{{
		Aspect:    "pms_" + strings.ReplaceAll(step.StepID, ".", "_"),
		Statement: statement,
	}}
}

func runtimePMSFocusedOrderFact(task callbacks.ReplyTaskPlanTraceData, data any) (runtimePMSReadTaskFact, bool, bool) {
	text := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	type projection struct {
		aspect string
		label  string
		match  bool
		value  func(map[string]any) string
	}
	projections := []projection{
		{
			aspect: "pms_order_checkout_time", label: "当前订单离店时间",
			match: containsAny(text, []string{"退房", "离店", "住到", "到几号", "到哪天", "到什么时候"}),
			value: func(order map[string]any) string {
				return firstRuntimePMSReadText(order, "checkOutTime", "checkOutBusinessDate")
			},
		},
		{
			aspect: "pms_order_checkin_time", label: "当前订单入住时间",
			match: containsAny(text, []string{"入住时间", "几点入住", "哪天入住", "什么时候入住", "到店时间"}),
			value: func(order map[string]any) string {
				return firstRuntimePMSReadText(order, "checkInTime", "checkInBusinessDate")
			},
		},
		{
			aspect: "pms_order_room_number", label: "当前订单房号",
			match: containsAny(text, []string{"房号", "哪间房", "住哪间", "什么房间"}),
			value: func(order map[string]any) string { return firstRuntimePMSReadText(order, "homeName") },
		},
		{
			aspect: "pms_order_room_type", label: "当前订单房型",
			match: containsAny(text, []string{"房型", "什么房", "订的什么房"}),
			value: func(order map[string]any) string { return strings.Join(runtimePMSOrderRoomNames(order), "/") },
		},
		{
			aspect: "pms_order_amount", label: "当前订单金额",
			match: containsAny(text, []string{"多少钱", "金额", "费用", "房费"}),
			value: func(order map[string]any) string {
				return firstRuntimePMSReadText(order, "payableAmount", "roomFee", "payAmount", "waitPayAmount")
			},
		},
		{
			aspect: "pms_order_status", label: "当前订单状态",
			match: containsAny(text, []string{"订单状态", "预订状态", "成功了吗", "确认了吗"}),
			value: runtimePMSCustomerOrderStatus,
		},
	}
	for _, item := range projections {
		if !item.match {
			continue
		}
		values := runtimePMSUniqueOrderValues(data, item.value)
		if len(values) != 1 {
			return runtimePMSReadTaskFact{}, true, false
		}
		return runtimePMSReadTaskFact{
			Aspect: item.aspect, Statement: item.label + "为" + values[0] + "。",
			CriticalValues: []string{values[0]},
		}, true, true
	}
	return runtimePMSReadTaskFact{}, false, false
}

func runtimePMSUniqueOrderValues(data any, value func(map[string]any) string) []string {
	orders := runtimePMSOrderObjects(data)
	ret := make([]string, 0, len(orders))
	seen := make(map[string]struct{}, len(orders))
	for _, order := range orders {
		item := strings.TrimSpace(value(order))
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		ret = append(ret, item)
	}
	return ret
}

func runtimePMSReadTaskHasFact(task callbacks.ReplyTaskPlanTraceData, aspect, statement string) bool {
	aspect = strings.TrimSpace(aspect)
	statement = strings.TrimSpace(statement)
	for _, fact := range task.SupportedFacts {
		if strings.TrimSpace(fact.Aspect) == aspect && strings.TrimSpace(fact.Statement) == statement {
			return true
		}
	}
	return false
}

func runtimePMSCustomerAnswer(task callbacks.ReplyTaskPlanTraceData, plan pmsReadPlan, result pmsReadPlanResult) string {
	switch plan.Scenario {
	case pmsReadScenarioOrder:
		return runtimePMSCustomerOrderAnswer(task, result)
	case pmsReadScenarioRoomChange, pmsReadScenarioRoomUpgrade:
		return runtimePMSCustomerRoomChoiceAnswer(task, plan, result)
	case pmsReadScenarioMemberInfo, pmsReadScenarioMemberBenefit:
		return runtimePMSCustomerMemberAnswer(result)
	default:
		return ""
	}
}

func runtimePMSCustomerOrderAnswer(task callbacks.ReplyTaskPlanTraceData, result pmsReadPlanResult) string {
	stay, ok := runtimePMSCustomerStay(result)
	if !ok {
		return ""
	}
	text := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	checkIn := runtimePMSCustomerDateTime(firstRuntimePMSReadText(stay.data, "checkInTime", "checkInBusinessDate"))
	checkOut := runtimePMSCustomerDateTime(firstRuntimePMSReadText(stay.data, "checkOutTime", "checkOutBusinessDate"))
	roomType := firstNonEmpty(stay.roomName, strings.Join(runtimePMSOrderRoomNames(stay.data), "/"))
	homeName := stay.homeName
	amount := runtimePMSCustomerAmount(firstRuntimePMSReadText(stay.data, "payableAmount", "roomFee", "payAmount", "waitPayAmount"))
	status := runtimePMSCustomerOrderStatus(stay.data)

	switch {
	case containsAny(text, []string{"退房", "离店"}) && checkOut != "":
		return "查到了，您这笔订单是" + checkOut + "前退房。"
	case containsAny(text, []string{"入住", "到店"}) && checkIn != "":
		return "查到了，您这笔订单是" + checkIn + "入住。"
	case containsAny(text, []string{"房号", "哪间房", "住哪"}) && homeName != "":
		return "查到了，您当前安排的房号是" + homeName + "。"
	case containsAny(text, []string{"多少钱", "金额", "费用", "房费"}) && amount != "":
		return "查到了，您这笔订单的金额是" + amount + "。"
	case (task.SubIntent == "order_status" || containsAny(text, []string{"状态", "成功了吗", "确认了吗"})) && status != "":
		return "查到了，您这笔订单当前是" + status + "。"
	}

	fields := make([]string, 0, 5)
	if roomType != "" {
		fields = append(fields, "订的是"+roomType)
	}
	if homeName != "" {
		fields = append(fields, "房号"+homeName)
	}
	if checkIn != "" {
		fields = append(fields, checkIn+"入住")
	}
	if checkOut != "" {
		fields = append(fields, checkOut+"前退房")
	}
	if amount != "" {
		fields = append(fields, "订单金额"+amount)
	}
	if len(fields) == 0 {
		return ""
	}
	return "查到了，您" + strings.Join(fields, "，") + "。"
}

func runtimePMSCustomerRoomChoiceAnswer(task callbacks.ReplyTaskPlanTraceData, plan pmsReadPlan, result pmsReadPlanResult) string {
	stay, hasStay := runtimePMSCustomerStay(result)
	options := runtimePMSCustomerRoomOptions(result)
	if !hasStay || len(options) == 0 {
		return ""
	}
	currentRoom := firstNonEmpty(stay.roomName, strings.Join(runtimePMSOrderRoomNames(stay.data), "/"))
	currentLabel := currentRoom
	if stay.homeName != "" {
		currentLabel += stay.homeName
	}
	targetText := runtimePMSTargetRoomTypeText(task)
	normalizedTarget := normalizeRuntimePMSRoomTypeText(targetText)
	if runtimePMSGenericRoomChoice(normalizedTarget) {
		normalizedTarget = ""
	}

	if normalizedTarget != "" {
		for _, option := range options {
			if normalizeRuntimePMSRoomTypeText(option.name) != normalizedTarget {
				continue
			}
			prefix := ""
			if currentLabel != "" {
				prefix = "您现在住的是" + currentLabel + "。"
			}
			if strings.TrimSpace(option.available) == "" {
				return prefix + "我查到了" + option.name + "房型，但完整入住期间的可售数量还没返回，暂时不能确认有没有空房。"
			}
			if !runtimePMSPositiveAvailability(option.available) {
				alternatives := runtimePMSAlternativeRoomNames(options, currentRoom, option.name, 3)
				if len(alternatives) == 0 {
					return prefix + option.name + "在您当前入住期间暂时没有可售房。"
				}
				return prefix + option.name + "在您当前入住期间暂时没有可售房，目前还可以看" + strings.Join(alternatives, "、") + "，您更想换哪一种？"
			}
			parts := []string{option.name + "在您当前入住期间还有房"}
			if rooms := runtimePMSCustomerCandidateRooms(result, option.name, 3); len(rooms) > 0 {
				parts = append(parts, "当前可选房间有"+strings.Join(rooms, "、"))
			}
			if price := runtimePMSCustomerPriceDifference(result); price != "" {
				parts = append(parts, price)
			} else if option.price != "" {
				parts = append(parts, "当前房型价格为"+runtimePMSCustomerAmount(option.price))
			} else {
				parts = append(parts, "具体差价还需要进一步核对")
			}
			return prefix + strings.Join(parts, "，") + "。"
		}
	}

	alternatives := runtimePMSAlternativeRoomNames(options, currentRoom, "", 3)
	if len(alternatives) == 0 {
		return ""
	}
	prefix := ""
	if currentLabel != "" {
		prefix = "您现在住的是" + currentLabel + "。"
	}
	verb := "换"
	if plan.Scenario == pmsReadScenarioRoomUpgrade {
		verb = "升级到"
	}
	return prefix + "当前完整入住期间可以" + verb + strings.Join(alternatives, "、") + "，您更想选哪一种？我再帮您核对具体房间和差价。"
}

type runtimePMSCustomerRoomOption struct {
	id        string
	name      string
	available string
	price     string
}

func runtimePMSCustomerRoomOptions(result pmsReadPlanResult) []runtimePMSCustomerRoomOption {
	for _, step := range result.Steps {
		if step.StepID != "inventory.stay" || (step.Status != pmsReadStepOK && step.Status != pmsReadStepPartial) {
			continue
		}
		items, _ := step.Data.([]any)
		expected, _ := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
		options := make([]runtimePMSCustomerRoomOption, 0, len(items))
		for _, value := range items {
			row, ok := value.(map[string]any)
			if !ok {
				continue
			}
			name := firstRuntimePMSReadText(row, "roomTypeName", "productName", "roomName")
			if name == "" {
				continue
			}
			options = append(options, runtimePMSCustomerRoomOption{
				id: firstRuntimePMSReadText(row, "roomTypeId", "productId", "roomId"), name: name,
				available: runtimePMSInventoryAvailability(row, expected), price: firstRuntimePMSReadText(row, "price"),
			})
		}
		return options
	}
	return nil
}

func runtimePMSCustomerStay(result pmsReadPlanResult) (runtimePMSStayCandidate, bool) {
	byStep := make(map[string]pmsReadStepResult, len(result.Steps))
	for _, step := range result.Steps {
		byStep[step.StepID] = step
	}
	candidates := runtimePMSStayCandidates(byStep, []string{"order.reserve", "order.recept"})
	if len(candidates) != 1 {
		return runtimePMSStayCandidate{}, false
	}
	return candidates[0], true
}

func runtimePMSGenericRoomChoice(value string) bool {
	switch value {
	case "", "吗", "吧", "可以", "其他", "别的", "另外", "另一", "随便", "都行", "其他的", "别的的":
		return true
	default:
		return false
	}
}

func runtimePMSAlternativeRoomNames(options []runtimePMSCustomerRoomOption, current, skip string, limit int) []string {
	ret := make([]string, 0, limit)
	for _, option := range options {
		if option.name == "" || !runtimePMSPositiveAvailability(option.available) ||
			normalizeRuntimePMSRoomTypeText(option.name) == normalizeRuntimePMSRoomTypeText(current) ||
			normalizeRuntimePMSRoomTypeText(option.name) == normalizeRuntimePMSRoomTypeText(skip) {
			continue
		}
		ret = appendIfMissing(ret, option.name)
		if len(ret) >= limit {
			break
		}
	}
	return ret
}

func runtimePMSPositiveAvailability(value string) bool {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && number > 0
}

func runtimePMSCustomerCandidateRooms(result pmsReadPlanResult, roomType string, limit int) []string {
	for _, step := range result.Steps {
		if step.StepID != "stay.room_availability" || (step.Status != pmsReadStepOK && step.Status != pmsReadStepPartial) {
			continue
		}
		root, _ := step.Data.(map[string]any)
		items, _ := root["candidates"].([]any)
		ret := make([]string, 0, limit)
		for _, value := range items {
			candidate, ok := value.(map[string]any)
			if !ok || normalizeRuntimePMSRoomTypeText(firstRuntimePMSReadText(candidate, "roomTypeName")) != normalizeRuntimePMSRoomTypeText(roomType) {
				continue
			}
			if homeName := firstRuntimePMSReadText(candidate, "homeName"); homeName != "" {
				ret = appendIfMissing(ret, homeName)
			}
			if len(ret) >= limit {
				break
			}
		}
		return ret
	}
	return nil
}

func runtimePMSCustomerPriceDifference(result pmsReadPlanResult) string {
	for _, step := range result.Steps {
		if step.StepID != "price.difference" || (step.Status != pmsReadStepOK && step.Status != pmsReadStepPartial) {
			continue
		}
		root, _ := step.Data.(map[string]any)
		assessment, _ := root["assessment"].(map[string]any)
		if assessment == nil {
			assessment = root
		}
		if firstRuntimePMSReadText(assessment, "status") != "exact" {
			return ""
		}
		difference := firstRuntimePMSReadText(assessment, "difference")
		if difference == "" {
			return ""
		}
		value, err := strconv.ParseFloat(difference, 64)
		if err != nil {
			return "差价为" + runtimePMSCustomerAmount(difference)
		}
		switch {
		case value > 0:
			return "需要补" + runtimePMSCustomerAmount(difference)
		case value < 0:
			return "预计退回" + runtimePMSCustomerAmount(strings.TrimPrefix(difference, "-"))
		default:
			return "不需要补差价"
		}
	}
	return ""
}

func runtimePMSCustomerMemberAnswer(result pmsReadPlanResult) string {
	for _, step := range result.Steps {
		if step.StepID != "member.info" && step.StepID != "member.benefits" {
			continue
		}
		root, _ := step.Data.(map[string]any)
		member, _ := root["member"].(map[string]any)
		grade, _ := root["grade"].(map[string]any)
		if member == nil {
			member = root
		}
		gradeName := firstRuntimePMSReadText(member, "gradeName")
		benefits := runtimePMSMemberBenefitTexts(grade)
		if gradeName == "" && len(benefits) == 0 {
			return ""
		}
		answer := "查到了"
		if gradeName != "" {
			answer += "，您当前是" + gradeName
		}
		if len(benefits) > 0 {
			answer += "，可用权益包括" + strings.Join(benefits, "、")
		}
		return answer + "。"
	}
	return ""
}

func runtimePMSCustomerDateTime(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if parsed.Hour() == 0 && parsed.Minute() == 0 {
			return fmt.Sprintf("%d月%d日", parsed.Month(), parsed.Day())
		}
		if parsed.Minute() == 0 {
			return fmt.Sprintf("%d月%d日%d点", parsed.Month(), parsed.Day(), parsed.Hour())
		}
		return fmt.Sprintf("%d月%d日%02d:%02d", parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute())
	}
	if date := normalizePMSReadDate(value); date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err == nil {
			return fmt.Sprintf("%d月%d日", parsed.Month(), parsed.Day())
		}
	}
	return value
}

func runtimePMSCustomerAmount(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "元") {
		return value
	}
	return value + "元"
}

func runtimePMSReadPlanStepByID(plan pmsReadPlan, stepID string) (pmsReadPlanStep, bool) {
	for _, step := range plan.Steps {
		if step.ID == stepID {
			return step, true
		}
	}
	return pmsReadPlanStep{}, false
}

func runtimePMSOptionalStepRelevantToTask(plan pmsReadPlan, task callbacks.ReplyTaskPlanTraceData, stepID string) bool {
	text := strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n")
	switch stepID {
	case "order.reserve", "order.recept":
		return true
	case "member.benefits":
		return plan.Scenario == pmsReadScenarioMemberBenefit || containsAny(text, []string{"会员", "权益", "免差", "免费升", "等级"})
	case "price.difference":
		return plan.Scenario == pmsReadScenarioPrice || containsAny(text, []string{"差价", "补多少", "多少钱", "费用", "价格"})
	case "room.status":
		return plan.Scenario == pmsReadScenarioRoomStatus || containsAny(text, []string{"房态", "空净", "空脏", "住净", "住脏", "打扫", "清扫", "维修", "锁房"})
	case "stay.room_availability":
		return containsAny(text, []string{"具体房", "房号", "哪间", "可分配", "能安排"})
	default:
		return false
	}
}

func runtimePMSReadHasOtherOrderFact(result pmsReadPlanResult, skipStepID string) bool {
	for _, step := range result.Steps {
		if step.StepID == skipStepID || (step.StepID != "order.reserve" && step.StepID != "order.recept") {
			continue
		}
		if (step.Status == pmsReadStepOK || step.Status == pmsReadStepPartial) && runtimePMSOrderFact(step.Data) != "" {
			return true
		}
	}
	return false
}

func runtimePMSReadFactStatement(plan pmsReadPlan, step pmsReadStepResult) string {
	switch step.StepID {
	case "order.reserve", "order.recept":
		if plan.Scenario == pmsReadScenarioDateInventory || plan.Scenario == pmsReadScenarioRoomStatus || plan.Scenario == pmsReadScenarioPrice {
			return ""
		}
		return runtimePMSOrderFact(step.Data)
	case "inventory.stay":
		return runtimePMSInventoryFactForPlan(plan, step)
	case "member.benefits":
		return runtimePMSMemberFactForPlan(plan, step.Data)
	case "member.info":
		return runtimePMSMemberFact(step.Data)
	case "price.difference":
		return runtimePMSPriceFact(step.Data)
	case "room.status":
		return runtimePMSRoomStatusFact(step.Data)
	case "stay.room_availability":
		return runtimePMSStayRoomAvailabilityFact(step.Data)
	case "renew.candidates":
		return runtimePMSRenewCandidateFact(step.Data)
	case "late_checkout.assessment":
		return runtimePMSLateCheckoutFact(step.Data)
	default:
		_ = plan
		return ""
	}
}

func runtimePMSOrderFact(data any) string {
	orders := runtimePMSOrderObjects(data)
	if len(orders) == 0 {
		return ""
	}
	type customerOrderFact struct {
		roomNames string
		homeName  string
		checkIn   string
		checkOut  string
		status    string
		amount    string
	}
	merged := make(map[string]customerOrderFact, len(orders))
	keys := make([]string, 0, len(orders))
	for _, order := range orders {
		fact := customerOrderFact{
			roomNames: strings.Join(runtimePMSOrderRoomNames(order), "/"),
			homeName:  firstRuntimePMSReadText(order, "homeName"),
			checkIn:   firstRuntimePMSReadText(order, "checkInTime", "checkInBusinessDate"),
			checkOut:  firstRuntimePMSReadText(order, "checkOutTime", "checkOutBusinessDate"),
			status:    runtimePMSCustomerOrderStatus(order),
			amount:    firstRuntimePMSReadText(order, "payableAmount", "roomFee", "payAmount", "waitPayAmount"),
		}
		key := strings.Join([]string{fact.roomNames, fact.homeName, normalizePMSReadDate(fact.checkIn), normalizePMSReadDate(fact.checkOut)}, "|")
		if current, exists := merged[key]; exists {
			if current.status == "" {
				current.status = fact.status
			}
			if current.amount == "" {
				current.amount = fact.amount
			}
			merged[key] = current
			continue
		}
		merged[key] = fact
		keys = append(keys, key)
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		order := merged[key]
		fields := make([]string, 0, 6)
		appendRuntimePMSFactField(&fields, "房型", order.roomNames)
		appendRuntimePMSFactField(&fields, "房号", order.homeName)
		appendRuntimePMSFactField(&fields, "入住", order.checkIn)
		appendRuntimePMSFactField(&fields, "离店", order.checkOut)
		appendRuntimePMSFactField(&fields, "状态", order.status)
		appendRuntimePMSFactField(&fields, "金额", order.amount)
		if len(fields) > 0 {
			parts = append(parts, strings.Join(fields, "，"))
		}
	}
	if len(parts) == 0 {
		return "已查询到当前有效订单，但返回字段不足以确认客户所问详情。"
	}
	return "PMS 当前有效订单：" + strings.Join(parts, "；") + "。"
}

func runtimePMSCustomerOrderStatus(order map[string]any) string {
	if status := firstRuntimePMSReadText(order, "orderStatusName", "reserveStatusName", "statusName"); status != "" {
		return status
	}
	if firstRuntimePMSReadText(order, "orderStatus", "reserveStatus") != "" {
		return "暂未返回可读状态"
	}
	return ""
}

func runtimePMSOrderRoomNames(order map[string]any) []string {
	ret := make([]string, 0, 2)
	if roomName := firstRuntimePMSReadText(order, "roomName"); roomName != "" {
		ret = append(ret, roomName)
	}
	products, _ := order["reserveProductList"].([]any)
	for _, value := range products {
		product, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if roomName := firstRuntimePMSReadText(product, "roomName"); roomName != "" {
			ret = appendIfMissing(ret, roomName)
		}
	}
	return ret
}

func runtimePMSOrderObjects(data any) []map[string]any {
	root, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	ret := []map[string]any{root}
	if nested, ok := root["receptOrderList"].([]any); ok {
		for _, value := range nested {
			if item, ok := value.(map[string]any); ok {
				ret = append(ret, item)
			}
		}
	}
	return ret
}

func runtimePMSInventoryFact(step pmsReadStepResult) string {
	return runtimePMSInventoryFactForPlan(pmsReadPlan{}, step)
}

func runtimePMSInventoryFactForPlan(plan pmsReadPlan, step pmsReadStepResult) string {
	items, ok := step.Data.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(items), 6))
	expected, _ := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
	targetRoomTypeID := strings.TrimSpace(step.Args["roomTypeId"])
	for _, candidate := range plan.Steps {
		if targetRoomTypeID == "" && candidate.ID == "price.difference" {
			targetRoomTypeID = strings.TrimSpace(candidate.Args["roomTypeId"])
			break
		}
	}
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if targetRoomTypeID != "" && firstRuntimePMSReadText(row, "roomTypeId", "productId", "roomId") != targetRoomTypeID {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomTypeName", "productName", "roomName")
		if name == "" {
			continue
		}
		availability := runtimePMSInventoryAvailability(row, expected)
		price := firstRuntimePMSReadText(row, "price")
		text := name
		if availability != "" {
			text += "可售" + availability + "间"
		}
		if price != "" {
			text += "，价格" + price
		}
		parts = append(parts, text)
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	prefix := "PMS 当前日期区间的房型库存："
	suffix := "。库存是查询时结果，不代表已经锁房。"
	if plan.Scenario == pmsReadScenarioRenewal {
		prefix = "PMS 当前续住日期区间的同房型库存："
		suffix = "。这是当前续住库存查询结果，不代表已经锁房或完成续住。"
	}
	if step.Status == pmsReadStepPartial {
		prefix = "PMS 仅返回部分日期的房型库存，不能确认整个入住区间："
		if plan.Scenario == pmsReadScenarioRenewal {
			prefix = "PMS 仅返回部分续住日期的同房型库存，暂时不能确认完整续住区间："
		}
	}
	return prefix + strings.Join(parts, "；") + suffix
}

func runtimePMSInventoryAvailability(row map[string]any, expectedDates []string) string {
	bookings, ok := row["bookings"].(map[string]any)
	if !ok || len(bookings) == 0 || len(expectedDates) == 0 {
		return ""
	}
	minimum := ""
	for _, date := range expectedDates {
		day, exists := bookings[date].(map[string]any)
		if !exists {
			continue
		}
		available := firstRuntimePMSReadText(day, "available")
		if available == "" {
			return ""
		}
		if minimum == "" || compareRuntimePMSNumericText(available, minimum) < 0 {
			minimum = available
		}
	}
	return minimum
}

func runtimePMSMemberFact(data any) string {
	return runtimePMSMemberFactForPlan(pmsReadPlan{}, data)
}

func runtimePMSMemberFactForPlan(plan pmsReadPlan, data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	member, _ := root["member"].(map[string]any)
	grade, _ := root["grade"].(map[string]any)
	if member == nil && grade == nil {
		member = root
	}
	fields := make([]string, 0, 5)
	appendRuntimePMSFactField(&fields, "会员等级", firstRuntimePMSReadText(member, "gradeName"))
	appendRuntimePMSFactField(&fields, "会员状态", firstRuntimePMSReadText(member, "statusName"))
	if available := firstRuntimePMSReadText(member, "gradeAvailable"); available != "" {
		if available == "true" {
			fields = append(fields, "当前等级有效")
		} else if available == "false" {
			fields = append(fields, "当前等级不可用")
		}
	}
	benefits := runtimePMSMemberBenefitTextsForScenario(grade, plan.Scenario)
	if len(benefits) > 0 {
		fields = append(fields, "权益"+strings.Join(benefits, "、"))
	} else if grade != nil && plan.Scenario == pmsReadScenarioRoomUpgrade {
		fields = append(fields, "当前权益中未查到免费升房或免差价说明")
	}
	if len(fields) == 0 {
		return ""
	}
	return "PMS 会员信息：" + strings.Join(fields, "，") + "。权益配置不代表已办理升房或减免费用。"
}

func runtimePMSMemberBenefitTextsForScenario(grade map[string]any, scenario pmsReadScenario) []string {
	benefits := runtimePMSMemberBenefitTexts(grade)
	if scenario == "" || scenario == pmsReadScenarioMemberBenefit || scenario == pmsReadScenarioMemberInfo {
		return benefits
	}
	keywords := []string(nil)
	switch scenario {
	case pmsReadScenarioRoomUpgrade, pmsReadScenarioRoomChange, pmsReadScenarioPrice:
		keywords = []string{"升房", "升级", "房型", "差价", "免差"}
	case pmsReadScenarioLateCheckout:
		keywords = []string{"延迟", "延退", "退房"}
	default:
		return benefits
	}
	filtered := make([]string, 0, len(benefits))
	for _, benefit := range benefits {
		for _, keyword := range keywords {
			if strings.Contains(benefit, keyword) {
				filtered = appendIfMissing(filtered, benefit)
				break
			}
		}
	}
	return filtered
}

func runtimePMSMemberBenefitTexts(grade map[string]any) []string {
	if grade == nil {
		return nil
	}
	items, _ := grade["benefits"].([]any)
	ret := make([]string, 0, min(len(items), 5))
	for _, value := range items {
		benefit, ok := value.(map[string]any)
		if !ok {
			continue
		}
		text := firstRuntimePMSReadText(benefit, "label", "benefitName", "contentText")
		if text != "" {
			ret = appendIfMissing(ret, text)
		}
		if len(ret) >= 5 {
			break
		}
	}
	return ret
}

func runtimePMSPriceFact(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	assessment, _ := root["assessment"].(map[string]any)
	if assessment == nil {
		assessment = root
	}
	status := firstRuntimePMSReadText(assessment, "status")
	availability := firstRuntimePMSReadText(assessment, "availability")
	difference := firstRuntimePMSReadText(assessment, "difference")
	currency := firstRuntimePMSReadText(assessment, "currency")
	reason := firstRuntimePMSReadText(assessment, "reason")
	parts := make([]string, 0, 3)
	switch availability {
	case pms.PriceAvailabilityAvailable:
		parts = append(parts, "目标房型在完整入住区间有可售库存")
	case pms.PriceAvailabilityUnavailable:
		parts = append(parts, "目标房型在至少一个入住日没有可售库存")
	}
	if status == "exact" && difference != "" {
		parts = append(parts, "按相同逐日计价口径计算的差价为"+difference+currency)
	} else if reason != "" && availability != pms.PriceAvailabilityUnavailable {
		parts = append(parts, reason)
	}
	if len(parts) == 0 {
		return ""
	}
	return "当前查询结果：" + strings.Join(parts, "，") + "。这只是实时查询结果，尚未办理升房或换房。"
}

func runtimePMSRoomStatusFact(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	groups, _ := root["list"].([]any)
	parts := make([]string, 0, 6)
	for _, groupValue := range groups {
		group, ok := groupValue.(map[string]any)
		if !ok {
			continue
		}
		cards, _ := group["homeCardList"].([]any)
		for _, cardValue := range cards {
			card, ok := cardValue.(map[string]any)
			if !ok {
				continue
			}
			home := firstRuntimePMSReadText(card, "homeName")
			status := firstRuntimePMSReadText(card, "homeStatusName", "homeStatus")
			if home != "" || status != "" {
				parts = append(parts, strings.TrimSpace(home+" "+status))
			}
			if len(parts) >= 6 {
				break
			}
		}
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "PMS 当前房态：" + strings.Join(parts, "；") + "。"
}

func runtimePMSStayRoomAvailabilityFact(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	status := firstRuntimePMSReadText(root, "status")
	startDate := firstRuntimePMSReadText(root, "startDate")
	endDate := firstRuntimePMSReadText(root, "endDate")
	candidateCount := firstRuntimePMSReadText(root, "candidateCount")
	readyNowCount := firstRuntimePMSReadText(root, "readyNowCount")
	reason := firstRuntimePMSReadText(root, "reason")
	roomNames := make([]string, 0, 6)
	items, _ := root["candidates"].([]any)
	for _, value := range items {
		candidate, ok := value.(map[string]any)
		if !ok {
			continue
		}
		roomTypeName := firstRuntimePMSReadText(candidate, "roomTypeName")
		homeName := firstRuntimePMSReadText(candidate, "homeName")
		if homeName == "" {
			continue
		}
		label := strings.TrimSpace(roomTypeName + " " + homeName)
		roomNames = append(roomNames, label)
		if len(roomNames) >= 6 {
			break
		}
	}
	rangeText := strings.TrimSpace(startDate + "至" + endDate)
	switch status {
	case pms.StayRoomAvailabilityAvailable:
		parts := []string{"PMS 已逐间核对" + rangeText + "内返回的订单入住和离店区间"}
		if candidateCount != "" {
			parts = append(parts, "找到"+candidateCount+"间全程无占用冲突且未锁房、未维修的候选房")
		}
		if len(roomNames) > 0 {
			parts = append(parts, "候选为"+strings.Join(roomNames, "、"))
		}
		if readyNowCount != "" {
			parts = append(parts, "其中当前空净"+readyNowCount+"间")
		}
		return strings.Join(parts, "，") + "。这是只读可分配评估，尚未锁房或排房。"
	case pms.StayRoomAvailabilityUnavailable:
		if reason == "" {
			reason = "未找到全程无占用冲突的具体房间"
		}
		return "PMS 已逐间核对" + rangeText + "内返回的订单区间，" + reason + "。"
	case pms.StayRoomAvailabilityPartial:
		parts := []string{"PMS 对" + rangeText + "的具体房间核对不完整"}
		if reason != "" {
			parts = append(parts, reason)
		}
		if len(roomNames) > 0 {
			parts = append(parts, "当前仅能看到候选"+strings.Join(roomNames, "、"))
		}
		return strings.Join(parts, "，") + "，不能据此确认整个入住区间可分配。"
	default:
		return ""
	}
}

func runtimePMSRenewCandidateFact(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	rows, _ := root["rows"].([]any)
	if len(rows) == 0 {
		return "PMS 当前没有返回可用的续住候选。"
	}
	parts := make([]string, 0, min(len(rows), 5))
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomName", "homeName")
		end := firstRuntimePMSReadText(row, "checkOutTime", "checkOutBusinessDate")
		if name != "" || end != "" {
			parts = append(parts, strings.TrimSpace(name+" "+end))
		}
	}
	if len(parts) == 0 {
		return "PMS 已返回续住候选，但客户侧字段不足。"
	}
	return "PMS 续住候选：" + strings.Join(parts, "；") + "。当前只完成查询，没有提交续住。"
}

func runtimePMSLateCheckoutFact(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	fields := make([]string, 0, 2)
	if current := firstRuntimePMSReadText(root, "currentCheckoutTime"); current != "" {
		fields = append(fields, "PMS 当前订单退房时间为"+current)
	}
	target := firstRuntimePMSReadText(root, "targetCheckoutTime")
	if targetDate := firstRuntimePMSReadText(root, "targetCheckoutDate"); targetDate != "" && target != "" {
		target = targetDate + " " + target
	}
	if target != "" {
		fields = append(fields, "客户希望延迟至"+target)
	}
	if len(fields) == 0 {
		return ""
	}
	return "延迟退房只读评估：" + strings.Join(fields, "，") + "。当前仅完成查询评估，尚未办理延迟退房。"
}

func runtimePMSReadMissingAspect(value string) string {
	switch value {
	case "customerLocator", "receptOrderLocator":
		return "缺少可用于定位当前订单的手机号或真实订单 ID"
	case "inventoryStartDate", "inventoryEndDate", "validInventoryDateRange":
		return "缺少完整有效的入住和离店日期，未查询无日期库存"
	case "targetRoomTypeId", "targetRoomTypeUnmatched":
		return "客户目标房型尚未与 PMS 返回的真实房型唯一匹配"
	case "targetRoomTypeAmbiguous":
		return "客户目标描述对应多个 PMS 房型，需要客户选择"
	case "targetCheckoutTime":
		return "缺少客户明确希望延迟到的退房时间"
	case "orderIdForPrice":
		return "缺少真实订单 ID，不能确认差价"
	case "order.reserve":
		return "当前预订单信息暂未确认"
	case "order.recept":
		return "当前接待单信息暂未确认"
	case "inventory.stay":
		return "目标日期库存暂未确认"
	case "member.benefits":
		return "会员权益暂未确认"
	case "member.info":
		return "会员等级和状态暂未确认"
	case "memberPhone":
		return "缺少用于查询会员信息的手机号"
	case "roomLocator":
		return "缺少用于查询当前房态的房号或订单定位信息"
	case "price.difference":
		return "最终差价暂未确认"
	case "room.status":
		return "当前房态暂未确认"
	case "stay.room_availability":
		return "完整入住区间的具体可分配房号暂未确认"
	case "renew.candidates":
		return "续住候选暂未确认"
	default:
		return value
	}
}

func firstRuntimePMSReadText(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := runtimePMSReadText(source[key]); text != "" {
			return text
		}
	}
	return ""
}

func runtimePMSReadText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func appendRuntimePMSFactField(fields *[]string, label, value string) {
	if fields == nil || strings.TrimSpace(value) == "" {
		return
	}
	*fields = append(*fields, label+value)
}

func compareRuntimePMSNumericText(left, right string) int {
	var leftValue, rightValue float64
	if _, err := fmt.Sscanf(left, "%f", &leftValue); err != nil {
		return strings.Compare(left, right)
	}
	if _, err := fmt.Sscanf(right, "%f", &rightValue); err != nil {
		return strings.Compare(left, right)
	}
	switch {
	case leftValue < rightValue:
		return -1
	case leftValue > rightValue:
		return 1
	default:
		return 0
	}
}
