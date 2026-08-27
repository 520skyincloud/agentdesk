package callbacks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntentTaskSemanticTraceFieldsAreSerialized(t *testing.T) {
	data := IntentTraceData{IntentTasks: []IntentTaskTraceData{{
		Intent:             "hotel_info",
		SubIntent:          "air_conditioner",
		Objective:          "availability",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Entities: []IntentEntityTraceData{{
			Text: "空调",
			Type: "facility",
		}},
		Text:         "房间有空调吗",
		ResolvedText: "房间有空调吗",
	}}}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal intent semantic trace: %v", err)
	}
	serialized := string(payload)
	for _, expected := range []string{
		`"objective":"availability"`,
		`"relationToPrevious":"independent"`,
		`"resolutionState":"clear"`,
		`"entities":[{"text":"空调","type":"facility"}]`,
	} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("intent semantic trace missing %q: %s", expected, serialized)
		}
	}
}

func TestReplyTaskPlanSemanticTraceFieldsAreSerialized(t *testing.T) {
	data := ReplyTaskPlanTraceData{
		TaskID:             "task-1",
		Intent:             "hotel_info",
		SubIntent:          "room_facilities",
		Objective:          "compound_information",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "resolved_from_context",
	}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal reply task semantic trace: %v", err)
	}
	serialized := string(payload)
	for _, expected := range []string{
		`"objective":"compound_information"`,
		`"relationToPrevious":"reference_previous"`,
		`"resolutionState":"resolved_from_context"`,
	} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("reply task semantic trace missing %q: %s", expected, serialized)
		}
	}
}
