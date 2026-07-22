package runtime

import "encoding/json"

type aiReplyTraceData struct {
	Status                    string          `json:"status"`
	StoreID                   int64           `json:"storeId,omitempty"`
	ModelProfileID            int64           `json:"modelProfileId,omitempty"`
	ModelProfileRevision      int64           `json:"modelProfileRevision,omitempty"`
	ModelSlotID               int64           `json:"modelSlotId,omitempty"`
	UsageSlot                 string          `json:"usageSlot,omitempty"`
	CredentialRevision        int64           `json:"credentialRevision,omitempty"`
	ModelSource               string          `json:"modelSource,omitempty"`
	ConfiguredMaxOutputTokens int             `json:"configuredMaxOutputTokens,omitempty"`
	EffectiveMaxOutputTokens  int             `json:"effectiveMaxOutputTokens,omitempty"`
	SettleMs                  int64           `json:"settleMs,omitempty"`
	RuntimeLatencyMs          int64           `json:"runtimeLatencyMs,omitempty"`
	RecheckMs                 int64           `json:"recheckMs,omitempty"`
	CommitMs                  int64           `json:"commitMs,omitempty"`
	FinalAction               string          `json:"finalAction,omitempty"`
	ResumeSource              string          `json:"resumeSource,omitempty"`
	ReplySent                 bool            `json:"replySent,omitempty"`
	ReplyMessageID            int64           `json:"replyMessageId,omitempty"`
	ReplyMessageType          string          `json:"replyMessageType,omitempty"`
	Runtime                   json.RawMessage `json:"runtime,omitempty"`
}

const (
	defaultAIReplyAsyncTimeoutSeconds = 180
	maxAIReplyAsyncTimeoutSeconds     = 600
)
