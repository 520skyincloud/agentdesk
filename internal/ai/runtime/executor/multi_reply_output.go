package executor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

type generatedReplyPartsEnvelope struct {
	ReplyParts []generatedReplyPart `json:"replyParts"`
}

type generatedReplyPart struct {
	TaskKey  string   `json:"taskKey,omitempty"`
	TaskID   string   `json:"taskId,omitempty"`
	TaskKeys []string `json:"taskKeys,omitempty"`
	Content  string   `json:"content"`
}

type textReplyTaskGroup struct {
	TaskID   string
	TaskKeys []string
	Texts    []string
}

type textReplyTask struct {
	TaskKey     string
	AnswerGroup string
	Text        string
}

type multiReplyProtocolError struct {
	RawResponse string
	Reason      string
}

func (e *multiReplyProtocolError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "multi-task reply protocol invalid"
	}
	return "multi-task reply protocol invalid: " + e.Reason
}

func buildMultiReplyOutputInstruction(plan callbacks.ReplyPlanTraceData) string {
	tasks := buildTextReplyTasks(plan)
	if len(tasks) <= 1 {
		return ""
	}
	groups := buildTextReplyTaskGroups(plan)
	var b strings.Builder
	b.WriteString("【多任务输出契约】本轮有多个文本任务。只输出一个 JSON 对象，不要输出 Markdown 代码块或 JSON 之外的文字。格式为：")
	b.WriteString(`{"replyParts":[{"taskKeys":["任务键1","任务键2"],"content":"一次覆盖该组全部问题的自然回复"}]}`)
	b.WriteString("。必须严格按下面的任务组输出：每组只生成一个 replyPart，taskKeys 不得增删、拆分或跨组；content 一次覆盖组内全部问题，同一事实只说一次。不同任务组最多形成三条客户消息；不要把结构化变量动作写进 content。\n")
	for index, group := range groups {
		b.WriteString(fmt.Sprintf("- 任务组%d taskKeys=%s：", index+1, strings.Join(group.TaskKeys, ",")))
		b.WriteString(strings.Join(group.Texts, "；"))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildMultiReplyProtocolRepairInstruction(plan callbacks.ReplyPlanTraceData) string {
	instruction := buildMultiReplyOutputInstruction(plan)
	if instruction == "" {
		return ""
	}
	return "【协议修复，仅此一次】上一轮输出没有完整遵守多任务 JSON 契约。重新输出全部任务，不要省略或改写 taskKey，必须保持指定的 taskKeys 任务组。\n" + instruction
}

func normalizeGeneratedReplyPartsStrict(text string, plan callbacks.ReplyPlanTraceData) (string, error) {
	tasks := buildTextReplyTasks(plan)
	if len(tasks) <= 1 {
		return strings.TrimSpace(text), nil
	}
	raw := strings.TrimSpace(text)
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```JSON"))
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}
	envelope := generatedReplyPartsEnvelope{}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil || len(envelope.ReplyParts) == 0 {
		return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "invalid_json"}
	}
	expectedGroups := buildTextReplyTaskGroups(plan)
	groupBySignature := make(map[string]textReplyTaskGroup, len(expectedGroups))
	for _, group := range expectedGroups {
		groupBySignature[taskKeyGroupSignature(group.TaskKeys)] = group
	}
	contentByGroup := make(map[string]string, len(envelope.ReplyParts))
	for _, part := range envelope.ReplyParts {
		taskKeys := normalizeGeneratedReplyTaskKeys(part)
		content := strings.TrimSpace(part.Content)
		if len(taskKeys) == 0 || content == "" {
			return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "empty_task_group"}
		}
		signature := taskKeyGroupSignature(taskKeys)
		if _, expected := groupBySignature[signature]; !expected {
			return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "unexpected_task_group"}
		}
		if _, exists := contentByGroup[signature]; exists {
			return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "duplicate_task_group"}
		}
		contentByGroup[signature] = content
	}
	parts := make([]string, 0, len(expectedGroups))
	for _, group := range expectedGroups {
		content := strings.TrimSpace(contentByGroup[taskKeyGroupSignature(group.TaskKeys)])
		if content == "" {
			return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "missing_task_group"}
		}
		parts = append(parts, content)
	}
	if len(parts) == 0 {
		return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "empty_reply_parts"}
	}
	return strings.Join(parts, "\n<<NEXT_MESSAGE>>\n"), nil
}

func normalizeGeneratedReplyTaskKeys(part generatedReplyPart) []string {
	values := append([]string(nil), part.TaskKeys...)
	if len(values) == 0 {
		if value := strings.TrimSpace(part.TaskKey); value != "" {
			values = append(values, value)
		} else if value := strings.TrimSpace(part.TaskID); value != "" {
			values = append(values, value)
		}
	}
	ret := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func taskKeyGroupSignature(taskKeys []string) string {
	keys := append([]string(nil), taskKeys...)
	for index := range keys {
		keys[index] = strings.TrimSpace(keys[index])
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x1f")
}

func buildTextReplyTaskGroups(plan callbacks.ReplyPlanTraceData) []textReplyTaskGroup {
	textTasks := buildTextReplyTasks(plan)
	if len(textTasks) == 0 {
		return nil
	}
	groups := make([]textReplyTaskGroup, 0, len(textTasks))
	groupIndexes := make(map[string]int, len(textTasks))
	for _, task := range textTasks {
		groupKey := strings.TrimSpace(task.AnswerGroup)
		if groupKey == "" {
			groupKey = task.TaskKey
		}
		if index, exists := groupIndexes[groupKey]; exists {
			groups[index].TaskKeys = append(groups[index].TaskKeys, task.TaskKey)
			groups[index].Texts = append(groups[index].Texts, task.Text)
			continue
		}
		groupIndexes[groupKey] = len(groups)
		groups = append(groups, textReplyTaskGroup{TaskID: task.TaskKey, TaskKeys: []string{task.TaskKey}, Texts: []string{task.Text}})
	}
	if len(groups) <= 3 {
		return groups
	}
	ret := append([]textReplyTaskGroup(nil), groups[:2]...)
	merged := textReplyTaskGroup{TaskID: groups[2].TaskID}
	for _, group := range groups[2:] {
		merged.TaskKeys = append(merged.TaskKeys, group.TaskKeys...)
		merged.Texts = append(merged.Texts, group.Texts...)
	}
	return append(ret, merged)
}

func buildTextReplyTasks(plan callbacks.ReplyPlanTraceData) []textReplyTask {
	ret := make([]textReplyTask, 0, len(plan.TaskPlans))
	for index, task := range plan.TaskPlans {
		if task.Output == "structured_resource_commit" || task.Output == "human_route_confirmation_or_dispatch" || task.Intent == "hotel_variable" {
			continue
		}
		if strings.TrimSpace(task.Output) == "" && strings.TrimSpace(task.Intent) == "" {
			continue
		}
		text := strings.TrimSpace(task.Text)
		if text == "" {
			text = strings.TrimSpace(task.SubIntent)
		}
		if text == "" {
			text = strings.TrimSpace(task.Intent)
		}
		taskKey := strings.TrimSpace(task.TaskKey)
		if taskKey == "" {
			taskKey = fmt.Sprintf("task-%d", index+1)
		}
		ret = append(ret, textReplyTask{TaskKey: taskKey, AnswerGroup: strings.TrimSpace(task.AnswerGroup), Text: text})
	}
	return ret
}
