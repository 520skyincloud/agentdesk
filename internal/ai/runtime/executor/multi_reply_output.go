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
	TaskID  string `json:"taskId"`
	Content string `json:"content"`
}

type textReplyTaskGroup struct {
	TaskID string
	Texts  []string
}

func buildMultiReplyOutputInstruction(plan callbacks.ReplyPlanTraceData) string {
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) <= 1 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【多任务输出契约】本轮有多个独立文本任务。只输出一个 JSON 对象，不要输出 Markdown 代码块或 JSON 之外的文字。格式为：")
	b.WriteString(`{"replyParts":[{"taskId":"task-1","content":"给客户的自然回复"}]}`)
	b.WriteString("。replyParts 必须按以下任务顺序输出，每个 content 只回答对应任务；最多三条消息，不要把结构化变量动作写进 content。\n")
	for _, group := range groups {
		b.WriteString("- ")
		b.WriteString(group.TaskID)
		b.WriteString("：")
		b.WriteString(strings.Join(group.Texts, "；"))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func normalizeGeneratedReplyParts(text string, plan callbacks.ReplyPlanTraceData, deferredKnowledgeTaskIDs []string) string {
	groups := buildTextReplyTaskGroups(plan)
	deferredTaskIDs := deferredReplyPartTaskIDs(deferredKnowledgeTaskIDs)
	if len(groups) <= 1 && len(deferredTaskIDs) == 0 {
		return strings.TrimSpace(text)
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
		if len(deferredTaskIDs) > 0 {
			return ""
		}
		return strings.TrimSpace(text)
	}
	contentByTaskID := make(map[string]string, len(envelope.ReplyParts))
	for _, part := range envelope.ReplyParts {
		taskID := strings.TrimSpace(part.TaskID)
		content := strings.TrimSpace(part.Content)
		if taskID == "" || content == "" {
			continue
		}
		if _, exists := contentByTaskID[taskID]; !exists {
			contentByTaskID[taskID] = content
		}
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		if deferredTaskIDs[group.TaskID] {
			continue
		}
		content := strings.TrimSpace(contentByTaskID[group.TaskID])
		if content == "" {
			if len(deferredTaskIDs) > 0 {
				return ""
			}
			return strings.TrimSpace(text)
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n<<NEXT_MESSAGE>>\n")
}

func deferredReplyPartTaskIDs(taskIDs []string) map[string]bool {
	ret := make(map[string]bool, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if len(taskID) < 2 || (taskID[0] != 'T' && taskID[0] != 't') {
			continue
		}
		index := 0
		if _, err := fmt.Sscanf(taskID[1:], "%d", &index); err != nil || index <= 0 {
			continue
		}
		ret[fmt.Sprintf("task-%d", index)] = true
	}
	return ret
}

func buildTextReplyTaskGroups(plan callbacks.ReplyPlanTraceData) []textReplyTaskGroup {
	textTasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plan.TaskPlans))
	for _, task := range plan.TaskPlans {
		if task.Output == "structured_resource_commit" || task.Output == "human_route_confirmation_or_dispatch" || task.Intent == "hotel_variable" {
			continue
		}
		if strings.TrimSpace(task.Output) == "" && strings.TrimSpace(task.Intent) == "" {
			continue
		}
		textTasks = append(textTasks, task)
	}
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
		for _, task := range textTasks[index:end] {
			text := strings.TrimSpace(task.Text)
			if text == "" {
				text = strings.TrimSpace(task.SubIntent)
			}
			if text == "" {
				text = strings.TrimSpace(task.Intent)
			}
			texts = append(texts, text)
		}
		groups = append(groups, textReplyTaskGroup{TaskID: fmt.Sprintf("task-%d", index+1), Texts: texts})
	}
	return groups
}
