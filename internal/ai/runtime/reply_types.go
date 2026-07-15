package runtime

import "encoding/json"

type aiReplyTraceData struct {
	Status                    string          `json:"status"`
	AIConfigID                int64           `json:"aiConfigId,omitempty"`
	ModelSource               string          `json:"modelSource,omitempty"`
	ModelSettingID            int64           `json:"modelSettingId,omitempty"`
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
