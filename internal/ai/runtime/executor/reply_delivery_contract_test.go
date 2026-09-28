package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/services"
	"github.com/cloudwego/eino/schema"
)

func TestReplyRecoveryOnlyRegeneratesFailedTask(t *testing.T) {
	for _, secondQuestion := range []string{"我这次几点退房？", "那我最晚什么时候走？"} {
		t.Run(secondQuestion, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "停车收费吗？", OutputKind: "text", ReplyRequired: true,
					SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Aspect: "price", Statement: "停车免费。"}}},
				{TaskID: "T2", Intent: "hotel_info", SubIntent: "order_detail", Text: secondQuestion, OutputKind: "text", ReplyRequired: true,
					SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P2F1", Aspect: "pms_order_checkout_time", Statement: "9月28日13点退房。"}}},
			}})
			summary := &RunResult{InvokedToolCodes: []string{toolx.BuiltinPMSQuery.Code}, ToolCallCount: 1}
			attempts := 0
			result, err := runGeneratedReplyWithRecovery(context.Background(), nil, summary, collector, nil,
				func(_ context.Context, messages []*schema.Message) error {
					attempts++
					raw := `{"replyParts":[{"taskId":"T1","content":"停车免费。","coveredFactIds":["T1F1"]}]}`
					if attempts == 2 {
						if tasks := collector.Data.Pipeline.ReplyPlan.TaskPlans; len(tasks) != 1 || tasks[0].TaskID != "T2" {
							t.Fatalf("retry reintroduced a completed task: %#v", tasks)
						}
						if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "只回答其中待修复的任务") {
							t.Fatal("retry did not replace the original generation scope")
						}
						raw = `{"replyParts":[{"taskId":"T2","content":"您这笔订单是9月28日13点退房。","coveredFactIds":["P2F1"]}]}`
					}
					content, parseErr := normalizeGeneratedReplyPartsWithReceipt(raw, collector.Data.Pipeline.ReplyPlan, true,
						func(parts map[string]string) { summary.generatedTaskReplies = parts })
					if parseErr == nil {
						summary.ReplyText = content
						summary.Status = "completed"
					}
					return parseErr
				})
			if err != nil || attempts != 2 || result.FallbackMode != "" {
				t.Fatalf("bounded task recovery failed: result=%#v attempts=%d err=%v", result, attempts, err)
			}
			if summary.ReplyText != "停车免费。\n<<NEXT_MESSAGE>>\n您这笔订单是9月28日13点退房。" {
				t.Fatalf("valid answer or task order was lost: %q", summary.ReplyText)
			}
			if len(collector.Data.Pipeline.ReplyPlan.TaskPlans) != 2 {
				t.Fatal("temporary retry scope replaced the original audit plan")
			}
			if len(summary.InvokedToolCodes) != 1 || summary.InvokedToolCodes[0] != toolx.BuiltinPMSQuery.Code {
				t.Fatal("generation repair lost or duplicated the previously completed read-only PMS query")
			}
		})
	}
}

func TestReadOnlyReplyLanguageDoesNotAuthorizeStaffActions(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	for _, text := range []string{
		"我帮您查到这笔订单是13点退房。",
		"我看看，您这笔订单是9月28日离店。\n\n当前查询的沐阳有房。",
		"需要我帮您转人工吗？",
		"您需要我帮您转给同事吗？",
	} {
		if err := validateGeneratedReplyActionAuthorization(text, collector); err != nil {
			t.Fatalf("read-only result was mistaken for an unperformed action: %v", err)
		}
	}
	for _, text := range []string{"我帮您转给同事。", "我已经通知同事上门了。"} {
		if err := validateGeneratedReplyActionAuthorization(text, collector); !errors.Is(err, ErrGeneratedReplyProtocol) {
			t.Fatalf("unperformed action must enter bounded reply recovery, not execute: %q %v", text, err)
		}
	}
	if len(collector.Data.ActionLedger.RequestedActions) > 0 || len(collector.Data.ActionLedger.CommittedActions) > 0 {
		t.Fatal("wording validation changed action authorization")
	}
}

func TestHandoffSuccessNoticeRequiresRealDispatchReceipt(t *testing.T) {
	for _, status := range []string{"", "requested", "awaiting_room_number", "off_hours", "dispatched", "already_active"} {
		collector := callbacks.NewRuntimeTraceCollector()
		collector.Data.ActionLedger.CommittedActions = []callbacks.ActionLedgerItem{{Action: "human_route", Status: status}}
		err := validateGeneratedReplyActionAuthorization(services.DirectHandoffSuccessMessage, collector)
		success := status == "dispatched" || status == "already_active"
		if (err == nil) != success {
			t.Fatalf("success notice did not respect receipt status %q: %v", status, err)
		}
		if !success {
			summary := &RunResult{ReplyText: services.DirectHandoffSuccessMessage}
			enforceGeneratedReplyActionLedger(summary, collector)
			if strings.Contains(summary.ReplyText, services.DirectHandoffSuccessMessage) || collector.Data.Pipeline.Validate.Status != "failed" {
				t.Fatalf("final enforcement allowed fake success for %q: %q", status, summary.ReplyText)
			}
		}
	}
}
func TestFailedGenerationKeepsValidAnswersWithoutClaimingBusinessSuccess(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "member_benefits", OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P1F1", Aspect: "pms_member_benefit", Statement: "银卡允许13点退房。"}},
	}}})
	summary := &RunResult{}
	attempts := 0
	_, err := runGeneratedReplyWithRecovery(context.Background(), nil, summary, collector, nil,
		func(context.Context, []*schema.Message) error {
			attempts++
			return ErrGeneratedReplyProtocol
		})
	if err != nil || attempts != 2 || summary.ReplyText == "" {
		t.Fatalf("failure must stop after one recovery and deliver a limited notice: %#v err=%v", summary, err)
	}
	if collector.Data.Pipeline.Validate.Status != "failed" {
		t.Fatal("failure notice was falsely recorded as a successful business answer")
	}
	for _, forbidden := range []string{"再发", "没有会员", "未查到", "PMS", "整理"} {
		if strings.Contains(summary.ReplyText, forbidden) {
			t.Fatalf("generation failure blamed customer or query: %q", summary.ReplyText)
		}
	}
}

func TestReplyReceiptDoesNotRetainUnauthorizedSiblingAfterParseFailure(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "service_request", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T3", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}})
	for _, parseFailure := range []bool{false, true} {
		summary := &RunResult{generatedTaskReplies: map[string]string{
			"T1": "停车免费。",
			"T2": "我已经通知同事上门了。",
		}}
		var parseErr error
		if parseFailure {
			parseErr = &generatedReplyTaskError{err: ErrGeneratedReplyProtocol, validParts: summary.generatedTaskReplies}
		}
		_, err := validateGeneratedReplyReceipt("", parseErr, summary, collector)
		var taskErr *generatedReplyTaskError
		if !errors.As(err, &taskErr) || len(taskErr.validParts) != 1 || taskErr.validParts["T1"] != "停车免费。" {
			t.Fatalf("recovery retained unsafe content or lost the valid sibling: parts=%#v err=%v", summary.generatedTaskReplies, err)
		}
	}
}

func TestReplyReceiptSanitizesTaskHeadersBeforeRetaining(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}})
	summary := &RunResult{generatedTaskReplies: map[string]string{"T1": "[AI客服]停车免费。", "T2": "咖啡在大堂。"}}
	text, err := validateGeneratedReplyReceipt("", nil, summary, collector)
	if err != nil || strings.Contains(text, "[AI客服]") || summary.generatedTaskReplies["T1"] != "停车免费。" {
		t.Fatalf("task receipt bypassed final text sanitation: %q %v", text, err)
	}
}

func TestReplyRecoveryNeverReturnsLateSuccessOrFallback(t *testing.T) {
	for _, success := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		collector := callbacks.NewRuntimeTraceCollector()
		collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "T1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		}})
		summary := &RunResult{}
		attempts := 0
		result, err := runGeneratedReplyWithRecovery(ctx, nil, summary, collector, nil,
			func(context.Context, []*schema.Message) error {
				attempts++
				cancel()
				if success {
					summary.Status = "completed"
					summary.ReplyText = "已过期的回复"
					return nil
				}
				return ErrGeneratedReplyProtocol
			})
		if !errors.Is(err, context.Canceled) || attempts != 1 || summary.ReplyText != "" || result.FallbackMode != "" {
			t.Fatalf("cancelled run produced a late reply: result=%#v reply=%q err=%v", result, summary.ReplyText, err)
		}
	}
}

func TestFinalWhitespaceCleanupCannotTurnFailureIntoPass(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Validate.Status = "failed"
	summary := &RunResult{ReplyText: "停车免费。\n\n会员信息这次暂时无法准确答复。"}
	enforceGeneratedReplyActionLedger(summary, collector)
	if collector.Data.Pipeline.Validate.Status != "failed" {
		t.Fatal("final formatting hid the business generation failure")
	}
}
