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
	"agent-desk/internal/pms"
)

var (
	runtimePMSExplicitDatePattern = regexp.MustCompile(`(?P<year>20[0-9]{2})[-/.年](?P<month>1[0-2]|0?[1-9])[-/.月](?P<day>3[01]|[12][0-9]|0?[1-9])日?`)
	runtimePMSMonthDayPattern     = regexp.MustCompile(`(?P<month>1[0-2]|0?[1-9])月(?P<day>3[01]|[12][0-9]|0?[1-9])日?`)
	runtimePMSDayOnlyPattern      = regexp.MustCompile(`(?P<day>3[01]|[12][0-9]|0?[1-9])(?:日|号)`)
	runtimePMSTargetRoomPattern   = regexp.MustCompile(`(?:升级|升房|换房|换|改)(?:到|成|为)?\s*([^，。！？,.!?\n]{1,12}房)`)
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
	sessionLocator := runtimePMSSessionLocatorFromHistory(history)
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
		resolveRuntimePMSKnowledgeHandoffForTask(req, task, replyPlan, summary, collector)
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

func resolveRuntimePMSKnowledgeHandoffForTask(req RunInput, task *callbacks.ReplyTaskPlanTraceData, plan callbacks.ReplyPlanTraceData, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
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
	if runtimeReplyTaskHasPMSFact(*task) {
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
		HasAnswer:      originalDisposition == runtimeKnowledgeDispositionAnswerThenHandoff,
		NeedsHandoff:   true,
		MissingAspects: append([]string(nil), taskTrace.MissingAspects...),
		HandoffHit:     rag.RetrieveResult{Content: "转人工"},
	}
	if !runtimeKnowledgeAutoHandoffEnabledForCollector(req.Conversation.ID, []runtimeKnowledgeQuestionDisposition{pending}, collector) {
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
	if originalDisposition == runtimeKnowledgeDispositionDirectHandoff {
		task.Output = runtimeKnowledgeDeferredHandoffOutput
		task.OutputKind = "handoff"
		task.ReplyRequired = false
		if summary != nil && !runtimePMSReplyPlanHasAnswerableSibling(plan, taskID) {
			summary.handoffDirective = true
			summary.handoffDirectiveReason = deferredRuntimeKnowledgeHandoffReason([]runtimeKnowledgeQuestionDisposition{pending})
			summary.handoffDirectiveSource = "knowledge_top_answer"
		}
	} else {
		task.Output = "knowledge_text_reply"
		task.OutputKind = "text"
		task.ReplyRequired = true
	}
	collector.SetKnowledgeEvidenceJudge(trace)
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
	return "PMS 只读事实已经由服务端查询并写入当前任务的已确认事实。Generate 只负责按客户问题整理这些事实，不得再次调用 pms_query，不得补全尚未确认方面，也不得把可售、可选或评估结果说成已经锁房、换房、升房、续住、延退或完成收费。"
}

func runtimePMSReadPlanInputForTask(task callbacks.ReplyTaskPlanTraceData, sessionLocator runtimePMSSessionLocator, now time.Time) pmsReadPlanInput {
	text := strings.TrimSpace(strings.Join([]string{task.OriginalText, task.Text, task.ResolvedText}, "\n"))
	phone := runtimePMSLastUsableCustomerPhone(text)
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
	input.ReserveOrderID, input.ReceptOrderID, input.CustomerNo = runtimePMSOrderLocators(text)
	if input.ReserveOrderID == "" && input.ReceptOrderID == "" && input.CustomerNo == "" {
		reserveID, receptID, customerNo := runtimePMSOrderLocators(sessionLocator.OrderLocator)
		input.ReserveOrderID = reserveID
		input.ReceptOrderID = receptID
		input.CustomerNo = customerNo
	}
	input.StartDate, input.EndDate = runtimePMSReadDates(task, input.Scenario, now)
	return input
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
	if match := runtimePMSTargetRoomPattern.FindStringSubmatch(combined); len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
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
	order := pmsReadStepResult{}
	for _, stepID := range []string{"order.recept", "order.reserve"} {
		candidate, ok := results[stepID]
		if ok && (candidate.Status == pmsReadStepOK || candidate.Status == pmsReadStepPartial) {
			order = candidate
			break
		}
	}
	inventory, ok := results["inventory.stay"]
	if order.Data == nil || !ok || (inventory.Status != pmsReadStepOK && inventory.Status != pmsReadStepPartial) {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "订单或目标日期库存尚未确认，不能计算差价"}
	}
	startDate := firstNonEmpty(args["beginTime"], inventory.Args["beginTime"])
	endDate := firstNonEmpty(args["endTime"], inventory.Args["endTime"])
	assessment, err := pms.AssessPriceDifference(order.Data, inventory.Data, args["roomTypeId"], startDate, endDate)
	if err != nil {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "PMS 差价评估暂时不可用"}
	}
	return pmsReadStepResult{Status: pmsReadStepOK, Data: map[string]any{"assessment": runtimePMSJSONValue(assessment)}}
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
	missingByRoom := make([]string, 0)
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
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
		"renew_candidates", "room_status", "inventory",
		"member_info_by_phone", "member_benefits_by_phone", "price_difference":
		return true
	default:
		return false
	}
}

func resolveRuntimePMSReadStepArgs(step pmsReadPlanStep, results map[string]pmsReadStepResult) (map[string]string, pmsReadStepStatus, string) {
	args := clonePMSReadArgs(step.Args)
	for _, binding := range step.Bindings {
		values := make([]string, 0, 2)
		for _, source := range binding.Sources {
			result, ok := results[source.StepID]
			if !ok || (result.Status != pmsReadStepOK && result.Status != pmsReadStepPartial) {
				continue
			}
			for _, field := range source.Fields {
				values = append(values, runtimePMSReadPathStrings(result.Data, field)...)
			}
		}
		values = uniquePMSReadStrings(values)
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
			object, ok := value.(map[string]any)
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
	containedCandidates := make([]candidate, 0)
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
		} else if strings.Contains(normalizedTarget, normalizedName) {
			containedCandidates = append(containedCandidates, candidate{id: id, name: name})
		}
	}
	candidates := exactCandidates
	if len(candidates) == 0 && len(containedCandidates) > 0 {
		longest := 0
		for _, item := range containedCandidates {
			if length := len([]rune(normalizeRuntimePMSRoomTypeText(item.name))); length > longest {
				longest = length
			}
		}
		for _, item := range containedCandidates {
			if len([]rune(normalizeRuntimePMSRoomTypeText(item.name))) == longest {
				candidates = append(candidates, item)
			}
		}
	}
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
	return b.String()
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
	factIndex := 0
	for _, step := range result.Steps {
		if step.Status != pmsReadStepOK && step.Status != pmsReadStepPartial {
			continue
		}
		statement := runtimePMSReadFactStatement(plan, step)
		if statement == "" {
			continue
		}
		factIndex++
		task.SupportedFacts = append(task.SupportedFacts, callbacks.KnowledgeEvidenceFactTraceData{
			FactID:    fmt.Sprintf("P%dF%d", taskIndex+1, factIndex),
			Aspect:    "pms_" + strings.ReplaceAll(step.StepID, ".", "_"),
			Statement: statement,
		})
	}
	for _, missing := range result.Unconfirmed {
		task.MissingAspects = appendIfMissing(task.MissingAspects, runtimePMSReadMissingAspect(missing))
	}
	for _, step := range result.Steps {
		if step.Status == pmsReadStepOK || step.Status == pmsReadStepPartial {
			continue
		}
		message := strings.TrimSpace(step.Message)
		if message == "" {
			message = runtimePMSReadMissingAspect(step.StepID)
		}
		task.MissingAspects = appendIfMissing(task.MissingAspects, message)
	}
}

func runtimePMSReadFactStatement(plan pmsReadPlan, step pmsReadStepResult) string {
	switch step.StepID {
	case "order.reserve", "order.recept":
		return runtimePMSOrderFact(step.Data)
	case "inventory.stay":
		return runtimePMSInventoryFact(step)
	case "member.benefits":
		return runtimePMSMemberFact(step.Data)
	case "member.info":
		return runtimePMSMemberFact(step.Data)
	case "price.difference":
		return runtimePMSPriceFact(step.Data)
	case "room.status":
		return runtimePMSRoomStatusFact(step.Data)
	case "renew.candidates":
		return runtimePMSRenewCandidateFact(step.Data)
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
	parts := make([]string, 0, len(orders))
	for _, order := range orders {
		fields := make([]string, 0, 6)
		appendRuntimePMSFactField(&fields, "房型", strings.Join(runtimePMSOrderRoomNames(order), "/"))
		appendRuntimePMSFactField(&fields, "房号", firstRuntimePMSReadText(order, "homeName"))
		appendRuntimePMSFactField(&fields, "入住", firstRuntimePMSReadText(order, "checkInTime", "checkInBusinessDate"))
		appendRuntimePMSFactField(&fields, "离店", firstRuntimePMSReadText(order, "checkOutTime", "checkOutBusinessDate"))
		appendRuntimePMSFactField(&fields, "状态", firstRuntimePMSReadText(order, "orderStatus", "reserveStatus"))
		appendRuntimePMSFactField(&fields, "金额", firstRuntimePMSReadText(order, "payableAmount", "roomFee", "payAmount", "waitPayAmount"))
		if len(fields) > 0 {
			parts = append(parts, strings.Join(fields, "，"))
		}
	}
	if len(parts) == 0 {
		return "已查询到当前有效订单，但返回字段不足以确认客户所问详情。"
	}
	return "PMS 当前有效订单：" + strings.Join(parts, "；") + "。"
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
	items, ok := step.Data.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(items), 6))
	expected, _ := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
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
	if step.Status == pmsReadStepPartial {
		prefix = "PMS 仅返回部分日期的房型库存，不能确认整个入住区间："
	}
	return prefix + strings.Join(parts, "；") + "。库存是查询时结果，不代表已经锁房。"
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
	benefits := runtimePMSMemberBenefitTexts(grade)
	if len(benefits) > 0 {
		fields = append(fields, "权益"+strings.Join(benefits, "、"))
	}
	if len(fields) == 0 {
		return ""
	}
	return "PMS 会员信息：" + strings.Join(fields, "，") + "。权益配置不代表已办理升房或减免费用。"
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
	parts := make([]string, 0, 4)
	if availability != "" {
		parts = append(parts, "目标房型库存状态"+availability)
	}
	if status == "exact" && difference != "" {
		parts = append(parts, "按相同逐日计价口径计算的差价为"+difference+currency)
	} else if reason != "" {
		parts = append(parts, reason)
	}
	if len(parts) == 0 {
		return ""
	}
	return "PMS 只读差价评估：" + strings.Join(parts, "，") + "。该结果仅为查询评估，尚未办理。"
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
