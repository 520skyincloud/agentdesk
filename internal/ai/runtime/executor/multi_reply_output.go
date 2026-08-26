package executor

import (
	"encoding/json"
	"errors"
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

// ErrGeneratedReplyProtocol marks generated output that failed the internal
// replyParts contract and is safe to retry without exposing the raw payload.
var ErrGeneratedReplyProtocol = errors.New("generated reply protocol validation failed")

var errGeneratedReplyProtocol = ErrGeneratedReplyProtocol

func IsGeneratedReplyProtocolError(err error) bool {
	return errors.Is(err, ErrGeneratedReplyProtocol)
}

func buildMultiReplyOutputInstruction(plan callbacks.ReplyPlanTraceData, requireStructured bool) string {
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) == 0 || (len(groups) <= 1 && !requireStructured) {
		return ""
	}
	var b strings.Builder
	b.WriteString("【任务输出契约】本轮只允许回答下面列出的文本任务。只输出一个 JSON 对象，不要输出 Markdown 代码块或 JSON 之外的文字。格式为：")
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

func normalizeGeneratedReplyParts(text string, plan callbacks.ReplyPlanTraceData, requireStructured bool) string {
	normalized, _ := normalizeGeneratedReplyPartsResult(text, plan, requireStructured)
	return normalized
}

func normalizeGeneratedReplyPartsResult(text string, plan callbacks.ReplyPlanTraceData, requireStructured bool) (string, error) {
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) == 0 {
		return "", nil
	}
	raw := strings.TrimSpace(text)
	envelope, parsed := parseGeneratedReplyParts(raw)
	if !parsed {
		if looksLikeGeneratedReplyPartsProtocol(raw) {
			return "", fmt.Errorf("%w: malformed replyParts payload", errGeneratedReplyProtocol)
		}
		if requireStructured {
			return "", fmt.Errorf("%w: structured replyParts payload required", errGeneratedReplyProtocol)
		}
		return raw, nil
	}
	expectedTaskIDs := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		expectedTaskIDs[group.TaskID] = struct{}{}
	}
	contentByTaskID := make(map[string]string, len(envelope.ReplyParts))
	for _, part := range envelope.ReplyParts {
		taskID := strings.TrimSpace(part.TaskID)
		content := strings.TrimSpace(part.Content)
		if taskID == "" {
			return "", fmt.Errorf("%w: reply part is missing taskId", errGeneratedReplyProtocol)
		}
		if _, expected := expectedTaskIDs[taskID]; !expected {
			return "", fmt.Errorf("%w: unknown taskId %s", errGeneratedReplyProtocol, taskID)
		}
		if _, exists := contentByTaskID[taskID]; exists {
			return "", fmt.Errorf("%w: duplicate taskId %s", errGeneratedReplyProtocol, taskID)
		}
		if content == "" {
			return "", fmt.Errorf("%w: missing content for %s", errGeneratedReplyProtocol, taskID)
		}
		contentByTaskID[taskID] = content
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		content := strings.TrimSpace(contentByTaskID[group.TaskID])
		if content == "" {
			return "", fmt.Errorf("%w: missing content for %s", errGeneratedReplyProtocol, group.TaskID)
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n<<NEXT_MESSAGE>>\n"), nil
}

func parseGeneratedReplyParts(text string) (generatedReplyPartsEnvelope, bool) {
	return parseGeneratedReplyPartsPayload(strings.TrimSpace(text), 0)
}

func parseGeneratedReplyPartsPayload(raw string, depth int) (generatedReplyPartsEnvelope, bool) {
	if depth > 4 {
		return generatedReplyPartsEnvelope{}, false
	}
	raw = unwrapGeneratedReplyMarkdownFence(strings.TrimSpace(raw))
	if raw == "" {
		return generatedReplyPartsEnvelope{}, false
	}

	envelope := generatedReplyPartsEnvelope{}
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil && len(envelope.ReplyParts) > 0 {
		return envelope, true
	}

	var quoted string
	if err := json.Unmarshal([]byte(raw), &quoted); err == nil && strings.TrimSpace(quoted) != raw {
		return parseGeneratedReplyPartsPayload(quoted, depth+1)
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		for _, key := range []string{"output", "result", "content", "text", "answer", "data"} {
			wrapped, ok := wrapper[key]
			if !ok || len(wrapped) == 0 {
				continue
			}
			if parsed, ok := parseGeneratedReplyPartsPayload(string(wrapped), depth+1); ok {
				return parsed, true
			}
		}
	}
	return generatedReplyPartsEnvelope{}, false
}

func unwrapGeneratedReplyMarkdownFence(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "```")
	if start < 0 {
		return raw
	}
	afterFence := raw[start+3:]
	lineEnd := strings.IndexByte(afterFence, '\n')
	if lineEnd < 0 {
		return raw
	}
	language := strings.TrimSpace(afterFence[:lineEnd])
	if language != "" && !strings.EqualFold(language, "json") {
		return raw
	}
	body := afterFence[lineEnd+1:]
	end := strings.LastIndex(body, "```")
	if end < 0 {
		return raw
	}
	return strings.TrimSpace(body[:end])
}

func looksLikeGeneratedReplyPartsProtocol(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(normalized, "replyparts") {
		return true
	}
	return strings.Contains(normalized, `"taskid"`) && strings.Contains(normalized, `"content"`)
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
