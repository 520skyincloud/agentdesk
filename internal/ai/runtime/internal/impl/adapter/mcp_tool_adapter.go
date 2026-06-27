package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/ai/mcps"
	"agent-desk/internal/ai/runtime/tooling"

	"github.com/eino-contrib/jsonschema"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type MCPTool struct {
	definition tooling.MCPToolDefinition
	info       *schema.ToolInfo
}

func NewMCPTool(definition tooling.MCPToolDefinition, metadata *mcps.ToolInfo) *MCPTool {
	return &MCPTool{
		definition: definition,
		info:       buildToolInfo(definition, metadata),
	}
}

var _ tool.InvokableTool = (*MCPTool)(nil)

func (t *MCPTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.info == nil {
		return nil, nil
	}
	return t.info, nil
}

func (t *MCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if t == nil {
		return "", fmt.Errorf("mcp tool is nil")
	}
	arguments, err := parseArguments(argumentsInJSON)
	if err != nil {
		return "", err
	}
	arguments = mergeFixedArguments(arguments, t.definition.FixedArgs)
	result, err := mcps.Runtime.CallTool(ctx, t.definition.ServerCode, t.definition.ToolName, arguments)
	if err != nil {
		return "", err
	}
	return BuildReducedToolResultSummary(result), nil
}

func buildToolInfo(definition tooling.MCPToolDefinition, metadata *mcps.ToolInfo) *schema.ToolInfo {
	desc := strings.TrimSpace(definition.Description)
	if desc == "" && metadata != nil {
		desc = strings.TrimSpace(metadata.Description)
	}
	title := strings.TrimSpace(definition.Title)
	if title == "" && metadata != nil {
		title = strings.TrimSpace(metadata.Title)
	}
	if title != "" && desc != "" {
		desc = title + "\n\n" + desc
	} else if title != "" {
		desc = title
	}
	if desc == "" {
		desc = "Call MCP tool " + strings.TrimSpace(definition.ToolCode)
	}
	info := &schema.ToolInfo{
		Name: tooling.BuildModelToolName(definition),
		Desc: desc,
		Extra: map[string]any{
			"toolCode":   definition.ToolCode,
			"serverCode": definition.ServerCode,
			"toolName":   definition.ToolName,
		},
	}
	if js := buildParamsSchema(metadata); js != nil {
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	}
	return info
}

func buildParamsSchema(metadata *mcps.ToolInfo) *jsonschema.Schema {
	if metadata == nil || metadata.InputSchema == nil {
		return genericObjectSchema()
	}
	raw, err := json.Marshal(metadata.InputSchema)
	if err != nil || len(raw) == 0 {
		return genericObjectSchema()
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, js); err != nil {
		return genericObjectSchema()
	}
	return js
}

func genericObjectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Version:              jsonschema.Version,
		Type:                 "object",
		AdditionalProperties: &jsonschema.Schema{},
	}
}

func parseArguments(argumentsInJSON string) (map[string]any, error) {
	argumentsInJSON = strings.TrimSpace(argumentsInJSON)
	if argumentsInJSON == "" {
		return map[string]any{}, nil
	}
	args := make(map[string]any)
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return args, nil
}

func mergeFixedArguments(arguments map[string]any, fixedArgs map[string]string) map[string]any {
	if len(arguments) == 0 && len(fixedArgs) == 0 {
		return map[string]any{}
	}
	ret := make(map[string]any, len(arguments)+len(fixedArgs))
	for key, value := range arguments {
		ret[key] = value
	}
	for key, value := range fixedArgs {
		ret[key] = strings.TrimSpace(value)
	}
	return ret
}
