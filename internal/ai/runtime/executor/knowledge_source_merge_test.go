package executor

import (
	"context"
	"strings"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/pkg/enums"
)

func TestKnowledgeMissPreservesSpecificMissingAspectsAcrossStages(t *testing.T) {
	for _, query := range []string{
		"空调还是不制冷，热得没法休息，先不要转人工",
		"热水一直不热，有什么解决办法？先不要通知门店",
	} {
		t.Run(query, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Intent: "hotel_info", Query: query,
				Candidates: []knowledgeEvidenceJudgeCandidate{
					{CandidateID: "C1", Layer: "store", Hit: rag.RetrieveResult{Content: "问题：设备故障\n答案：转接"}},
					{CandidateID: "C2", Layer: "general", Hit: rag.RetrieveResult{Content: "问题：有什么设备\n答案：客房有基础设施。"}},
				},
			}
			missing := "客户所述设备故障的可用解决办法"
			batch := structuralKnowledgeBatch(task)
			outcome := knowledgeEvidenceJudgeOutcome{Applied: true, Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					"store":   {Decision: knowledgeEvidenceDecisionInsufficient, MissingAspects: []string{missing}},
					"general": {Decision: knowledgeEvidenceDecisionInsufficient, MissingAspects: []string{missing}},
				},
			}}
			trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, outcome)
			dispositions := runtimeKnowledgeQuestionDispositions(batch)
			if len(trace.Tasks[0].MissingAspects) != 1 || len(batch.Questions[0].MissingAspects) != 1 ||
				len(dispositions[0].MissingAspects) != 1 || dispositions[0].NeedsHandoff {
				t.Fatalf("a miss lost its boundary or authorized handoff: trace=%+v questions=%+v dispositions=%+v", trace.Tasks, batch.Questions, dispositions)
			}
			for _, stableID := range []bool{true, false} {
				id := ""
				if stableID {
					id = "T1"
				}
				plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
					TaskID: id, Intent: "hotel_info", Text: query, ResolvedText: query,
					NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true,
				}}}
				got := applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan, trace, batch.Questions).TaskPlans[0]
				if got.TaskID != "T1" || len(got.MissingAspects) != 1 || got.MissingAspects[0] != missing ||
					len(got.SupportedFacts) != 0 || got.SelectedLayer != "" || got.NeedsHumanRoute {
					t.Fatalf("missing evidence did not reach the existing reply task (stable=%v): %+v", stableID, got)
				}
			}
		})
	}
}

func TestKnowledgeMissPreservesPMSButDropsStaleKnowledge(t *testing.T) {
	for _, query := range []string{"房间太热，看看还有什么房", "浴室坏了，看看能不能换一间"} {
		t.Run(query, func(t *testing.T) {
			oldAnswer := "沿用旧的酒店办法。"
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "T1", Intent: "hotel_info", Text: query, NeedsKnowledge: true,
				OutputKind: "text", ReplyRequired: true, SelectedLayer: "store", AnswerText: &oldAnswer,
				PMSOutcome: &callbacks.PMSOutcomeTraceData{Status: "partial"},
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
					{FactID: "P1F1", Aspect: "pms_room_inventory", Statement: "当前有大床房库存。"},
					{FactID: "T1Old", Aspect: "method", Statement: oldAnswer},
				},
				MissingAspects: []string{"最终差价"},
			}}}
			trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{{
				TaskID: "T1", Decision: knowledgeEvidenceDecisionInsufficient,
				MissingAspects: []string{"本次故障的可用自助办法"},
			}}}
			got := applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan, trace, nil).TaskPlans[0]
			if len(got.SupportedFacts) != 1 || got.SupportedFacts[0].FactID != "P1F1" ||
				len(got.MissingAspects) != 2 || got.AnswerText != nil || got.PMSOutcome == nil || got.SelectedLayer != "" {
				t.Fatalf("a knowledge miss reused stale policy or discarded PMS: %+v", got)
			}
		})
	}
}

func TestKnowledgePartialGuidanceDoesNotInferPolicyFromAgentBackground(t *testing.T) {
	for _, mode := range []enums.KnowledgeAnswerMode{enums.KnowledgeAnswerModeStrict, enums.KnowledgeAnswerModeAssist} {
		instruction := buildKnowledgeRuntimeInstruction(mode)
		for _, required := range []string{
			"answerText是基于已选证据形成的可用回答",
			"不能把未知写成肯定或否定",
			"门店背景、客服角色说明、历史AI回复都不能替代本轮业务证据",
			"自助领取办法不证明是否提供送房",
			"missingAspects不是必须逐项告知客户的清单",
		} {
			if !strings.Contains(instruction, required) {
				t.Fatalf("mode %v lost partial-evidence boundary %q", mode, required)
			}
		}
	}
}

func TestKnowledgeJudgeSeparatesRejectedSelfServiceFromHandoffConsent(t *testing.T) {
	instruction := knowledgeEvidenceJudgeSystemPrompt()
	for _, required := range []string{
		"已被客户拒绝、无法采用或尝试失败的自助方案",
		"客户拒绝人工不等于酒店没有相关处理流程",
		"是否执行由程序依据客户许可处理",
	} {
		if !strings.Contains(instruction, required) {
			t.Fatalf("knowledge selection still confuses evidence applicability with execution consent: %q", required)
		}
	}
}

func TestKnowledgeProtocolIsolationKeepsGoalWithoutFabricatingFallbackEvidence(t *testing.T) {
	for _, query := range []string{"空调还是不冷，今晚怎么休息", "换房多少钱，还有毛巾在哪里拿"} {
		decision := buildRuntimeKnowledgeProtocolIsolationDecision([]runtimeKnowledgeQuestionDisposition{{
			TaskID: "T1", Query: query, NeedsRetry: true,
		}})
		if len(decision.Instructions) != 1 {
			t.Fatalf("missing isolated-task instruction: %+v", decision)
		}
		instruction := decision.Instructions[0].Content
		if strings.Contains(instruction, "runtime_safe_fallback") || strings.Contains(instruction, "提供的固定安全事实") {
			t.Fatalf("protocol failure manufactured evidence: %s", instruction)
		}
		for _, required := range []string{
			query, "不能使用这些任务的原始候选资料", "保留客户当前目标和真实缺口",
			"不得把固定兜底话术当作证据", "PMS事实和结构化资源继续按原来源",
			"不得因为该异常新增、取消或重复转人工",
		} {
			if !strings.Contains(instruction, required) {
				t.Fatalf("protocol isolation lost boundary %q: %s", required, instruction)
			}
		}
	}
}

func TestKnowledgeJudgeMergesAfterCompletedPMSRead(t *testing.T) {
	for _, query := range []string{"我这单能升房吗，会员有什么规定？", "帮我看看换大床还有没有房，酒店怎么规定？"} {
		t.Run(query, func(t *testing.T) {
			answer := "会员升房需要符合酒店权益条件。"
			pmsFact := callbacks.KnowledgeEvidenceFactTraceData{FactID: "P1F1", Aspect: "pms_room_inventory", Statement: "大床房全程有库存。"}
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "T1", Intent: "hotel_info", SubIntent: "room_upgrade", Text: query,
				NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true,
				PMSOutcome:     &callbacks.PMSOutcomeTraceData{Status: "partial"},
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{pmsFact},
				MissingAspects: []string{"最终差价暂未确认"},
			}}}
			trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{{
				TaskID: "T1", SelectedLayer: "store", SelectedCandidateIDs: []string{"C1"}, AnswerText: &answer,
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Aspect: "policy", Statement: answer}},
				MissingAspects: []string{"会员可用次数尚未确认"},
			}}}
			got := applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan, trace, []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: query}})
			task := got.TaskPlans[0]
			if len(task.SupportedFacts) != 2 || task.SupportedFacts[0].FactID != "P1F1" ||
				len(task.MissingAspects) != 2 || task.AnswerText != nil || task.PMSOutcome == nil {
				t.Fatalf("knowledge replaced another source or locked a partial answer: %+v", task)
			}
			if isolated, ids := isolateUngroundedKnowledgeReplyTasks(got); len(ids) != 0 || len(isolated.TaskPlans[0].SupportedFacts) != 2 {
				t.Fatalf("merged evidence became an ungrounded task: %+v ids=%v", isolated, ids)
			}
		})
	}
}

func TestKnowledgeHandoffFinalizedAfterPMSRead(t *testing.T) {
	for _, query := range []string{"帮我查一下能不能升房", "还有大床房吗，我想换过去"} {
		for _, status := range []string{"complete", "partial", "needs_input"} {
			t.Run(query+"/"+status, func(t *testing.T) {
				hit := rag.RetrieveResult{Content: "转接"}
				batch := &runtimeKnowledgeRetrieveBatch{
					Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: query,
						Disposition: runtimeKnowledgeDispositionDirectHandoff, HandoffHit: hit,
						Result: &retrievers.KnowledgeRetrieveResult{Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}}}},
					Merged: &retrievers.KnowledgeRetrieveResult{},
				}
				task := callbacks.ReplyTaskPlanTraceData{
					TaskID: "T1", Intent: "hotel_info", SubIntent: "room_upgrade", Text: query, NeedsKnowledge: true,
					OutputKind: "text", ReplyRequired: true, PMSOutcome: &callbacks.PMSOutcomeTraceData{Status: "ok"},
					SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P1F1", Aspect: "pms_room_inventory", Statement: "大床房有库存。"}},
				}
				switch status {
				case "partial":
					task.PMSOutcome.Status = "partial"
					task.MissingAspects = []string{"最终差价尚未确认"}
				case "needs_input":
					task.PMSOutcome.Status = "partial"
					task.PMSOutcome.MissingFields = []string{"customerLocator"}
					task.SupportedFacts = nil
					answer := runtimePMSOrderPhoneClarification
					task.AnswerText = &answer
				}
				collector := callbacks.NewRuntimeTraceCollector()
				collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{task}})
				trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{{
					TaskID: "T1", SelectedLayer: "store", Disposition: runtimeKnowledgeDispositionDirectHandoff,
				}}}
				deferRuntimeKnowledgeHandoffForPMSTasks(batch, collector, &trace)
				dispositions := runtimeKnowledgeQuestionDispositions(batch)
				if status == "partial" {
					if !dispositions[0].HasAnswer || !dispositions[0].NeedsHandoff {
						t.Fatalf("partial PMS facts must be answered before a valid independent directive: %+v", dispositions)
					}
					active := rebuildRuntimeKnowledgeReplyPlan(collector.Data.Pipeline.ReplyPlan, batch.Questions, dispositions, true)
					if !active.TaskPlans[0].ReplyRequired || len(active.TaskPlans[0].SupportedFacts) == 0 {
						t.Fatalf("partial PMS task was converted into a non-text handoff: %+v", active)
					}
					return
				}
				active := rebuildRuntimeKnowledgeReplyPlan(collector.Data.Pipeline.ReplyPlan, batch.Questions, nil, false)
				active = applyKnowledgeEvidenceJudgeTraceToReplyPlan(active, trace, batch.Questions)
				collector.SetReplyPlan(active)
				if dispositions[0].NeedsHandoff || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer ||
					collector.Data.Pipeline.ReplyPlan.TaskPlans[0].NeedsKnowledge {
					t.Fatalf("completed lookup or a necessary input must precede handoff: %+v trace=%+v", dispositions, trace)
				}
				if runtimeReplyTaskUsesKnowledge(collector.Data.Pipeline.ReplyPlan.TaskPlans[0]) {
					t.Fatal("resolved PMS task still became an ungrounded FAQ task")
				}
			})
		}
	}
}

func TestCompletedPMSReadSurvivesRealKnowledgeGateOrder(t *testing.T) {
	for _, query := range []string{"帮我查这单能不能换房", "我想升个大床房，你看看还有没有"} {
		t.Run(query, func(t *testing.T) {
			hit := rag.RetrieveResult{KnowledgeBaseID: 1, Content: "问题：" + query + "\n答案：转接", Score: .99}
			retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}, result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{1}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content,
			}}
			intent := hotelInfoIntent()
			intent.IntentTasks = []callbacks.IntentTaskTraceData{{
				Intent: "hotel_info", SubIntent: "room_upgrade", Text: query, ResolvedText: query, NeedsKnowledge: true,
			}}
			collector := callbacks.NewRuntimeTraceCollector()
			collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "T1", Intent: "hotel_info", SubIntent: "room_upgrade", Text: query, ResolvedText: query,
				NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "text_reply",
				PMSOutcome:     &callbacks.PMSOutcomeTraceData{Status: "ok"},
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P1F1", Aspect: "pms_room_inventory", Statement: "大床房有库存。"}},
			}}})
			summary := &RunResult{}
			_, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
				Request: newKnowledgePolicyRunInput(query, "1"), Summary: summary, Collector: collector, Intent: intent,
			})
			if err != nil || summary.handoffDirective || collector.Data.Pipeline.EvidenceJudge.DeferredHandoff {
				t.Fatalf("PMS success was rerouted by later Judge: err=%v summary=%+v trace=%+v", err, summary, collector.Data.Pipeline.EvidenceJudge)
			}
			task := collector.Data.Pipeline.ReplyPlan.TaskPlans[0]
			if len(task.SupportedFacts) != 1 || task.SupportedFacts[0].FactID != "P1F1" || !task.ReplyRequired {
				t.Fatalf("PMS evidence was lost in the live stage order: %+v", task)
			}
		})
	}
}

func TestDeclinedHandoffPreservesKnowledgeAndPMSFacts(t *testing.T) {
	for _, query := range []string{"毛巾在哪拿，空调坏了但先不要转人工", "告诉我有没大床房，别找同事"} {
		t.Run(query, func(t *testing.T) {
			task := callbacks.ReplyTaskPlanTraceData{
				TaskID: "T1", Intent: "service_request", SubIntent: "room_change", Text: query,
				NeedsKnowledge: true, NeedsHumanRoute: true, SelectedLayer: "store", SelectedCandidateIDs: []string{"C1"},
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
					{FactID: "P1F1", Aspect: "pms_room_inventory", Statement: "大床房有库存。"},
					{FactID: "T1F1", Aspect: "location", Statement: "毛巾可在洗衣房自取。"},
				}, MissingAspects: []string{"送到房间尚未确认"},
			}
			applyDeclinedKnowledgeHandoffReply(&task, query)
			applyDeclinedKnowledgeHandoffReply(&task, query)
			if len(task.SupportedFacts) != 3 || len(task.MissingAspects) != 1 || task.SelectedLayer != "store" ||
				task.NeedsHumanRoute || task.AnswerText != nil || !task.ReplyRequired {
				t.Fatalf("declining an action erased answers or duplicated its boundary: %+v", task)
			}
			if !strings.Contains(task.SupportedFacts[2].Statement, "先不转接") {
				t.Fatalf("missing explicit action boundary: %+v", task)
			}
		})
	}
}

func TestAnsweredHandoffDoesNotBecomeNoAnswerInstruction(t *testing.T) {
	for _, query := range []string{"毛巾在哪里拿，空调不制冷", "先说停车怎么走，房门打不开也帮我处理"} {
		pending := []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: query, HasAnswer: true, NeedsHandoff: true}}
		for _, enabled := range []bool{true, false} {
			instruction := buildDeferredRuntimeKnowledgeInstruction(pending, enabled)
			if strings.Contains(instruction, "完整无答案") || strings.Contains(instruction, "不得猜测、复述、概括或提及") ||
				!strings.Contains(instruction, "supportedFacts") {
				t.Fatalf("an answer plus action was instructed to omit its facts: %q", instruction)
			}
		}
	}
}

func TestTruncatedSynonymousEvidenceCannotAuthorizeHandoff(t *testing.T) {
	for _, query := range []string{"房间毛巾不够了", "能再拿条毛巾吗"} {
		t.Run(query, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{TaskID: "T1", Intent: "service_request", Query: query, Candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "C1", Layer: "store", Hit: rag.RetrieveResult{Content: "问题：毛巾不够\n答案：转接", Score: .99}},
				{CandidateID: "C2", Layer: "store", Hit: rag.RetrieveResult{Content: "问题：可以多拿浴巾吗\n答案：可在洗衣房自取。" + strings.Repeat("使用后放回回收处。", 100), Score: .9}},
			}}
			limited := limitKnowledgeEvidenceJudgeInput([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeCandidateInputBytes(task.Candidates[0]))
			if len(limited) != 1 || knowledgeEvidenceCandidateBoundaryComplete(limited[0]) {
				t.Fatalf("truncated source scope was not retained: %+v", limited)
			}
			batch := structuralKnowledgeBatch(limited[0])
			outcome := knowledgeEvidenceJudgeOutcome{Applied: true, Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {"store": {Decision: knowledgeEvidenceDecisionInsufficient, HandoffCandidateIDs: []string{"C1"}}},
			}}
			trace := applyKnowledgeEvidenceJudgeOutcome(batch, limited, outcome)
			dispositions := runtimeKnowledgeQuestionDispositions(batch)
			if dispositions[0].NeedsHandoff || batch.Questions[0].HandoffHit.Content != "" ||
				trace.Tasks[0].DecisionSource != "candidate_boundary_incomplete" {
				t.Fatalf("short instruction gained authority from an incomplete input: %+v trace=%+v", dispositions, trace.Tasks)
			}
		})
	}
}
