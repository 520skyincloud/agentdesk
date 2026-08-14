package enums

// MessageAnalysisStatus 是媒体/文本分析的状态机（多模态可靠性计划 7.2）。
// 旧数据中的模糊 "failed" 兼容读取为 failed_terminal；新写入必须使用明确枚举。
type MessageAnalysisStatus string

const (
	MessageAnalysisStatusPending         MessageAnalysisStatus = "pending"
	MessageAnalysisStatusProcessing      MessageAnalysisStatus = "processing"
	MessageAnalysisStatusReady           MessageAnalysisStatus = "ready"
	MessageAnalysisStatusFailedRetryable MessageAnalysisStatus = "failed_retryable"
	MessageAnalysisStatusFailedTerminal  MessageAnalysisStatus = "failed_terminal"
	MessageAnalysisStatusStale           MessageAnalysisStatus = "stale"
	MessageAnalysisStatusLegacyFailed    MessageAnalysisStatus = "failed"
)

// NormalizeMessageAnalysisStatus 把历史值归一为当前合法枚举。
func NormalizeMessageAnalysisStatus(value string) MessageAnalysisStatus {
	switch MessageAnalysisStatus(value) {
	case MessageAnalysisStatusPending, MessageAnalysisStatusProcessing,
		MessageAnalysisStatusReady, MessageAnalysisStatusFailedRetryable,
		MessageAnalysisStatusFailedTerminal, MessageAnalysisStatusStale:
		return MessageAnalysisStatus(value)
	case MessageAnalysisStatusLegacyFailed:
		return MessageAnalysisStatusFailedTerminal
	default:
		return MessageAnalysisStatusPending
	}
}
