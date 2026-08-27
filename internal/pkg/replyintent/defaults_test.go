package replyintent

import (
	"strings"
	"testing"
)

func TestDefaultHotelIntentPromptDeclaresLightweightTaskSemantics(t *testing.T) {
	prompt := DefaultHotelIntentDetectPrompt()
	for _, expected := range []string{
		"每个任务还必须输出 objective、relationToPrevious、resolutionState、entities",
		"action_request 只表示客户明确要求系统或门店同事执行现实动作",
		"relationToPrevious 只允许：independent、follow_up、clarification_answer、reference_previous、correction、modify_previous、cancel_previous、answer_rejected",
		"resolutionState 只允许：clear、resolved_from_context、ambiguous、unresolved",
		"不能只因为 confidence 较低就标记歧义",
		"功能相近”不等于“同一物品",
		"needsClarification=true 只能来自真正的 ambiguous 或 unresolved 任务",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("default intent prompt missing semantic contract %q", expected)
		}
	}
}

func TestDefaultHotelIntentSchemaFixesSemanticTaskFields(t *testing.T) {
	schema := DefaultHotelIntentJSONSchema()
	for _, expected := range []string{
		`"objective": "availability|quantity|location|price|time|policy|method|explanation|recommendation|identity|general_guidance|compound_information|action_request|status|modify|cancel|confirm|complaint|social|unknown"`,
		`"relationToPrevious": "independent|follow_up|clarification_answer|reference_previous|correction|modify_previous|cancel_previous|answer_rejected"`,
		`"resolutionState": "clear|resolved_from_context|ambiguous|unresolved"`,
		`"entities": [`,
		`"type": "facility|supply|room_type|room|service|location|order|resource|person|company|other"`,
		"字段固定为 intent、subIntent、objective、relationToPrevious、resolutionState、entities、text、resolvedText、sourceRefs、needsKnowledge、needsResource、needsTool、needsHumanRoute、resourceAction、reason",
		"entities 只能是由 text、type 构成的对象数组",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("default intent schema missing strict semantic field %q", expected)
		}
	}
	if strings.Contains(schema, `"canonicalEntity"`) || strings.Contains(schema, `"mappingType"`) {
		t.Fatal("model schema must not ask the model to invent entity normalization")
	}
}
