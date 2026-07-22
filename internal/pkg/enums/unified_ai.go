package enums

type ModelProfileStatus string

const (
	ModelProfileStatusDraft     ModelProfileStatus = "draft"
	ModelProfileStatusCandidate ModelProfileStatus = "candidate"
	ModelProfileStatusActive    ModelProfileStatus = "active"
	ModelProfileStatusRetired   ModelProfileStatus = "retired"
	ModelProfileStatusDisabled  ModelProfileStatus = "disabled"
)

type ModelUsageSlot string

const (
	ModelUsageSlotReplyLLM        ModelUsageSlot = "reply_llm"
	ModelUsageSlotIntentDetectLLM ModelUsageSlot = "intent_detect_llm"
	ModelUsageSlotMemorySummary   ModelUsageSlot = "memory_summary_llm"
	ModelUsageSlotCustomerTag     ModelUsageSlot = "customer_tag_llm"
	ModelUsageSlotVision          ModelUsageSlot = "vision"
	ModelUsageSlotASR             ModelUsageSlot = "asr"
	ModelUsageSlotEmbedding       ModelUsageSlot = "embedding"
	ModelUsageSlotRerank          ModelUsageSlot = "rerank"
	ModelUsageSlotDocumentParser  ModelUsageSlot = "document_parser"
)

var RequiredModelUsageSlots = []ModelUsageSlot{
	ModelUsageSlotReplyLLM,
	ModelUsageSlotIntentDetectLLM,
	ModelUsageSlotMemorySummary,
	ModelUsageSlotCustomerTag,
	ModelUsageSlotVision,
	ModelUsageSlotASR,
	ModelUsageSlotEmbedding,
	ModelUsageSlotRerank,
	ModelUsageSlotDocumentParser,
}

type StoreModelAssignmentStatus string

const (
	StoreModelAssignmentStatusAssigned StoreModelAssignmentStatus = "assigned"
	StoreModelAssignmentStatusReady    StoreModelAssignmentStatus = "ready"
	StoreModelAssignmentStatusBlocked  StoreModelAssignmentStatus = "blocked"
	StoreModelAssignmentStatusDisabled StoreModelAssignmentStatus = "disabled"
)

type StoreCredentialStatus string

const (
	StoreCredentialStatusUnconfigured StoreCredentialStatus = "unconfigured"
	StoreCredentialStatusTesting      StoreCredentialStatus = "testing"
	StoreCredentialStatusSyncing      StoreCredentialStatus = "syncing_fastgpt"
	StoreCredentialStatusReady        StoreCredentialStatus = "ready"
	StoreCredentialStatusActive       StoreCredentialStatus = "active"
	StoreCredentialStatusFailed       StoreCredentialStatus = "failed"
	StoreCredentialStatusDisabled     StoreCredentialStatus = "disabled"
)

type CredentialApprovalStatus string

const (
	CredentialApprovalStatusNotRequired CredentialApprovalStatus = "not_required"
	CredentialApprovalStatusPending     CredentialApprovalStatus = "pending"
	CredentialApprovalStatusApproved    CredentialApprovalStatus = "approved"
	CredentialApprovalStatusRejected    CredentialApprovalStatus = "rejected"
)

type CredentialAuditAction string

const (
	CredentialAuditActionConfigure CredentialAuditAction = "configure"
	CredentialAuditActionTest      CredentialAuditAction = "test"
	CredentialAuditActionSubmit    CredentialAuditAction = "submit"
	CredentialAuditActionApprove   CredentialAuditAction = "approve"
	CredentialAuditActionReject    CredentialAuditAction = "reject"
	CredentialAuditActionActivate  CredentialAuditAction = "activate"
	CredentialAuditActionDisable   CredentialAuditAction = "disable"
)

type CredentialAuditResult string

const (
	CredentialAuditResultPending CredentialAuditResult = "pending"
	CredentialAuditResultSuccess CredentialAuditResult = "success"
	CredentialAuditResultFailure CredentialAuditResult = "failure"
)
