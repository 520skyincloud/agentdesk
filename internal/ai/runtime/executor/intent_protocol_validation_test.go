package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestValidateRuntimeIntentDetectProtocolRejectsMissingTaskSemantics(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:       "hotel_info",
		Text:         "早餐几点",
		ResolvedText: "早餐几点",
		SourceRefs:   runtimeIntentSourceRefList{"U1"},
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected missing semantic field error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingSourceRefs(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		Objective:          "time",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Text:               "早餐几点",
		ResolvedText:       "早餐几点",
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "sourceRefs") {
		t.Fatalf("expected missing sourceRefs error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsUnknownSourceRef(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("早餐几点", "time"),
	}}
	parsed.IntentTasks[0].SourceRefs = runtimeIntentSourceRefList{"U2"}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "invalid ref") {
		t.Fatalf("expected invalid source ref error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingAtomicQuestion(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("你们有没有外卖机器人", "availability"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
	}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "你们有没有外卖机器人？外卖地址怎么填？布草是不是一客一换？")
	if err == nil || !strings.Contains(err.Error(), "atomic question 3 of 3") {
		t.Fatalf("expected atomic coverage error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsCompleteAtomicQuestions(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("你们有没有外卖机器人", "availability"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
		validRuntimeIntentProtocolTask("布草是不是一客一换", "policy"),
	}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "你们有没有外卖机器人？外卖地址怎么填？布草是不是一客一换？"); err != nil {
		t.Fatalf("expected complete protocol to pass, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsLegacyProfileCompatible(t *testing.T) {
	profile := &models.ReplyIntentProfile{IntentJSONSchema: `{"intentTasks":[{"intent":"","text":""}]}`}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{Intent: "hotel_info", Text: "早餐几点"}}}
	if err := validateRuntimeIntentDetectProtocol(parsed, profile, "早餐几点"); err != nil {
		t.Fatalf("legacy profile must not require V2 semantic fields: %v", err)
	}
}

func validRuntimeIntentProtocolTask(text string, objective string) runtimeIntentTaskJSON {
	return runtimeIntentTaskJSON{
		Intent:             "hotel_info",
		SubIntent:          "store_knowledge",
		Objective:          objective,
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Text:               text,
		ResolvedText:       text,
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}
}
