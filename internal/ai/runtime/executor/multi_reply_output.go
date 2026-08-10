package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

type generatedReplyPartsEnvelope struct {
	ReplyParts []generatedReplyPart `json:"replyParts"`
}

type generatedReplyPart struct {
	TaskKey string `json:"taskKey"`
	TaskID  string `json:"taskId,omitempty"`
	Content string `json:"content"`
}

type textReplyTaskGroup struct {
	TaskID   string
	TaskKeys []string
	Texts    []string
}

type textReplyTask struct {
	TaskKey string
	Text    string
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
	var b strings.Builder
	b.WriteString("【多任务输出契约】本轮有多个独立文本任务。只输出一个 JSON 对象，不要输出 Markdown 代码块或 JSON 之外的文字。格式为：")
	b.WriteString(`{"replyParts":[{"taskKey":"任务键","content":"给客户的自然回复"}]}`)
	b.WriteString("。replyParts 必须完整覆盖以下每个 taskKey，每个 content 只回答对应任务；不要把结构化变量动作写进 content。\n")
	for _, task := range tasks {
		b.WriteString("- ")
		b.WriteString(task.TaskKey)
		b.WriteString("：")
		b.WriteString(task.Text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildMultiReplyProtocolRepairInstruction(plan callbacks.ReplyPlanTraceData) string {
	instruction := buildMultiReplyOutputInstruction(plan)
	if instruction == "" {
		return ""
	}
	return "【协议修复，仅此一次】上一轮输出没有完整遵守多任务 JSON 契约。重新输出全部任务，不要省略、合并或改写 taskKey。\n" + instruction
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
	contentByTaskID := make(map[string]string, len(envelope.ReplyParts))
	for _, part := range envelope.ReplyParts {
		taskID := strings.TrimSpace(part.TaskKey)
		if taskID == "" {
			taskID = strings.TrimSpace(part.TaskID)
		}
		content := strings.TrimSpace(part.Content)
		if taskID == "" || content == "" {
			continue
		}
		if _, exists := contentByTaskID[taskID]; !exists {
			contentByTaskID[taskID] = content
		}
	}
	contents := make(map[string]string, len(tasks))
	for _, task := range tasks {
		content := strings.TrimSpace(contentByTaskID[task.TaskKey])
		if content == "" {
			return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "missing_task_key"}
		}
		contents[task.TaskKey] = content
	}
	groups := buildTextReplyTaskGroups(plan)
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		groupParts := make([]string, 0, len(group.TaskKeys))
		for _, taskKey := range group.TaskKeys {
			groupParts = append(groupParts, contents[taskKey])
		}
		parts = append(parts, strings.Join(groupParts, "\n"))
	}
	if len(parts) == 0 {
		return "", &multiReplyProtocolError{RawResponse: strings.TrimSpace(text), Reason: "empty_reply_parts"}
	}
	return strings.Join(parts, "\n<<NEXT_MESSAGE>>\n"), nil
}

func buildTextReplyTaskGroups(plan callbacks.ReplyPlanTraceData) []textReplyTaskGroup {
	textTasks := buildTextReplyTasks(plan)
	if len(textTasks) == 0 {
		return nil
	}
	groupCount := len(textTasks)
	if groupCount > 3 {
		groupCount = 3
	}
	groups := make([]textReplyTaskGroup, 0, groupCount)
	for index := 0; index < groupCount; index++ {
		end := index + 1
		if index == groupCount-1 {
			end = len(textTasks)
		}
		texts := make([]string, 0, end-index)
		taskKeys := make([]string, 0, end-index)
		for _, task := range textTasks[index:end] {
			texts = append(texts, task.Text)
			taskKeys = append(taskKeys, task.TaskKey)
		}
		groups = append(groups, textReplyTaskGroup{TaskID: taskKeys[0], TaskKeys: taskKeys, Texts: texts})
	}
	return groups
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
		ret = append(ret, textReplyTask{TaskKey: taskKey, Text: text})
	}
	return ret
}
