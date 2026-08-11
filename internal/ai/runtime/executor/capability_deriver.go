package executor

import (
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

// DerivedTaskCapabilities is the backend-owned execution policy for one
// semantic task. IntentDetect never controls these fields directly.
type DerivedTaskCapabilities struct {
	Task               contracts.IntentTaskV2
	ConfigID           int64
	ConfigName         string
	NeedsKnowledge     bool
	NeedsResource      bool
	NeedsTool          bool
	NeedsHumanRoute    bool
	NeedsClarification bool
	ResourceType       string
	ResourceAction     string
	ToolCodes          []string
	HumanRoutePolicy   string
	OutputMode         string
	NoReply            bool
	Constraints        []string
}

func DeriveRuntimeIntentCapabilities(v2 contracts.IntentTasksV2, configs []models.ReplyIntentConfig) ([]DerivedTaskCapabilities, error) {
	if v2.SchemaVersion != contracts.IntentTasksV2SchemaVersion {
		return nil, fmt.Errorf("intent contract version %q is unsupported", v2.SchemaVersion)
	}
	if len(v2.Tasks) == 0 || len(v2.Tasks) > 12 {
		return nil, fmt.Errorf("intent task count %d is invalid", len(v2.Tasks))
	}
	configByCode := make(map[string]models.ReplyIntentConfig, len(configs))
	for _, config := range normalizeIntentConfigs(configs) {
		code := strings.TrimSpace(config.Code)
		if code == "" || config.Status != enums.StatusOk {
			continue
		}
		configByCode[code] = config
	}

	ordered := append([]contracts.IntentTaskV2(nil), v2.Tasks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	seenSequence := make(map[int]struct{}, len(ordered))
	ret := make([]DerivedTaskCapabilities, 0, len(ordered))
	for _, task := range ordered {
		if _, exists := seenSequence[task.Sequence]; exists {
			return nil, fmt.Errorf("duplicate intent task sequence %d", task.Sequence)
		}
		seenSequence[task.Sequence] = struct{}{}
		if task.Sequence < 1 || task.Sequence > 12 {
			return nil, fmt.Errorf("intent task sequence %d is out of range", task.Sequence)
		}
		task.Intent = strings.TrimSpace(task.Intent)
		task.SubIntent = strings.TrimSpace(task.SubIntent)
		task.Text = strings.TrimSpace(task.Text)
		task.RequestMode = strings.TrimSpace(task.RequestMode)
		config, ok := configByCode[task.Intent]
		if !ok {
			return nil, fmt.Errorf("intent code %q is not present in the published profile", task.Intent)
		}
		derived, err := deriveRuntimeIntentTaskCapabilities(task, config)
		if err != nil {
			return nil, err
		}
		ret = append(ret, derived)
	}
	return ret, nil
}

func deriveRuntimeIntentTaskCapabilities(task contracts.IntentTaskV2, config models.ReplyIntentConfig) (DerivedTaskCapabilities, error) {
	derived := DerivedTaskCapabilities{
		Task: task, ConfigID: config.ID, ConfigName: strings.TrimSpace(config.Name),
		NeedsKnowledge: config.NeedsKnowledge, NeedsResource: config.NeedsResource,
		NeedsTool: config.NeedsTool, NeedsHumanRoute: config.NeedsHumanRoute,
		HumanRoutePolicy: strings.TrimSpace(config.HumanRoutePolicy),
		NoReply:          config.NoReplyWhenMatched,
		ToolCodes:        splitIntentTerms(config.ToolCodes),
	}
	derived.NeedsClarification = task.RequestMode == "clarify_previous"
	if derived.NeedsResource {
		configuredType := normalizeHotelVariableResourceType(config.ResourceType)
		if configuredType == "" || configuredType == "store_variable" {
			configuredType = task.SubIntent
		}
		resourceType, resourceAction := normalizeHotelVariableResourceAction("", configuredType, task.SubIntent)
		if resourceAction == "provide_store_variable" {
			return DerivedTaskCapabilities{}, fmt.Errorf("intent %q subIntent %q does not resolve to a configured resource action", task.Intent, task.SubIntent)
		}
		derived.ResourceType = resourceType
		derived.ResourceAction = resourceAction
	}
	if task.Intent != "human_complaint_risk" {
		derived.NeedsHumanRoute = false
		derived.HumanRoutePolicy = ""
	}
	if task.Intent == "human_complaint_risk" && derived.NeedsHumanRoute && derived.HumanRoutePolicy == "" {
		derived.HumanRoutePolicy = "managed_mode"
	}

	switch {
	case derived.NoReply:
		derived.OutputMode = "none"
	case derived.NeedsHumanRoute:
		derived.OutputMode = "handoff"
	case derived.NeedsResource && derived.NeedsKnowledge:
		derived.OutputMode = "text_and_resource"
	case derived.NeedsResource:
		derived.OutputMode = "resource_only"
	case derived.NeedsClarification:
		derived.OutputMode = "clarification"
	default:
		derived.OutputMode = "text"
	}
	if rules := strings.TrimSpace(config.ValidationRules); rules != "" {
		derived.Constraints = splitIntentLines(rules)
	}
	return derived, nil
}

func AdaptIntentV2ToLegacyTrace(v2 contracts.IntentTasksV2, derived []DerivedTaskCapabilities) callbacks.IntentTraceData {
	trace := callbacks.IntentTraceData{ShouldReply: true, MatchMode: "intent_tasks.v2", DialogueAct: v2.DialogueAct, Reason: "intent_tasks.v2 with backend-derived capabilities"}
	if len(derived) == 0 {
		return trace
	}
	trace.IntentTasks = make([]callbacks.IntentTaskTraceData, 0, len(derived))
	allNoReply := true
	for index, item := range derived {
		task := item.Task
		trace.IntentTasks = append(trace.IntentTasks, callbacks.IntentTaskTraceData{
			Sequence: task.Sequence, Intent: task.Intent, SubIntent: task.SubIntent, Text: task.Text,
			RequestMode: task.RequestMode, Confidence: task.Confidence,
			NeedsKnowledge: item.NeedsKnowledge, NeedsResource: item.NeedsResource,
			NeedsTool: item.NeedsTool, NeedsHumanRoute: item.NeedsHumanRoute,
			ResourceAction: item.ResourceAction, MatchedConfigID: item.ConfigID,
			Reason: "capabilities derived from published intent config",
		})
		if index == 0 {
			trace.PrimaryIntent = task.Intent
			trace.SubIntent = task.SubIntent
			trace.IntentConfidence = task.Confidence
			trace.MatchedConfigID = item.ConfigID
			trace.MatchedConfig = item.ConfigName
			trace.ResourceType = item.ResourceType
		}
		if task.Intent != trace.PrimaryIntent {
			trace.SecondaryIntents = appendIfMissing(trace.SecondaryIntents, task.Intent)
			trace.SecondaryIntentCodes = appendIfMissing(trace.SecondaryIntentCodes, task.Intent)
		}
		trace.NeedsKnowledge = trace.NeedsKnowledge || item.NeedsKnowledge
		trace.NeedsResource = trace.NeedsResource || item.NeedsResource
		trace.NeedsTool = trace.NeedsTool || item.NeedsTool
		trace.NeedsHumanRoute = trace.NeedsHumanRoute || item.NeedsHumanRoute
		trace.NeedsClarification = trace.NeedsClarification || item.NeedsClarification
		if item.ResourceAction != "" {
			trace.ResourceActions = appendIfMissing(trace.ResourceActions, item.ResourceAction)
		}
		trace.ToolCodes = appendUniqueStrings(trace.ToolCodes, item.ToolCodes...)
		if item.NeedsHumanRoute && trace.HumanRoutePolicy == "" {
			trace.HumanRoutePolicy = item.HumanRoutePolicy
		}
		allNoReply = allNoReply && item.NoReply
	}
	trace.ShouldReply = !allNoReply
	if len(trace.ResourceActions) > 0 {
		trace.ResourceAction = trace.ResourceActions[0]
		if trace.ResourceType == "" {
			trace.ResourceType = hotelVariableResourceTypeFromAction(trace.ResourceAction)
		}
	}
	trace.DetectedIntent = trace.PrimaryIntent
	trace.MatchedIntentCode = trace.PrimaryIntent
	if trace.IntentConfidence <= 0 || trace.IntentConfidence > 1 {
		trace.IntentConfidence = 0.65
	}
	if v2.DialogueAct == "unknown" {
		trace.NeedsClarification = true
	}
	return trace
}
