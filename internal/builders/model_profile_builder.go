package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
)

func BuildModelProfileTemplate(item *models.ModelProfileTemplate, slots []models.ModelProfileSlot) response.ModelProfileTemplateResponse {
	result := response.ModelProfileTemplateResponse{
		ID: item.ID, Code: item.Code, Name: item.Name, Description: item.Description,
		Revision: item.Revision, GatewayBaseURL: item.GatewayBaseURL, Status: string(item.Status),
		PublishedAt: item.PublishedAt, PublishedByName: item.PublishedByName,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		Slots: make([]response.ModelProfileSlotResponse, 0, len(slots)),
	}
	for i := range slots {
		result.Slots = append(result.Slots, BuildModelProfileSlot(&slots[i]))
	}
	return result
}

func BuildModelProfileTemplateWithTest(
	item *models.ModelProfileTemplate,
	slots []models.ModelProfileSlot,
	configDigest string,
	latestTest *models.ModelProfileTestRun,
) response.ModelProfileTemplateResponse {
	result := BuildModelProfileTemplate(item, slots)
	result.ConfigDigest = configDigest
	result.LatestTest = BuildModelProfileTestRun(latestTest)
	return result
}

func BuildModelProfileTestRun(item *models.ModelProfileTestRun) *response.ModelProfileTestRunResponse {
	if item == nil {
		return nil
	}
	return &response.ModelProfileTestRunResponse{
		ID: item.ID, TenantID: item.TenantID, TenantName: item.TenantName,
		StoreID: item.StoreID, StoreName: item.StoreName, StoreStaffBindingID: item.StoreStaffBindingID,
		CredentialRevision: item.CredentialRevision, CredentialSource: string(item.CredentialSource),
		Status: string(item.Status), FailedUsageCode: string(item.FailedUsageCode),
		ErrorClass: item.ErrorClass, ErrorMessage: item.ErrorMessage,
		LatencyMS: item.LatencyMS, OperatorName: item.OperatorName, CreatedAt: item.CreatedAt,
	}
}

func BuildModelProfileSlot(item *models.ModelProfileSlot) response.ModelProfileSlotResponse {
	return response.ModelProfileSlotResponse{
		ID: item.ID, UsageCode: string(item.UsageCode), DisplayName: item.DisplayName,
		ModelType: string(item.ModelType), Provider: item.Provider, ModelName: item.ModelName,
		APIMode: item.APIMode, Dimension: item.Dimension,
		MaxContextTokens: item.MaxContextTokens, MaxOutputTokens: item.MaxOutputTokens,
		TimeoutMS: item.TimeoutMS, MaxRetryCount: item.MaxRetryCount, Temperature: item.Temperature,
		SchemaVersion: item.SchemaVersion, PromptTemplate: item.PromptTemplate, JSONSchema: item.JSONSchema,
		Enabled: item.Enabled, SortNo: item.SortNo,
	}
}

func BuildStoreModelProfileOption(item *models.ModelProfileTemplate, slots []models.ModelProfileSlot) response.StoreModelProfileOptionResponse {
	modelNames := make([]string, 0, len(slots))
	seen := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		if slot.ModelName == "" {
			continue
		}
		if _, exists := seen[slot.ModelName]; exists {
			continue
		}
		seen[slot.ModelName] = struct{}{}
		modelNames = append(modelNames, slot.ModelName)
	}
	return response.StoreModelProfileOptionResponse{
		TemplateID: item.ID, Code: item.Code, Name: item.Name, Revision: item.Revision,
		Status: string(item.Status), ModelNames: modelNames,
	}
}

func BuildStoreModelProfileAssignment(
	store *models.Store,
	assignment *models.StoreModelProfileAssignment,
	templates map[int64]models.ModelProfileTemplate,
) response.StoreModelProfileAssignmentResponse {
	result := response.StoreModelProfileAssignmentResponse{
		TenantID: store.TenantID, StoreID: store.ID, StoreCode: store.StoreCode, StoreName: store.Name,
		Status: "unassigned", ReadinessStatus: "unconfigured",
	}
	if assignment == nil {
		return result
	}
	result.AssignmentID = assignment.ID
	result.Status = string(assignment.Status)
	result.ReadinessStatus = assignment.ReadinessStatus
	result.ActiveTemplateID = assignment.TemplateID
	result.ActiveTemplateRevision = assignment.TemplateRevision
	result.PendingTemplateID = assignment.PendingTemplateID
	result.PendingTemplateRevision = assignment.PendingTemplateRevision
	result.PendingRequestedAt = assignment.PendingRequestedAt
	result.LastValidatedAt = assignment.LastValidatedAt
	result.LastReadyAt = assignment.LastReadyAt
	result.LastErrorMessage = assignment.LastErrorMessage
	if item, exists := templates[assignment.TemplateID]; exists {
		result.ActiveTemplateName = item.Name
	}
	if item, exists := templates[assignment.PendingTemplateID]; exists {
		result.PendingTemplateName = item.Name
	}
	return result
}
