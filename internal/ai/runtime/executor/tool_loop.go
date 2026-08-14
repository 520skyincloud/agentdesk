package executor

import (
	"context"
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/factory"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// runtimeToolLoopMaxRounds 是模型最多「调用工具」的轮数。达到上限后强制进入最终表达，
// 避免无界工具循环耗尽预算。
const runtimeToolLoopMaxRounds = 3

// collectRuntimeBaseTools 汇总本轮可用的基础工具：静态 graph 工具（工单/搜索/转人工）
// 加 MCP 动态工具。MCP 工具构建失败时降级为仅静态工具，不阻断主链。
func collectRuntimeBaseTools(ctx context.Context, req RunInput) []einotool.BaseTool {
	tools := make([]einotool.BaseTool, 0, 8)
	if req.ToolSet != nil {
		tools = append(tools, req.ToolSet.StaticTools...)
	}
	if mcpTools, err := factory.NewToolFactory().BuildBaseTools(ctx, req.AIAgent); err == nil {
		tools = append(tools, mcpTools...)
	}
	return tools
}

// buildRuntimeToolInfos 把 BaseTool 集合转成模型可见的 ToolInfo 列表。
func buildRuntimeToolInfos(ctx context.Context, tools []einotool.BaseTool) []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		info, err := tool.Info(ctx)
		if err != nil || info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

// executeRuntimeToolCall 按 tool name 找到对应工具并执行，返回给模型的结果文本。
func executeRuntimeToolCall(ctx context.Context, tools []einotool.BaseTool, toolCall schema.ToolCall) string {
	name := strings.TrimSpace(toolCall.Function.Name)
	if name == "" {
		return "工具调用缺少 name"
	}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		info, err := tool.Info(ctx)
		if err != nil || info == nil || strings.TrimSpace(info.Name) != name {
			continue
		}
		invokable, ok := tool.(einotool.InvokableTool)
		if !ok {
			return fmt.Sprintf("工具 %s 不可执行", name)
		}
		result, err := invokable.InvokableRun(ctx, toolCall.Function.Arguments)
		if err != nil {
			return fmt.Sprintf("工具 %s 执行失败: %v", name, err)
		}
		return result
	}
	return fmt.Sprintf("未找到工具 %s", name)
}

// runtimeToolNameToCode 建立 tool name → tool code 映射，用于 trace 归因。
func runtimeToolNameToCode(req RunInput, toolInfos []*schema.ToolInfo) map[string]string {
	ret := make(map[string]string, len(toolInfos))
	if req.ToolSet != nil {
		for name, code := range req.ToolSet.StaticToolCodes {
			if n := strings.TrimSpace(name); n != "" && strings.TrimSpace(code) != "" {
				ret[n] = strings.TrimSpace(code)
			}
		}
	}
	for _, info := range toolInfos {
		if info == nil || info.Extra == nil {
			continue
		}
		if code, ok := info.Extra["toolCode"].(string); ok && strings.TrimSpace(code) != "" {
			ret[strings.TrimSpace(info.Name)] = strings.TrimSpace(code)
		}
	}
	return ret
}
