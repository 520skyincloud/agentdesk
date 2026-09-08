package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
)

type coverageTestJudge func(context.Context, RunInput, []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome

func (judge coverageTestJudge) JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	return judge(ctx, req, tasks)
}

func coverageTestIntent(texts ...string) callbacks.IntentTraceData {
	intent := callbacks.IntentTraceData{PrimaryIntent: "hotel_info", NeedsKnowledge: true, SemanticContractExpected: true, SourceRefsValidated: true}
	for _, text := range texts {
		intent.IntentTasks = append(intent.IntentTasks, callbacks.IntentTaskTraceData{
			Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "availability",
			Text: text, ResolvedText: text, SourceRefs: []string{"U1"}, NeedsKnowledge: true,
			RelationToPrevious: "independent", ResolutionState: "clear",
		})
	}
	return intent
}

func TestQuestionCoverageValidatesOnlyProtocolAndSource(t *testing.T) {
	input := &runtimeQuestionCoverageInput{
		Sources: []runtimeQuestionCoverageSource{{Ref: "U1", Text: "咖啡、毛巾有吗"}},
		Tasks:   []runtimeQuestionCoverageTask{{TaskID: "task-1"}},
	}
	valid := runtimeQuestionCoverage{Status: "repair_required", Issues: []runtimeQuestionCoverageIssue{
		{Kind: "merged_questions", TaskID: "task-1", SourceRef: "U1", Text: "咖啡、毛巾有吗", Reason: "不同物品需分别回答"},
	}}
	if err := validateRuntimeQuestionCoverage(&valid, input); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*runtimeQuestionCoverage){
		"unknown task":                      func(c *runtimeQuestionCoverage) { c.Issues[0].TaskID = "unknown" },
		"unknown source":                    func(c *runtimeQuestionCoverage) { c.Issues[0].SourceRef = "U9" },
		"invented question":                 func(c *runtimeQuestionCoverage) { c.Issues[0].Text = "早餐有没有" },
		"false completion":                  func(c *runtimeQuestionCoverage) { c.Status = "complete" },
		"evidence shortage is not omission": func(c *runtimeQuestionCoverage) { c.Issues[0].Kind = "no_evidence" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := valid
			copy.Issues = append([]runtimeQuestionCoverageIssue(nil), valid.Issues...)
			mutate(&copy)
			if validateRuntimeQuestionCoverage(&copy, input) == nil {
				t.Fatal("accepted invalid coverage")
			}
		})
	}
	if err := validateRuntimeQuestionCoverage(&runtimeQuestionCoverage{Status: "complete"}, input); err != nil {
		t.Fatal(err)
	}
	if _, err := parseRuntimeQuestionCoverage(`{"tasks":[]}`, input); err == nil {
		t.Fatal("missing coverage must not be counted complete")
	}
}

func TestQuestionCoverageRepairPreservesOtherTaskIDsAndQueries(t *testing.T) {
	oldIntent := coverageTestIntent("咖啡和毛巾有吗", "停车收费吗")
	oldPlan := buildReplyPlan(oldIntent, selectIntentPromptPack(oldIntent))
	req := RunInput{UserMessage: models.Message{Content: "咖啡和毛巾有吗，停车收费吗"}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = oldIntent
	collector.SetReplyPlan(oldPlan)
	state := &answerabilityGateState{Input: answerabilityGateInput{Request: req, Intent: oldIntent, Collector: collector}}
	input := buildRuntimeQuestionCoverageInput(req, oldPlan)
	coverage := &runtimeQuestionCoverage{Status: "repair_required", Issues: []runtimeQuestionCoverageIssue{
		{Kind: "merged_questions", TaskID: oldPlan.TaskPlans[0].TaskID, SourceRef: "U1", Text: "咖啡和毛巾有吗", Reason: "两个独立答案"},
	}}
	oldBatch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{
		{TaskID: oldPlan.TaskPlans[0].TaskID, Query: "咖啡和毛巾有吗", Result: &retrievers.KnowledgeRetrieveResult{}},
		{TaskID: oldPlan.TaskPlans[1].TaskID, Query: "停车收费吗", Result: &retrievers.KnowledgeRetrieveResult{ContextText: "停车免费"}},
	}}
	oldTasks := []knowledgeEvidenceJudgeTask{
		{TaskID: oldPlan.TaskPlans[0].TaskID, Coverage: input},
		{TaskID: oldPlan.TaskPlans[1].TaskID},
	}
	oldSelection := map[string]knowledgeEvidenceLayerSelection{
		"store": {Decision: "direct_single", SelectedCandidateIDs: []string{"parking-C1"}},
	}
	outcome := knowledgeEvidenceJudgeOutcome{
		Applied: true, Coverage: coverage,
		Selections: map[string]map[string]knowledgeEvidenceLayerSelection{oldPlan.TaskPlans[1].TaskID: oldSelection},
	}
	hit := rag.RetrieveResult{KnowledgeBaseID: 1, Content: "问题：有用品吗\n答案：提供所需用品。", Score: .9}
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}, result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{hit}}}
	intentCalls, judgeCalls := 0, 0
	gate := &KnowledgeAnswerabilityGate{
		repairIntent: func(context.Context, RunInput, callbacks.IntentTraceData, *runtimeQuestionCoverageInput, []runtimeQuestionCoverageIssue) (callbacks.IntentTraceData, error) {
			intentCalls++
			return coverageTestIntent("咖啡", "毛巾有吗", "停车收费吗"), nil
		},
		judge: coverageTestJudge(func(_ context.Context, _ RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
			judgeCalls++
			if len(tasks) != 2 || len(tasks[0].Coverage.Tasks) != 3 {
				t.Fatalf("only changed tasks need evidence judging, with full coverage context: %#v", tasks)
			}
			selections := make(map[string]map[string]knowledgeEvidenceLayerSelection)
			for _, task := range tasks {
				selections[task.TaskID] = map[string]knowledgeEvidenceLayerSelection{"store": {Decision: "direct_single"}}
			}
			return knowledgeEvidenceJudgeOutcome{Applied: true, Coverage: &runtimeQuestionCoverage{Status: "complete"}, Selections: selections}
		}),
	}
	batch, tasks, got, err := gate.repairQuestionCoverageOnce(context.Background(), state, retriever,
		retrievers.DefaultKnowledgeRetrieveOptions(), oldBatch, oldTasks, outcome, []int64{1}, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if intentCalls != 1 || judgeCalls != 1 || len(retriever.queries) != 2 {
		t.Fatalf("repair calls intent=%d judge=%d queries=%v", intentCalls, judgeCalls, retriever.queries)
	}
	if len(batch.Questions) != 3 || len(tasks) != 3 || batch.Questions[2].TaskID != oldPlan.TaskPlans[1].TaskID {
		t.Fatalf("lost original order or unaffected ID: %#v", batch.Questions)
	}
	if !reflect.DeepEqual(got.Selections[oldPlan.TaskPlans[1].TaskID], oldSelection) {
		t.Fatal("unaffected evidence was overwritten")
	}
	for _, query := range retriever.queries {
		if query == "停车收费吗" || query == "咖啡和毛巾有吗" {
			t.Fatalf("repeated old retrieval: %q", query)
		}
	}

	// A second invalid coverage response must stop, not start another repair.
	collector.SetReplyPlan(oldPlan)
	state.Input.Intent = oldIntent
	gate.judge = coverageTestJudge(func(_ context.Context, _ RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		judgeCalls++
		return knowledgeEvidenceJudgeOutcome{Applied: true, Coverage: &runtimeQuestionCoverage{
			Status: "repair_required",
			Issues: []runtimeQuestionCoverageIssue{{Kind: "missing_question", SourceRef: "U1", Text: "毛巾有吗", Reason: "仍有漏题"}},
		}}
	})
	_, _, _, err = gate.repairQuestionCoverageOnce(context.Background(), state, retriever,
		retrievers.DefaultKnowledgeRetrieveOptions(), oldBatch, oldTasks, outcome, []int64{1}, []int64{1})
	if err == nil || intentCalls != 2 || judgeCalls != 2 {
		t.Fatalf("repair was not bounded: intent=%d judge=%d err=%v", intentCalls, judgeCalls, err)
	}
	if !reflect.DeepEqual(collector.Data.Pipeline.ReplyPlan, oldPlan) {
		t.Fatal("failed repair published an incomplete plan")
	}
}

func TestQuestionCoverageFiveObjectsRetrieveIndependentlyWithinBudget(t *testing.T) {
	texts := []string{"有没有咖啡", "有没有剃须刀", "有没有牙刷", "有没有毛巾", "能不能给我纸笔"}
	intent := coverageTestIntent(texts...)
	plan := buildReplyPlan(intent, selectIntentPromptPack(intent))
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1, 2}, resultsByQuery: make(map[string]*retrievers.KnowledgeRetrieveResult)}
	for _, text := range texts {
		hits := make([]rag.RetrieveResult, 0)
		for _, kb := range []int64{1, 2} {
			for duplicate := 0; duplicate < 5; duplicate++ {
				hits = append(hits, rag.RetrieveResult{KnowledgeBaseID: kb, Content: "问题：" + text + "\n答案：该物品可在指定位置领取。", Score: .9})
			}
		}
		retriever.resultsByQuery[text] = &retrievers.KnowledgeRetrieveResult{RawHits: hits}
	}
	batch, err := retrieveContextForRuntimeQuestions(context.Background(), retriever, retrievers.DefaultKnowledgeRetrieveOptions(),
		strings.Join(texts, "，"), intent, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSliceSetEqual(retriever.queries, texts) || len(batch.Questions) != 5 {
		t.Fatalf("not one query per independent object: %v", retriever.queries)
	}
	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1, 2}, nil, strings.Join(texts, "，"), intent)
	if len(tasks) != 5 {
		t.Fatalf("candidate budget lost a question: %d", len(tasks))
	}
	total := 0
	for _, task := range tasks {
		total += len(task.Candidates)
		if len(task.Candidates) != 2 {
			t.Fatalf("exact duplicates consumed per-task budget: %+v", task.Candidates)
		}
	}
	if total > knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("candidate budget exceeded: %d", total)
	}
}

func TestQuestionCoverageKeepsTasksWithoutCandidatesVisible(t *testing.T) {
	intent := coverageTestIntent("能换纸币吗")
	plan := buildReplyPlan(intent, selectIntentPromptPack(intent))
	tasks := appendRuntimeCoverageOnlyJudgeTasks(nil, plan, nil)
	if len(tasks) != 1 || len(tasks[0].Candidates) != 0 {
		t.Fatalf("no-hit question disappeared from coverage: %+v", tasks)
	}
}

func TestQuestionCoverageCompleteDoesNotRetryInsufficientEvidence(t *testing.T) {
	calls := 0
	gate := &KnowledgeAnswerabilityGate{
		repairIntent: func(context.Context, RunInput, callbacks.IntentTraceData, *runtimeQuestionCoverageInput, []runtimeQuestionCoverageIssue) (callbacks.IntentTraceData, error) {
			calls++
			return callbacks.IntentTraceData{}, errors.New("must not call")
		},
	}
	input := &runtimeQuestionCoverageInput{Sources: []runtimeQuestionCoverageSource{{Ref: "U1", Text: "能换纸币吗"}}}
	outcome := knowledgeEvidenceJudgeOutcome{
		Coverage:   &runtimeQuestionCoverage{Status: "complete"},
		Selections: map[string]map[string]knowledgeEvidenceLayerSelection{"task-1": {"store": {Decision: "insufficient"}}},
	}
	_, _, got, err := gate.repairQuestionCoverageOnce(context.Background(), nil, nil, retrievers.KnowledgeRetrieveOptions{},
		nil, []knowledgeEvidenceJudgeTask{{TaskID: "task-1", Coverage: input}}, outcome, nil, nil)
	if err != nil || calls != 0 || got.Selections["task-1"]["store"].Decision != "insufficient" {
		t.Fatalf("normal evidence shortage retried: calls=%d outcome=%+v err=%v", calls, got, err)
	}
}

func TestQuestionCoverageRepairRejectsChangingUntouchedQuestion(t *testing.T) {
	old := coverageTestIntent("咖啡和毛巾有吗", "停车收费吗")
	oldPlan := buildReplyPlan(old, selectIntentPromptPack(old))
	req := RunInput{UserMessage: models.Message{Content: "咖啡和毛巾有吗，停车收费吗"}}
	input := buildRuntimeQuestionCoverageInput(req, oldPlan)
	issues := []runtimeQuestionCoverageIssue{{TaskID: oldPlan.TaskPlans[0].TaskID}}
	for _, texts := range [][]string{
		{"咖啡", "毛巾有吗"},                // Dropped a valid task.
		{"停车收费吗", "咖啡", "毛巾有吗"},       // Changed current-source order.
		{"咖啡", "咖啡", "毛巾有吗", "停车收费吗"}, // Duplicate task.
	} {
		intent := coverageTestIntent(texts...)
		plan := buildReplyPlan(intent, selectIntentPromptPack(intent))
		if _, _, err := reconcileRuntimeQuestionRepairPlan(oldPlan, plan, input, issues); err == nil {
			t.Fatalf("accepted invalid repair: %v", texts)
		}
	}
}

func TestQuestionCoveragePromptSeparatesTaskAndEvidenceFailures(t *testing.T) {
	prompt := runtimeQuestionCoverageInstruction()
	for _, text := range []string{"不是 missing_question", "比较、交集", "背景、礼貌", "实际跳过了该问题的检索", "真正指代不明"} {
		if !strings.Contains(prompt, text) {
			t.Errorf("missing coverage boundary %q", text)
		}
	}
	if !strings.HasPrefix(runtimeIntentDetectSystemPrompt(), runtimeQuestionFirstIntentInstruction()) {
		t.Fatal("question-first contract must precede profile classification")
	}
	if !strings.Contains(runtimeQuestionFirstIntentInstruction(), "客户问题能否理解，不是酒店是否能做到") {
		t.Fatal("unknown answer must not become an ambiguous question")
	}
	if !strings.Contains(runtimeQuestionFirstIntentInstruction(), "回指多个对象时也先解析本轮实际对象集合") ||
		!strings.Contains(prompt, "resolvedText 已列出多个独立对象却仍共用一条检索任务") {
		t.Fatal("contextual plural requests must follow the same per-object coverage contract")
	}
	judgePrompt := knowledgeEvidenceJudgeSystemPrompt()
	applicability := strings.Index(judgePrompt, "先检查方案适用性")
	completeness := strings.Index(judgePrompt, "事实维度完整性检查")
	if applicability < 0 || applicability >= completeness {
		t.Fatal("an unusable solution must be excluded before retaining partial facts")
	}
	intent := coverageTestIntent("咖啡有吗")
	intent.IntentTasks[0].EvidenceQuery = "酒店咖啡"
	repairPrompt := runtimeQuestionRepairInstruction(&runtimeQuestionRepairRequest{
		Coverage: &runtimeQuestionCoverageInput{}, IntentTasks: intent.IntentTasks,
	})
	for _, field := range []string{`"previousIntentTasks"`, `"subIntent"`, `"evidenceQuery":"酒店咖啡"`, `"resolutionState"`} {
		if !strings.Contains(repairPrompt, field) {
			t.Fatalf("repair cannot preserve an omitted original task field: %s", field)
		}
	}
}

func TestQuestionCoverageContextRepairRetainsSourceAndSeparateQueries(t *testing.T) {
	const current = "那有的这些东西去哪里拿？"
	old := coverageTestIntent(current)
	old.IntentTasks[0].ResolvedText = "咖啡、剃须刀、牙刷、毛巾去哪里拿？"
	oldPlan := buildReplyPlan(old, selectIntentPromptPack(old))
	req := RunInput{UserMessage: models.Message{Content: current}}
	input := buildRuntimeQuestionCoverageInput(req, oldPlan)
	coverage := &runtimeQuestionCoverage{Status: "repair_required", Issues: []runtimeQuestionCoverageIssue{
		{Kind: "merged_questions", TaskID: oldPlan.TaskPlans[0].TaskID, SourceRef: "U1", Text: current, Reason: "回指四个独立对象共用检索"},
	}}
	if err := validateRuntimeQuestionCoverage(coverage, input); err != nil {
		t.Fatal(err)
	}
	repaired := coverageTestIntent(current, current, current, current)
	queries := []string{"咖啡在哪里拿", "剃须刀在哪里拿", "牙刷在哪里拿", "毛巾在哪里拿"}
	for i := range repaired.IntentTasks {
		task := &repaired.IntentTasks[i]
		task.Objective = "method"
		task.RelationToPrevious = "follow_up"
		task.ResolutionState = "resolved_from_context"
		task.ResolvedText, task.EvidenceQuery = queries[i], queries[i]
	}
	plan, changed, err := reconcileRuntimeQuestionRepairPlan(oldPlan,
		buildReplyPlan(repaired, selectIntentPromptPack(repaired)), input, coverage.Issues)
	if err != nil || len(changed) != 4 {
		t.Fatalf("shared current source was rejected as duplicate: changed=%v err=%v", changed, err)
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result:           &retrievers.KnowledgeRetrieveResult{},
	}
	batch, err := retrieveContextForRuntimeQuestions(context.Background(), retriever,
		retrievers.DefaultKnowledgeRetrieveOptions(), current, repaired, plan)
	if err != nil || len(batch.Questions) != 4 || !stringSliceSetEqual(retriever.queries, queries) {
		t.Fatalf("contextual queries merged or lost: queries=%v err=%v", retriever.queries, err)
	}
}

func TestQuestionCoverageRetainsJudgeFailureReason(t *testing.T) {
	gate := &KnowledgeAnswerabilityGate{}
	outcome := knowledgeEvidenceJudgeOutcome{Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
		ErrorMessage: "provider request timed out",
	}}
	_, _, _, err := gate.repairQuestionCoverageOnce(context.Background(), nil, nil, retrievers.KnowledgeRetrieveOptions{},
		nil, []knowledgeEvidenceJudgeTask{{Coverage: &runtimeQuestionCoverageInput{}}}, outcome, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "provider request timed out") {
		t.Fatalf("lost actual Judge error: %v", err)
	}
}

func TestQuestionCoverageClarificationRemainsAQuestionAtGeneration(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", SubIntent: "supplies_self_help", Text: "有用品吗", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "interaction", SubIntent: "clarify", Text: "那个可以吗", OutputKind: "text", ReplyRequired: true},
	}}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != 2 || groups[0].ClarificationOnly || !groups[1].ClarificationOnly {
		t.Fatalf("clarification mode lost or applied to another task: %+v", groups)
	}
	prompt := buildMultiReplyOutputInstruction(plan, true)
	if !strings.Contains(prompt, "T2：那个可以吗（仅澄清") || !strings.Contains(prompt, "不得擅自回答酒店有或没有") {
		t.Fatal("Generate did not receive the existing clarification task mode")
	}
	plan.TaskPlans = plan.TaskPlans[1:]
	if prompt := buildMultiReplyOutputInstruction(plan, false); !strings.Contains(prompt, "仅澄清") {
		t.Fatal("single clarification task lost its mode")
	}
}
