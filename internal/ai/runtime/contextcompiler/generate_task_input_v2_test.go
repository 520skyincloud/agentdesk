package contextcompiler

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func testReplyPlanForV2() contracts.ReplyPlanV4 {
	return contracts.ReplyPlanV4{
		SchemaVersion: contracts.ReplyPlanV4SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
		Tasks: []contracts.ReplyPlanTaskV4{
			{TaskKey: "task_invoice", Sequence: 1, Intent: "hotel_info", AnswerGroupKey: "grp_a", Objective: "发票抬头是啥", OutputMode: "text",
				Knowledge: contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}},
			{TaskKey: "task_wifi", Sequence: 2, Intent: "hotel_info", AnswerGroupKey: "grp_b", Objective: "WiFi密码是多少", OutputMode: "text",
				Knowledge: contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}},
		},
		ReplyGroups: []contracts.ReplyPlanGroupV4{
			{GroupKey: "grp_a", TaskKeys: []string{"task_invoice"}, Sequence: 1, OutputMode: "text", Required: true},
			{GroupKey: "grp_b", TaskKeys: []string{"task_wifi"}, Sequence: 2, OutputMode: "text", Required: true},
		},
		GlobalConstraints: contracts.ReplyPlanGlobalV4{MaxReplyParts: 2, MaxQuestionsPerPart: 4},
	}
}

// 文档 §9.1：生成 v2 且真实 groupKey 出现在 user message。
func TestGenerateTaskInputV2IncludesAuthoritativeGroups(t *testing.T) {
	plan := testReplyPlanForV2()
	raw, err := generateTaskInputTextV2(&plan)
	if err != nil {
		t.Fatalf("generateTaskInputTextV2: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["schemaVersion"] != "generate_task_input.v2" {
		t.Fatalf("schema: %#v", got["schemaVersion"])
	}
	groups := got["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups: %d", len(groups))
	}
	g1 := groups[0].(map[string]any)
	if g1["groupKey"] != "grp_a" || g1["groupRef"] != "G1" {
		t.Fatalf("group1: %#v", g1)
	}
	tasks := got["tasks"].([]any)
	t1 := tasks[0].(map[string]any)
	if t1["groupKey"] != "grp_a" || t1["groupRef"] != "G1" {
		t.Fatalf("task1 group binding: %#v", t1)
	}
	// 确认 raw 中包含真实 key（模型可见）
	if !strings.Contains(raw, "grp_a") || !strings.Contains(raw, "grp_b") {
		t.Fatal("real group keys not present in output")
	}
}

// 文档 §9.1：resource_only/handoff 不进入文本 Generate。
func TestGenerateTaskInputV2ExcludesNonTextGroups(t *testing.T) {
	plan := testReplyPlanForV2()
	plan.ReplyGroups = append(plan.ReplyGroups, contracts.ReplyPlanGroupV4{
		GroupKey: "grp_resource", TaskKeys: []string{"task_res"}, Sequence: 3, OutputMode: "resource_only", Required: true,
	})
	raw, err := generateTaskInputTextV2(&plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "grp_resource") {
		t.Fatal("resource_only group must not appear in text generate input")
	}
}

// 文档 §9.1：空组失败。
func TestGenerateTaskInputV2FailsWithoutGroups(t *testing.T) {
	plan := contracts.ReplyPlanV4{SchemaVersion: contracts.ReplyPlanV4SchemaVersion}
	if _, err := generateTaskInputTextV2(&plan); err == nil {
		t.Fatal("empty plan must fail")
	}
}
