package builders

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/services"
)

func BuildStoreModelCredential(data *services.StoreModelCredentialData) *response.StoreModelCredentialResponse {
	if data == nil {
		return nil
	}
	ret := &response.StoreModelCredentialResponse{
		TenantID: data.Store.TenantID,
		StoreID:  data.Store.ID,
		StoreStaffBindingID: func() int64 {
			if data.Binding == nil {
				return 0
			}
			return data.Binding.ID
		}(),
		StoreStaffAccountName: data.BindingAccountName,
		StoreCode:             data.Store.StoreCode,
		StoreName:             data.Store.Name,
		CredentialStatus:      string(enums.StoreCredentialStatusUnconfigured),
		ActiveModelNames:      storeCredentialModelNames(data.ActiveSlots),
		PendingModelNames:     storeCredentialModelNames(data.PendingSlots),
		CanSelfService:        data.CanSelfService,
	}
	if data.Assignment != nil {
		ret.ActiveProfileID = data.Assignment.TemplateID
		ret.ActiveProfileRevision = data.Assignment.TemplateRevision
		ret.PendingProfileID = data.Assignment.PendingTemplateID
		ret.PendingProfileRevision = data.Assignment.PendingTemplateRevision
	}
	if data.ActiveTemplate != nil {
		ret.ActiveProfileName = data.ActiveTemplate.Name
	}
	if data.PendingTemplate != nil {
		ret.PendingProfileName = data.PendingTemplate.Name
	}
	if data.Policy != nil {
		ret.AllowCredentialSelfService = data.Policy.AllowCredentialSelfService
		ret.RequireSupervisorApproval = data.Policy.RequireSupervisorApproval
	}
	if data.Credential == nil {
		return ret
	}
	credential := data.Credential
	ret.HasKey = credential.CredentialRevision > 0 && strings.TrimSpace(credential.EncryptedKey) != ""
	ret.KeyMask = services.MaskedStoreCredentialKey(ret.HasKey)
	ret.FingerprintLast6 = securex.FingerprintLast6(credential.KeyFingerprint)
	ret.CredentialRevision = credential.CredentialRevision
	if credential.Status != "" {
		ret.CredentialStatus = string(credential.Status)
	}
	ret.CandidateRevision = credential.CandidateRevision
	ret.CandidateStatus = string(credential.CandidateStatus)
	ret.CandidateApprovalStatus = string(credential.CandidateApprovalStatus)
	ret.CandidateProfileID = credential.CandidateProfileID
	ret.CandidateProfileRevision = credential.CandidateProfileRevision
	ret.CandidateFingerprintLast6 = securex.FingerprintLast6(credential.CandidateKeyFingerprint)
	ret.CandidateRequestedAt = credential.CandidateRequestedAt
	ret.LastTestStatus = credential.LastTestStatus
	ret.LastTestedAt = credential.LastTestedAt
	ret.LastTestLatencyMS = credential.LastTestLatencyMS
	ret.LastFastGPTSyncStatus = credential.LastFastGPTSyncStatus
	ret.LastFastGPTSyncedAt = credential.LastFastGPTSyncedAt
	ret.LastErrorClass = credential.LastErrorClass
	ret.LastErrorMessage = credential.LastErrorMessage
	return ret
}

func BuildStoreModelCredentialAuditList(items []models.StoreModelCredentialAuditLog) []response.StoreModelCredentialAuditResponse {
	ret := make([]response.StoreModelCredentialAuditResponse, 0, len(items))
	for i := range items {
		item := &items[i]
		ret = append(ret, response.StoreModelCredentialAuditResponse{
			ID: item.ID, Action: string(item.Action), Result: string(item.Result),
			FromRevision: item.FromRevision, ToRevision: item.ToRevision,
			ProfileID: item.ProfileID, ProfileRevision: item.ProfileRevision,
			FingerprintLast6: item.FingerprintLast6,
			OperatorName:     item.OperatorName, OperatorRole: item.OperatorRole,
			ApproverName: item.ApproverName, ErrorClass: item.ErrorClass,
			RequestID: item.RequestID, ClientIP: item.ClientIP, CreatedAt: item.CreatedAt,
		})
	}
	return ret
}

func storeCredentialModelNames(slots []models.ModelProfileSlot) []string {
	ret := make([]string, 0, len(slots))
	seen := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		name := strings.TrimSpace(slot.ModelName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		ret = append(ret, name)
	}
	return ret
}
