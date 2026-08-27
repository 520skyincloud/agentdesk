package executor

import (
	"context"
	"encoding/json"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/registry"
	runtimetooling "agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"

	einotool "github.com/cloudwego/eino/components/tool"
)

type preparedTooling struct {
	definitions         []runtimetooling.MCPToolDefinition
	toolCodes           []string
	toolDefsByModelName map[string]string
	staticToolCodes     []string
	staticTools         []einotool.BaseTool
	staticToolCodeMap   map[string]string
	staticToolMetadata  map[string]registry.ToolMetadata
}

func prepareGenerateToolingForIntent(defs []runtimetooling.MCPToolDefinition, toolSet *registry.ToolSet, intent callbacks.IntentTraceData, includeSkillTool bool) preparedTooling {
	requestedToolCodes := toolx.NormalizeToolCodes(intent.ToolCodes)
	if !intent.NeedsTool {
		return prepareTooling(nil, nil, nil, false)
	}
	if len(requestedToolCodes) == 0 {
		return prepareTooling(defs, nil, toolSet, includeSkillTool)
	}

	requestedSet := make(map[string]struct{}, len(requestedToolCodes))
	for _, toolCode := range requestedToolCodes {
		requestedSet[toolCode] = struct{}{}
	}

	filteredDefs := make([]runtimetooling.MCPToolDefinition, 0, len(defs))
	for _, item := range defs {
		toolCode := toolx.NormalizeToolCodeAlias(strings.TrimSpace(item.ToolCode))
		if _, ok := requestedSet[toolCode]; ok {
			filteredDefs = append(filteredDefs, item)
		}
	}

	return prepareTooling(filteredDefs, nil, filterStaticToolSetByCodes(toolSet, requestedSet), false)
}

func filterStaticToolSetByCodes(toolSet *registry.ToolSet, requestedSet map[string]struct{}) *registry.ToolSet {
	if toolSet == nil || len(requestedSet) == 0 {
		return nil
	}

	selectedNames := make(map[string]struct{})
	filtered := &registry.ToolSet{
		StaticToolCodes:    make(map[string]string),
		StaticToolMetadata: make(map[string]registry.ToolMetadata),
	}
	for modelName, toolCode := range toolSet.StaticToolCodes {
		modelName = strings.TrimSpace(modelName)
		toolCode = toolx.NormalizeToolCodeAlias(strings.TrimSpace(toolCode))
		if modelName == "" || toolCode == "" {
			continue
		}
		if _, ok := requestedSet[toolCode]; !ok {
			continue
		}
		selectedNames[modelName] = struct{}{}
		filtered.StaticToolCodes[modelName] = toolCode
		if metadata, ok := toolSet.StaticToolMetadata[modelName]; ok {
			filtered.StaticToolMetadata[modelName] = metadata
		}
	}

	for _, staticTool := range toolSet.StaticTools {
		modelName := staticToolModelName(staticTool)
		if _, ok := selectedNames[modelName]; ok {
			filtered.StaticTools = append(filtered.StaticTools, staticTool)
		}
	}
	if len(filtered.StaticTools) == 0 && len(filtered.StaticToolCodes) == 0 {
		return nil
	}
	return filtered
}

func staticToolModelName(staticTool einotool.BaseTool) string {
	if staticTool == nil {
		return ""
	}
	if namedTool, ok := staticTool.(interface{ Name() string }); ok {
		if name := strings.TrimSpace(namedTool.Name()); name != "" {
			return name
		}
	}
	info, err := staticTool.Info(context.Background())
	if err != nil || info == nil {
		return ""
	}
	return strings.TrimSpace(info.Name)
}

func prepareTooling(defs []runtimetooling.MCPToolDefinition, selectedSkill *models.SkillDefinition, toolSet *registry.ToolSet, includeSkillTool bool) preparedTooling {
	filteredDefs := filterToolDefinitionsBySkill(defs, selectedSkill)
	ret := preparedTooling{
		definitions:         filteredDefs,
		toolCodes:           make([]string, 0, len(filteredDefs)+2),
		toolDefsByModelName: make(map[string]string, len(filteredDefs)),
		staticToolCodes:     staticToolCodeList(toolSet),
		staticTools:         toolSetStaticTools(toolSet),
		staticToolCodeMap:   toolSetStaticToolCodes(toolSet),
		staticToolMetadata:  toolSetStaticToolMetadata(toolSet),
	}
	for _, item := range filteredDefs {
		toolCode := strings.TrimSpace(item.ToolCode)
		modelName := strings.TrimSpace(item.ModelName)
		if toolCode == "" || modelName == "" {
			continue
		}
		ret.toolCodes = appendIfMissing(ret.toolCodes, toolCode)
		ret.toolDefsByModelName[modelName] = toolCode
	}
	if len(filteredDefs) > 0 {
		ret.toolCodes = appendIfMissing(ret.toolCodes, toolx.BuiltinToolSearch.Code)
		ret.toolDefsByModelName[toolx.BuiltinToolSearch.Name] = toolx.BuiltinToolSearch.Code
	}
	if includeSkillTool {
		ret.toolCodes = appendIfMissing(ret.toolCodes, toolx.BuiltinSkill.Code)
		ret.toolDefsByModelName[toolx.BuiltinSkill.Name] = toolx.BuiltinSkill.Code
	}
	for modelName, toolCode := range ret.staticToolCodeMap {
		modelName = strings.TrimSpace(modelName)
		toolCode = strings.TrimSpace(toolCode)
		if modelName == "" || toolCode == "" {
			continue
		}
		ret.toolCodes = appendIfMissing(ret.toolCodes, toolCode)
		ret.toolDefsByModelName[modelName] = toolCode
	}
	return ret
}

func toolSetStaticTools(toolSet *registry.ToolSet) []einotool.BaseTool {
	if toolSet == nil {
		return nil
	}
	return append([]einotool.BaseTool(nil), toolSet.StaticTools...)
}

func toolSetStaticToolCodes(toolSet *registry.ToolSet) map[string]string {
	if toolSet == nil || len(toolSet.StaticToolCodes) == 0 {
		return nil
	}
	ret := make(map[string]string, len(toolSet.StaticToolCodes))
	for name, code := range toolSet.StaticToolCodes {
		ret[strings.TrimSpace(name)] = strings.TrimSpace(code)
	}
	return ret
}

func toolSetStaticToolMetadata(toolSet *registry.ToolSet) map[string]registry.ToolMetadata {
	if toolSet == nil || len(toolSet.StaticToolMetadata) == 0 {
		return nil
	}
	ret := make(map[string]registry.ToolMetadata, len(toolSet.StaticToolMetadata))
	for name, item := range toolSet.StaticToolMetadata {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		item.ToolCode = strings.TrimSpace(item.ToolCode)
		item.ServerCode = strings.TrimSpace(item.ServerCode)
		item.ToolName = strings.TrimSpace(item.ToolName)
		ret[trimmedName] = item
	}
	return ret
}

func staticToolCodeList(toolSet *registry.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	metadata := toolSetStaticToolMetadata(toolSet)
	ret := make([]string, 0, len(metadata))
	for _, item := range metadata {
		code := strings.TrimSpace(item.ToolCode)
		if code == "" {
			continue
		}
		ret = appendIfMissing(ret, code)
	}
	if len(ret) > 0 {
		return ret
	}
	for _, code := range toolSetStaticToolCodes(toolSet) {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		ret = appendIfMissing(ret, code)
	}
	return ret
}

func definitionToolCodes(defs []runtimetooling.MCPToolDefinition) []string {
	ret := make([]string, 0, len(defs))
	for _, item := range defs {
		code := strings.TrimSpace(item.ToolCode)
		if code == "" {
			continue
		}
		ret = append(ret, code)
	}
	return ret
}

func filterToolDefinitionsBySkill(defs []runtimetooling.MCPToolDefinition, skill *models.SkillDefinition) []runtimetooling.MCPToolDefinition {
	if skill == nil || strings.TrimSpace(skill.ToolWhitelist) == "" {
		return defs
	}
	var allowed []string
	if err := json.Unmarshal([]byte(skill.ToolWhitelist), &allowed); err != nil {
		return defs
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, item := range allowed {
		item = toolx.NormalizeToolCodeAlias(item)
		if strings.TrimSpace(item) == "" {
			continue
		}
		allowedSet[strings.TrimSpace(item)] = struct{}{}
	}
	if len(allowedSet) == 0 {
		return defs
	}
	ret := make([]runtimetooling.MCPToolDefinition, 0, len(defs))
	for _, item := range defs {
		if _, ok := allowedSet[strings.TrimSpace(item.ToolCode)]; ok {
			ret = append(ret, item)
		}
	}
	return ret
}

func parseJSONArrayList(raw string) []string {
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
	}
	return ret
}
