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
		"同一当前轮中后一个 URef 需要前一个 URef 才能补全时，用 sourceRefs 记录该上下文并保持 independent",
		"resolutionState 只允许：clear、resolved_from_context、ambiguous、unresolved",
		"我开电车来的你懂我意思吗",
		"不能只因为 confidence 较低就标记歧义",
		"功能相近”不等于“同一物品",
		"needsClarification=true 只能来自真正的 ambiguous 或 unresolved 任务",
		"interaction/conversation_recap",
		"是的啊/对/可以",
		"单独“不是”",
		"AI 追问姓名、房号或其他必要字段",
		"玩的呢/玩的勒",
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
