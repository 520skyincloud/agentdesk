package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"

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
	messages := buildRunMessages(ctx, req, summary, collector, s.answerabilityGate)
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
	consumeAgentEvents(runner.Run(ctx, messages, buildRunOptions(checkPointID)...), summary, collector, tooling.toolDefsByModelName)
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(generateStartedAt).Milliseconds()
	summary.ModelName = req.ModelConfig.ModelName
	enforceGeneratedReplyActionLedger(summary, collector)
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
	consumeAgentEvents(iter, summary, collector, tooling.toolDefsByModelName)
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(generateStartedAt).Milliseconds()
	summary.ModelName = req.ModelConfig.ModelName
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	collector.Data.Output.FinishReason = summary.Status
	syncSkillSummaryFromCollector(summary, collector)
	summary.TraceData = collector.Marshal()
	return summary, nil
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
