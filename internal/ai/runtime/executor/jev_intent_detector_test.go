package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"agent-desk/internal/ai/jev"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

func TestJevIntentSixQuestionsSharePhysicalSourceAndRetainOrder(t *testing.T) {
	texts := []string{"酒店有停车场吗？", "几点退房？", "帮我查订单。", "会员有哪些权益？", "能升房吗？", "发一下入住小程序。"}
	sources := []adapter.CurrentTurnSource{{Ref: "U1", Text: strings.Join(texts, "")}}
	choices := map[string]string{"U1_count": "6"}
	offset := 0
	for index, text := range texts {
		if index > 0 {
			choices[fmt.Sprintf("U1_start_%d", index+1)] = strconv.Itoa(offset)
		}
		offset += len([]rune(text))
	}
	spans := jevTestSegment(t, sources, choices)
	if len(spans) != len(texts) {
		t.Fatalf("got %d tasks, want six: %#v", len(spans), spans)
	}
	routes := []string{"parking", "checkout_process", "order_query", "member_benefits", "room_upgrade", "provide_mini_program"}
	for index, span := range spans {
		wantRef := fmt.Sprintf("T%d", index+1)
		if span.Ref != wantRef || span.SourceRef != "U1" || span.Text != texts[index] {
			t.Fatalf("task %d changed text, order or provenance: %#v", index, span)
		}
		choices[span.Ref+"_route"] = routes[index]
	}
	classification, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	result, err := buildIntentTraceFromJev(jevTestResponse(classification, choices, nil), spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range result.IntentTasks {
		if task.Text != texts[index] || task.SubIntent != routes[index] || !reflect.DeepEqual(task.SourceRefs, []string{"U1"}) {
			t.Fatalf("mapped task %d lost its question: %#v", index, task)
		}
	}
	if !result.NeedsKnowledge || !result.NeedsTool || !result.NeedsResource {
		t.Fatalf("mixed knowledge/PMS/resource capabilities lost: %#v", result)
	}
}

func TestJevBoundariesDoNotSplitIdentifiersOrAssumePunctuation(t *testing.T) {
	text := "手机号13800138000订单ABC-123查一下还有停车场吗"
	sources := []adapter.CurrentTurnSource{{Ref: "U1", Text: text}}
	runes := []rune(text)
	for index := 1; index < len(runes); index++ {
		if runes[index] >= '0' && runes[index] <= '9' && runes[index-1] >= '0' && runes[index-1] <= '9' {
			if jevCandidateBoundary(runes, index) {
				t.Fatalf("identifier has a candidate boundary at %d", index)
			}
		}
	}
	offset := len([]rune("手机号13800138000订单ABC-123查一下"))
	spans := jevTestSegment(t, sources, map[string]string{"U1_count": "2", "U1_start_2": strconv.Itoa(offset)})
	if len(spans) != 2 || spans[0].Text+spans[1].Text != text || spans[1].Text != "还有停车场吗" {
		t.Fatalf("unpunctuated independent request not preserved: %#v", spans)
	}
	one := jevTestSegment(t, []adapter.CurrentTurnSource{{Ref: "U1", Text: "不是这个号码，是13800138000。"}}, nil)
	if len(one) != 1 || one[0].Text != "不是这个号码，是13800138000。" {
		t.Fatalf("punctuation must not independently split or rewrite a correction: %#v", one)
	}
	withSlotAfterPeriod := "帮我查订单。手机号13800138000，还有停车场吗"
	parkingOffset := len([]rune("帮我查订单。手机号13800138000，"))
	spans = jevTestSegment(t, []adapter.CurrentTurnSource{{Ref: "U1", Text: withSlotAfterPeriod}}, map[string]string{
		"U1_count": "2", "U1_terminal_alignment": "not_exact", "U1_start_2": strconv.Itoa(parkingOffset),
	})
	if len(spans) != 2 || spans[0].Text != "帮我查订单。手机号13800138000，" || spans[1].Text != "还有停车场吗" {
		t.Fatalf("sentence punctuation split the order lookup from its phone slot: %#v", spans)
	}
}

func TestJevSegmentationKeepsDecisionDimensionsInOneGoal(t *testing.T) {
	for _, text := range []string{
		"能不能升大床房，有没有房，会员能免差价吗，要补多少？",
		"房间太吵想换房，今晚有没有别的房，差价多少？",
	} {
		checked := false
		spans, err := segmentJevIntentSources([]adapter.CurrentTurnSource{{Ref: "U1", Text: text}}, jevIntentState{}, func(_ any, questions map[string]jev.Question) (jev.Response, error) {
			if question, ok := questions["U1_count"]; ok {
				instructions, _ := question.Instructions.(map[string]any)
				rule, _ := instructions["question"].(string)
				if !strings.Contains(rule, "dimensions of that one decision goal") {
					t.Fatalf("decision dimensions are not grouped in JEV segmentation: %s", rule)
				}
				checked = true
			}
			return jevTestResponse(questions, map[string]string{"U1_count": "1"}, nil), nil
		})
		if err != nil || !checked || len(spans) != 1 || spans[0].Text != text {
			t.Fatalf("decision request was not preserved as one goal: spans=%#v checked=%v err=%v", spans, checked, err)
		}
	}
}

func TestJevRouteCriteriaSeparateBroadDecisionsFromStandaloneFollowups(t *testing.T) {
	criteria := jevIntentRouteCriteria()
	for _, route := range []string{"room_upgrade", "room_change", "late_checkout", "renewal"} {
		description, _ := criteria[route].(string)
		if !strings.Contains(description, "One ") || !strings.Contains(description, " when asked together") {
			t.Fatalf("%s must describe one combined customer decision: %q", route, description)
		}
	}
	for _, route := range []string{"price_difference", "upgrade_eligibility"} {
		description, _ := criteria[route].(string)
		if !strings.Contains(description, "standalone") || !strings.Contains(description, "broader current") {
			t.Fatalf("%s must remain a standalone follow-up only: %q", route, description)
		}
	}
}

func TestJevRouteCriteriaKeepsSpecificOrderCheckoutFollowupsOnPMS(t *testing.T) {
	criteria := jevIntentRouteCriteria()
	checkout, _ := criteria["checkout_process"].(string)
	orderDetail, _ := criteria["order_detail"].(string)
	if !strings.Contains(checkout, "no specific order") || !strings.Contains(checkout, "selected history context") {
		t.Fatalf("general checkout route must exclude an identified order: %q", checkout)
	}
	if !strings.Contains(orderDetail, "original/latest checkout time") {
		t.Fatalf("specific order checkout follow-ups must remain order_detail: %q", orderDetail)
	}
	for _, phrase := range []string{"this order", "my original/latest checkout time"} {
		if !strings.Contains(jevIntentClassificationRules, phrase) {
			t.Fatalf("classification rules are missing contextual order phrase %q", phrase)
		}
	}
}

func TestJevCurrentContextUsesEarlierSpansWithoutChangingSource(t *testing.T) {
	message := models.Message{
		ID: 103, MessageType: enums.IMMessageTypeText,
		Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
			"1. [消息101] 帮我查询订单",
			"2. [消息102] 13800138000",
			"3. [消息103] 酒店有停车场吗",
		}),
	}
	sources := adapter.BuildCurrentTurnSources(message)
	spans := jevTestSegment(t, sources, nil)
	state := buildJevIntentState(RunInput{UserMessage: message}, adapter.HistoryBuildResult{}, sources)
	questions, contexts := buildJevClassificationQuestions(spans, state)
	options := jevTestCriteria(questions["T2_context"].Criteria)
	if _, exists := options["T1"]; !exists {
		t.Fatal("second message cannot reference the first message")
	}
	for _, forbidden := range []string{"T2", "T3"} {
		if _, exists := options[forbidden]; exists {
			t.Fatalf("context options include self/future task %s", forbidden)
		}
	}
	response := jevTestResponse(questions, map[string]string{
		"T1_route": "order_query",
		"T2_route": "order_query", "T2_context": "T1",
		"T2_relation": "clarification_answer", "T2_resolution": "resolved_from_context",
		"T3_route": "parking",
	}, nil)
	result, err := buildIntentTraceFromJev(response, spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	task := result.IntentTasks[1]
	if task.Text != "13800138000" || task.RelationToPrevious != "independent" ||
		!reflect.DeepEqual(task.SourceRefs, []string{"U2", "U1"}) ||
		!strings.Contains(task.ResolvedText, "帮我查询订单") || !task.NeedsTool {
		t.Fatalf("same-turn slot lost order topic or provenance: %#v", task)
	}
	if result.IntentTasks[2].ResolvedText != "酒店有停车场吗" {
		t.Fatalf("new topic inherited previous lookup: %#v", result.IntentTasks[2])
	}
}

func TestJevHistoryReferencesRemainStableAndExcludeAssistantFacts(t *testing.T) {
	history := adapter.HistoryBuildResult{}
	for index := 0; index < 20; index++ {
		role := enums.IMSenderTypeCustomer
		if index%2 == 1 {
			role = enums.IMSenderTypeAI
		}
		history.RawItems = append(history.RawItems, models.Message{
			ID: int64(index + 1), SenderType: role, MessageType: enums.IMMessageTypeText,
			Content: fmt.Sprintf("历史消息%d", index),
		})
	}
	history.RawItems[18].Content = "用13800138000查我的订单"
	history.RawItems[19].Content = "错误的客服答复13900139000已退房"
	req := RunInput{UserMessage: models.Message{ID: 30, Content: "号码写错了，是13700137000", MessageType: enums.IMMessageTypeText}}
	sources := adapter.BuildCurrentTurnSources(req.UserMessage)
	state := buildJevIntentState(req, history, sources)
	if len(state.History) != 15 || state.History[0].Ref != "H5" || state.History[14].Ref != "H19" {
		t.Fatalf("bounded history has unstable references: %#v", state.History)
	}
	if state.History[13].Role != "customer" || state.History[14].Role != "service" {
		t.Fatalf("history speaker roles lost: %#v", state.History[13:])
	}
	spans := jevTestSegment(t, sources, nil)
	questions, contexts := buildJevClassificationQuestions(spans, state)
	if _, exists := contexts["H19"]; exists {
		t.Fatal("assistant facts can be selected as customer-provided context")
	}
	if contexts["H18"].Text != state.History[13].Text || !strings.Contains(contexts["H18"].Text, history.RawItems[18].Content) {
		t.Fatalf("selected context does not match its stable reference: %#v", contexts["H18"])
	}
	response := jevTestResponse(questions, map[string]string{
		"T1_route": "order_query", "T1_relation": "correction",
		"T1_resolution": "resolved_from_context", "T1_context": "H18",
	}, nil)
	result, err := buildIntentTraceFromJev(response, spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	task := result.IntentTasks[0]
	if task.SubIntent != "order_query" || !task.NeedsTool || task.NeedsHumanRoute ||
		task.RelationToPrevious != "correction" || !strings.Contains(task.ResolvedText, "13700137000") ||
		strings.Contains(task.ResolvedText, "13900139000") {
		t.Fatalf("phone correction lost route or inherited assistant facts: %#v", task)
	}
	response.Answers["T1_context"] = jev.Answer{Type: "choice", Choice: "H19"}
	if _, err := buildIntentTraceFromJev(response, spans, contexts); err == nil {
		t.Fatal("unselectable assistant context should fail mapping")
	}
}

func TestJevNormalizationRetainsHumanAuthorizationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, text, route, objective string
		handoff                      bool
	}{
		{name: "explicit", text: "帮我转人工", route: "explicit_handoff", objective: "action_request", handoff: true},
		{name: "denied_even_if_model_misclassifies", text: "不要转人工，先回答我", route: "explicit_handoff", objective: "action_request"},
		{name: "cancel", text: "取消转人工", route: "explicit_handoff", objective: "cancel"},
		{name: "service", text: "帮忙送条毛巾", route: "room_supplies", objective: "action_request"},
		{name: "correction", text: "你回答错了", route: "answer_rejected", objective: "explanation"},
		{name: "emergency_without_explicit_handoff", text: "有人受伤流血了", route: "emergency_safety", objective: "action_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := RunInput{UserMessage: models.Message{Content: tc.text, MessageType: enums.IMMessageTypeText}}
			spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: tc.text}}
			questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
			intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
				"T1_route": tc.route, "T1_objective": tc.objective,
			}, nil), spans, contexts)
			if err != nil {
				t.Fatal(err)
			}
			intent.SemanticContractExpected, intent.SourceRefsValidated = true, true
			intent = normalizeModelIntentTrace(intent, req, adapter.HistoryBuildResult{}, nil)
			if intent.NeedsHumanRoute != tc.handoff {
				t.Fatalf("human-route authorization changed: %#v", intent)
			}
		})
	}
}

func TestJevCheckinNormalizationAttachesOneResourceWithoutLosingQuestion(t *testing.T) {
	req := RunInput{UserMessage: models.Message{Content: "我应该怎么入住", MessageType: enums.IMMessageTypeText}}
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: req.UserMessage.Content}}
	questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
		"T1_route": "checkin_process", "T1_objective": "method",
	}, nil), spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	intent.SemanticContractExpected, intent.SourceRefsValidated = true, true
	intent = normalizeModelIntentTrace(intent, req, adapter.HistoryBuildResult{}, nil)
	plan := buildReplyPlan(intent, selectIntentPromptPack(intent))
	resources, knowledge := 0, 0
	for _, task := range plan.TaskPlans {
		if task.OutputKind == "resource" && task.ResourceAction == "provide_mini_program" {
			resources++
			if !reflect.DeepEqual(task.SourceRefs, []string{"U1"}) || task.OriginalText != req.UserMessage.Content {
				t.Fatalf("check-in card lost parent question: %#v", task)
			}
		}
		if task.OutputKind == "text" && task.NeedsKnowledge {
			knowledge++
		}
	}
	if resources != 1 || knowledge != 1 {
		t.Fatalf("want one knowledge response plus one card, got %#v", plan.TaskPlans)
	}
}

func TestJevIntentConfigNeverInheritsOtherProviderCredentials(t *testing.T) {
	t.Setenv(jevAPIKeyEnv, "")
	t.Setenv(jevBaseURLEnv, "")
	t.Setenv(jevModelEnv, "")
	previous := models.AIConfig{ID: 87, APIKey: "old-provider-secret", BaseURL: "https://old-provider.invalid/v1", ModelName: "old-model"}
	config := applyJevIntentConfig(previous)
	if config.APIKey != "" || config.ID != 0 || config.BaseURL != jev.DefaultBaseURL || config.ModelName != jev.DefaultModel ||
		config.Provider != enums.AIProviderTypeSafeJev {
		t.Fatalf("JEV inherited another model's credentials or identity: %#v", config)
	}
	if _, err := jev.NewClient(config.BaseURL, config.APIKey, runtimeIntentDetectTimeout); err == nil {
		t.Fatal("missing JEV key should fail, not borrow another provider credential")
	}
}

func TestJevCoverageRepairFeedbackIsIncludedInTypedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request jev.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		encoded, err := json.Marshal(request.State)
		if err != nil {
			t.Error(err)
		}
		state := string(encoded)
		for _, required := range []string{"repair", "previousTasks", "previousIntentTasks", "coverageIssues", "missing_question"} {
			if !strings.Contains(state, required) {
				t.Errorf("repair feedback missing %q from JEV state: %s", required, state)
			}
		}
		jevTestWriteResponse(t, w, request.Questions, map[string]string{"T1_route": "parking"}, nil)
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	repair := &runtimeQuestionRepairRequest{
		Coverage:    &runtimeQuestionCoverageInput{Tasks: []runtimeQuestionCoverageTask{{TaskID: "task-1", Text: "酒店有停车场吗"}}},
		IntentTasks: []callbacks.IntentTaskTraceData{{Text: "酒店有停车场吗", SubIntent: "parking"}},
		Issues:      []runtimeQuestionCoverageIssue{{Kind: "missing_question", SourceRef: "U1", Text: "酒店有停车场吗", Reason: "missing"}},
	}
	ctx := context.WithValue(context.Background(), runtimeQuestionRepairContextKey{}, repair)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(ctx, RunInput{
		UserMessage: models.Message{Content: "酒店有停车场吗", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err != nil || len(result.IntentTasks) != 1 || result.IntentTasks[0].SubIntent != "parking" {
		t.Fatalf("typed repair failed: result=%#v err=%v", result, err)
	}
}

func TestJevUnknownProviderOverrideFailsClosed(t *testing.T) {
	t.Setenv(intentDetectProviderEnv, "typesafe_jev_typo")
	_, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{}, adapter.HistoryBuildResult{}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported intent detect provider") {
		t.Fatalf("unknown intent provider did not fail closed: %v", err)
	}
}

func TestJevDetectRuntimeIntentUsesTypedEndpointWithoutDatabase(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer intent-test-key" {
			t.Errorf("wrong JEV transport: %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong transport", http.StatusBadRequest)
			return
		}
		var request jev.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.Model != jev.DefaultModel {
			t.Errorf("wrong intent model: %q", request.Model)
		}
		jevTestWriteResponse(t, w, request.Questions, map[string]string{"T1_route": "order_query"}, nil)
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{
		UserMessage: models.Message{Content: "帮我查询订单", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(result.IntentTasks) != 1 || result.IntentTasks[0].SubIntent != "order_query" ||
		!result.NeedsTool || result.NeedsHumanRoute || !result.SourceRefsValidated || !result.SemanticContractExpected {
		t.Fatalf("raw Intent integration failed: calls=%d result=%#v", calls.Load(), result)
	}
}

func TestJevDetectRuntimeIntentRejectsMissingTypedAnswers(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": jev.DefaultModel, "answers": map[string]any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0},
		})
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{
		UserMessage: models.Message{Content: "查询订单", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err == nil || len(result.IntentTasks) != 0 || calls.Load() != 1 {
		t.Fatalf("missing answers must fail without old-model fallback or fake reply: calls=%d result=%#v err=%v", calls.Load(), result, err)
	}
}

func TestJevIntentMappingRejectsMissingRoute(t *testing.T) {
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "酒店有停车场吗"}}
	questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	response := jevTestResponse(questions, nil, nil)
	delete(response.Answers, "T1_route")
	if _, err := buildIntentTraceFromJev(response, spans, contexts); err == nil {
		t.Fatal("missing typed route must not silently become interaction")
	}
}

func jevTestSetEnvironment(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv(intentDetectProviderEnv, "typesafe_jev")
	t.Setenv(jevAPIKeyEnv, "intent-test-key")
	t.Setenv(jevBaseURLEnv, baseURL)
	t.Setenv(jevModelEnv, jev.DefaultModel)
}

func jevTestResponse(questions map[string]jev.Question, choices map[string]string, nouls map[string]float64) jev.Response {
	response := jev.Response{Model: jev.DefaultModel, Answers: make(map[string]jev.Answer)}
	for key, question := range questions {
		if question.Type == "noul" {
			response.Answers[key] = jev.Answer{Type: "noul", Noul: nouls[key]}
			continue
		}
		choice := choices[key]
		if choice == "" {
			for suffix, value := range map[string]string{
				"_route": "parking", "_objective": "availability", "_relation": "independent", "_resolution": "clear", "_context": "none", "_count": "1", "_terminal_alignment": "exact",
			} {
				if strings.HasSuffix(key, suffix) {
					choice = value
					break
				}
			}
		}
		probabilities := make(map[string]float64)
		for option := range jevTestCriteria(question.Criteria) {
			probabilities[option] = 0
		}
		probabilities[choice] = 1
		response.Answers[key] = jev.Answer{Type: "choice", Choice: choice, Confidence: 1, Probabilities: probabilities}
	}
	return response
}

func jevTestSegment(t *testing.T, sources []adapter.CurrentTurnSource, choices map[string]string) []jevIntentSpan {
	t.Helper()
	spans, err := segmentJevIntentSources(sources, jevIntentState{}, func(_ any, questions map[string]jev.Question) (jev.Response, error) {
		response := jevTestResponse(questions, choices, nil)
		for key, question := range questions {
			if _, ok := jevTestCriteria(question.Criteria)[response.Answers[key].Choice]; !ok {
				t.Fatalf("invalid mocked segmentation choice %s=%q", key, response.Answers[key].Choice)
			}
		}
		return response, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func jevTestCriteria(value any) map[string]any {
	criteria, _ := value.(map[string]any)
	return criteria
}

func jevTestWriteResponse(t *testing.T, w http.ResponseWriter, questions map[string]jev.Question, choices map[string]string, nouls map[string]float64) {
	t.Helper()
	response := jevTestResponse(questions, choices, nouls)
	answers := make(map[string]any)
	for key, answer := range response.Answers {
		if answer.Type == "noul" {
			answers[key] = map[string]any{"type": "noul", "noul": answer.Noul}
		} else {
			answers[key] = map[string]any{
				"type": "choice", "choice": answer.Choice, "confidence": answer.Confidence, "probabilities": answer.Probabilities,
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"model": response.Model, "answers": answers,
		"usage": map[string]int64{"input_tokens": 1, "output_tokens": 1},
	}); err != nil {
		t.Error(err)
	}
}
