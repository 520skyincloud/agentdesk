package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/usagex"

	"github.com/cloudwego/eino/schema"
)

func TestRunGeneratedReplyWithRecoveryRetriesOnlyGenerate(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		[]*schema.Message{schema.UserMessage("房间里有几瓶矿泉水？")},
		summary,
		collector,
		func() bool { return true },
		func(_ context.Context, messages []*schema.Message) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("%w: missing content for task-1", ErrGeneratedReplyProtocol)
			}
			if len(messages) != 2 || !strings.Contains(messages[1].Content, "任务、来源和知识证据已经冻结") {
				t.Fatalf("second attempt must receive only a repair instruction over frozen input, messages=%#v", messages)
			}
			summary.Status = "completed"
			summary.ReplyText = "房间内有两瓶矿泉水。"
			return nil
		},
	)
	if err != nil || attempts != 2 || result.AttemptCount != 2 || result.FallbackMode != "" {
		t.Fatalf("generate-only retry failed: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.ReplyText != "房间内有两瓶矿泉水。" {
		t.Fatalf("unexpected recovered reply %q", summary.ReplyText)
	}
	if collector.Data.Pipeline.Generate.AttemptCount != 2 || collector.Data.Pipeline.Generate.FallbackMode != "" {
		t.Fatalf("expected generate retry trace, got %+v", collector.Data.Pipeline.Generate)
	}
	if !strings.Contains(collector.Data.Pipeline.Generate.LastProtocolError, "missing content for task-1") {
		t.Fatalf("successful retry must retain the first compact protocol failure, got %+v", collector.Data.Pipeline.Generate)
	}
}

func TestLockedInputErrorDoesNotRetryGenerate(t *testing.T) {
	answer := "矿泉水免费。"
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
		SelectedLayer: "store", SelectedCandidateIDs: []string{"T1C1"}, AnswerText: &answer,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "F1", Aspect: "quantity", Statement: "房间有两瓶矿泉水，免费。", CriticalValues: []string{"两瓶", "免费"},
		}},
	}}}
	summary, attempts := &RunResult{}, 0
	result, err := runGeneratedReplyWithRecovery(context.Background(), nil, summary, collector, func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			_, err := normalizeGeneratedReplyPartsResult(`{"replyParts":[{"taskId":"T1","content":"","coveredFactIds":["F1"]}]}`, collector.Data.Pipeline.ReplyPlan, true)
			return err
		})
	if err != nil || attempts != 1 || result.AttemptCount != 1 || result.FallbackMode != "supported_facts" {
		t.Fatalf("fixed input must not consume another Generate: %+v attempts=%d err=%v", result, attempts, err)
	}
	if !strings.Contains(summary.ReplyText, "两瓶") || !strings.Contains(summary.ReplyText, "免费") {
		t.Fatalf("safe known facts lost: %q", summary.ReplyText)
	}
}

func TestRecordGeneratedReplyProtocolErrorIgnoresExecutionFailures(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	recordGeneratedReplyProtocolError(collector, fmt.Errorf("%w: status 503", ErrGeneratedReplyExecution))
	if collector.Data.Pipeline.Generate.LastProtocolError != "" {
		t.Fatalf("execution failures must not populate lastProtocolError, got %+v", collector.Data.Pipeline.Generate)
	}
}

func TestRunGeneratedReplyWithRecoveryRetriesEmptyGenerateOutput(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		[]*schema.Message{schema.UserMessage("早餐几点？")},
		summary,
		collector,
		func() bool { return true },
		func(_ context.Context, messages []*schema.Message) error {
			attempts++
			if attempts == 1 {
				summary.Status = "fallback"
				return nil
			}
			if len(messages) != 2 || !strings.Contains(messages[1].Content, "without a customer-visible reply") {
				t.Fatalf("empty output retry must explain the missing customer reply, messages=%#v", messages)
			}
			summary.Status = "completed"
			summary.ReplyText = "早餐时间是7:00到9:30。"
			return nil
		},
	)
	if err != nil || attempts != 2 || result.AttemptCount != 2 || result.FallbackMode != "" {
		t.Fatalf("empty generate output must retry only Generate: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.ReplyText != "早餐时间是7:00到9:30。" {
		t.Fatalf("unexpected reply after empty output retry: %+v", summary)
	}
}

func TestRunGeneratedReplyWithRecoveryFallsBackWhenBothGenerateOutputsAreEmpty(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "F1", Aspect: "time", Statement: "早餐时间是7:00到9:30。", CriticalValues: []string{"7:00", "9:30"},
		}},
	}}})
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		nil,
		summary,
		collector,
		func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			summary.Status = "fallback"
			return nil
		},
	)
	if err != nil || attempts != 2 || result.FallbackMode != "supported_facts" {
		t.Fatalf("two empty outputs must use deterministic facts: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.Status != "completed" || summary.ReplyText != "早餐时间是7:00到9:30。" {
		t.Fatalf("empty outputs must not leave the customer without a reply: %+v", summary)
	}
}

func TestDeterministicGeneratedReplyFallbackNeverReturnsEmptyForUnknownTextTask(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", OutputKind: "text", ReplyRequired: true, Text: "最终确认，对吗？",
	}}})
	got := deterministicGeneratedReplyFallback(collector)
	if strings.TrimSpace(got) == "" || !strings.Contains(got, "麻烦") {
		t.Fatalf("unknown text task must still have a customer-visible fallback, got %q", got)
	}
}

func TestDeterministicGeneratedReplyFallbackAddsExternalProxyBoundaryAndFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "F1", Aspect: "location", Statement: "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号。",
		}},
	}}})
	want := externalProxyActionCapabilityBoundaryReply + "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号。"
	if got := deterministicGeneratedReplyFallback(collector); got != want {
		t.Fatalf("external proxy fallback must preserve the boundary and selected fact, got %q", got)
	}
}

func TestDeterministicGeneratedReplyFallbackUsesOnlyBoundaryWithoutExternalProxyFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		OutputKind: "text", ReplyRequired: true,
	}}})
	if got := deterministicGeneratedReplyFallback(collector); got != externalProxyActionCapabilityBoundaryReply {
		t.Fatalf("factless external proxy fallback must use the fixed boundary, got %q", got)
	}
}

func TestDeterministicGeneratedReplyFallbackCompactsContainedComplementaryFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
			{FactID: "F2", Aspect: "price", Statement: "房间内有两瓶矿泉水，都是免费的。", CriticalValues: []string{"免费"}},
		},
	}}})

	got := deterministicGeneratedReplyFallback(collector)
	if got != "房间内有两瓶矿泉水，都是免费的。" || strings.Count(got, "两瓶矿泉水") != 1 {
		t.Fatalf("one complete statement must cover contained quantity and price facts once, got %q", got)
	}
}

func TestDeterministicGeneratedReplyFallbackDropsContainedFactWhenCriticalValuesCovered(t *testing.T) {
	longFact := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F1",
		Aspect:         "method",
		Statement:      "酒店没有传统前台，可以通过入住机或小程序线上智能化方式办理入住。",
		CriticalValues: []string{"传统前台", "入住机", "小程序"},
	}
	shortFact := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F2",
		Aspect:         "existence",
		Statement:      "酒店没有传统前台。",
		CriticalValues: []string{"传统前台"},
	}

	for _, facts := range [][]callbacks.KnowledgeEvidenceFactTraceData{
		{longFact, shortFact},
		{shortFact, longFact},
		{
			shortFact,
			{FactID: "F3", Aspect: "method", Statement: longFact.Statement, CriticalValues: []string{"入住机", "小程序"}},
			{FactID: "F4", Aspect: "existence", Statement: longFact.Statement, CriticalValues: []string{"传统前台"}},
		},
	} {
		collector := callbacks.NewRuntimeTraceCollector()
		collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
			TaskID:         "task-1",
			Intent:         "hotel_info",
			OutputKind:     "text",
			ReplyRequired:  true,
			SupportedFacts: facts,
		}}})

		got := deterministicGeneratedReplyFallback(collector)
		if got != longFact.Statement || strings.Count(got, "传统前台") != 1 {
			t.Fatalf("covered contained fact must be emitted once regardless of input order, got %q", got)
		}
	}
}

func TestDeterministicGeneratedReplyFallbackCompactsPoliteClauseVariantsWithoutLosingDistinctFacts(t *testing.T) {
	checkIn := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F1",
		Aspect:         "method",
		Statement:      "酒店没有传统前台，可以通过入住机或小程序线上智能化方式办理入住。",
		CriticalValues: []string{"传统前台", "入住机", "小程序"},
	}
	doorAccess := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F4",
		Aspect:         "method",
		Statement:      "完成登记后扫人脸就可以开门，无需房卡。",
		CriticalValues: []string{"完成登记", "扫人脸", "房卡"},
	}
	waterQuantity := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F7",
		Aspect:         "quantity",
		Statement:      "房间内有两瓶矿泉水。",
		CriticalValues: []string{"两瓶"},
	}
	waterPrice := callbacks.KnowledgeEvidenceFactTraceData{
		FactID:         "F8",
		Aspect:         "price",
		Statement:      "房间内的矿泉水免费。",
		CriticalValues: []string{"免费"},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			checkIn,
			{FactID: "F2", Aspect: "existence", Statement: "我们酒店没有传统前台。", CriticalValues: []string{"传统前台"}},
			{FactID: "F3", Aspect: "method", Statement: "你可以通过入住机或小程序线上智能化方式办理入住。"},
			doorAccess,
			{FactID: "F5", Aspect: "method", Statement: "完成登记扫人脸就可以开门啦。"},
			{FactID: "F6", Aspect: "condition", Statement: "无需房卡。", CriticalValues: []string{"房卡"}},
			waterQuantity,
			waterPrice,
		},
	}}})

	want := strings.Join([]string{checkIn.Statement, doorAccess.Statement, waterQuantity.Statement, waterPrice.Statement}, " ")
	if got := deterministicGeneratedReplyFallback(collector); got != want {
		t.Fatalf("fallback must keep complete sentences and distinct quantity/price facts: got %q want %q", got, want)
	}
}

func TestDeterministicGeneratedReplyFallbackCompactsProductionStyleFragments(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "F1", Aspect: "existence", Statement: "酒店周边有丰富的餐饮选择，如罍街小吃街、贡街小吃街等。", CriticalValues: []string{"罍街小吃街", "贡街小吃街"}},
			{FactID: "F2", Aspect: "method", Statement: "酒店周边有丰富的餐饮选择。", CriticalValues: []string{"选择"}},
			{FactID: "F3", Aspect: "existence", Statement: "酒店附近的小丁小吃尤其推荐，需要驾车前往。", CriticalValues: []string{"小丁小吃"}},
			{FactID: "F4", Aspect: "location", Statement: "另外酒店附近的小丁小吃尤其推荐。"},
			{FactID: "F5", Aspect: "condition", Statement: "需要驾车前往。"},
		},
	}}})

	got := deterministicGeneratedReplyFallback(collector)
	if strings.Count(got, "餐饮选择") != 1 || strings.Count(got, "小丁小吃") != 1 || strings.Count(got, "驾车前往") != 1 {
		t.Fatalf("production-style full facts and fragments must each be emitted once, got %q", got)
	}
}

func TestDeterministicGeneratedReplyFallbackKeepsComplementaryAndConflictingContextsSeparate(t *testing.T) {
	facts := []callbacks.KnowledgeEvidenceFactTraceData{
		{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
		{FactID: "F2", Aspect: "price", Statement: "房间内的矿泉水免费。", CriticalValues: []string{"免费"}},
		{FactID: "F3", Aspect: "existence", Statement: "合柴房型有办公桌。", CriticalValues: []string{"合柴"}},
		{FactID: "F4", Aspect: "existence", Statement: "麦田房型有办公桌。", CriticalValues: []string{"麦田"}},
		{FactID: "F5", Aspect: "time", Statement: "工作日早餐时间是7:00-9:00。", CriticalValues: []string{"工作日", "7:00-9:00"}},
		{FactID: "F6", Aspect: "time", Statement: "周末早餐时间是8:00-10:00。", CriticalValues: []string{"周末", "8:00-10:00"}},
		{FactID: "F7", Aspect: "capability", Statement: "可以开门。"},
		{FactID: "F8", Aspect: "capability", Statement: "不可以开门。"},
	}
	compacted := compactGeneratedReplyFallbackFacts(replyFactRequirements(facts))
	if len(compacted) != len(facts) {
		t.Fatalf("different aspects, objects, conditions, and polarities must remain separate: %#v", compacted)
	}
}

func TestDeterministicGeneratedReplyFallbackKeepsScopedSubstringAndPolarityFactsSeparate(t *testing.T) {
	facts := []callbacks.KnowledgeEvidenceFactTraceData{
		{FactID: "F1", Aspect: "price", Statement: "儿童早餐免费。", CriticalValues: []string{"免费"}},
		{FactID: "F2", Aspect: "price", Statement: "早餐免费。", CriticalValues: []string{"免费"}},
		{FactID: "F3", Aspect: "scope", Statement: "并非所有房间都可以开门。"},
		{FactID: "F4", Aspect: "scope", Statement: "房间都可以开门。"},
	}
	compacted := compactGeneratedReplyFallbackFacts(replyFactRequirements(facts))
	if len(compacted) != len(facts) {
		t.Fatalf("narrower scope and opposite polarity facts must not swallow broader facts: %#v", compacted)
	}
}

func TestRunGeneratedReplyWithRecoveryFallsBackToSupportedFacts(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		SubIntent:     "supplies_self_help",
		Text:          "那这两瓶是不是都免费？",
		ResolvedText:  "房间内两瓶矿泉水是否都免费？",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
			{FactID: "F2", Aspect: "price", Statement: "房间内矿泉水都是免费的。", CriticalValues: []string{"免费"}},
		},
	}}})
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		nil,
		summary,
		collector,
		func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: missing coveredFactId F2", ErrGeneratedReplyProtocol)
		},
	)
	if err != nil || attempts != 2 || result.FallbackMode != "supported_facts" {
		t.Fatalf("fact fallback failed: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.Status != "completed" || !strings.Contains(summary.ReplyText, "两瓶") || !strings.Contains(summary.ReplyText, "免费") {
		t.Fatalf("fallback must preserve every confirmed fact, summary=%+v", summary)
	}
	if collector.Data.Pipeline.Generate.AttemptCount != 2 || collector.Data.Pipeline.Generate.FallbackMode != "supported_facts" || collector.Data.Pipeline.Generate.ComposedMessageCount != 1 {
		t.Fatalf("expected supported-fact fallback trace, got %+v", collector.Data.Pipeline.Generate)
	}
	if !strings.Contains(collector.Data.Pipeline.Generate.LastProtocolError, "missing coveredFactId F2") {
		t.Fatalf("expected compact protocol failure in trace, got %+v", collector.Data.Pipeline.Generate)
	}
}

func TestRunGeneratedReplyWithRecoveryPreservesFactsWhenAnotherKnowledgeTaskHasNoFacts(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "task-1",
			Intent:        "hotel_info",
			SubIntent:     "supplies_self_help",
			Text:          "房间里有几瓶矿泉水？",
			OutputKind:    "text",
			ReplyRequired: true,
			Output:        "knowledge_text_reply",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
			},
		},
		{
			TaskID:        "task-2",
			Intent:        "hotel_info",
			SubIntent:     "unknown_service",
			Text:          "房间能提供传真机吗？",
			OutputKind:    "text",
			ReplyRequired: true,
			Output:        "knowledge_text_reply",
			MissingAspects: []string{
				"是否提供传真机",
			},
		},
	}})
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		nil,
		summary,
		collector,
		func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: missing content for task-2", ErrGeneratedReplyProtocol)
		},
	)
	if err != nil || attempts != 2 || result.FallbackMode != "supported_facts" {
		t.Fatalf("mixed fallback failed: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.Status != "completed" || strings.TrimSpace(summary.ReplyText) == "" {
		t.Fatalf("mixed fallback must remain non-empty, summary=%+v", summary)
	}
	if !strings.Contains(summary.ReplyText, "两瓶") {
		t.Fatalf("mixed fallback must preserve the confirmed fact, reply=%q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "暂时没法准确回答") {
		t.Fatalf("knowledge task without facts must receive a safe boundary reply, reply=%q", summary.ReplyText)
	}
	if count := generatedReplyMessageCount(summary.ReplyText); count < 1 || count > 3 {
		t.Fatalf("mixed fallback must compose one to three messages, count=%d reply=%q", count, summary.ReplyText)
	}
}

func TestRunGeneratedReplyWithRecoveryFallsBackForCommonInteractions(t *testing.T) {
	tests := map[string]string{
		"social":                  "嗯嗯，在的呀。",
		"correction":              "不好意思，是我理解错了。",
		"clarify":                 "您具体想问哪方面呀？",
		"chat":                    "在的呀，您说。",
		"media_context_follow_up": "您具体想问图片或文件里的哪一部分呀？",
		"unknown_interaction":     "在的呀，您说。",
	}
	for subIntent, want := range tests {
		t.Run(subIntent, func(t *testing.T) {
			summary := &RunResult{Status: "started"}
			collector := callbacks.NewRuntimeTraceCollector()
			collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID:        "task-1",
				Intent:        "interaction",
				SubIntent:     subIntent,
				OutputKind:    "text",
				ReplyRequired: true,
			}}})
			attempts := 0

			result, err := runGeneratedReplyWithRecovery(
				context.Background(),
				nil,
				summary,
				collector,
				func() bool { return true },
				func(context.Context, []*schema.Message) error {
					attempts++
					return fmt.Errorf("%w: invalid interaction reply", ErrGeneratedReplyProtocol)
				},
			)
			if err != nil || attempts != 2 || result.FallbackMode != "supported_facts" {
				t.Fatalf("interaction fallback failed: result=%+v attempts=%d err=%v", result, attempts, err)
			}
			if summary.Status != "completed" || summary.ReplyText != want {
				t.Fatalf("expected safe %s fallback %q, got %+v", subIntent, want, summary)
			}
		})
	}
}

func TestRunGeneratedReplyWithRecoveryStopsWhenReplyBecomesIneligible(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runGeneratedReplyWithRecovery(
		context.Background(),
		nil,
		summary,
		collector,
		func() bool { return false },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: missing content", ErrGeneratedReplyProtocol)
		},
	)
	if err != nil || attempts != 1 || result.FallbackMode != "cancelled_before_retry" {
		t.Fatalf("ineligible reply must stop before retry: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if !summary.SkipReply || summary.ReplyText != "" {
		t.Fatalf("cancelled retry must not commit stale content, summary=%+v", summary)
	}
}

func TestRunResumedGeneratedReplyWithRecoveryRetriesAndKeepsInterruptPending(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runResumedGeneratedReplyWithRecovery(
		context.Background(),
		summary,
		collector,
		"interrupt-1",
		func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: malformed resumed reply", ErrGeneratedReplyProtocol)
		},
	)
	if err != nil || attempts != 2 || result.AttemptCount != 2 || result.FallbackMode != resumeGeneratedReplyFallbackMode {
		t.Fatalf("resume recovery must retry only Generate once and re-interrupt: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.Status != "interrupted" || !summary.Interrupted || summary.SkipReply {
		t.Fatalf("resume fallback must preserve a pending interrupt instead of ending empty: %+v", summary)
	}
	if len(summary.Interrupts) != 1 || summary.Interrupts[0].ID != "interrupt-1" || !strings.Contains(summary.Interrupts[0].InfoPreview, "确认") {
		t.Fatalf("resume fallback must retain the same target and confirmation prompt: %+v", summary.Interrupts)
	}
	if collector.Data.Pipeline.Generate.AttemptCount != 2 || collector.Data.Pipeline.Generate.FallbackMode != resumeGeneratedReplyFallbackMode {
		t.Fatalf("resume recovery trace is incomplete: %+v", collector.Data.Pipeline.Generate)
	}
}

func TestRunResumedGeneratedReplyWithRecoveryStopsWhenReplyBecomesIneligible(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runResumedGeneratedReplyWithRecovery(
		context.Background(),
		summary,
		collector,
		"interrupt-1",
		func() bool { return false },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: status 503", ErrGeneratedReplyExecution)
		},
	)
	if err != nil || attempts != 1 || result.FallbackMode != "cancelled_before_retry" {
		t.Fatalf("ineligible resumed reply must stop before retry: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if !summary.SkipReply || summary.Interrupted || len(summary.Interrupts) != 0 {
		t.Fatalf("stale resume must not be re-opened or committed: %+v", summary)
	}
}

func TestRunResumedGeneratedReplyWithRecoveryPreservesCheckpointErrors(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	attempts := 0

	result, err := runResumedGeneratedReplyWithRecovery(
		context.Background(),
		summary,
		collector,
		"interrupt-1",
		func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			return fmt.Errorf("%w: checkpoint not found", ErrGeneratedReplyExecution)
		},
	)
	if err == nil || attempts != 1 || result.FallbackMode != "" {
		t.Fatalf("non-retryable checkpoint errors must remain visible to expiry handling: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if summary.Interrupted || len(summary.Interrupts) != 0 {
		t.Fatalf("checkpoint errors must not create a synthetic pending interrupt: %+v", summary)
	}
}

func TestResolveResumeInterruptIDUsesStableTargetOrder(t *testing.T) {
	req := ResumeInput{ResumeData: map[string]string{
		"interrupt-b": "确认",
		"interrupt-a": "确认",
	}}
	if got := resolveResumeInterruptID(req); got != "interrupt-a" {
		t.Fatalf("expected stable resume target selection, got %q", got)
	}
}

func TestIsRetryableGeneratedReplyErrorClassifiesTransientExecutionFailures(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("%w: responses api status 429", ErrGeneratedReplyExecution), true},
		{fmt.Errorf("%w: responses api status 503", ErrGeneratedReplyExecution), true},
		{fmt.Errorf("%w: connection reset by peer", ErrGeneratedReplyExecution), true},
		{fmt.Errorf("%w: responses api status 401", ErrGeneratedReplyExecution), false},
		{fmt.Errorf("ordinary failure"), false},
	}
	for _, tc := range cases {
		if got := isRetryableGeneratedReplyError(tc.err); got != tc.want {
			t.Fatalf("isRetryableGeneratedReplyError(%q)=%v want %v", tc.err, got, tc.want)
		}
	}
}

func TestResetGeneratedReplyAttemptStateKeepsRunUsageOnly(t *testing.T) {
	summary := &RunResult{
		Status:           "interrupted",
		ReplyText:        "stale reply",
		ErrorMessage:     "stale error",
		SkipReply:        true,
		Interrupted:      true,
		Interrupts:       []InterruptContextSummary{{ID: "interrupt-1"}},
		InvokedToolCodes: []string{"mcp/maps/route"},
		ToolCallCount:    1,
		PromptTokens:     20,
		TotalTokens:      30,
		ModelUsageCalls: []ModelUsageCall{{
			PromptTokens: 20,
			HasUsage:     true,
			Status:       "completed",
		}},
	}

	resetGeneratedReplyAttemptState(summary)

	if summary.Status != "started" || summary.ReplyText != "" || summary.ErrorMessage != "" || summary.SkipReply {
		t.Fatalf("attempt-local reply state was not reset: %+v", summary)
	}
	if summary.Interrupted || len(summary.Interrupts) != 0 || len(summary.InvokedToolCodes) != 0 || summary.ToolCallCount != 0 {
		t.Fatalf("attempt-local interrupt/tool state was not reset: %+v", summary)
	}
	if summary.PromptTokens != 20 || summary.TotalTokens != 30 || len(summary.ModelUsageCalls) != 1 || !summary.ModelUsageCalls[0].HasUsage {
		t.Fatalf("run-level usage must survive attempt reset: %+v", summary)
	}
}

func TestRunGeneratedReplyWithRecoveryStopsAfterAnyToolInvocation(t *testing.T) {
	for _, toolCode := range []string{
		"mcp/maps/route",
		toolx.BuiltinToolSearch.Code,
		toolx.GraphHandoffConversation.Code,
	} {
		t.Run(toolCode, func(t *testing.T) {
			summary := &RunResult{Status: "started"}
			attempts := 0

			result, err := runGeneratedReplyWithRecovery(
				context.Background(),
				nil,
				summary,
				callbacks.NewRuntimeTraceCollector(),
				func() bool { return true },
				func(context.Context, []*schema.Message) error {
					attempts++
					summary.InvokedToolCodes = []string{toolCode}
					summary.ToolCallCount = 1
					return fmt.Errorf("%w: malformed output after tool call", ErrGeneratedReplyProtocol)
				},
			)
			if err == nil || attempts != 1 || result.AttemptCount != 1 {
				t.Fatalf("tool invocation must stop Generate retry: result=%+v attempts=%d err=%v", result, attempts, err)
			}
			if len(summary.InvokedToolCodes) != 1 || summary.InvokedToolCodes[0] != toolCode || summary.ToolCallCount != 1 {
				t.Fatalf("run-level tool audit must retain the invocation: %+v", summary)
			}
		})
	}
}

func TestRunGeneratedReplyWithRecoveryRecordsFailedReceiptsWhenFallbackSucceeds(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "F1", Aspect: "time", Statement: "早餐时间是7:00到9:30。", CriticalValues: []string{"7:00", "9:30"},
		}},
	}}})
	ctx, _ := usagex.WithCapture(context.Background())
	attempts := 0
	client := &http.Client{Transport: usagex.TrackingTransport{Base: generatedReplyTestRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header: http.Header{
				usagex.NewAPIRequestIDHeader: []string{fmt.Sprintf("failed-request-%d", attempts)},
			},
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})}}

	result, err := runGeneratedReplyWithRecovery(
		ctx,
		nil,
		summary,
		collector,
		func() bool { return true },
		func(attemptCtx context.Context, _ []*schema.Message) error {
			req, requestErr := http.NewRequestWithContext(attemptCtx, http.MethodPost, "http://model.invalid/generate", nil)
			if requestErr != nil {
				return requestErr
			}
			resp, callErr := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if callErr != nil {
				return callErr
			}
			return fmt.Errorf("%w: status 503", ErrGeneratedReplyExecution)
		},
	)
	if err != nil || result.FallbackMode != "supported_facts" || attempts != 2 {
		t.Fatalf("failed calls must still reach deterministic fallback: result=%+v attempts=%d err=%v", result, attempts, err)
	}
	if len(summary.ModelUsageCalls) != 2 {
		t.Fatalf("every failed gateway receipt must have one call record: %+v", summary.ModelUsageCalls)
	}
	for index, call := range summary.ModelUsageCalls {
		if call.Status != "failed" || call.HasUsage || call.GatewayReceiptOrdinal != index+1 || call.CallSequence != index+1 {
			t.Fatalf("failed call %d was not preserved exactly: %+v", index+1, call)
		}
	}
}

type generatedReplyTestRoundTripper func(*http.Request) (*http.Response, error)

func (f generatedReplyTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
