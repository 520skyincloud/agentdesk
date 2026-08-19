package enums

type AIReplyJobStatus string

const (
	AIReplyJobStatusPending    AIReplyJobStatus = "pending"
	AIReplyJobStatusProcessing AIReplyJobStatus = "processing"
	AIReplyJobStatusRetry      AIReplyJobStatus = "retry"
	AIReplyJobStatusCompleted  AIReplyJobStatus = "completed"
	AIReplyJobStatusSkipped    AIReplyJobStatus = "skipped"
	AIReplyJobStatusSuperseded AIReplyJobStatus = "superseded"
	AIReplyJobStatusExpired    AIReplyJobStatus = "expired"
	AIReplyJobStatusFailed     AIReplyJobStatus = "failed"
)

type AIReplyJobTriggerKind string

const (
	AIReplyJobTriggerKindText          AIReplyJobTriggerKind = "text"
	AIReplyJobTriggerKindMedia         AIReplyJobTriggerKind = "media"
	AIReplyJobTriggerKindStandaloneOne AIReplyJobTriggerKind = "standalone_one"
)
