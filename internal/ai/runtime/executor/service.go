package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
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
	if taskIDs := ungroundedKnowledgeReplyTaskIDs(collector.Data.Pipeline.ReplyPlan); len(taskIDs) > 0 {
		return completeUngroundedKnowledgeFallback(summary, collector, taskIDs)
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

func completeIntentDetectUnavailable(summary *RunResult, collector *callbacks.RuntimeTraceCollector) (*RunResult, error) {
	const reply = "不好意思，刚才这段我没完整理解好。麻烦把几个问题分开再发一下，我会逐个回答。"
	summary.Status = "completed"
	summary.ReplyText = reply
	summary.ModelName = collector.Data.Model.Name
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = reply
	collector.Data.Output.FinishReason = "intent_detect_safe_fallback"
	collector.Data.Pipeline.Generate.Status = "skipped"
	collector.Data.Pipeline.Generate.Reason = "IntentDetect failed after protocol repair; blocked ungrounded hotel fact generation"
	collector.Data.Pipeline.Generate.FallbackMode = "intent_detect_safe_reply"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "local safe reply contains no ungrounded hotel facts"
	summary.TraceData = collector.Marshal()
	return summary, nil
}

const ungroundedKnowledgeSafeReply = "不好意思，这个我暂时没法准确回答。"

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
		if _, blocked := ungrounded[index]; blocked {
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
	for index := range plan.TaskPlans {
		taskID, blocked := ungrounded[index]
		if !blocked {
			continue
		}
		isolatedTaskIDs = append(isolatedTaskIDs, taskID)
		plan.TaskPlans[index].TaskID = taskID
		plan.TaskPlans[index].Output = "knowledge_safe_fallback"
		plan.TaskPlans[index].SelectedLayer = "runtime_safe_fallback"
		plan.TaskPlans[index].SelectedCandidateIDs = nil
		plan.TaskPlans[index].SupportedFacts = []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID:         taskID + "FSafe",
			Aspect:         "other",
			Statement:      ungroundedKnowledgeSafeReply,
			CriticalValues: []string{"暂时没法准确回答"},
		}}
		plan.TaskPlans[index].MissingAspects = appendIfMissing(plan.TaskPlans[index].MissingAspects, "缺少可核验的知识证据")
	}
	plan.ReplyRequiredTaskCount = countReplyRequiredTasks(plan.TaskPlans)
	plan.DoNot = appendIfMissing(plan.DoNot, "标记为知识安全兜底的任务只能表达无法准确回答，不得补充任何酒店事实")
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
	hasFact := false
	for _, fact := range task.SupportedFacts {
		if strings.TrimSpace(fact.Statement) != "" {
			hasFact = true
			break
		}
	}
	return strings.TrimSpace(task.SelectedLayer) == "" || !hasFact
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
		}
	}
	summary.ReplyText = strings.TrimSpace(strings.Join(nonEmptyStrings(textParts), "\n"))
	return hasStructuredCommit || strings.TrimSpace(summary.ReplyText) != ""
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
