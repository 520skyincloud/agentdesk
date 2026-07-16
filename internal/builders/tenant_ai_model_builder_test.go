package builders

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
)

func TestBuildTenantAIModelAccessMatchesRuntimeFallbackPriority(t *testing.T) {
	const tenantID int64 = 10
	configs := map[int64]models.AIConfig{
		1: {ID: 1, Name: "older", ModelType: enums.AIModelTypeLLM, ModelName: "older-model", APIKey: "key", SortNo: 10, Status: enums.StatusOk},
		2: {ID: 2, Name: "preferred", ModelType: enums.AIModelTypeLLM, ModelName: "preferred-model", APIKey: "key", SortNo: 50, Status: enums.StatusOk},
		3: {ID: 3, Name: "intent", ModelType: enums.AIModelTypeLLM, ModelName: "intent-model", APIKey: "key", SortNo: 1, IntentDetectEnabled: true, Status: enums.StatusOk},
	}
	grants := []models.TenantAIModelGrant{
		{TenantID: tenantID, AIConfigID: 1, Status: enums.StatusOk},
		{TenantID: tenantID, AIConfigID: 2, Status: enums.StatusOk},
		{TenantID: tenantID, AIConfigID: 3, Status: enums.StatusOk},
	}

	result := BuildTenantAIModelAccess(tenantID, 0, grants, nil, configs, nil)
	reply := findTenantModelUsage(t, result.Usages, constants.AIModelUsageReplyLLM)
	if reply.EffectiveAIConfigID != 2 || reply.EffectiveSource != constants.AIModelSourceTenantFallback {
		t.Fatalf("reply fallback = %#v", reply)
	}
	intent := findTenantModelUsage(t, result.Usages, constants.AIModelUsageIntentDetectLLM)
	if intent.EffectiveAIConfigID != 3 || intent.EffectiveSource != constants.AIModelSourceTenantFallback {
		t.Fatalf("intent fallback = %#v", intent)
	}
}

func TestBuildTenantAIModelAccessLabelsTenantDefaultSource(t *testing.T) {
	const tenantID int64 = 11
	config := models.AIConfig{ID: 4, Name: "default", ModelType: enums.AIModelTypeLLM, ModelName: "default-model", APIKey: "key", Status: enums.StatusOk}
	result := BuildTenantAIModelAccess(
		tenantID,
		0,
		[]models.TenantAIModelGrant{{TenantID: tenantID, AIConfigID: config.ID, Status: enums.StatusOk}},
		[]models.StoreAIModelSetting{{TenantID: tenantID, UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: config.ID, Status: enums.StatusOk}},
		map[int64]models.AIConfig{config.ID: config},
		nil,
	)
	reply := findTenantModelUsage(t, result.Usages, constants.AIModelUsageReplyLLM)
	if reply.EffectiveSource != constants.AIModelSourceTenantDefault {
		t.Fatalf("tenant default source = %q", reply.EffectiveSource)
	}
}

func findTenantModelUsage(t *testing.T, usages []response.TenantAIModelUsageResponse, code string) response.TenantAIModelUsageResponse {
	t.Helper()
	for _, usage := range usages {
		if usage.UsageCode == code {
			return usage
		}
	}
	t.Fatalf("usage %s not found", code)
	return response.TenantAIModelUsageResponse{}
}
