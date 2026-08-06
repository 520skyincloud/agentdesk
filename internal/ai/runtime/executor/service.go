package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	svc "agent-desk/internal/services"

	"github.com/cloudwego/eino/adk"
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
	summary.ModelName = req.ModelConfig.ModelName
	collector.Data.Model.Provider = string(req.ModelConfig.Provider)
	collector.Data.Model.Name = req.ModelConfig.ModelName

	checkPointID := resolveCheckPointID(req.CheckPointID, summary.RunID)
	summary.CheckPointID = checkPointID
	messages, err := buildRunMessagesStrict(ctx, req, summary, collector, s.answerabilityGate)
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = runtimeErrorStage(err, "intent_detect")
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	if summary.SkipReply {
		summary.Status = "completed"
		summary.ModelName = req.ModelConfig.ModelName
		collector.Data.Status = summary.Status
		collector.Data.Output.FinishReason = "no_reply"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "intent selected no reply"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "intent policy selected no reply"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
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
		summary.ModelName = req.ModelConfig.ModelName
		collector.Data.Status = summary.Status
		if isEmergencySafetyHandoff(collector.Data.Pipeline.Intent) {
			collector.Data.Output.FinishReason = "intent_emergency_human_route_dispatched"
			collector.Data.Pipeline.Generate.Status = "skipped"
			collector.Data.Pipeline.Generate.Reason = "intent stage dispatched emergency safety directly to human reception"
			collector.Data.Pipeline.Validate.Status = "passed"
			collector.Data.Pipeline.Validate.Reason = "emergency safety route dispatched without customer confirmation"
			summary.TraceData = collector.Marshal()
			return summary, nil
		}
		collector.Data.Output.FinishReason = "intent_human_route_confirmation_requested"
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "intent stage requested customer confirmation before human route"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "human route waits for explicit customer confirmation"
		summary.TraceData = collector.Marshal()
		return summary, nil
	}
	if prepareHotelVariableDirectCommit(req, summary, collector) {
		summary.Status = "completed"
		summary.ModelName = req.ModelConfig.ModelName
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
	hasVisibleSkills := factory.HasVisibleSkills(req.AIAgent)
	tooling := prepareTooling(toolDefs, nil, req.ToolSet, hasVisibleSkills)
	summary.ToolCodes = append(summary.ToolCodes, tooling.toolCodes...)
	collector.Data.Input.ToolCodes = append(collector.Data.Input.ToolCodes, summary.ToolCodes...)
	collector.SetTooling(tooling.staticToolCodes, definitionToolCodes(tooling.definitions), len(tooling.definitions) > 0)

	agent, err := s.agentFactory.BuildCustomerServiceAgent(ctx, factory.BuildCustomerServiceAgentInput{
		AIAgent:                    req.AIAgent,
		ModelConfig:                req.ModelConfig,
		InstructionToolDefinitions: tooling.definitions,
		DynamicMCPToolDefinitions:  tooling.definitions,
		StaticTools:                tooling.staticTools,
		StaticToolCodes:            tooling.staticToolCodeMap,
		StaticToolMetadata:         tooling.staticToolMetadata,
		Collector:                  collector,
	})
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "prepare"
		summary.TraceData = collector.Marshal()
		return summary, err
	}

	runner := s.runnerFactory.Build(ctx, agent, false, true)
	if runner == nil {
		summary.Status = "error"
		summary.ErrorMessage = "failed to build runner"
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = "prepare"
		summary.TraceData = collector.Marshal()
		return summary, fmt.Errorf("%s", summary.ErrorMessage)
	}
	collector.Data.Interrupt.CheckPointID = checkPointID
	generateStartedAt := time.Now()
	if err = finishRuntimeGeneration(
		runner.Run(ctx, messages, buildRunOptions(checkPointID)...),
		summary,
		collector,
		tooling.toolDefsByModelName,
		req.ModelConfig.ModelName,
		generateStartedAt,
	); err != nil {
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func runtimeErrorStage(err error, fallback string) string {
	code, ok := svc.AIReplyExecutionErrorCodeOf(err)
	if !ok {
		return fallback
	}
	switch code {
	case svc.AIReplyExecutionErrorIntentDetectFailed:
		return "intent_detect"
	case svc.AIReplyExecutionErrorKnowledgeUnavailable:
		return "tool_knowledge"
	case svc.AIReplyExecutionErrorEmptyOutput:
		return "validate"
	default:
		return fallback
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
	if intent.NeedsKnowledge || intent.NeedsTool || intent.NeedsHumanRoute {
		return false
	}
	resourceTypes := requestedHotelVariableResourceTypes(req.UserMessage.Content, intent)
	if len(resourceTypes) == 0 {
		return false
	}
	// The Commit service owns resource validation. Keeping this reply empty ensures a
	// missing or malformed resource fails atomically instead of becoming fallback text.
	summary.ReplyText = ""
	return true
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
	collector.Data.Model.Provider = string(req.ModelConfig.Provider)
	collector.Data.Model.Name = req.ModelConfig.ModelName

	agent, err := s.agentFactory.BuildCustomerServiceAgent(ctx, factory.BuildCustomerServiceAgentInput{
		AIAgent:                    req.AIAgent,
		ModelConfig:                req.ModelConfig,
		InstructionToolDefinitions: tooling.definitions,
		DynamicMCPToolDefinitions:  tooling.definitions,
		StaticTools:                tooling.staticTools,
		StaticToolCodes:            tooling.staticToolCodeMap,
		StaticToolMetadata:         tooling.staticToolMetadata,
		Collector:                  collector,
	})
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "resume_prepare"
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	runner := s.runnerFactory.Build(ctx, agent, false, true)
	if runner == nil {
		summary.Status = "error"
		summary.ErrorMessage = "failed to build runner"
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = "resume_prepare"
		summary.TraceData = collector.Marshal()
		return summary, fmt.Errorf("%s", summary.ErrorMessage)
	}
	resumeData := buildResumeDataMessage(req.ResumeData)
	resumeTargets := buildResumeTargets(req.ResumeData)
	var (
		iter *adk.AsyncIterator[*adk.AgentEvent]
	)
	if len(resumeTargets) > 0 {
		iter, err = runner.ResumeWithParams(ctx, summary.CheckPointID, &adk.ResumeParams{
			Targets: resumeTargets,
		}, buildResumeOptions(summary.CheckPointID, resumeData)...)
	} else {
		iter, err = runner.Resume(ctx, summary.CheckPointID, buildResumeOptions(summary.CheckPointID, resumeData)...)
	}
	if err != nil {
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = err.Error()
		collector.Data.Error.Stage = "resume_execute"
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	generateStartedAt := time.Now()
	if err = finishRuntimeGeneration(
		iter,
		summary,
		collector,
		tooling.toolDefsByModelName,
		req.ModelConfig.ModelName,
		generateStartedAt,
	); err != nil {
		summary.TraceData = collector.Marshal()
		return summary, err
	}
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, nil
}

func finishRuntimeGeneration(
	events *adk.AsyncIterator[*adk.AgentEvent],
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	toolDefsByModelName map[string]string,
	modelName string,
	startedAt time.Time,
) error {
	if summary == nil || collector == nil {
		return svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, fmt.Errorf("runtime generation state is required"))
	}
	if consumeErr := consumeAgentEvents(events, summary, collector, toolDefsByModelName); consumeErr != nil {
		err := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, consumeErr)
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = "generate"
		collector.Data.Pipeline.Generate.Status = "failed"
		collector.Data.Pipeline.Generate.Reason = summary.ErrorMessage
		collector.Data.Pipeline.Validate.Status = "failed"
		collector.Data.Pipeline.Validate.Reason = summary.ErrorMessage
		collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
		return err
	}
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	summary.ModelName = modelName
	enforceGeneratedReplyActionLedger(summary, collector)
	if !summary.Interrupted && strings.TrimSpace(summary.ReplyText) == "" && !hasInvokedGraphTool(summary.InvokedToolCodes) {
		err := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorEmptyOutput, fmt.Errorf("runtime produced no reply or action"))
		summary.Status = "error"
		summary.ErrorMessage = err.Error()
		collector.Data.Status = summary.Status
		collector.Data.Error.Message = summary.ErrorMessage
		collector.Data.Error.Stage = "validate"
		collector.Data.Pipeline.Generate.Status = "failed"
		collector.Data.Pipeline.Generate.Reason = summary.ErrorMessage
		collector.Data.Pipeline.Validate.Status = "failed"
		collector.Data.Pipeline.Validate.Reason = summary.ErrorMessage
		return err
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
	return nil
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
