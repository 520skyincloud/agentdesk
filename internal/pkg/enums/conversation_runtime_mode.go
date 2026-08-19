package enums

type ConversationRuntimeMode string

const (
	ConversationRuntimeModeAIActive      ConversationRuntimeMode = "ai_active"
	ConversationRuntimeModeAIDegraded    ConversationRuntimeMode = "ai_degraded"
	ConversationRuntimeModeHumanPending  ConversationRuntimeMode = "human_pending"
	ConversationRuntimeModeHumanActive   ConversationRuntimeMode = "human_active"
	ConversationRuntimeModeResumePending ConversationRuntimeMode = "resume_pending"
	ConversationRuntimeModeClosed        ConversationRuntimeMode = "closed"
)
