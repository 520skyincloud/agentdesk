package executor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestCompleteGeneratedReplyProtocolFailureReturnsExecutorError(t *testing.T) {
	protocolErr := fmt.Errorf("%w: missing content for task-2", errGeneratedReplyProtocol)
	summary := &RunResult{Status: "fallback", ReplyText: `{"replyParts":[]}`}
	collector := callbacks.NewRuntimeTraceCollector()

	got, err := completeGeneratedReplyProtocolFailure(summary, collector, protocolErr, "generate")

	if got != summary || !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("protocol failure must return the executor error, summary=%#v err=%v", got, err)
	}
	if summary.Status != "error" || summary.ReplyText != "" || summary.ErrorMessage == "" {
		t.Fatalf("protocol failure must suppress output and mark the run failed, got %#v", summary)
	}
	if collector.Data.Output.FinishReason != "generated_reply_protocol_error" || collector.Data.Pipeline.Validate.Status != "failed" {
		t.Fatalf("protocol failure trace must remain retryable and diagnosable, got %#v", collector.Data)
	}
	if strings.Contains(summary.ReplyText, "replyParts") {
		t.Fatalf("internal protocol leaked into the final reply: %q", summary.ReplyText)
	}
}

func TestCompleteIntentDetectUnavailableBlocksUngroundedGenerate(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Model.Name = "deepseek-v4-pro"

	got, err := completeIntentDetectUnavailable(summary, collector)

	if err != nil || got != summary || summary.Status != "completed" || strings.TrimSpace(summary.ReplyText) == "" {
		t.Fatalf("intent failure must produce one deterministic customer-visible safe reply, summary=%#v err=%v", summary, err)
	}
	if collector.Data.Pipeline.Generate.Status != "skipped" || collector.Data.Pipeline.Generate.FallbackMode != "intent_detect_safe_reply" {
		t.Fatalf("intent failure must never enter free Generate, got %#v", collector.Data.Pipeline.Generate)
	}
	if strings.Contains(summary.ReplyText, "几个问题") || !strings.Contains(summary.ReplyText, "再发一次") {
		t.Fatalf("intent availability failure must not blame the customer or pretend a single message was multi-question: %q", summary.ReplyText)
	}
	for _, inventedFact := range []string{"家常菜", "老街", "门卡", "充电桩", "电子发票"} {
		if strings.Contains(summary.ReplyText, inventedFact) {
			t.Fatalf("safe fallback must not contain hotel facts, got %q", summary.ReplyText)
		}
	}
}

func TestPrepareGroundedSingleKnowledgeDirectCommit(t *testing.T) {
	answer := "酒店提供速溶咖啡，您可以在1313房间对面的洗衣房内自行取用。"
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{NeedsKnowledge: true}
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "store_knowledge",
		DialogueAct: "new_request", ReplyStrategy: "answer_current_goal", RelationToPrevious: "independent", ResolutionState: "clear",
		NeedsKnowledge: true, OutputKind: "text", Output: "knowledge_text_reply", ReplyRequired: true,
		SelectedLayer: "store", AnswerText: &answer,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "task-1F1", Aspect: "existence", Statement: "酒店提供速溶咖啡。", CriticalValues: []string{"速溶咖啡"}},
			{FactID: "task-1F2", Aspect: "location", Statement: "速溶咖啡放置在1313房间对面的洗衣房内，您可以自行取用。", CriticalValues: []string{"1313房间对面的洗衣房"}},
		},
	}}}
	summary := &RunResult{Status: "started"}

	if !prepareGroundedSingleKnowledgeDirectCommit(summary, collector) {
		t.Fatal("complete independent Judge-grounded knowledge answer should skip redundant Generate")
	}
	if summary.ReplyText != answer {
		t.Fatalf("direct commit changed the Judge-grounded customer answer: %q", summary.ReplyText)
	}
}

func TestPrepareGroundedSingleKnowledgeDirectCommitKeepsContextAndToolTasksOnGenerate(t *testing.T) {
	answer := "酒店提供速溶咖啡。"
	baseTask := callbacks.ReplyTaskPlanTraceData{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "store_knowledge",
		DialogueAct: "new_request", ReplyStrategy: "answer_current_goal", RelationToPrevious: "independent", ResolutionState: "clear",
		NeedsKnowledge: true, OutputKind: "text", Output: "knowledge_text_reply", ReplyRequired: true,
		SelectedLayer: "store", AnswerText: &answer,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "existence", Statement: answer}},
	}
	tests := []struct {
		name   string
		intent callbacks.IntentTraceData
		mutate func(*callbacks.ReplyTaskPlanTraceData)
	}{
		{name: "context follow up", intent: callbacks.IntentTraceData{NeedsKnowledge: true}, mutate: func(task *callbacks.ReplyTaskPlanTraceData) { task.RelationToPrevious = "follow_up" }},
		{name: "tool task", intent: callbacks.IntentTraceData{NeedsKnowledge: true, NeedsTool: true}, mutate: func(task *callbacks.ReplyTaskPlanTraceData) { task.NeedsTool = true }},
		{name: "partial evidence", intent: callbacks.IntentTraceData{NeedsKnowledge: true}, mutate: func(task *callbacks.ReplyTaskPlanTraceData) { task.MissingAspects = []string{"取用时间"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := baseTask
			tt.mutate(&task)
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = tt.intent
			collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{task}}
			if prepareGroundedSingleKnowledgeDirectCommit(&RunResult{Status: "started"}, collector) {
				t.Fatal("contextual, tool or partial task must still use the normal Generate path")
			}
		})
	}
}

func TestPrepareGroundedSingleKnowledgeDirectCommitRejectsUnsafeJudgeAnswer(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		facts  []callbacks.KnowledgeEvidenceFactTraceData
	}{
		{
			name:   "unsupported staff action",
			answer: "酒店有拖鞋，我帮您联系前台同事送到房间。",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "existence", Statement: "酒店有拖鞋。", CriticalValues: []string{"拖鞋"}}},
		},
		{
			name:   "capability beyond selected fact",
			answer: "门店有外卖机器人，可以送到房间。",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "existence", Statement: "门店有外卖机器人。", CriticalValues: []string{"外卖机器人"}}},
		},
		{
			name:   "json envelope",
			answer: `{"content":"房间有两瓶矿泉水。"}`,
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "quantity", Statement: "房间有两瓶矿泉水。", CriticalValues: []string{"两瓶"}}},
		},
		{
			name:   "markdown fenced json envelope",
			answer: "```json\n{\"content\":\"房间有两瓶矿泉水。\"}\n```",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "quantity", Statement: "房间有两瓶矿泉水。", CriticalValues: []string{"两瓶"}}},
		},
		{
			name:   "unfinished markdown fenced json envelope",
			answer: "```json\n{\"content\":\"房间有两瓶矿泉水。\"}",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "quantity", Statement: "房间有两瓶矿泉水。", CriticalValues: []string{"两瓶"}}},
		},
		{
			name:   "opposite fact polarity",
			answer: "酒店设有停车场。",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "existence", Statement: "酒店没有停车场。", CriticalValues: []string{"停车场"}}},
		},
		{
			name:   "opposite polarity in compound fact",
			answer: "酒店不提供停车场。",
			facts:  []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Aspect: "existence", Statement: "酒店不提供早餐，但提供停车场。", CriticalValues: []string{"停车场"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = callbacks.IntentTraceData{NeedsKnowledge: true}
			collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "task-1", Intent: "hotel_info", SubIntent: "store_knowledge",
				DialogueAct: "new_request", ReplyStrategy: "answer_current_goal", RelationToPrevious: "independent", ResolutionState: "clear",
				NeedsKnowledge: true, OutputKind: "text", Output: "knowledge_text_reply", ReplyRequired: true,
				SelectedLayer: "store", AnswerText: &tt.answer, SupportedFacts: tt.facts,
			}}}
			summary := &RunResult{Status: "started"}
			if prepareGroundedSingleKnowledgeDirectCommit(summary, collector) {
				t.Fatalf("unsafe Judge answer must fall back to the normal Generate path: %q", summary.ReplyText)
			}
			if summary.ReplyText != "" {
				t.Fatalf("rejected direct answer leaked into summary: %q", summary.ReplyText)
			}
		})
	}
}

func TestUngroundedKnowledgeReplyTaskIDsRequiresSelectedFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "missing-layer", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Statement: "早餐时间是7:00到9:30。"}}},
		{TaskID: "missing-facts", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store"},
		{TaskID: "grounded", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store", SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Statement: "停车免费。"}}},
		{TaskID: "interaction", Intent: "interaction", OutputKind: "text", ReplyRequired: true},
	}}

	got := ungroundedKnowledgeReplyTaskIDs(plan)
	if len(got) != 2 || got[0] != "missing-layer" || got[1] != "missing-facts" {
		t.Fatalf("expected only ungrounded knowledge text tasks, got %#v", got)
	}
}

func TestUngroundedKnowledgeGuardPreservesMixedPMSFacts(t *testing.T) {
	for _, aspect := range []string{"pms_order_recept", "pms_inventory_stay"} {
		task := callbacks.ReplyTaskPlanTraceData{
			TaskID: "mixed-pms", Intent: "hotel_info", SubIntent: "room_upgrade", NeedsKnowledge: true,
			OutputKind: "text", ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID: "P1F1", Aspect: aspect, Statement: "PMS 已确认当前订单和可售情况。",
			}},
			MissingAspects: []string{"门店补偿政策尚未确认"},
		}
		if !isUngroundedKnowledgeReplyTask(task) {
			t.Fatalf("PMS facts must not count as evidence for the missing knowledge portion: %#v", task)
		}
		plan, isolated := isolateUngroundedKnowledgeReplyTasks(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			task,
			{TaskID: "weather", Intent: "interaction", SubIntent: "weather_query", OutputKind: "text", ReplyRequired: true},
		}})
		if len(isolated) != 1 || plan.TaskPlans[0].SelectedLayer != "runtime_safe_fallback" || len(plan.TaskPlans[0].SupportedFacts) != 2 || plan.TaskPlans[0].SupportedFacts[0].Aspect != aspect {
			t.Fatalf("PMS facts must survive while only the knowledge portion is constrained: plan=%#v isolated=%#v", plan, isolated)
		}
	}
}

func TestUngroundedKnowledgeGuardStillBlocksKnowledgeOnlyTasks(t *testing.T) {
	for _, task := range []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "missing-layer", Intent: "hotel_info", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Aspect: "policy", Statement: "停车免费。"}}},
		{TaskID: "missing-fact", Intent: "hotel_info", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, SelectedLayer: "store"},
	} {
		if !isUngroundedKnowledgeReplyTask(task) {
			t.Fatalf("knowledge-only evidence guard was weakened: %#v", task)
		}
	}
}

func TestCompleteUngroundedKnowledgeFallbackKeepsGroundedSiblingFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Model.Name = "deepseek-v4-pro"
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Statement: "停车免费。", CriticalValues: []string{"免费"}}},
		},
		{TaskID: "task-2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}}
	summary := &RunResult{Status: "started"}

	got, err := completeUngroundedKnowledgeFallback(summary, collector, []string{"task-2"})
	if err != nil || got != summary || summary.Status != "completed" {
		t.Fatalf("unexpected fallback result summary=%#v err=%v", summary, err)
	}
	if !strings.Contains(summary.ReplyText, "停车免费") || !strings.Contains(summary.ReplyText, "暂时没法准确回答") {
		t.Fatalf("fallback must preserve grounded siblings and fail closed for missing evidence, got %q", summary.ReplyText)
	}
	if collector.Data.Pipeline.Generate.Status != "skipped" || collector.Data.Pipeline.Generate.FallbackMode != "deterministic_knowledge_evidence_guard" {
		t.Fatalf("ungrounded knowledge must not enter Generate, got %#v", collector.Data.Pipeline.Generate)
	}
}

func TestCompleteUngroundedKnowledgeFallbackOffersMaintenanceTicketWithoutAutomaticAction(t *testing.T) {
	tests := []struct {
		name      string
		subIntent string
		customer  string
		wantReply string
	}{
		{
			name:      "maintenance respects rejected handoff",
			subIntent: "maintenance",
			customer:  "空调不制冷，我住1304，先告诉我怎么处理，不要转人工",
			wantReply: ungroundedMaintenanceOfferNoHandoffReply,
		},
		{
			name:      "air conditioner malfunction offers a ticket",
			subIntent: "air_conditioner",
			customer:  "房间空调坏了，能帮我处理吗",
			wantReply: ungroundedMaintenanceOfferReply,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Model.Name = "test-model"
			collector.Data.Pipeline.Normalize.CurrentUserText = tt.customer
			collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "task-1", Intent: "service_request", SubIntent: tt.subIntent,
				OriginalText: tt.customer, ResolvedText: tt.customer,
				NeedsKnowledge: true, Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true,
			}}}
			summary := &RunResult{Status: "started"}

			got, err := completeUngroundedKnowledgeFallback(summary, collector, []string{"task-1"})

			if err != nil || got != summary || summary.Status != "completed" || summary.ReplyText != tt.wantReply {
				t.Fatalf("unexpected maintenance fallback summary=%#v err=%v", summary, err)
			}
			if summary.handoffDirective || summary.handoffDispatchStatus != "" {
				t.Fatalf("maintenance fallback must not dispatch a human route: %#v", summary)
			}
			if strings.Contains(summary.ReplyText, "已登记") || strings.Contains(summary.ReplyText, "已创建") || strings.Contains(summary.ReplyText, "已转接") {
				t.Fatalf("maintenance fallback must only offer the existing ticket flow: %q", summary.ReplyText)
			}
			if len(collector.Data.ActionLedger.RequestedActions) != 0 || len(collector.Data.ActionLedger.CommittedActions) != 0 {
				t.Fatalf("maintenance fallback must not create or commit an action: %#v", collector.Data.ActionLedger)
			}
		})
	}
}

func TestIsolateUngroundedMaintenanceTaskKeepsOfferInsideMixedReply(t *testing.T) {
	tests := []struct {
		name      string
		subIntent string
		customer  string
		wantReply string
	}{
		{name: "air conditioner repair", subIntent: "air_conditioner_repair", customer: "空调不出风了怎么办", wantReply: ungroundedMaintenanceOfferReply},
		{name: "generic maintenance without handoff", subIntent: "maintenance", customer: "马桶堵了，先不要转人工", wantReply: ungroundedMaintenanceOfferNoHandoffReply},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{
					TaskID: "repair", Intent: "service_request", SubIntent: tt.subIntent,
					OriginalText: tt.customer, ResolvedText: tt.customer,
					NeedsKnowledge: true, Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true,
				},
				{TaskID: "weather", Intent: "interaction", SubIntent: "weather_query", Output: "text_reply", OutputKind: "text", ReplyRequired: true},
			}}

			got, isolated := isolateUngroundedKnowledgeReplyTasks(plan)

			if len(isolated) != 1 || isolated[0] != "repair" {
				t.Fatalf("expected only the maintenance task to be isolated, got %#v", isolated)
			}
			facts := got.TaskPlans[0].SupportedFacts
			if got.TaskPlans[0].SelectedLayer != "runtime_safe_fallback" || len(facts) != 1 || facts[0].Statement != tt.wantReply || facts[0].Aspect != "service_resolution" {
				t.Fatalf("maintenance task did not receive the fixed service offer: %#v", got.TaskPlans[0])
			}
			if strings.Contains(facts[0].Statement, "已登记") || strings.Contains(facts[0].Statement, "已转接") {
				t.Fatalf("isolated maintenance task claimed an action: %q", facts[0].Statement)
			}
		})
	}
}

func TestUngroundedMaintenanceFallbackDoesNotReplaceExplicitTicketToolTask(t *testing.T) {
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "ticket", Intent: "service_request", SubIntent: "create_ticket",
		OriginalText: "空调坏了，请创建维修工单", NeedsTool: true, Output: "text_reply", OutputKind: "text", ReplyRequired: true,
	}
	if reply, ok := ungroundedMaintenanceServiceReply(task, task.OriginalText); ok || reply != "" {
		t.Fatalf("explicit create_ticket must remain owned by the existing tool flow, reply=%q ok=%v", reply, ok)
	}
}

func TestIsolateUngroundedKnowledgeReplyTasksKeepsIndependentToolTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "interaction", SubIntent: "weather_query", OutputKind: "text", ReplyRequired: true, Output: "text_reply"},
	}}

	got, isolated := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(isolated) != 1 || isolated[0] != "task-1" {
		t.Fatalf("expected only the ungrounded knowledge task to be isolated, got %#v", isolated)
	}
	if got.TaskPlans[0].SelectedLayer != "runtime_safe_fallback" || len(got.TaskPlans[0].SupportedFacts) != 1 || got.TaskPlans[0].SupportedFacts[0].Statement != ungroundedKnowledgeSafeReply {
		t.Fatalf("ungrounded task must become a constrained safe reply, got %#v", got.TaskPlans[0])
	}
	if got.TaskPlans[1].TaskID != plan.TaskPlans[1].TaskID || got.TaskPlans[1].SubIntent != plan.TaskPlans[1].SubIntent || got.TaskPlans[1].Output != plan.TaskPlans[1].Output || len(got.TaskPlans[1].SupportedFacts) != 0 {
		t.Fatalf("independent tool task must remain unchanged, got %#v", got.TaskPlans[1])
	}
	if blocked := ungroundedKnowledgeReplyTaskIDs(got); len(blocked) != 0 {
		t.Fatalf("isolated plan must be safe to continue into Generate, got %#v", blocked)
	}
}

func TestIsolateUngroundedKnowledgeReplyTasksLeavesOnlyTaskForLocalFallback(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}}

	got, isolated := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(isolated) != 0 || got.TaskPlans[0].SelectedLayer != "" || len(got.TaskPlans[0].SupportedFacts) != 0 {
		t.Fatalf("a lone ungrounded task must still use the deterministic local fallback, got plan=%#v isolated=%#v", got, isolated)
	}
}
