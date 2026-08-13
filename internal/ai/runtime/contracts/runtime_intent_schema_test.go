package contracts

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
)

func TestBuildRuntimeIntentSchemaConstrainsPublishedIntentAndResourceValues(t *testing.T) {
	schema, catalog, err := BuildRuntimeIntentSchema(MustSchema(SchemaIntentTasksV2), []models.ReplyIntentConfig{
		{Code: "hotel_info", Status: enums.StatusOk, NeedsKnowledge: true},
		{Code: "hotel_variable", Status: enums.StatusOk, NeedsResource: true, ResourceType: "store_variable"},
		{Code: "disabled", Status: enums.StatusDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(schema) || len(catalog.IntentCodes) != 2 || catalog.ResourceSubIntent["location"] != "provide_location" {
		t.Fatalf("unexpected runtime catalog: %#v", catalog)
	}
	valid := `{"schemaVersion":"intent_tasks.v2","dialogueAct":"new_topic","tasks":[{"sequence":1,"intent":"hotel_variable","subIntent":"location","text":"发定位","requestMode":"request_action","confidence":0.9}]}`
	if _, err := strictjson.DecodeObject[IntentTasksV2]([]byte(valid), strictjson.DecodeOptions{Schema: schema}); err != nil {
		t.Fatalf("valid runtime intent rejected: %v", err)
	}
	for _, invalid := range []string{
		`{"schemaVersion":"intent_tasks.v2","dialogueAct":"new_topic","tasks":[{"sequence":1,"intent":"unknown","subIntent":"","text":"你好","requestMode":"social","confidence":0.9}]}`,
		`{"schemaVersion":"intent_tasks.v2","dialogueAct":"new_topic","tasks":[{"sequence":1,"intent":"hotel_variable","subIntent":"community_location","text":"发小区定位","requestMode":"request_action","confidence":0.9}]}`,
	} {
		if _, err := strictjson.DecodeObject[IntentTasksV2]([]byte(invalid), strictjson.DecodeOptions{Schema: schema}); err == nil {
			t.Fatalf("invalid runtime intent accepted: %s", invalid)
		}
	}
}
