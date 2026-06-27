package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einocallbacks "agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const activeSkillRunLocalKey = "runtime_active_skill_code"

type RuntimeToolFilterMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	collector          *einocallbacks.RuntimeTraceCollector
	toolMetadataByName map[string]einocallbacks.ToolMetadata
	skillMetadataBy    map[string]einocallbacks.SkillMetadata
	dynamicToolNames   []string
}

func NewRuntimeToolFilterMiddleware(
	collector *einocallbacks.RuntimeTraceCollector,
	toolMetadataByName map[string]einocallbacks.ToolMetadata,
	skillMetadataBy map[string]einocallbacks.SkillMetadata,
	dynamicToolNames []string,
) *RuntimeToolFilterMiddleware {
	return &RuntimeToolFilterMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		collector:                    collector,
		toolMetadataByName:           cloneToolMetadataMap(toolMetadataByName),
		skillMetadataBy:              cloneSkillMetadataMap(skillMetadataBy),
		dynamicToolNames:             append([]string(nil), dynamicToolNames...),
	}
}

func (m *RuntimeToolFilterMiddleware) WrapModel(_ context.Context, cm model.BaseChatModel, mc *adk.ModelContext) (model.BaseChatModel, error) {
	if mc == nil {
		return cm, nil
	}
	return &runtimeToolFilterModelWrapper{
		cm:                 cm,
		allTools:           append([]*schema.ToolInfo(nil), mc.Tools...),
		collector:          m.collector,
		toolMetadataByName: m.toolMetadataByName,
		skillMetadataBy:    m.skillMetadataBy,
		dynamicToolNames:   append([]string(nil), m.dynamicToolNames...),
	}, nil
}

func (m *RuntimeToolFilterMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
		toolName := ""
		if tCtx != nil {
			toolName = strings.TrimSpace(tCtx.Name)
		}
		metadata, _ := resolveRuntimeToolMetadata(toolName, m.toolMetadataByName)
		if !isRuntimeBuiltinAlwaysAllowed(metadata.ToolCode) {
			activeSkill, restricted := m.resolveActiveSkill(ctx)
			if restricted && !isToolCodeAllowedForSkill(metadata.ToolCode, activeSkill.AllowedToolCodes) {
				return "", m.blockToolCall(metadata, argumentsInJSON, activeSkill)
			}
		}
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			return result, err
		}
		if strings.TrimSpace(metadata.ToolCode) == toolx.BuiltinSkill.Code {
			_ = m.setActiveSkill(ctx, skillCodeFromArguments(argumentsInJSON))
			return result, nil
		}
		if strings.TrimSpace(metadata.ToolCode) == toolx.BuiltinToolSearch.Code {
			activeSkill, restricted := m.resolveActiveSkill(ctx)
			if !restricted {
				return result, nil
			}
			filtered, filterErr := filterToolSearchResult(result, activeSkill.AllowedToolCodes, m.toolMetadataByName)
			if filterErr == nil {
				return filtered, nil
			}
		}
		return result, nil
	}, nil
}

func (m *RuntimeToolFilterMiddleware) blockToolCall(metadata einocallbacks.ToolMetadata, argumentsInJSON string, activeSkill einocallbacks.SkillMetadata) error {
	err := fmt.Errorf("tool %s is not allowed for active skill %s", strings.TrimSpace(metadata.ToolCode), strings.TrimSpace(activeSkill.Code))
	if m.collector != nil {
		m.collector.AddToolItem(einocallbacks.ToolTraceItem{
			ToolCode:      strings.TrimSpace(metadata.ToolCode),
			ServerCode:    strings.TrimSpace(metadata.ServerCode),
			ToolName:      strings.TrimSpace(metadata.ToolName),
			Arguments:     parseRuntimeToolArguments(argumentsInJSON),
			Status:        "error",
			ErrorMessage:  err.Error(),
			Blocked:       true,
			BlockedReason: "skill_tool_not_allowed",
		})
	}
	return err
}

func (m *RuntimeToolFilterMiddleware) setActiveSkill(ctx context.Context, skillCode string) error {
	skillCode = strings.TrimSpace(skillCode)
	if skillCode == "" {
		return nil
	}
	return adk.SetRunLocalValue(ctx, activeSkillRunLocalKey, skillCode)
}

func (m *RuntimeToolFilterMiddleware) resolveActiveSkill(ctx context.Context) (einocallbacks.SkillMetadata, bool) {
	if len(m.skillMetadataBy) == 0 {
		return einocallbacks.SkillMetadata{}, false
	}
	value, found, err := adk.GetRunLocalValue(ctx, activeSkillRunLocalKey)
	if err != nil || !found {
		return einocallbacks.SkillMetadata{}, false
	}
	code, ok := value.(string)
	if !ok {
		return einocallbacks.SkillMetadata{}, false
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return einocallbacks.SkillMetadata{}, false
	}
	skill, ok := m.skillMetadataBy[code]
	if !ok {
		return einocallbacks.SkillMetadata{}, false
	}
	if len(skill.AllowedToolCodes) == 0 {
		return skill, false
	}
	return skill, true
}

type runtimeToolFilterModelWrapper struct {
	cm                 model.BaseChatModel
	allTools           []*schema.ToolInfo
	collector          *einocallbacks.RuntimeTraceCollector
	toolMetadataByName map[string]einocallbacks.ToolMetadata
	skillMetadataBy    map[string]einocallbacks.SkillMetadata
	dynamicToolNames   []string
}

func (w *runtimeToolFilterModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	tools := w.filteredTools(ctx, input)
	return w.cm.Generate(ctx, input, append(opts, model.WithTools(tools))...)
}

func (w *runtimeToolFilterModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	tools := w.filteredTools(ctx, input)
	return w.cm.Stream(ctx, input, append(opts, model.WithTools(tools))...)
}

func (w *runtimeToolFilterModelWrapper) filteredTools(ctx context.Context, input []*schema.Message) []*schema.ToolInfo {
	tools := filterDynamicToolInfos(w.allTools, w.dynamicToolNames, input)
	activeSkill, restricted := resolveActiveSkillMetadata(ctx, w.skillMetadataBy)
	if restricted {
		tools = filterToolInfosBySkill(tools, w.toolMetadataByName, activeSkill.AllowedToolCodes)
	}
	if w.collector != nil {
		w.collector.SetFilteredToolCodes(extractToolCodesFromInfos(tools, w.toolMetadataByName))
	}
	return tools
}

func resolveActiveSkillMetadata(ctx context.Context, skills map[string]einocallbacks.SkillMetadata) (einocallbacks.SkillMetadata, bool) {
	if len(skills) == 0 {
		return einocallbacks.SkillMetadata{}, false
	}
	value, found, err := adk.GetRunLocalValue(ctx, activeSkillRunLocalKey)
	if err != nil || !found {
		return einocallbacks.SkillMetadata{}, false
	}
	code, ok := value.(string)
	if !ok {
		return einocallbacks.SkillMetadata{}, false
	}
	code = strings.TrimSpace(code)
	skill, ok := skills[code]
	if !ok || len(skill.AllowedToolCodes) == 0 {
		return skill, false
	}
	return skill, true
}

func filterDynamicToolInfos(allTools []*schema.ToolInfo, dynamicToolNames []string, messages []*schema.Message) []*schema.ToolInfo {
	if len(allTools) == 0 {
		return nil
	}
	selectedToolNames := extractSelectedDynamicToolNames(messages)
	if len(dynamicToolNames) == 0 {
		return append([]*schema.ToolInfo(nil), allTools...)
	}
	removeMap := invertStringSelection(dynamicToolNames, selectedToolNames)
	ret := make([]*schema.ToolInfo, 0, len(allTools))
	for _, info := range allTools {
		if info == nil {
			continue
		}
		if _, ok := removeMap[strings.TrimSpace(info.Name)]; ok {
			continue
		}
		ret = append(ret, info)
	}
	return ret
}

func extractSelectedDynamicToolNames(messages []*schema.Message) []string {
	if len(messages) == 0 {
		return nil
	}
	selected := make([]string, 0)
	for _, message := range messages {
		if message == nil || message.Role != schema.Tool || strings.TrimSpace(message.ToolName) != toolx.BuiltinToolSearch.Name {
			continue
		}
		var payload struct {
			SelectedTools []string `json:"selectedTools"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(message.Content)), &payload); err != nil {
			continue
		}
		for _, item := range payload.SelectedTools {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			selected = append(selected, item)
		}
	}
	return selected
}

func invertStringSelection(all []string, selected []string) map[string]struct{} {
	selectedSet := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		selectedSet[item] = struct{}{}
	}
	ret := make(map[string]struct{})
	for _, item := range all {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := selectedSet[item]; ok {
			continue
		}
		ret[item] = struct{}{}
	}
	return ret
}

func filterToolInfosBySkill(allTools []*schema.ToolInfo, toolMetadataByName map[string]einocallbacks.ToolMetadata, allowedToolCodes []string) []*schema.ToolInfo {
	if len(allTools) == 0 || len(allowedToolCodes) == 0 {
		return append([]*schema.ToolInfo(nil), allTools...)
	}
	ret := make([]*schema.ToolInfo, 0, len(allTools))
	for _, info := range allTools {
		if info == nil {
			continue
		}
		metadata, _ := resolveRuntimeToolMetadata(strings.TrimSpace(info.Name), toolMetadataByName)
		if isRuntimeBuiltinAlwaysAllowed(metadata.ToolCode) || isToolCodeAllowedForSkill(metadata.ToolCode, allowedToolCodes) {
			ret = append(ret, info)
		}
	}
	return ret
}

func extractToolCodesFromInfos(infos []*schema.ToolInfo, toolMetadataByName map[string]einocallbacks.ToolMetadata) []string {
	ret := make([]string, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		metadata, ok := resolveRuntimeToolMetadata(strings.TrimSpace(info.Name), toolMetadataByName)
		if !ok || strings.TrimSpace(metadata.ToolCode) == "" {
			continue
		}
		ret = append(ret, metadata.ToolCode)
	}
	return toolx.NormalizeToolCodes(ret)
}

func isToolCodeAllowedForSkill(toolCode string, allowedToolCodes []string) bool {
	toolCode = toolx.NormalizeToolCodeAlias(strings.TrimSpace(toolCode))
	if toolCode == "" {
		return true
	}
	for _, item := range toolx.NormalizeToolCodes(allowedToolCodes) {
		if item == toolCode {
			return true
		}
	}
	return false
}

func isRuntimeBuiltinAlwaysAllowed(toolCode string) bool {
	toolCode = toolx.NormalizeToolCodeAlias(strings.TrimSpace(toolCode))
	return toolCode == toolx.BuiltinSkill.Code || toolCode == toolx.BuiltinToolSearch.Code
}

func resolveRuntimeToolMetadata(toolName string, toolMetadataByName map[string]einocallbacks.ToolMetadata) (einocallbacks.ToolMetadata, bool) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return einocallbacks.ToolMetadata{}, false
	}
	if spec, ok := toolx.GetRegisteredToolSpecByName(toolName); ok {
		resolved := toolx.ResolveToolMetadata(spec.Code, spec.Name)
		return einocallbacks.ToolMetadata{
			ToolCode:   resolved.ToolCode,
			ServerCode: resolved.ServerCode,
			ToolName:   resolved.ToolName,
			SourceType: resolved.SourceType,
		}, true
	}
	metadata, ok := toolMetadataByName[toolName]
	return metadata, ok
}

func skillCodeFromArguments(argumentsInJSON string) string {
	var args struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(argumentsInJSON)), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Skill)
}

func parseRuntimeToolArguments(argumentsInJSON string) map[string]any {
	argumentsInJSON = strings.TrimSpace(argumentsInJSON)
	if argumentsInJSON == "" {
		return nil
	}
	ret := make(map[string]any)
	if err := json.Unmarshal([]byte(argumentsInJSON), &ret); err != nil {
		return nil
	}
	return ret
}

func filterToolSearchResult(result string, allowedToolCodes []string, toolMetadataByName map[string]einocallbacks.ToolMetadata) (string, error) {
	result = strings.TrimSpace(result)
	if result == "" {
		return result, nil
	}
	var payload struct {
		SelectedTools []string `json:"selectedTools"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return result, err
	}
	filtered := make([]string, 0, len(payload.SelectedTools))
	for _, toolName := range payload.SelectedTools {
		metadata, _ := resolveRuntimeToolMetadata(toolName, toolMetadataByName)
		if isToolCodeAllowedForSkill(metadata.ToolCode, allowedToolCodes) {
			filtered = append(filtered, strings.TrimSpace(toolName))
		}
	}
	payload.SelectedTools = filtered
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func cloneToolMetadataMap(input map[string]einocallbacks.ToolMetadata) map[string]einocallbacks.ToolMetadata {
	if len(input) == 0 {
		return nil
	}
	ret := make(map[string]einocallbacks.ToolMetadata, len(input))
	for key, value := range input {
		ret[key] = value
	}
	return ret
}

func cloneSkillMetadataMap(input map[string]einocallbacks.SkillMetadata) map[string]einocallbacks.SkillMetadata {
	if len(input) == 0 {
		return nil
	}
	ret := make(map[string]einocallbacks.SkillMetadata, len(input))
	for key, value := range input {
		value.AllowedToolCodes = append([]string(nil), value.AllowedToolCodes...)
		ret[key] = value
	}
	return ret
}
