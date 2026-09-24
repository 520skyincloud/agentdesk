package executor

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestCustomerIdentityChangeInvalidatesDependentOrder(t *testing.T) {
	base := runtimePMSSessionLocator{
		Phone: "13800138000", OrderLocator: "接待单ID:OLD", TargetRoomTypeText: "湖景双床房",
	}
	for _, text := range []string{"号码改成13900139000", "帮我查13900139000的订单"} {
		t.Run(text, func(t *testing.T) {
			history := adapter.HistoryBuildResult{RawItems: []models.Message{{
				SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: text,
			}}}
			got := runtimePMSSessionLocatorFromHistoryWithBase(history, base)
			if got.Phone != "13900139000" || got.OrderLocator != "" {
				t.Fatalf("new identity retained a different customer's order: %#v", got)
			}
		})
	}
	task := callbacks.ReplyTaskPlanTraceData{
		SubIntent: "order_detail", OriginalText: "不是这个手机号，是13900139000",
		ResolvedText: "查询13800138000的订单，接待单ID:OLD",
		Entities: []callbacks.IntentEntityTraceData{
			{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
			{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:OLD"},
		},
	}
	input := runtimePMSReadPlanInputForTask(task, base, time.Now())
	if input.Phone != "13900139000" || input.ReceptOrderID != "" || input.ReserveOrderID != "" {
		t.Fatalf("execution used stale identity after correction: %#v", input)
	}
	task.OriginalText = "刚才号码写错了，换13900139000查"
	input = runtimePMSReadPlanInputForTask(task, base, time.Now())
	if input.TargetRoomTypeText != "" {
		t.Fatalf("identity correction was parsed as a room selection: %#v", input)
	}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "hotel_info", SubIntent: "order_detail", NeedsTool: true,
		Text: task.OriginalText, Entities: task.Entities,
	}}}
	resolved := applyRuntimePMSRequiredSlotPreflight(intent, base).IntentTasks[0]
	if runtimeIntentEntityValue(resolved.Entities, runtimeIntentEntityOrderLocator) != "" {
		t.Fatalf("corrected goal could persist the old order after a failed query: %#v", resolved)
	}
}

func TestCustomerCancellationPrecedesPopulatedQuerySlots(t *testing.T) {
	intent := callbacks.IntentTraceData{NeedsTool: true, IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "hotel_info", SubIntent: "order_query", Text: "不用查13800138000了",
		Objective: "cancel", RelationToPrevious: "cancel_previous", NeedsTool: true,
	}}}
	got := applyRuntimePMSRequiredSlotPreflight(intent, runtimePMSSessionLocator{Phone: "13800138000"})
	if got.NeedsTool || got.IntentTasks[0].NeedsTool || got.IntentTasks[0].Objective != "cancel" {
		t.Fatalf("a populated phone overrode cancellation: %#v", got)
	}
}

func TestCustomerGoalCancellationBlocksEarlierBusinessContext(t *testing.T) {
	trace := callbacks.RuntimeTraceData{}
	trace.Pipeline.Intent.IntentTasks = []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "acknowledgement", Objective: "cancel",
		DialogueAct: "cancellation", RelationToPrevious: "cancel_previous",
		ResolutionState: "resolved_from_context", Text: "算了，不换了",
	}}
	if !runtimeTraceBlocksEarlierBusinessContext(trace) {
		t.Fatal("cancelled goal can be resurrected by the next elliptical message")
	}
}

func TestCurrentChoiceOverridesPersistedChoice(t *testing.T) {
	for _, text := range []string{"换到庭院双床房", "那换庭院双床房吧"} {
		task := callbacks.ReplyTaskPlanTraceData{
			OriginalText: text, DialogueAct: "selection",
			Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityTargetRoomType, Text: "湖景大床房"}},
		}
		if got := runtimePMSTargetRoomTypeText(task); got != "庭院双床房" {
			t.Fatalf("persisted selection overrode current customer choice: %q", got)
		}
	}
}

func TestLocatorSnapshotIgnoresOlderHistoryButAppliesNewCorrection(t *testing.T) {
	base := runtimePMSSessionLocator{Phone: "13900139000", OrderLocator: "接待单ID:NEW", SourceMessageID: 20}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{ID: 10, SenderType: enums.IMSenderTypeCustomer, Content: "手机号13800138000"},
	}}
	got := runtimePMSSessionLocatorFromHistoryWithBase(history, base)
	if got.Phone != base.Phone || got.OrderLocator != base.OrderLocator {
		t.Fatalf("old history overwrote the latest successful snapshot: %#v", got)
	}
	history.RawItems = append(history.RawItems, models.Message{
		ID: 21, SenderType: enums.IMSenderTypeCustomer, Content: "手机号改成13700137000",
	})
	got = runtimePMSSessionLocatorFromHistoryWithBase(history, base)
	if got.Phone != "13700137000" || got.OrderLocator != "" {
		t.Fatalf("new correction failed to invalidate the dependent order: %#v", got)
	}
}

func TestInternalLocatorEntityTypesSurviveNormalization(t *testing.T) {
	for _, kind := range []string{runtimeIntentEntityCustomerPhone, runtimeIntentEntityOrderLocator, runtimeIntentEntityTargetRoomType} {
		got := normalizeRuntimeIntentEntities([]callbacks.IntentEntityTraceData{{Type: kind, Text: "value"}})
		if len(got) != 1 || got[0].Type != kind {
			t.Fatalf("normalization erased internal slot type %s: %#v", kind, got)
		}
	}
}

func TestLocatorSnapshotDoesNotCombineDifferentCustomers(t *testing.T) {
	trace := callbacks.RuntimeTraceData{}
	for _, customer := range []struct{ task, phone, order string }{
		{"task-a", "13800138000", "REC-A"},
		{"task-b", "13900139000", "REC-B"},
	} {
		trace.Pipeline.ReplyPlan.TaskPlans = append(trace.Pipeline.ReplyPlan.TaskPlans, callbacks.ReplyTaskPlanTraceData{
			TaskID: customer.task, SubIntent: "order_detail",
			Entities: []callbacks.IntentEntityTraceData{
				{Type: runtimeIntentEntityCustomerPhone, Text: customer.phone},
				{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:" + customer.order},
			},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Aspect: "pms_order_recept", Statement: "已查到订单"}},
		})
		trace.Output.CommitMessages = append(trace.Output.CommitMessages, callbacks.CommitMessageTraceData{
			Status: "sent", TaskIDs: []string{customer.task},
		})
	}
	if got := runtimePMSSessionLocatorFromTrace(trace); got != (runtimePMSSessionLocator{}) {
		t.Fatalf("a multi-customer turn cannot establish one unambiguous locator: %#v", got)
	}
	trace.Pipeline.ReplyPlan.TaskPlans = trace.Pipeline.ReplyPlan.TaskPlans[:1]
	got := runtimePMSSessionLocatorFromTrace(trace)
	if got.Phone != "13800138000" || got.OrderLocator != "接待单ID:REC-A" {
		t.Fatalf("a single coherent customer snapshot must remain usable: %#v", got)
	}
}

func TestOrderFieldProjectionHonorsSemanticGoalWithoutLiteralMatch(t *testing.T) {
	for _, objective := range []string{"time", "price"} {
		task := callbacks.ReplyTaskPlanTraceData{
			OriginalText: "用13800138000查", Objective: objective,
			ResolvedText: "我这次预订什么时候结束？",
		}
		count := 0
		for _, field := range runtimePMSOrderFieldProjections(task) {
			if !field.match {
				continue
			}
			count++
			if objective == "time" && field.aspect != "pms_order_checkin_time" && field.aspect != "pms_order_checkout_time" {
				t.Fatalf("time goal exposed unrelated field %s", field.aspect)
			}
			if objective == "price" && field.aspect != "pms_order_amount" {
				t.Fatalf("price goal exposed unrelated field %s", field.aspect)
			}
		}
		if count == 0 {
			t.Fatalf("semantic goal %s fell back to the whole order", objective)
		}
	}
}

func TestPMSUnavailableDoesNotAskCustomerToRepeatOrClaimAbsence(t *testing.T) {
	for _, subIntent := range []string{"order_detail", "room_change", "member_benefits", "room_status", "renewal"} {
		answer := deterministicPMSUnavailableReply(callbacks.ReplyTaskPlanTraceData{SubIntent: subIntent})
		for _, unwanted := range []string{"再问", "没有订单", "没查到", "已经转接", "帮您转接到同事了"} {
			if strings.Contains(answer, unwanted) {
				t.Fatalf("failed query invited a loop or claimed an unconfirmed result: %s", answer)
			}
		}
	}
}
