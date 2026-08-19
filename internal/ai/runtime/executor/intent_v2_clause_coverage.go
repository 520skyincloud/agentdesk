package executor

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/strictjson"
)

const intentInvariantSourceClauseMissing = "source_clause_missing"

type intentV2RequiredClause struct {
	SourceRef string
	Text      string
	Topic     string
}

// validateIntentV2ClauseCoverage closes the gap between sourceRefs coverage and
// semantic coverage inside one long text/voice utterance. A URef is not enough
// when that utterance contains several independently answerable clauses.
func validateIntentV2ClauseCoverage(parsed contracts.IntentTasksV2, scope intentV2SourceScope) error {
	missing := intentV2MissingRequiredClauses(parsed, scope)
	if len(missing) == 0 {
		return nil
	}
	labels := make([]string, 0, len(missing))
	for _, clause := range missing {
		labels = append(labels, clause.SourceRef+":"+clause.Text)
	}
	invariant := &runtimeIntentInvariantError{
		Code:  intentInvariantSourceClauseMissing,
		Path:  "$.tasks[*].text",
		Value: strings.Join(labels, " | "),
	}
	return &strictjson.ProtocolError{
		Code:    strictjson.ErrorJSONBusinessInvariant,
		Path:    invariant.Path,
		Message: fmt.Sprintf("independent current-input clauses are missing tasks: %s", invariant.Value),
		Err:     invariant,
	}
}

func isIntentV2ClauseCoverageError(err error) bool {
	invariant, ok := runtimeIntentInvariantDetails(err)
	return ok && invariant.Code == intentInvariantSourceClauseMissing
}

func intentV2MissingRequiredClauses(parsed contracts.IntentTasksV2, scope intentV2SourceScope) []intentV2RequiredClause {
	missing := make([]intentV2RequiredClause, 0)
	for _, utterance := range scope.Envelope.Utterances {
		if _, required := scope.RequiredRefs[utterance.Ref]; !required {
			continue
		}
		clauses := runtimeAtomicKnowledgeClauses(utterance.Text)
		actionable := make([]intentV2RequiredClause, 0, len(clauses))
		previousTopic := ""
		for _, clause := range clauses {
			clause = trimRuntimeAtomicClause(clause)
			if !intentV2ClauseNeedsIndependentTask(clause) {
				continue
			}
			topic := intentV2ClauseTopic(clause)
			if topic == "" && intentV2ClauseContinuesPreviousTopic(clause) {
				topic = previousTopic
			}
			if topic != "" {
				previousTopic = topic
			}
			actionable = append(actionable, intentV2RequiredClause{
				SourceRef: utterance.Ref,
				Text:      clause,
				Topic:     topic,
			})
		}
		// Single-clause utterances are already protected by required sourceRefs.
		// Keeping that rule avoids turning adjacent context such as "好困啊" into
		// a second task when it belongs to the following coffee question.
		if len(actionable) < 2 {
			continue
		}
		for _, clause := range actionable {
			matched := false
			for _, task := range parsed.Tasks {
				if !intentV2ContainsRef(task.SourceRefs, utterance.Ref) {
					continue
				}
				if !intentV2TaskCoversClause(task, clause) {
					continue
				}
				matched = true
				break
			}
			if !matched {
				missing = append(missing, clause)
			}
		}
	}
	return missing
}

func intentV2ContainsRef(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func intentV2ClauseNeedsIndependentTask(text string) bool {
	compact := normalizeRuntimeTaskText(text)
	if len([]rune(compact)) < 2 {
		return false
	}
	if containsAny(compact, []string{
		"吗", "么", "呢", "谁", "什么", "怎么", "怎样", "如何", "哪里", "哪儿", "哪个", "多少", "几点", "是否", "有没有", "能不能", "可不可以", "能否", "为什么", "为啥", "咋", "啥",
		"帮我", "给我", "发我", "告诉我", "麻烦", "请问", "查一下", "看一下", "我要", "想要", "需要",
		"坏了", "打不开", "进不去", "连不上", "用不了", "无法", "失败", "异常", "漏水", "不制冷", "不制热",
		"你好", "您好", "哈喽", "嗨", "嘿嘿", "哈哈", "谢谢", "再见", "晚安", "早安",
	}) {
		return true
	}
	return strings.Contains(compact, "我想") && intentV2LooksLikeUnclearASRTail(text)
}

func intentV2TaskCoversClause(task contracts.IntentTaskV2, clause intentV2RequiredClause) bool {
	if intentV2AggregateTask(task) {
		return false
	}
	taskText := []rune(normalizeRuntimeTaskText(task.Text))
	clauseText := []rune(normalizeRuntimeTaskText(clause.Text))
	if len(taskText) > 0 && len(clauseText) > 0 {
		if string(taskText) == string(clauseText) {
			return true
		}
		shorter, longer := taskText, clauseText
		if len(shorter) > len(longer) {
			shorter, longer = longer, shorter
		}
		if indexRuneSlice(longer, shorter) >= 0 {
			return true
		}
	}
	if clause.Topic == "" {
		return false
	}
	taskTopic := intentV2ClauseTopic(task.Text)
	if taskTopic == "" {
		taskTopic = intentV2SubIntentTopic(strings.TrimSpace(task.SubIntent))
	}
	return taskTopic == clause.Topic
}

func intentV2ClauseTopic(text string) string {
	topics := detectKnowledgeTopicClasses(text)
	for _, topic := range []string{
		"checkin", "checkout", "address", "parking", "breakfast", "coffee", "invoice", "wifi", "laundry", "luggage",
		"housekeeping", "room_change", "door_access", "tv", "aircon", "supplies", "discount", "nearby_food", "nearby_fun", "takeaway", "store_name",
	} {
		if _, ok := topics[topic]; ok {
			return topic
		}
	}
	return ""
}

func intentV2SubIntentTopic(subIntent string) string {
	switch strings.TrimSpace(subIntent) {
	case "checkin_process", "check_in":
		return "checkin"
	case "checkout_process", "check_out":
		return "checkout"
	case "address", "location":
		return "address"
	case "parking":
		return "parking"
	case "breakfast":
		return "breakfast"
	case "coffee":
		return "coffee"
	case "invoice":
		return "invoice"
	case "network_wifi", "wifi":
		return "wifi"
	case "laundry":
		return "laundry"
	case "luggage_storage", "luggage":
		return "luggage"
	case "housekeeping":
		return "housekeeping"
	case "room_change":
		return "room_change"
	case "door_access":
		return "door_access"
	case "tv_cast", "tv":
		return "tv"
	case "air_conditioner", "aircon":
		return "aircon"
	case "supplies_self_help", "supplies":
		return "supplies"
	case "discount":
		return "discount"
	case "order_food_delivery", "takeaway":
		return "takeaway"
	case "store_identity", "store_name":
		return "store_name"
	default:
		return ""
	}
}

func intentV2AggregateTask(task contracts.IntentTaskV2) bool {
	actionable := 0
	previousTopic := ""
	topics := make(map[string]struct{})
	hasUnknownTopic := false
	for _, clause := range runtimeAtomicKnowledgeClauses(task.Text) {
		clause = trimRuntimeAtomicClause(clause)
		if !intentV2ClauseNeedsIndependentTask(clause) {
			continue
		}
		actionable++
		topic := intentV2ClauseTopic(clause)
		if topic == "" && intentV2ClauseContinuesPreviousTopic(clause) {
			topic = previousTopic
		}
		if topic == "" {
			hasUnknownTopic = true
			continue
		}
		previousTopic = topic
		topics[topic] = struct{}{}
	}
	if actionable <= 1 {
		return false
	}
	if strings.TrimSpace(task.Intent) == "interaction" {
		return true
	}
	// A single knowledge task may combine dimensions of one proven topic, such
	// as breakfast time and location. If any clause has no provable topic, or
	// the text spans several topics, the task cannot stand in for every clause.
	return hasUnknownTopic || len(topics) != 1
}

func intentV2ClauseContinuesPreviousTopic(text string) bool {
	compact := normalizeRuntimeTaskText(text)
	if len([]rune(compact)) > 12 || len(detectKnowledgeTopicClasses(text)) > 0 {
		return false
	}
	return containsAny(compact, []string{"哪里", "哪儿", "在哪", "几点", "多久", "多少", "多少钱", "怎么样", "可以吗", "行吗"})
}

func intentV2LooksLikeUnclearASRTail(text string) bool {
	hasHan, hasLatin := false, false
	for _, current := range text {
		switch {
		case unicode.Is(unicode.Han, current):
			hasHan = true
		case unicode.Is(unicode.Latin, current):
			hasLatin = true
		}
	}
	return hasHan && hasLatin
}

func expandIntentV2AggregateTasks(parsed contracts.IntentTasksV2, configs []models.ReplyIntentConfig) contracts.IntentTasksV2 {
	kept := make([]contracts.IntentTaskV2, 0, len(parsed.Tasks))
	seen := make(map[string]struct{})
	for _, task := range parsed.Tasks {
		if !intentV2AggregateTask(task) {
			kept = append(kept, task)
			continue
		}
		if strings.TrimSpace(task.Intent) == "interaction" {
			continue
		}
		for _, clause := range runtimeAtomicKnowledgeClauses(task.Text) {
			clause = trimRuntimeAtomicClause(clause)
			if !intentV2ClauseNeedsIndependentTask(clause) {
				continue
			}
			key := strings.Join(task.SourceRefs, ",") + "|" + normalizeRuntimeTaskText(clause)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			item := task
			item.Text = clause
			item.SourceMessageIDs = nil
			item.SubIntent = runtimeAtomicClauseSubIntent(item.SubIntent, clause)
			intent, subIntent, requestMode := intentV2FallbackClauseIntent(clause, configs)
			if intent != "interaction" {
				item.Intent = intent
				item.SubIntent = subIntent
				item.RequestMode = requestMode
			}
			kept = append(kept, item)
		}
	}
	parsed.Tasks = kept
	return parsed
}

// completeIntentV2ClauseCoverage appends deterministic local tasks only after
// the existing single protocol-repair opportunity was unable to cover every
// clause. It does not make another model call.
func completeIntentV2ClauseCoverage(parsed contracts.IntentTasksV2, scope intentV2SourceScope, configs []models.ReplyIntentConfig) (contracts.IntentTasksV2, []DerivedTaskCapabilities, error) {
	parsed = expandIntentV2AggregateTasks(parsed, configs)
	intentV2ResequenceTasksBySource(&parsed, scope)
	missing := intentV2MissingRequiredClauses(parsed, scope)
	if len(missing) == 0 {
		if err := resolveIntentV2TaskSources(&parsed, scope); err != nil {
			return parsed, nil, err
		}
		derived, err := DeriveRuntimeIntentCapabilities(parsed, configs)
		return parsed, derived, err
	}
	for _, clause := range missing {
		if len(parsed.Tasks) >= 12 {
			return parsed, nil, validateIntentV2ClauseCoverage(parsed, scope)
		}
		intent, subIntent, requestMode := intentV2FallbackClauseIntent(clause.Text, configs)
		parsed.Tasks = append(parsed.Tasks, contracts.IntentTaskV2{
			Sequence:    len(parsed.Tasks) + 1,
			Intent:      intent,
			SubIntent:   subIntent,
			Text:        clause.Text,
			RequestMode: requestMode,
			Confidence:  0.5,
			SourceRefs:  []string{clause.SourceRef},
		})
	}
	intentV2ResequenceTasksBySource(&parsed, scope)
	if err := resolveIntentV2TaskSources(&parsed, scope); err != nil {
		return parsed, nil, err
	}
	if err := validateIntentV2ClauseCoverage(parsed, scope); err != nil {
		return parsed, nil, err
	}
	derived, err := DeriveRuntimeIntentCapabilities(parsed, configs)
	return parsed, derived, err
}

func intentV2FallbackClauseIntent(text string, configs []models.ReplyIntentConfig) (string, string, string) {
	if subIntent := runtimeAtomicClauseSubIntent("", text); subIntent != "" && runtimeIntentConfigEnabled(configs, "hotel_info") {
		return "hotel_info", subIntent, "answer"
	}
	return "interaction", "social", "social"
}

func intentV2ResequenceTasksBySource(parsed *contracts.IntentTasksV2, scope intentV2SourceScope) {
	if parsed == nil || len(parsed.Tasks) < 2 {
		return
	}
	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(scope.Envelope.Utterances))
	orderByRef := make(map[string]int, len(scope.Envelope.Utterances))
	for index, utterance := range scope.Envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
		orderByRef[utterance.Ref] = index
	}
	type rankedTask struct {
		Task          contracts.IntentTaskV2
		SourceOrder   int
		SpanStart     int
		OriginalOrder int
	}
	ranked := make([]rankedTask, 0, len(parsed.Tasks))
	for index, task := range parsed.Tasks {
		item := rankedTask{Task: task, SourceOrder: len(scope.Envelope.Utterances), SpanStart: 1 << 30, OriginalOrder: index}
		if len(task.SourceRefs) > 0 {
			if order, ok := orderByRef[task.SourceRefs[0]]; ok {
				item.SourceOrder = order
			}
			if utterance, ok := utteranceByRef[task.SourceRefs[0]]; ok {
				if start, _, precise := runtimeTaskSpanWithinSource(task.Text, utterance.Text); precise {
					item.SpanStart = start
				}
			}
		}
		ranked = append(ranked, item)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].SourceOrder != ranked[j].SourceOrder {
			return ranked[i].SourceOrder < ranked[j].SourceOrder
		}
		if ranked[i].SpanStart != ranked[j].SpanStart {
			return ranked[i].SpanStart < ranked[j].SpanStart
		}
		return ranked[i].OriginalOrder < ranked[j].OriginalOrder
	})
	for index := range ranked {
		ranked[index].Task.Sequence = index + 1
		parsed.Tasks[index] = ranked[index].Task
	}
}
