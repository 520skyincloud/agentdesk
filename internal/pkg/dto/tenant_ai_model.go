package dto

type AIModelUsageAggregate struct {
	RequestCount       int64
	PromptTokens       int64
	CompletionTokens   int64
	CachedPromptTokens int64
}
