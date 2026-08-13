package contracts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

type RuntimeIntentSchemaCatalog struct {
	IntentCodes       []string
	ResourceSubIntent map[string]string
	resourceByIntent  map[string][]string
}

type runtimeResourceDefinition struct {
	Canonical string
	Action    string
	Aliases   []string
}

var runtimeResourceDefinitions = []runtimeResourceDefinition{
	{Canonical: "phone", Action: "provide_phone", Aliases: []string{"contact_phone", "store_phone"}},
	{Canonical: "location", Action: "provide_location", Aliases: []string{"address", "navigation", "store_location"}},
	{Canonical: "mini_program", Action: "provide_mini_program", Aliases: []string{"miniprogram", "miniProgram", "checkin_miniprogram", "send_miniprogram"}},
	{Canonical: "store_group", Action: "provide_store_group", Aliases: []string{"room_group"}},
}

func BuildRuntimeIntentSchema(baseSchema []byte, configs []models.ReplyIntentConfig) ([]byte, RuntimeIntentSchemaCatalog, error) {
	var root map[string]any
	if err := json.Unmarshal(baseSchema, &root); err != nil {
		return nil, RuntimeIntentSchemaCatalog{}, fmt.Errorf("decode base intent schema: %w", err)
	}
	item, err := runtimeIntentTaskItemSchema(root)
	if err != nil {
		return nil, RuntimeIntentSchemaCatalog{}, err
	}
	catalog := buildRuntimeIntentSchemaCatalog(configs)
	if len(catalog.IntentCodes) == 0 {
		return nil, RuntimeIntentSchemaCatalog{}, fmt.Errorf("runtime intent schema has no enabled intent codes")
	}

	branches := make([]any, 0, len(catalog.IntentCodes))
	for _, code := range catalog.IntentCodes {
		branch, err := cloneRuntimeSchemaObject(item)
		if err != nil {
			return nil, RuntimeIntentSchemaCatalog{}, err
		}
		properties, _ := branch["properties"].(map[string]any)
		properties["intent"] = map[string]any{"type": "string", "enum": []string{code}}
		if values := catalog.resourceByIntent[code]; len(values) > 0 {
			properties["subIntent"] = map[string]any{"type": "string", "enum": values}
		}
		branches = append(branches, branch)
	}
	properties, _ := root["properties"].(map[string]any)
	tasks, _ := properties["tasks"].(map[string]any)
	tasks["items"] = map[string]any{"anyOf": branches}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, RuntimeIntentSchemaCatalog{}, fmt.Errorf("encode runtime intent schema: %w", err)
	}
	return raw, catalog, nil
}

func (c RuntimeIntentSchemaCatalog) AllowedSubIntents(intentCode string) []string {
	return append([]string(nil), c.resourceByIntent[strings.TrimSpace(intentCode)]...)
}

func NormalizeRuntimeResourceSubIntent(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	for _, definition := range runtimeResourceDefinitions {
		if value == definition.Canonical {
			return definition.Canonical, definition.Action, true
		}
		for _, alias := range definition.Aliases {
			if value == alias {
				return definition.Canonical, definition.Action, true
			}
		}
	}
	return value, "", false
}

func buildRuntimeIntentSchemaCatalog(configs []models.ReplyIntentConfig) RuntimeIntentSchemaCatalog {
	catalog := RuntimeIntentSchemaCatalog{
		IntentCodes:       make([]string, 0, len(configs)),
		ResourceSubIntent: make(map[string]string),
		resourceByIntent:  make(map[string][]string),
	}
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		code := strings.TrimSpace(config.Code)
		if code == "" || config.Status != enums.StatusOk {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		catalog.IntentCodes = append(catalog.IntentCodes, code)
		if !config.NeedsResource {
			continue
		}
		configured := strings.TrimSpace(config.ResourceType)
		if configured == "" || configured == "store_variable" {
			for _, definition := range runtimeResourceDefinitions {
				catalog.resourceByIntent[code] = append(catalog.resourceByIntent[code], definition.Canonical)
				catalog.ResourceSubIntent[definition.Canonical] = definition.Action
			}
			continue
		}
		canonical, action, ok := NormalizeRuntimeResourceSubIntent(configured)
		if ok {
			catalog.resourceByIntent[code] = []string{canonical}
			catalog.ResourceSubIntent[canonical] = action
		}
	}
	sort.Strings(catalog.IntentCodes)
	return catalog
}

func runtimeIntentTaskItemSchema(root map[string]any) (map[string]any, error) {
	properties, ok := root["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("base intent schema properties are invalid")
	}
	tasks, ok := properties["tasks"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("base intent schema tasks are invalid")
	}
	item, ok := tasks["items"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("base intent schema task item is invalid")
	}
	return item, nil
}

func cloneRuntimeSchemaObject(value map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("clone runtime schema object: %w", err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("decode cloned runtime schema object: %w", err)
	}
	return cloned, nil
}
