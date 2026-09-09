package replyintent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultHotelIntentSchemaDescribesQuestionsBeforeClassification(t *testing.T) {
	schema := DefaultHotelIntentJSONSchema()
	start, end := strings.Index(schema, "{"), strings.LastIndex(schema, "}")
	var example map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schema[start:end+1]), &example); err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(example["intentTasks"], &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("invalid task example: %v", err)
	}
	if len(example) != 14 || len(tasks[0]) != 15 {
		t.Fatalf("reordering must not add or remove contract fields: %d/%d", len(example), len(tasks[0]))
	}
	if strings.Index(schema, `"intentTasks"`) > strings.Index(schema, `"primaryIntent"`) ||
		strings.Index(schema, `"resolvedText"`) > strings.Index(schema, `"intent"`) {
		t.Fatal("the example must identify each question before assigning its category")
	}
}

func TestDefaultHotelIntentUsesBoundedContextWithoutReplayingHistory(t *testing.T) {
	prompt := DefaultHotelIntentDetectPrompt()
	for _, obsolete := range []string{
		"没有紧邻业务上下文时不得从更早历史继承对象",
		"上述继承只允许使用紧邻的业务上下文",
		"必须结合紧邻上下文补全对象和所问方面",
	} {
		if strings.Contains(prompt, obsolete) {
			t.Errorf("obsolete reference restriction remains: %s", obsolete)
		}
	}
	for _, rule := range []string{
		"有界历史中最近仍相关且唯一的对象",
		"新主题不继承旧对象",
		"不重新建立历史待答任务",
		"clarification_answer 只用于回答紧邻 AI 或人工客服正在追问的必要字段",
		"external_proxy_action 只用于 service_request + action_request",
	} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("missing scope boundary: %s", rule)
		}
	}
}

func TestDefaultHotelIntentKeepsClearNonHotelQuestionsAnswerable(t *testing.T) {
	for _, rule := range []string{
		"问题是否明确与是否属于酒店业务分开判断",
		"明确的非酒店常识、解释和日常建议使用 interaction/chat",
		"没有上下文依据时，不能把普通人物、事物擅自解释成入住人",
		"只有答案确实依赖实时天气时才使用 weather_query",
		"问号、短评、表情等也是会话反馈",
		"不因单个符号强制转人工",
		"酒店政策和门店事实仍必须走 hotel_info",
	} {
		if !strings.Contains(DefaultHotelIntentDetectPrompt(), rule) {
			t.Errorf("missing interaction boundary: %s", rule)
		}
	}
}

func TestDefaultHotelIntentPromptDeclaresLightweightTaskSemantics(t *testing.T) {
	prompt := DefaultHotelIntentDetectPrompt()
	for _, expected := range []string{
		"每个任务还必须输出 objective、relationToPrevious、resolutionState、entities",
		"即使 subIntent 相同也必须分别建 Task",
		"必须拆成餐饮推荐和游玩推荐两个 Task",
		"action_request 只表示客户明确要求系统或门店同事执行现实动作",
		"relationToPrevious 只允许：independent、follow_up、clarification_answer、reference_previous、correction、modify_previous、cancel_previous、answer_rejected",
		"同一当前轮中后一个 URef 需要前一个 URef 才能补全时，用 sourceRefs 记录该上下文并保持 independent",
		"任何包含自包含业务问题的 URef，都必须有对应 Task 以该 URef 作为 sourceRefs[0]",
		"必须建立停车 Task",
		"U1 作为充电 Task 的上下文不能取代停车 Task",
		"resolutionState 只允许：clear、resolved_from_context、ambiguous、unresolved",
		"我开电车来的你懂我意思吗",
		"不能只因为 confidence 较低就标记歧义",
		"功能相近”不等于“同一物品",
		"needsClarification=true 只能来自真正的 ambiguous 或 unresolved 任务",
		"interaction/conversation_recap",
		"是的啊/对/可以",
		"单独“不是”",
		"AI 或人工客服追问姓名、房号或其他必要字段",
		"clarification_answer 只用于回答紧邻 AI 或人工客服正在追问的必要字段",
		"follow_up 和 reference_previous 从已提供的有界历史中选择最近仍相关且唯一的对象",
		"即使紧邻答复说“没有资料、无法确认、需要同事处理”",
		"不能绕过新主题强行续接旧对象",
		"不同答案结果的问题绝不能合并",
		"同一个明确对象、且客户表达的是一个紧密答案目标",
		"必须拆成办公桌房型和沙发房型两个 Task",
		"单纯再次询问、要求复述、比较对象或补问细节",
		"玩的呢/玩的勒",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("default intent prompt missing semantic contract %q", expected)
		}
	}
	if strings.Contains(prompt, "只有紧邻 AI 的明确业务澄清问题才能触发上述继承") {
		t.Fatal("default intent prompt must not restrict all business references to clarification questions")
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
