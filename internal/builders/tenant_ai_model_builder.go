package builders

import (
	"slices"
	"sort"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
)

func BuildTenantAIModelAccess(
	tenantID int64,
	wxWorkInstanceID int64,
	grants []models.TenantAIModelGrant,
	assignments []models.StoreAIModelSetting,
	configs map[int64]models.AIConfig,
	usageStats map[int64]dto.AIModelUsageAggregate,
) response.TenantAIModelAccessResponse {
	activeGrantIDs := make(map[int64]struct{}, len(grants))
	result := response.TenantAIModelAccessResponse{
		TenantID:         tenantID,
		WxWorkInstanceID: wxWorkInstanceID,
		Grants:           make([]response.TenantAIModelGrantResponse, 0, len(grants)),
		Usages:           make([]response.TenantAIModelUsageResponse, 0, len(constants.AIModelUsageSpecs())),
	}

	for _, grant := range grants {
		if grant.Status != enums.StatusOk {
			continue
		}
		config, ok := configs[grant.AIConfigID]
		if !ok || config.Status == enums.StatusDeleted {
			continue
		}
		activeGrantIDs[grant.AIConfigID] = struct{}{}
		stats := usageStats[grant.AIConfigID]
		usageCodes := make([]string, 0)
		employees := make(map[int64]struct{})
		var assignmentCount int64
		for _, assignment := range assignments {
			if assignment.Status != enums.StatusOk || assignment.AIConfigID != grant.AIConfigID {
				continue
			}
			assignmentCount++
			if !slices.Contains(usageCodes, assignment.UsageCode) {
				usageCodes = append(usageCodes, assignment.UsageCode)
			}
			if assignment.WxWorkInstanceID > 0 {
				employees[assignment.WxWorkInstanceID] = struct{}{}
			}
		}
		sort.Strings(usageCodes)
		result.Grants = append(result.Grants, response.TenantAIModelGrantResponse{
			AIConfigID:            config.ID,
			Name:                  config.Name,
			Provider:              config.Provider,
			ModelType:             config.ModelType,
			ModelName:             config.ModelName,
			Status:                config.Status,
			RequestCount:          stats.RequestCount,
			PromptTokens:          stats.PromptTokens,
			CompletionTokens:      stats.CompletionTokens,
			CachedPromptTokens:    stats.CachedPromptTokens,
			AssignmentCount:       assignmentCount,
			AssignedUsageCodes:    usageCodes,
			AssignedEmployeeCount: int64(len(employees)),
		})
	}

	for _, spec := range constants.AIModelUsageSpecs() {
		selectedID := assignedConfigID(assignments, wxWorkInstanceID, spec.Code)
		effectiveID := selectedID
		source := ""
		if effectiveID > 0 {
			if wxWorkInstanceID > 0 {
				source = constants.AIModelSourceEmployeeOverride
			} else {
				source = constants.AIModelSourceTenantDefault
			}
		}
		if effectiveID == 0 && wxWorkInstanceID > 0 {
			effectiveID = assignedConfigID(assignments, 0, spec.Code)
			if effectiveID > 0 {
				source = constants.AIModelSourceTenantDefault
			}
		}
		if effectiveID == 0 {
			effectiveID = firstAuthorizedConfigID(result.Grants, configs, spec.ExpectedType, spec.Code)
			if effectiveID > 0 {
				source = constants.AIModelSourceTenantFallback
			}
		}
		if _, ok := activeGrantIDs[effectiveID]; !ok {
			effectiveID = 0
			source = ""
		}
		effectiveName := ""
		if config, ok := configs[effectiveID]; ok {
			effectiveName = config.ModelName
		}
		result.Usages = append(result.Usages, response.TenantAIModelUsageResponse{
			UsageCode:           spec.Code,
			UsageName:           spec.Name,
			ExpectedModelType:   spec.ExpectedType,
			AIConfigID:          selectedID,
			EffectiveAIConfigID: effectiveID,
			EffectiveModelName:  effectiveName,
			EffectiveSource:     source,
		})
	}

	return result
}

func assignedConfigID(assignments []models.StoreAIModelSetting, wxWorkInstanceID int64, usageCode string) int64 {
	for _, assignment := range assignments {
		if assignment.Status == enums.StatusOk && assignment.WxWorkInstanceID == wxWorkInstanceID && assignment.UsageCode == usageCode {
			return assignment.AIConfigID
		}
	}
	return 0
}

func firstAuthorizedConfigID(
	grants []response.TenantAIModelGrantResponse,
	configs map[int64]models.AIConfig,
	expectedType enums.AIModelType,
	usageCode string,
) int64 {
	candidates := make([]models.AIConfig, 0, len(grants))
	for _, grant := range grants {
		config := configs[grant.AIConfigID]
		if config.Status != enums.StatusOk || config.ModelType != expectedType || strings.TrimSpace(config.ModelName) == "" || strings.TrimSpace(config.APIKey) == "" {
			continue
		}
		candidates = append(candidates, config)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if usageCode == constants.AIModelUsageIntentDetectLLM && candidates[i].IntentDetectEnabled != candidates[j].IntentDetectEnabled {
			return candidates[i].IntentDetectEnabled
		}
		if candidates[i].SortNo != candidates[j].SortNo {
			return candidates[i].SortNo > candidates[j].SortNo
		}
		return candidates[i].ID > candidates[j].ID
	})
	if len(candidates) > 0 {
		return candidates[0].ID
	}
	return 0
}
