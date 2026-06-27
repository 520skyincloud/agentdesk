package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	runtimeinstruction "agent-desk/internal/ai/runtime/instruction"
	runtimetooling "agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

type runtimeSkillMetadata struct {
	Code             string
	Name             string
	Description      string
	AllowedToolCodes []string
}

type databaseSkillBackend struct {
	toolDefinitions []runtimetooling.MCPToolDefinition
	skillsByCode    map[string]models.SkillDefinition
	order           []string
}

func newDatabaseSkillBackend(aiAgent models.AIAgent, toolDefinitions []runtimetooling.MCPToolDefinition) (*databaseSkillBackend, error) {
	visibleSkills := loadVisibleSkills(aiAgent)
	if len(visibleSkills) == 0 {
		return nil, fmt.Errorf("no visible skills available")
	}
	ret := &databaseSkillBackend{
		toolDefinitions: append([]runtimetooling.MCPToolDefinition(nil), toolDefinitions...),
		skillsByCode:    make(map[string]models.SkillDefinition, len(visibleSkills)),
		order:           make([]string, 0, len(visibleSkills)),
	}
	for _, item := range visibleSkills {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		ret.skillsByCode[code] = item
		ret.order = append(ret.order, code)
	}
	if len(ret.skillsByCode) == 0 {
		return nil, fmt.Errorf("no visible skills available")
	}
	return ret, nil
}

func (b *databaseSkillBackend) List(_ context.Context) ([]einoskill.FrontMatter, error) {
	if b == nil || len(b.order) == 0 {
		return nil, nil
	}
	ret := make([]einoskill.FrontMatter, 0, len(b.order))
	for _, code := range b.order {
		item, ok := b.skillsByCode[code]
		if !ok {
			continue
		}
		ret = append(ret, einoskill.FrontMatter{
			Name:        strings.TrimSpace(item.Code),
			Description: skillListDescription(item),
		})
	}
	return ret, nil
}

func (b *databaseSkillBackend) Get(_ context.Context, name string) (einoskill.Skill, error) {
	if b == nil {
		return einoskill.Skill{}, fmt.Errorf("database skill backend is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return einoskill.Skill{}, fmt.Errorf("skill name is empty")
	}
	item, ok := b.skillsByCode[name]
	if !ok {
		return einoskill.Skill{}, fmt.Errorf("skill %q not found", name)
	}
	return einoskill.Skill{
		FrontMatter: einoskill.FrontMatter{
			Name:        strings.TrimSpace(item.Code),
			Description: skillListDescription(item),
		},
		Content:       runtimeinstruction.BuildSkillDocument(&item, filterSkillToolDefinitions(b.toolDefinitions, &item)),
		BaseDirectory: "",
	}, nil
}

func loadVisibleSkills(aiAgent models.AIAgent) []models.SkillDefinition {
	ids := utils.SplitInt64s(strings.TrimSpace(aiAgent.SkillIDs))
	if len(ids) == 0 {
		return nil
	}
	byID := services.SkillDefinitionService.GetByIDs(ids)
	if len(byID) == 0 {
		return nil
	}
	ret := make([]models.SkillDefinition, 0, len(ids))
	for _, id := range ids {
		item, ok := byID[id]
		if !ok || item.Status != enums.StatusOk || strings.TrimSpace(item.Code) == "" {
			continue
		}
		ret = append(ret, item)
	}
	return ret
}

func buildRuntimeSkillMetadataMap(aiAgent models.AIAgent) map[string]runtimeSkillMetadata {
	visibleSkills := loadVisibleSkills(aiAgent)
	if len(visibleSkills) == 0 {
		return nil
	}
	ret := make(map[string]runtimeSkillMetadata, len(visibleSkills))
	for _, item := range visibleSkills {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		ret[code] = runtimeSkillMetadata{
			Code:             code,
			Name:             strings.TrimSpace(item.Name),
			Description:      skillListDescription(item),
			AllowedToolCodes: parseSkillToolWhitelist(item.ToolWhitelist),
		}
	}
	return ret
}

func HasVisibleSkills(aiAgent models.AIAgent) bool {
	return len(buildRuntimeSkillMetadataMap(aiAgent)) > 0
}

func skillListDescription(item models.SkillDefinition) string {
	if desc := strings.TrimSpace(item.Description); desc != "" {
		return desc
	}
	if name := strings.TrimSpace(item.Name); name != "" {
		return name
	}
	return strings.TrimSpace(item.Code)
}

func parseSkillToolWhitelist(raw string) []string {
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

func filterSkillToolDefinitions(defs []runtimetooling.MCPToolDefinition, skill *models.SkillDefinition) []runtimetooling.MCPToolDefinition {
	allowed := parseSkillToolWhitelist(skill.ToolWhitelist)
	if len(allowed) == 0 {
		return defs
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, item := range allowed {
		allowedSet[item] = struct{}{}
	}
	ret := make([]runtimetooling.MCPToolDefinition, 0, len(defs))
	for _, item := range defs {
		if _, ok := allowedSet[strings.TrimSpace(item.ToolCode)]; ok {
			ret = append(ret, item)
		}
	}
	return ret
}
