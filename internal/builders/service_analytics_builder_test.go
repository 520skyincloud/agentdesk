package builders

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/services"
)

func TestBuildServiceAnalyticsOverviewUsesEmptyArrays(t *testing.T) {
	result := BuildServiceAnalyticsOverview(&services.ServiceAnalyticsOverview{})
	if result == nil {
		t.Fatal("overview response is nil")
	}
	if result.Trend == nil || result.FirstReplyDistribution == nil || result.ResponseDistribution == nil ||
		result.SessionDurationDistribution == nil || result.Agents == nil || result.Sources == nil {
		t.Fatal("overview collection must use empty arrays instead of nil slices")
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal overview response: %v", err)
	}
	for _, field := range []string{"trend", "firstReplyDistribution", "responseDistribution", "sessionDurationDistribution", "agents", "sources"} {
		if strings.Contains(string(payload), `"`+field+`":null`) {
			t.Fatalf("field %s was serialized as null: %s", field, payload)
		}
	}
}
