package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type Service struct {
	agentFactory      *factory.AgentFactory
	runnerFactory     *factory.RunnerFactory
	answerabilityGate *KnowledgeAnswerabilityGate
}

func NewService() *Service {
	return &Service{
		agentFactory:      factory.NewAgentFactory(),
		runnerFactory:     factory.NewRunnerFactory(),
		answerabilityGate: NewKnowledgeAnswerabilityGate(),
	}
}

func (s *Service) ExecuteRun(ctx context.Context, req RunInput) (*RunResult, error) {
	summary := &RunResult{
		RunID:            uuid.NewString(),
		Status:           "started",
		ToolCodes:        make([]string, 0),
		InvokedToolCodes: make([]string, 0),
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.RunID = summary.RunID
	summary.ModelName = req.AIConfig.ModelName
	collector.Data.Model.Provider = string(req.AIConfig.Provider)
	collector.Data.Model.Name = req.AIConfig.ModelName

	checkPointID := resolveCheckPointID(req.CheckPointID, summary.RunID)
	summary.CheckPointID = checkPointID
	messages := buildRunMessages(ctx, req, summary, collector, s.answerabilityGate)
	if collector.Data.Error.Stage == "question_coverage" {
		return completeGeneratedReplyProtocolFailure(summary, collector, fmt.Errorf("question coverage failed: %s", collector.Data.Error.Message), "question_coverage")
	}
	if collector.Data.Pipeline.Intent.DetectedIntent == "intent_detect_unavailable" {
		return completeIntentDetectUnavailable(summary, collector)
	}
	if summary.SkipReply {
		summary.Status = "completed"
		summary.ModelName = req.AIConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.FinishReason = "no_reply"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "intent selected no reply"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "intent policy selected no reply"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
	if handled, err := executeRuntimeHandoffDirective(req, summary, collector); handled || err != nil {
		return completeRuntimeHandoffDirective(summary, collector, err, false)
	}
	deferredIntentHumanRoute := deferMixedExplicitIntentHumanRoute(req, collector)
	if !deferredIntentHumanRoute {
		if handled, err := executeIntentHumanRoute(ctx, req, summary, collector); handled || err != nil {
			if err != nil {
				summary.Status = "error"
				summary.ErrorMessage = err.Error()
				collector.Data.Status = summary.Status
				collector.Data.Error.Message = err.Error()
				collector.Data.Error.Stage = "tool_knowledge"
				summary.TraceData = collector.Marshal()
				return summary, err
			}
			summary.Status = "completed"
			summary.ModelName = req.AIConfig.ModelName
			collector.Data.Status = summary.Status
			if isEmergencySafetyHandoff(collector.Data.Pipeline.Intent) && summary.handoffDispatchStatus == string(services.HandoffDispatchStatusDispatched) {
				collector.Data.Output.FinishReason = "intent_emergency_human_route_dispatched"
				collector.Data.Pipeline.Generate.Status = "skipped"
				collector.Data.Pipeline.Generate.Reason = "intent stage dispatched emergency safety directly to human reception"
				collector.Data.Pipeline.Validate.Status = "passed"
				collector.Data.Pipeline.Validate.Reason = "emergency safety route dispatched directly"
				summary.TraceData = collector.Marshal()
				return summary, nil
			}
			finishReason, generateReason, validateReason := handoffCompletionMetadata("intent_human_route", summary.handoffDispatchStatus)
			collector.Data.Output.FinishReason = finishReason
			collector.Data.Pipeline.Generate.Status = "skipped"
			collector.Data.Pipeline.Generate.Reason = generateReason
			collector.Data.Pipeline.Validate.Status = "passed"
			collector.Data.Pipeline.Validate.Reason = validateReason
			summary.TraceData = collector.Marshal()
			return summary, nil
		}
	}
	if prepareHotelVariableDirectCommit(req, summary, collector) {
		summary.Status = "completed"
		summary.ModelName = req.AIConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.ReplyText = summary.ReplyText
		collector.Data.Output.FinishReason = "hotel_variable_direct_commit"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "resource-only hotel variable request is committed by structured resource sender"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "direct hotel variable commit prepared"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
	if prepareDeterministicClarificationDirectCommit(summary, collector) {
		summary.Status = "completed"
		summary.ModelName = req.AIConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.ReplyText = summary.ReplyText
		collector.Data.Output.FinishReason = "clarification_direct_commit"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "the runtime already produced the exact customer clarification"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "deterministic clarification passed protocol and send-safety validation"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
	if taskIDs := ungroundedKnowledgeReplyTaskIDs(collector.Data.Pipeline.ReplyPlan); len(taskIDs) > 0 {
		return completeUngroundedKnowledgeFallback(summary, collector, taskIDs)
	}
	if prepareGroundedPMSDirectCommit(summary, collector) {
		summary.Status = "completed"
		summary.ModelName = req.AIConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.ReplyText = summary.ReplyText
		collector.Data.Output.FinishReason = "grounded_pms_direct_commit"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "all reply tasks have customer-safe answers grounded by structured PMS or Judge evidence"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "structured PMS answer passed protocol and send-safety validation"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
	if prepareGroundedIndependentKnowledgeDirectCommit(summary, collector) {
		summary.Status = "completed"
		summary.ModelName = req.AIConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.ReplyText = summary.ReplyText
		collector.Data.Output.FinishReason = "grounded_knowledge_direct_commit"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "knowledge tasks already have complete Judge-grounded customer answers, including resolved follow-ups"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "Judge-grounded answer passed protocol and send-safety validation"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}

	toolDefs, err := factory.NewToolFactory().BuildMCPTools(req.AIAgent)
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "prepare"
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	tooling := prepareGenerateToolingForIntent(toolDefs, req.ToolSet, collector.Data.Pipeline.Intent, factory.HasVisibleSkills(req.AIAgent))
	summary.ToolCodes = append(summary.ToolCodes, tooling.toolCodes...)
	collector.Data.Input.ToolCodes = append(collector.Data.Input.ToolCodes, summary.ToolCodes...)
	collector.SetTooling(tooling.staticToolCodes, definitionToolCodes(tooling.definitions), len(tooling.definitions) > 0)

	collector.Data.Interrupt.CheckPointID = checkPointID
	generateAIConfig := generatedReplyAIConfigForPlan(req.AIConfig, collector.Data.Pipeline.ReplyPlan)
	generateStartedAt := time.Now()
	_, consumeErr := runGeneratedReplyWithRecovery(
		ctx,
		messages,
		summary,
		collector,
		func() bool { return canContinueGeneratedReply(req) },
		func(attemptCtx context.Context, attemptMessages []*schema.Message) error {
			agent, buildErr := s.agentFactory.BuildCustomerServiceAgent(attemptCtx, factory.BuildCustomerServiceAgentInput{
				AIAgent:                    req.AIAgent,
				AIConfig:                   generateAIConfig,
				InstructionToolDefinitions: tooling.definitions,
				DynamicMCPToolDefinitions:  tooling.definitions,
				StaticTools:                tooling.staticTools,
				StaticToolCodes:            tooling.staticToolCodeMap,
				StaticToolMetadata:         tooling.staticToolMetadata,
				Collector:                  collector,
			})
			if buildErr != nil {
				return fmt.Errorf("%w: %v", ErrGeneratedReplyExecution, buildErr)
			}
			runner := s.runnerFactory.Build(attemptCtx, agent, false, true)
			if runner == nil {
				return fmt.Errorf("%w: failed to build runner", ErrGeneratedReplyExecution)
			}
			return consumeAgentEvents(attemptCtx, runner.Run(attemptCtx, attemptMessages, buildRunOptions(checkPointID)...), summary, collector, tooling.toolDefsByModelName)
		},
	)
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(generateStartedAt).Milliseconds()
	summary.ModelName = req.AIConfig.ModelName
	if consumeErr != nil {
		return completeGeneratedReplyProtocolFailure(summary, collector, consumeErr, "generate")
	}
	validation := enforceGeneratedReplyActionLedger(summary, collector)
	if validation.RequestHandoffConfirmation {
		summary.handoffDirective = true
		summary.handoffDirectiveReason = validation.HandoffReason
		summary.handoffDirectiveSource = "generated_reply_guard"
		ledger := collector.Data.ActionLedger
		ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
			Action: "human_route",
			Status: "requested",
			Reason: validation.HandoffReason,
		})
		collector.SetActionLedger(ledger)
		if handled, err := executeRuntimeHandoffDirective(req, summary, collector); handled || err != nil {
			return completeRuntimeHandoffDirective(summary, collector, err, true)
		}
		summary.ReplyText = "这个问题我目前还没有足够准确的资料，不能直接承诺已经安排同事处理。"
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(
			collector.Data.Pipeline.Validate.Reason,
			"automatic handoff is disabled, so the unsupported promise was replaced with a non-action reply",
		)
	}
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	if strings.TrimSpace(collector.Data.Output.FinishReason) == "" {
		collector.Data.Output.FinishReason = summary.Status
	}
	collector.Data.Pipeline.Generate.Status = summary.Status
	if strings.TrimSpace(summary.ReplyText) != "" {
		if strings.TrimSpace(collector.Data.Pipeline.Generate.Reason) == "" {
			collector.Data.Pipeline.Generate.Reason = "model generated reply from staged prompt and layered context"
		}
		if collector.Data.Pipeline.Validate.Status != "failed" && strings.TrimSpace(collector.Data.Pipeline.Validate.Status) == "" {
			collector.Data.Pipeline.Validate.Status = "passed"
			collector.Data.Pipeline.Validate.Reason = "runtime completed"
		}
	} else if summary.Status == "error" {
		collector.Data.Pipeline.Generate.Reason = summary.ErrorMessage
		collector.Data.Pipeline.Validate.Status = "failed"
		collector.Data.Pipeline.Validate.Reason = summary.ErrorMessage
	}
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func prepareDeterministicClarificationDirectCommit(summary *RunResult, collector *callbacks.RuntimeTraceCollector) bool {
	if summary == nil || collector == nil {
		return false
	}
	intent := collector.Data.Pipeline.Intent
	if intent.NeedsKnowledge || intent.NeedsTool || intent.NeedsResource || intent.NeedsHumanRoute || len(intent.ResourceActions) > 0 {
		return false
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) == 0 {
		return false
	}
	parts := make([]string, 0, len(plan.TaskPlans))
	for _, task := range plan.TaskPlans {
		if !task.ReplyRequired || task.NeedsKnowledge || task.NeedsTool || task.NeedsResource || task.NeedsHumanRoute ||
			strings.TrimSpace(task.OutputKind) != "text" || len(task.SupportedFacts) > 0 {
			return false
		}
		reply := strings.TrimSpace(task.ResolvedText)
		if reply != runtimePMSOrderPhoneClarification && reply != runtimePMSMemberPhoneClarification {
			return false
		}
		cleaned, err := SanitizeGeneratedReplyText(reply)
		if err != nil || strings.TrimSpace(cleaned) == "" {
			return false
		}
		parts = append(parts, cleaned)
	}
	summary.ReplyText = composeGeneratedReplyContents(parts, 3)
	return strings.TrimSpace(summary.ReplyText) != ""
}

func completeIntentDetectUnavailable(summary *RunResult, collector *callbacks.RuntimeTraceCollector) (*RunResult, error) {
	const reply = "不好意思，刚才没能处理好，麻烦再发一次。"
	summary.Status = "completed"
	summary.ReplyText = reply
	summary.ModelName = collector.Data.Model.Name
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = reply
	collector.Data.Output.FinishReason = "intent_detect_safe_fallback"
	collector.Data.Pipeline.Generate.Status = "skipped"
	collector.Data.Pipeline.Generate.Reason = "IntentDetect unavailable after model request or protocol recovery; blocked ungrounded hotel fact generation"
	collector.Data.Pipeline.Generate.FallbackMode = "intent_detect_safe_reply"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "local safe reply contains no ungrounded hotel facts"
	summary.TraceData = collector.Marshal()
	return summary, nil
}

const (
	ungroundedKnowledgeSafeReply             = "不好意思，这个我暂时没法准确回答。"
	ungroundedMaintenanceOfferReply          = "这个需要门店同事处理。需要的话，我可以帮您登记维修工单。"
	ungroundedMaintenanceOfferNoHandoffReply = "这个需要门店同事处理，按您的要求先不转接。需要的话，我可以帮您登记维修工单。"
)

func isolateUngroundedKnowledgeReplyTasks(plan callbacks.ReplyPlanTraceData) (callbacks.ReplyPlanTraceData, []string) {
	ungrounded := make(map[int]string)
	for index, task := range plan.TaskPlans {
		if !isUngroundedKnowledgeReplyTask(task) {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		ungrounded[index] = taskID
	}
	if len(ungrounded) == 0 {
		return plan, nil
	}

	hasExecutableSibling := false
	for index, task := range plan.TaskPlans {
		if _, blocked := ungrounded[index]; blocked && !runtimeReplyTaskHasPMSFact(task) {
			continue
		}
		if runtimeReplyTaskIsExecutable(task) {
			hasExecutableSibling = true
			break
		}
	}
	if !hasExecutableSibling {
		return plan, nil
	}

	plan.TaskPlans = append([]callbacks.ReplyTaskPlanTraceData(nil), plan.TaskPlans...)
	isolatedTaskIDs := make([]string, 0, len(ungrounded))
	hasMaintenanceFallback := false
	for index := range plan.TaskPlans {
		taskID, blocked := ungrounded[index]
		if !blocked {
			continue
		}
		fallbackReply := ungroundedKnowledgeSafeReply
		fallbackAspect := "other"
		if serviceReply, ok := ungroundedMaintenanceServiceReply(plan.TaskPlans[index], ""); ok {
			fallbackReply = serviceReply
			fallbackAspect = "service_resolution"
			hasMaintenanceFallback = true
		}
		isolatedTaskIDs = append(isolatedTaskIDs, taskID)
		plan.TaskPlans[index].TaskID = taskID
		preservedFacts := runtimeReplyTaskPMSFacts(plan.TaskPlans[index])
		plan.TaskPlans[index].Output = "knowledge_safe_fallback"
		if len(preservedFacts) > 0 {
			plan.TaskPlans[index].Output = "knowledge_partial_safe_fallback"
		}
		plan.TaskPlans[index].SelectedLayer = "runtime_safe_fallback"
		plan.TaskPlans[index].SelectedCandidateIDs = nil
		plan.TaskPlans[index].SupportedFacts = append(preservedFacts, callbacks.KnowledgeEvidenceFactTraceData{
			FactID:         taskID + "FSafe",
			Aspect:         fallbackAspect,
			Statement:      fallbackReply,
			CriticalValues: fallbackCriticalValues(fallbackReply),
		})
		plan.TaskPlans[index].MissingAspects = appendIfMissing(plan.TaskPlans[index].MissingAspects, "缺少可核验的知识证据")
	}
	plan.ReplyRequiredTaskCount = countReplyRequiredTasks(plan.TaskPlans)
	if hasMaintenanceFallback {
		plan.DoNot = appendIfMissing(plan.DoNot, "维修故障任务只能使用 runtime_safe_fallback 给出的固定处理说明和维修工单选择，不得声称已建单或已转人工；其他未获得知识证据的事实只能表达暂时无法准确回答")
	} else {
		plan.DoNot = appendIfMissing(plan.DoNot, "知识未获得证据的部分只能表达暂时无法准确回答；同一任务中已确认的 PMS 事实仍须正常回答，不得补充未确认的酒店政策")
	}
	return plan, isolatedTaskIDs
}

func ungroundedKnowledgeReplyTaskIDs(plan callbacks.ReplyPlanTraceData) []string {
	ret := make([]string, 0)
	for index, task := range plan.TaskPlans {
		if !isUngroundedKnowledgeReplyTask(task) {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		ret = append(ret, taskID)
	}
	return ret
}

func isUngroundedKnowledgeReplyTask(task callbacks.ReplyTaskPlanTraceData) bool {
	if !isReplyRequiredTextTask(task) || !runtimeReplyTaskUsesKnowledge(task) {
		return false
	}
	hasKnowledgeFact := false
	for _, fact := range task.SupportedFacts {
		if strings.TrimSpace(fact.Statement) != "" && !strings.HasPrefix(strings.TrimSpace(fact.Aspect), "pms_") {
			hasKnowledgeFact = true
		}
	}
	return strings.TrimSpace(task.SelectedLayer) == "" || !hasKnowledgeFact
}

func runtimeReplyTaskHasPMSFact(task callbacks.ReplyTaskPlanTraceData) bool {
	return len(runtimeReplyTaskPMSFacts(task)) > 0
}

func runtimeReplyTaskPMSFacts(task callbacks.ReplyTaskPlanTraceData) []callbacks.KnowledgeEvidenceFactTraceData {
	ret := make([]callbacks.KnowledgeEvidenceFactTraceData, 0)
	for _, fact := range task.SupportedFacts {
		if strings.HasPrefix(strings.TrimSpace(fact.Aspect), "pms_") && strings.TrimSpace(fact.Statement) != "" {
			ret = append(ret, fact)
		}
	}
	return ret
}

func runtimeReplyTaskIsExecutable(task callbacks.ReplyTaskPlanTraceData) bool {
	if isReplyRequiredTextTask(task) {
		return true
	}
	switch strings.TrimSpace(task.OutputKind) {
	case "resource", "handoff":
		return true
	case "context_only":
		return false
	}
	return strings.TrimSpace(task.Output) != "" && strings.TrimSpace(task.Output) != "context_only"
}

func completeUngroundedKnowledgeFallback(summary *RunResult, collector *callbacks.RuntimeTraceCollector, taskIDs []string) (*RunResult, error) {
	plan, serviceFallbackApplied := applyUngroundedMaintenanceServiceFallbacks(
		collector.Data.Pipeline.ReplyPlan,
		taskIDs,
		collector.Data.Pipeline.Normalize.CurrentUserText,
	)
	if serviceFallbackApplied {
		collector.SetReplyPlan(plan)
	}
	reply := strings.TrimSpace(deterministicGeneratedReplyFallback(collector))
	if reply == "" {
		reply = ungroundedKnowledgeSafeReply
	}
	summary.Status = "completed"
	summary.ReplyText = reply
	summary.ModelName = collector.Data.Model.Name
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = reply
	collector.Data.Output.FinishReason = "knowledge_evidence_safe_fallback"
	collector.Data.Pipeline.Generate.Status = "skipped"
	collector.Data.Pipeline.Generate.Reason = "knowledge task lacked selected grounded evidence: " + strings.Join(taskIDs, ",")
	collector.Data.Pipeline.Generate.FallbackMode = "deterministic_knowledge_evidence_guard"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "ungrounded knowledge tasks were blocked before free generation"
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func applyUngroundedMaintenanceServiceFallbacks(plan callbacks.ReplyPlanTraceData, taskIDs []string, currentText string) (callbacks.ReplyPlanTraceData, bool) {
	requested := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		if taskID = strings.TrimSpace(taskID); taskID != "" {
			requested[taskID] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return plan, false
	}

	updated := false
	plan.TaskPlans = append([]callbacks.ReplyTaskPlanTraceData(nil), plan.TaskPlans...)
	for index := range plan.TaskPlans {
		taskID := strings.TrimSpace(plan.TaskPlans[index].TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		if _, ok := requested[taskID]; !ok {
			continue
		}
		reply, ok := ungroundedMaintenanceServiceReply(plan.TaskPlans[index], currentText)
		if !ok {
			continue
		}
		preservedFacts := runtimeReplyTaskPMSFacts(plan.TaskPlans[index])
		plan.TaskPlans[index].TaskID = taskID
		plan.TaskPlans[index].Output = "knowledge_safe_fallback"
		if len(preservedFacts) > 0 {
			plan.TaskPlans[index].Output = "knowledge_partial_safe_fallback"
		}
		plan.TaskPlans[index].SelectedLayer = "runtime_safe_fallback"
		plan.TaskPlans[index].SelectedCandidateIDs = nil
		plan.TaskPlans[index].SupportedFacts = append(preservedFacts, callbacks.KnowledgeEvidenceFactTraceData{
			FactID:         taskID + "FSafe",
			Aspect:         "service_resolution",
			Statement:      reply,
			CriticalValues: fallbackCriticalValues(reply),
		})
		plan.TaskPlans[index].MissingAspects = appendIfMissing(plan.TaskPlans[index].MissingAspects, "缺少可核验的维修知识证据")
		updated = true
	}
	return plan, updated
}

func ungroundedMaintenanceServiceReply(task callbacks.ReplyTaskPlanTraceData, currentText string) (string, bool) {
	if strings.TrimSpace(task.Intent) != "service_request" || strings.TrimSpace(task.SubIntent) == "create_ticket" {
		return "", false
	}

	subIntent := strings.TrimSpace(task.SubIntent)
	taskText := strings.TrimSpace(strings.Join([]string{task.OriginalText, task.ResolvedText, task.Text}, "\n"))
	isMaintenance := subIntent == "maintenance" ||
		strings.Contains(subIntent, "repair") ||
		subIntent == "hvac_issue" ||
		subIntent == "ac_not_cooling" ||
		knowledgeEvidenceServiceOperationTarget(taskText) == "malfunction"
	if !isMaintenance {
		return "", false
	}

	handoffText := strings.TrimSpace(strings.Join([]string{taskText, currentText}, "\n"))
	if utils.IsExplicitHumanHandoffRejection(handoffText) {
		return ungroundedMaintenanceOfferNoHandoffReply, true
	}
	return ungroundedMaintenanceOfferReply, true
}

func fallbackCriticalValues(reply string) []string {
	if reply == ungroundedMaintenanceOfferNoHandoffReply {
		return []string{"门店同事处理", "先不转接", "维修工单"}
	}
	if reply == ungroundedMaintenanceOfferReply {
		return []string{"门店同事处理", "维修工单"}
	}
	return []string{"暂时没法准确回答"}
}

func completeRuntimeHandoffDirective(summary *RunResult, collector *callbacks.RuntimeTraceCollector, err error, afterGenerate bool) (*RunResult, error) {
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "human_route_dispatch"
		collector.Data.Pipeline.Validate.Status = "failed"
		collector.Data.Pipeline.Validate.Reason = err.Error()
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	summary.Status = "completed"
	summary.ModelName = collector.Data.Model.Name
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = ""
	finishReason, generateReason, validateReason := handoffCompletionMetadata("handoff_directive", summary.handoffDispatchStatus)
	collector.Data.Output.FinishReason = finishReason
	if afterGenerate {
		collector.Data.Pipeline.Generate.Status = "completed"
		collector.Data.Pipeline.Generate.Reason = "generated reply was replaced by the direct handoff flow: " + generateReason
	} else {
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = generateReason
	}
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = appendValidationReason(
		collector.Data.Pipeline.Validate.Reason,
		validateReason,
	)
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func handoffCompletionMetadata(prefix string, status string) (string, string, string) {
	switch services.HandoffDispatchStatus(status) {
	case services.HandoffDispatchStatusAwaitingRoomNumber:
		return prefix + "_awaiting_room_number",
			"room number was requested before direct human route",
			"required room number collection is active"
	case services.HandoffDispatchStatusDispatched:
		return prefix + "_dispatched",
			"human route was dispatched directly",
			"human route dispatch completed"
	case services.HandoffDispatchStatusAlreadyActive:
		return prefix + "_already_active",
			"human route was already active",
			"existing human route remains active"
	case services.HandoffDispatchStatusOffHours:
		return prefix + "_off_hours",
			"human route was unavailable outside service hours",
			"off-hours handling completed without a success claim"
	default:
		return prefix + "_unknown",
			"human route flow completed with an unknown status",
			"human route result requires inspection"
	}
}

func prepareHotelVariableDirectCommit(req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) bool {
	if summary == nil || collector == nil {
		return false
	}
	intent := collector.Data.Pipeline.Intent
	if !intent.NeedsResource && len(intent.ResourceActions) == 0 {
		return false
	}
	deferredHandoff := collector.Data.Pipeline.EvidenceJudge.DeferredHandoff
	knowledgeOnlyDeferred := deferredHandoff && !runtimeReplyPlanRequiresGeneratedText(collector.Data.Pipeline.ReplyPlan)
	if (intent.NeedsKnowledge && !knowledgeOnlyDeferred) || intent.NeedsTool || (intent.NeedsHumanRoute && !deferredHandoff) {
		return false
	}
	resourceTypes := requestedHotelVariableResourceTypes(req.UserMessage.Content, intent)
	if len(resourceTypes) == 0 {
		return false
	}
	instance := findRuntimeWxWorkInstance(req)
	textParts := make([]string, 0, len(resourceTypes))
	hasStructuredCommit := false
	for _, resourceType := range resourceTypes {
		switch resourceType {
		case "location":
			if canCommitStructuredLocation(instance) {
				hasStructuredCommit = true
			} else {
				textParts = append(textParts, buildLocationDirectReply(instance))
			}
		case "mini_program":
			if canCommitStructuredMiniProgram(instance) {
				hasStructuredCommit = true
			} else {
				textParts = append(textParts, buildMiniProgramDirectReply(instance))
			}
		case "phone":
			if canCommitStructuredPhone(instance) {
				hasStructuredCommit = true
			} else {
				textParts = append(textParts, buildPhoneDirectReply(instance))
			}
		case "pillow_product":
			hasStructuredCommit = true
		}
	}
	summary.ReplyText = strings.TrimSpace(strings.Join(nonEmptyStrings(textParts), "\n<<NEXT_MESSAGE>>\n"))
	return hasStructuredCommit || strings.TrimSpace(summary.ReplyText) != ""
}

func prepareGroundedPMSDirectCommit(summary *RunResult, collector *callbacks.RuntimeTraceCollector) bool {
	if summary == nil || collector == nil {
		return false
	}
	intent := collector.Data.Pipeline.Intent
	if intent.NeedsTool || intent.NeedsResource || intent.NeedsHumanRoute || len(intent.ResourceActions) > 0 {
		return false
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) == 0 {
		return false
	}
	hasPMSFact := false
	for _, task := range plan.TaskPlans {
		if !task.ReplyRequired || task.NeedsTool || task.NeedsResource || task.NeedsHumanRoute ||
			strings.TrimSpace(task.OutputKind) != "text" || len(task.SupportedFacts) == 0 {
			return false
		}
		pmsTask := runtimeReplyTaskHasPMSFact(task)
		if (task.AnswerText == nil || strings.TrimSpace(*task.AnswerText) == "") && deterministicPMSRoomExplanation(task) == "" {
			return false
		}
		if !pmsTask && len(task.MissingAspects) > 0 {
			return false
		}
		hasPMSFact = hasPMSFact || pmsTask
	}
	if !hasPMSFact {
		return false
	}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != len(plan.TaskPlans) {
		return false
	}
	parts := make([]string, 0, len(groups))
	for index, group := range groups {
		task := plan.TaskPlans[index]
		reply := ""
		if (task.AnswerText == nil || strings.TrimSpace(*task.AnswerText) == "") && runtimePMSTaskAsksRoomExplanation(task) {
			reply = deterministicPMSRoomExplanation(task)
		}
		if reply == "" && runtimeReplyTaskHasPMSFact(task) {
			var err error
			reply, err = SanitizeGeneratedReplyText(strings.TrimSpace(*task.AnswerText))
			if err != nil {
				return false
			}
		} else if reply == "" {
			group.EvidenceLocked = true
			var err error
			reply, err = validateLockedReplyContent(group)
			if err != nil || validateGeneratedReplyFactAspectBoundaries(reply, group.Facts) != nil {
				return false
			}
		}
		if strings.TrimSpace(reply) == "" || strings.Contains(reply, "```") ||
			json.Valid([]byte(unwrapGeneratedReplyMarkdownFence(reply))) {
			return false
		}
		parts = append(parts, strings.TrimSpace(reply))
	}
	previousOutput := collector.Data.Output
	previousValidate := collector.Data.Pipeline.Validate
	summary.ReplyText = composeGeneratedReplyContents(parts, 3)
	expectedReply := summary.ReplyText
	validation := enforceGeneratedReplyActionLedger(summary, collector)
	if validation.RequestHandoffConfirmation || summary.ReplyText != expectedReply {
		summary.ReplyText = ""
		collector.Data.Output = previousOutput
		collector.Data.Pipeline.Validate = previousValidate
		return false
	}
	return true
}

func prepareGroundedIndependentKnowledgeDirectCommit(summary *RunResult, collector *callbacks.RuntimeTraceCollector) bool {
	if summary == nil || collector == nil {
		return false
	}
	intent := collector.Data.Pipeline.Intent
	if intent.NeedsTool || intent.NeedsResource || intent.NeedsHumanRoute || len(intent.ResourceActions) > 0 {
		return false
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) == 0 {
		return false
	}
	for _, task := range plan.TaskPlans {
		externalProxy := isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective)
		if (!externalProxy && strings.TrimSpace(task.Intent) != "hotel_info") || !task.ReplyRequired || task.NeedsTool || task.NeedsResource || task.NeedsHumanRoute ||
			strings.TrimSpace(task.OutputKind) != "text" {
			return false
		}
		if !externalProxy && (!task.NeedsKnowledge || strings.TrimSpace(task.Output) != "knowledge_text_reply" || len(task.MissingAspects) > 0 ||
			task.AnswerText == nil || strings.TrimSpace(*task.AnswerText) == "" || len(task.SupportedFacts) == 0 ||
			isKnowledgeHandoffDirectiveContent(*task.AnswerText)) {
			return false
		}
		switch relation := strings.TrimSpace(task.RelationToPrevious); relation {
		case "", "independent", "follow_up", "clarification_answer", "reference_previous", "correction", "modify_previous":
		default:
			return false
		}
		switch act := strings.TrimSpace(task.DialogueAct); act {
		case "", "new_request", "follow_up", "selection", "confirmation", "correction", "recommendation":
		default:
			return false
		}
		switch state := strings.TrimSpace(task.ResolutionState); state {
		case "", "clear", runtimeIntentResolutionResolvedFromContext:
		default:
			return false
		}
		switch strategy := strings.TrimSpace(task.ReplyStrategy); strategy {
		case "", "answer_current_goal", "recommend_one_supported_option", "confirm_selection_and_continue_goal", "continue_confirmed_goal":
		default:
			return false
		}
	}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != len(plan.TaskPlans) {
		return false
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		group.EvidenceLocked = true
		reply, err := validateLockedReplyContent(group)
		if err != nil || strings.TrimSpace(reply) == "" {
			return false
		}
		trimmedReply := strings.TrimSpace(reply)
		if strings.Contains(trimmedReply, "```") || json.Valid([]byte(unwrapGeneratedReplyMarkdownFence(trimmedReply))) {
			return false
		}
		if !group.ExternalProxyAction {
			if err := validateGeneratedReplyFactAspectBoundaries(reply, group.Facts); err != nil {
				return false
			}
		}
		parts = append(parts, reply)
	}
	previousOutput := collector.Data.Output
	previousValidate := collector.Data.Pipeline.Validate
	summary.ReplyText = composeGeneratedReplyContents(parts, 3)
	expectedReply := summary.ReplyText
	validation := enforceGeneratedReplyActionLedger(summary, collector)
	if validation.RequestHandoffConfirmation || summary.ReplyText != expectedReply {
		summary.ReplyText = ""
		collector.Data.Output = previousOutput
		collector.Data.Pipeline.Validate = previousValidate
		return false
	}
	return true
}

func runtimeReplyPlanRequiresGeneratedText(plan callbacks.ReplyPlanTraceData) bool {
	for _, task := range plan.TaskPlans {
		if replyTaskRequiresText(task) {
			return true
		}
	}
	return false
}

func canCommitStructuredLocation(instance *models.WxWorkProtocolInstance) bool {
	if instance == nil {
		return false
	}
	return strings.TrimSpace(instance.StoreLongitude) != "" && strings.TrimSpace(instance.StoreLatitude) != ""
}

func canCommitStructuredMiniProgram(instance *models.WxWorkProtocolInstance) bool {
	if instance == nil {
		return false
	}
	return strings.TrimSpace(instance.DefaultMiniProgramPayload) != ""
}

func canCommitStructuredPhone(instance *models.WxWorkProtocolInstance) bool {
	if instance == nil {
		return false
	}
	return strings.TrimSpace(instance.StoreContactPhone) != ""
}

func recoverMissingMiniProgramToolResult(req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if summary == nil || collector == nil {
		return
	}
	if summary.Status != "error" || !strings.Contains(summary.ErrorMessage, "tool send_miniprogram not found") {
		return
	}
	intent := collector.Data.Pipeline.Intent
	if intent.PrimaryIntent != "hotel_variable" || intent.SubIntent != "mini_program" {
		return
	}
	reply := buildMiniProgramDirectReply(findRuntimeWxWorkInstance(req))
	if strings.TrimSpace(reply) == "" {
		return
	}
	summary.Status = "completed"
	summary.ErrorMessage = ""
	summary.ReplyText = reply
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = reply
	collector.Data.Output.FinishReason = "recovered_missing_miniprogram_tool"
	collector.Data.Error.Message = ""
	collector.Data.Error.Stage = ""
	collector.Data.Pipeline.Generate.Status = "completed"
	collector.Data.Pipeline.Generate.Reason = "recovered missing send_miniprogram tool with current account mini program variable"
}

func (s *Service) ExecuteResume(ctx context.Context, req ResumeInput) (*RunResult, error) {
	summary := &RunResult{
		RunID:            uuid.NewString(),
		Status:           "started",
		CheckPointID:     strings.TrimSpace(req.CheckPointID),
		ToolCodes:        make([]string, 0),
		InvokedToolCodes: make([]string, 0),
		Interrupts:       make([]InterruptContextSummary, 0),
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.RunID = summary.RunID
	collector.Data.Interrupt.CheckPointID = summary.CheckPointID
	if summary.CheckPointID == "" {
		summary.Status = "error"
		summary.ErrorMessage = "checkpoint id is required"
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = "resume_prepare"
		summary.TraceData = collector.Marshal()
		return summary, fmt.Errorf("%s", summary.ErrorMessage)
	}
	toolDefs, err := factory.NewToolFactory().BuildMCPTools(req.AIAgent)
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "resume_prepare"
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	hasVisibleSkills := factory.HasVisibleSkills(req.AIAgent)
	tooling := prepareTooling(toolDefs, nil, req.ToolSet, hasVisibleSkills)
	summary.ToolCodes = append(summary.ToolCodes, tooling.toolCodes...)
	collector.Data.Input.ToolCodes = append(collector.Data.Input.ToolCodes, summary.ToolCodes...)
	collector.SetTooling(tooling.staticToolCodes, definitionToolCodes(tooling.definitions), len(tooling.definitions) > 0)
	collector.Data.Model.Provider = string(req.AIConfig.Provider)
	collector.Data.Model.Name = req.AIConfig.ModelName

	resumeAIConfig := generatedReplyAIConfigForPlan(req.AIConfig, callbacks.ReplyPlanTraceData{})
	resumeData := buildResumeDataMessage(req.ResumeData)
	resumeTargets := buildResumeTargets(req.ResumeData)
	generateStartedAt := time.Now()
	_, consumeErr := runResumedGeneratedReplyWithRecovery(
		ctx,
		summary,
		collector,
		resolveResumeInterruptID(req),
		func() bool { return canContinueResumedGeneratedReply(req) },
		func(attemptCtx context.Context, _ []*schema.Message) error {
			agent, buildErr := s.agentFactory.BuildCustomerServiceAgent(attemptCtx, factory.BuildCustomerServiceAgentInput{
				AIAgent:                    req.AIAgent,
				AIConfig:                   resumeAIConfig,
				InstructionToolDefinitions: tooling.definitions,
				DynamicMCPToolDefinitions:  tooling.definitions,
				StaticTools:                tooling.staticTools,
				StaticToolCodes:            tooling.staticToolCodeMap,
				StaticToolMetadata:         tooling.staticToolMetadata,
				Collector:                  collector,
			})
			if buildErr != nil {
				return fmt.Errorf("%w: %v", ErrGeneratedReplyExecution, buildErr)
			}
			runner := s.runnerFactory.Build(attemptCtx, agent, false, true)
			if runner == nil {
				return fmt.Errorf("%w: failed to build runner", ErrGeneratedReplyExecution)
			}
			var (
				iter *adk.AsyncIterator[*adk.AgentEvent]
				err  error
			)
			if len(resumeTargets) > 0 {
				iter, err = runner.ResumeWithParams(attemptCtx, summary.CheckPointID, &adk.ResumeParams{
					Targets: resumeTargets,
				}, buildResumeOptions(summary.CheckPointID, resumeData)...)
			} else {
				iter, err = runner.Resume(attemptCtx, summary.CheckPointID, buildResumeOptions(summary.CheckPointID, resumeData)...)
			}
			if err != nil {
				return fmt.Errorf("%w: %v", ErrGeneratedReplyExecution, err)
			}
			return consumeAgentEvents(attemptCtx, iter, summary, collector, tooling.toolDefsByModelName)
		},
	)
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(generateStartedAt).Milliseconds()
	summary.ModelName = req.AIConfig.ModelName
	if consumeErr != nil {
		return completeGeneratedReplyProtocolFailure(summary, collector, consumeErr, "resume_generate")
	}
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	if strings.TrimSpace(collector.Data.Output.FinishReason) == "" {
		collector.Data.Output.FinishReason = summary.Status
	}
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func completeGeneratedReplyProtocolFailure(summary *RunResult, collector *callbacks.RuntimeTraceCollector, err error, stage string) (*RunResult, error) {
	summary.Status = "error"
	summary.ReplyText = ""
	summary.ErrorMessage = err.Error()
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = ""
	collector.Data.Output.FinishReason = "generated_reply_protocol_error"
	collector.Data.Error.Message = summary.ErrorMessage
	collector.Data.Error.Stage = stage
	collector.Data.Pipeline.Generate.Status = "error"
	collector.Data.Pipeline.Generate.Reason = summary.ErrorMessage
	collector.Data.Pipeline.Validate.Status = "failed"
	collector.Data.Pipeline.Validate.Reason = summary.ErrorMessage
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, err
}

func syncSkillSummaryFromCollector(summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if summary == nil || collector == nil {
		return
	}
	trace := collector.Data.Skill
	summary.SelectedSkillCode = strings.TrimSpace(trace.Code)
	summary.SelectedSkillName = strings.TrimSpace(trace.Name)
	summary.SkillRouteReason = strings.TrimSpace(trace.RouteReason)
	summary.SkillRouteTrace = strings.TrimSpace(trace.RouteTrace)
	summary.SkillAllowedToolCodes = append([]string(nil), trace.AllowedToolCodes...)
	if len(trace.FilteredToolCodes) > 0 {
		summary.ToolCodes = append([]string(nil), trace.FilteredToolCodes...)
	}
}
