package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"

	"github.com/mlogclub/simple/common/strs"
)

const routeSkillSystemPrompt = `你是客服技能路由器。你只能在候选 Skill 中选择一个最合适的 skillCode，或者返回 NONE。
只有当用户问题与 Skill 的职责边界明确匹配时才选择；
如果不明确、信息不足、多个 Skill 都不够确定，就返回 NONE。
输出只能是 skillCode 或 NONE，不能输出其他内容。`

func routeSkillWithLLM(ctx context.Context, runtimeCtx RuntimeContext, candidates []models.SkillDefinition) (*models.SkillDefinition, *RouteTrace, error) {
	trace := &RouteTrace{Status: "started"}
	if len(candidates) == 0 {
		trace.Status = "no_candidate"
		return nil, trace, nil
	}
	if strs.IsBlank(runtimeCtx.UserMessage) {
		trace.Status = "empty_user_message"
		return nil, trace, nil
	}
	userPrompt := buildSkillRoutePrompt(runtimeCtx.UserMessage, candidates)
	startedAt := time.Now()
	callCtx, capture := usagex.WithCapture(ctx)
	result, err := ai.LLM.ChatWithConfig(callCtx, runtimeCtx.AIConfig, routeSkillSystemPrompt, userPrompt)
	trace.LatencyMs = time.Since(startedAt).Milliseconds()
	if err != nil {
		ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
			Stage: "skill_route", OperationType: "skill_route",
			Config: runtimeCtx.AIConfig, LatencyMS: trace.LatencyMs,
			Status: "failed", ErrorClass: "model_call_failed",
			Receipt: lastSkillRouteReceipt(capture),
		})
		trace.Status = "route_error"
		trace.Error = err.Error()
		return nil, trace, err
	}
	ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
		Stage: "skill_route", OperationType: "skill_route",
		Config: runtimeCtx.AIConfig, PromptTokens: int64(result.PromptTokens),
		CompletionTokens: int64(result.CompletionTokens), LatencyMS: trace.LatencyMs,
		Status: "completed", Receipt: lastSkillRouteReceipt(capture),
	})
	decision := normalizeRouteDecision(result.Content)
	trace.RawDecision = strings.TrimSpace(result.Content)
	if decision == "" || decision == "NONE" {
		trace.Status = "not_matched"
		return nil, trace, nil
	}
	for _, item := range candidates {
		if strings.EqualFold(item.Code, decision) {
			trace.Status = "llm_selected"
			trace.SelectedSkillCode = item.Code
			return &item, trace, nil
		}
	}
	trace.Status = "invalid_decision"
	trace.Error = fmt.Sprintf("invalid route decision: %s", decision)
	return nil, trace, nil
}

func lastSkillRouteReceipt(capture *usagex.Capture) *usagex.Receipt {
	if capture == nil {
		return nil
	}
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func buildSkillRoutePrompt(userMessage string, candidates []models.SkillDefinition) string {
	lines := make([]string, 0, len(candidates)+4)
	lines = append(lines, "用户问题：")
	lines = append(lines, strings.TrimSpace(userMessage))
	lines = append(lines, "")
	lines = append(lines, "候选 Skills：")
	for _, item := range candidates {
		line := fmt.Sprintf("- skillCode=%s; name=%s; description=%s", strings.TrimSpace(item.Code), strings.TrimSpace(item.Name), strings.TrimSpace(item.Description))
		if examples := parseSkillExamples(item.Examples); len(examples) > 0 {
			line += "; examples=" + strings.Join(examples, " | ")
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, "请只输出一个 skillCode 或 NONE。")
	return strings.Join(lines, "\n")
}

func parseSkillExamples(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	ret := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		ret = append(ret, item)
		if len(ret) >= 3 {
			break
		}
	}
	return ret
}

func normalizeRouteDecision(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "\n"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`")
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'")
	if strings.EqualFold(raw, "NONE") {
		return "NONE"
	}
	return raw
}
