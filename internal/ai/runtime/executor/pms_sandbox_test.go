package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pms/sandbox"
)

func enableSandboxRuntimeTest(t *testing.T) {
	t.Helper()
	previous := config.CurrentOrNil()
	t.Cleanup(func() { config.SetCurrent(previous) })
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{
		Enabled: true, Provider: "sandbox", Environment: "test-2", SandboxEnabled: true,
	}})
}

func TestSandboxIntentContractPreservesModelTasksAndSourceOrder(t *testing.T) {
	enableSandboxRuntimeTest(t)
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request", ShouldReply: true, NeedsKnowledge: true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "service_request", SubIntent: "room_upgrade", SandboxScene: "B", Text: "换个更好的", ResolvedText: "换个更好的房型", SourceRefs: []string{"U1"}, ResolutionState: "resolved_from_context"},
			{Intent: "hotel_info", SubIntent: "mineral_water", Text: "水免费吗", SourceRefs: []string{"U2"}, NeedsKnowledge: true, ResolutionState: "clear"},
			{Intent: "hotel_info", SubIntent: "pillow_product", SandboxScene: "F", Text: "同款枕头链接发我", SourceRefs: []string{"U3"}, ResolutionState: "clear"},
		},
	}
	got := normalizeSandboxSceneIntent(intent)
	if len(got.IntentTasks) != 3 || !got.NeedsKnowledge || !got.NeedsTool {
		t.Fatalf("mixed task contract lost: %#v", got)
	}
	for index, task := range got.IntentTasks {
		if task.Text != intent.IntentTasks[index].Text || task.SourceRefs[0] != intent.IntentTasks[index].SourceRefs[0] {
			t.Fatalf("changed model-owned source or order: %#v", got.IntentTasks)
		}
	}
	plan := buildReplyPlan(got, selectIntentPromptPack(got))
	if len(plan.TaskPlans) != 3 {
		t.Fatalf("lost scene task: %#v", plan)
	}
	if runtimeReplyTaskUsesKnowledge(plan.TaskPlans[0]) || !runtimeReplyTaskUsesKnowledge(plan.TaskPlans[1]) || runtimeReplyTaskUsesKnowledge(plan.TaskPlans[2]) {
		t.Fatalf("sandbox tasks require FAQ or sibling knowledge was bypassed: %#v", plan.TaskPlans)
	}
	if plan.TaskPlans[2].ResourceAction != "" || plan.TaskPlans[2].Output == "structured_resource_commit" {
		t.Fatalf("pillow was routed to check-in variable: %#v", plan.TaskPlans[2])
	}
}

func TestSandboxSceneInstructionsKeepSemanticBoundaries(t *testing.T) {
	enableSandboxRuntimeTest(t)
	prompt := sandboxSceneIntentInstruction()
	for _, want := range []string{
		"同义", "会员等级升级", "枕头脏了", "普通首次用品请求", "text 仍保留当前原话", "订单 ID", "orderNumber", "客户确认",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing classification boundary %q", want)
		}
	}
	if pmsQueryIntentInstruction() != "" {
		t.Fatal("sandbox prompt retained external HPMS instructions")
	}
}

func TestSandboxInputPreservesCustomerOrderNumber(t *testing.T) {
	input := sandboxInputForTask(callbacks.ReplyTaskPlanTraceData{
		SandboxScene: "A",
		Text:         "查订单 TEST-001",
		SandboxParams: &callbacks.SandboxTaskParams{
			Phone:       "",
			OrderNumber: "TEST-001",
			Topics:      []string{"order"},
		},
	})
	if input.OrderNumber != "TEST-001" || input.Phone != "" {
		t.Fatalf("customer order identifier was not preserved: %#v", input)
	}
}

func TestSandboxDisabledCannotActivateModelSuppliedScene(t *testing.T) {
	previous := config.CurrentOrNil()
	t.Cleanup(func() { config.SetCurrent(previous) })
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{Enabled: true, Provider: "hpms"}})
	got := normalizeSandboxSceneIntent(callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "service_request", SandboxScene: "B", SandboxParams: &callbacks.SandboxTaskParams{Phone: "13800000000"},
	}}})
	if got.IntentTasks[0].SandboxScene != "" || got.IntentTasks[0].SandboxParams != nil || sandboxSceneIntentInstruction() != "" {
		t.Fatalf("sandbox activated outside test-2: %#v", got)
	}
}

func TestSandboxFixedReplySurvivesGeneratorRewrite(t *testing.T) {
	answer := "【测试 PMS】可升级为高级大床房，应补差价 50.00 元。确认后才会修改测试订单。"
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "service_request", SandboxScene: "B", OutputKind: "text", ReplyRequired: true,
		Output: "text_reply", FixedReply: true, AnswerText: &answer,
	}}}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != 1 || !groups[0].EvidenceLocked || !groups[0].FixedReply {
		t.Fatalf("fixed service response not locked: %#v", groups)
	}
	got, err := renderLockedReplyContent(groups[0])
	if err != nil || got != answer {
		t.Fatalf("fixed response rewritten or lost: %q %v", got, err)
	}
}

func TestSandboxFixedReplyDoesNotRebuildUnsafeResultAsEmptyFacts(t *testing.T) {
	answer := `{"replyParts":[{"taskId":"T1","content":"已办理"}]}`
	_, err := renderLockedReplyContent(textReplyTaskGroup{
		TaskID: "T1", EvidenceLocked: true, FixedReply: true, AnswerText: &answer,
	})
	if err == nil {
		t.Fatal("unsafe service output escaped protocol guard")
	}
}

type sandboxRuntimeStub struct {
	calls    []sandbox.SceneInput
	changes  []sandbox.ChangeRequest
	state    *sandbox.CustomerState
	fail     map[string]bool
	orderNil bool
}

func (s *sandboxRuntimeStub) ExecuteScene(_ context.Context, _ sandbox.Scope, input sandbox.SceneInput) (*sandbox.SceneResult, error) {
	s.calls = append(s.calls, input)
	if s.fail[input.Scene] {
		return nil, errors.New("test backend failed")
	}
	if s.orderNil {
		return &sandbox.SceneResult{Scene: input.Scene, Reply: "【测试 PMS】请提供测试手机号。", State: &sandbox.CustomerState{}, NeedsInput: []string{"phone"}}, nil
	}
	result := &sandbox.SceneResult{Scene: input.Scene, Reply: "【测试 PMS】" + input.Scene + "查询已完成。", State: s.state, Completed: true}
	if input.Scene == "F" {
		result.Resource = &sandbox.Resource{ID: 88, Code: "pillow"}
	}
	return result, nil
}

func (s *sandboxRuntimeStub) Prepare(_ context.Context, scope sandbox.Scope, change sandbox.ChangeRequest) (*sandbox.Operation, error) {
	s.changes = append(s.changes, change)
	return &sandbox.Operation{
		ID: 77, Provider: sandbox.Provider, StoreID: scope.StoreID, DatasetID: s.state.Dataset.ID,
		ConversationID: scope.ConversationID, SourceMessageID: scope.SourceMessageID, Status: "pending",
		PreviewText: "【测试 PMS】完整方案：高级大床房，502房，退房时间14:00，应付300.00元。请确认。",
	}, nil
}

func sandboxRuntimeFixture() (*sandboxRuntimeStub, sandbox.Scope, *callbacks.RuntimeTraceCollector) {
	order := &sandbox.Order{ID: 9, RoomTypeID: 1, RoomID: 4, CheckOut: time.Now().Add(24 * time.Hour)}
	service := &sandboxRuntimeStub{state: &sandbox.CustomerState{
		Dataset: sandbox.Dataset{ID: 2, StoreID: 1}, Order: order,
		Availability: []sandbox.RoomAvailability{{RoomType: sandbox.RoomType{ID: 2, Name: "高级大床房"}, Rooms: []sandbox.Room{{ID: 5, Number: "502", RoomTypeID: 2}}}},
	}}
	return service, sandbox.Scope{StoreID: 1, ConversationID: 3, CustomerID: 4, SourceMessageID: 5}, callbacks.NewRuntimeTraceCollector()
}

func TestSandboxFixedQueriesExecuteEvenWhenSiblingFails(t *testing.T) {
	enableSandboxRuntimeTest(t)
	service, scope, collector := sandboxRuntimeFixture()
	service.fail = map[string]bool{"A": true}
	collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", SandboxScene: "A", Text: "订单含早吗", SandboxParams: &callbacks.SandboxTaskParams{Topics: []string{"breakfast"}}},
		{TaskID: "T2", SandboxScene: "E", Text: "会员有什么福利"},
		{TaskID: "T3", SandboxScene: "F", Text: "同款枕头链接"},
		{TaskID: "T4", Intent: "hotel_info", Text: "矿泉水免费吗", NeedsKnowledge: true},
	}
	summary := &RunResult{}
	executeSandboxSceneTasksWithService(context.Background(), scope, summary, collector, service)
	if len(service.calls) != 3 || summary.ToolCallCount != 3 {
		t.Fatalf("fixed dispatch skipped required queries: %#v", service.calls)
	}
	for index := 0; index < 3; index++ {
		task := collector.Data.Pipeline.ReplyPlan.TaskPlans[index]
		if !task.FixedReply || task.AnswerText == nil || strings.TrimSpace(*task.AnswerText) == "" ||
			strings.Contains(*task.AnswerText, "测试") || strings.Contains(*task.AnswerText, "Sandbox") {
			t.Fatalf("task has no deterministic outcome: %#v", task)
		}
	}
	if collector.Data.Pipeline.ReplyPlan.TaskPlans[3].FixedReply || !collector.Data.Pipeline.ReplyPlan.TaskPlans[3].NeedsKnowledge {
		t.Fatal("non-scene FAQ was intercepted")
	}
	if len(collector.Data.SandboxResources) != 1 || collector.Data.SandboxResources[0].TaskID != "T3" || collector.Data.SandboxResources[0].ResourceID != 88 {
		t.Fatalf("pillow reference missing: %#v", collector.Data.SandboxResources)
	}
}

func TestSandboxCombinesUpgradeRoomAndCheckoutBeforeOnePrepare(t *testing.T) {
	enableSandboxRuntimeTest(t)
	service, scope, collector := sandboxRuntimeFixture()
	collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", SandboxScene: "B", Text: "升房", SandboxParams: &callbacks.SandboxTaskParams{RoomTypeName: "高级大床房"}},
		{TaskID: "T2", SandboxScene: "C", Text: "换到502", SandboxParams: &callbacks.SandboxTaskParams{RoomNumber: "502"}},
		{TaskID: "T3", SandboxScene: "D", Text: "延迟到两点", SandboxParams: &callbacks.SandboxTaskParams{Actions: []string{"late_checkout"}, CheckoutAt: "14:00"}},
	}
	executeSandboxSceneTasksWithService(context.Background(), scope, &RunResult{}, collector, service)
	if len(service.changes) != 1 {
		t.Fatalf("expected one atomic proposal, got %#v", service.changes)
	}
	change := service.changes[0]
	if !change.Upgrade || !change.ChangeRoom || !change.LateCheckout || change.Recovery || change.TargetRoomID != 5 || change.CheckOut == nil || change.CheckOut.Hour() != 14 {
		t.Fatalf("combined change lost a customer request: %#v", change)
	}
	if len(collector.Data.SandboxOperationIDs) != 1 || collector.Data.SandboxOperationIDs[0] != 77 {
		t.Fatalf("preview audit hook missing: %#v", collector.Data.SandboxOperationIDs)
	}
	summary := &RunResult{}
	if !completeSandboxFixedReply(summary, collector) || !strings.Contains(summary.ReplyText, "完整方案") {
		t.Fatalf("fixed preview cannot be committed: %#v", summary)
	}
}

func TestSandboxMissingOrderPromptsWithoutPreparing(t *testing.T) {
	enableSandboxRuntimeTest(t)
	service, scope, collector := sandboxRuntimeFixture()
	service.orderNil = true
	collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", SandboxScene: "B", Text: "可以升房吗"},
		{TaskID: "T2", SandboxScene: "D", Text: "延迟退房可以吗"},
	}
	executeSandboxSceneTasksWithService(context.Background(), scope, &RunResult{}, collector, service)
	if len(service.changes) != 0 || len(collector.Data.SandboxOperationIDs) != 0 {
		t.Fatal("missing order created a proposal")
	}
	for _, task := range collector.Data.Pipeline.ReplyPlan.TaskPlans {
		if task.AnswerText == nil || !strings.Contains(*task.AnswerText, "请提供手机号") ||
			strings.Contains(*task.AnswerText, "测试") || strings.Contains(*task.AnswerText, "Sandbox") {
			t.Fatalf("missing required detail was not asked: %#v", task)
		}
	}
}

func TestSandboxLateCheckoutDoesNotEraseEarlierCommitment(t *testing.T) {
	enableSandboxRuntimeTest(t)
	service, _, _ := sandboxRuntimeFixture()
	change := sandbox.ChangeRequest{OrderID: 9}
	tasks := []callbacks.ReplyTaskPlanTraceData{
		{SandboxScene: "D", SandboxParams: &callbacks.SandboxTaskParams{Actions: []string{"commitment"}}},
		{SandboxScene: "D", SandboxParams: &callbacks.SandboxTaskParams{Actions: []string{"late_checkout"}, CheckoutAt: "14:00"}},
	}
	for _, task := range tasks {
		if err := mergeSandboxTaskChange(&change, task, service.state); err != nil {
			t.Fatal(err)
		}
	}
	if !change.Recovery || !change.LateCheckout {
		t.Fatalf("one modification overwrote another: %#v", change)
	}
}
