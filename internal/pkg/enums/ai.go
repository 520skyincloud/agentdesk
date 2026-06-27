package enums

type AIProvider string

const (
	AIProviderOpenAI AIProvider = "openai"
)

var aiProviderLabelMap = map[AIProvider]string{
	AIProviderOpenAI: "OpenAI",
}

func GetAIProviderLabel(provider AIProvider) string {
	return aiProviderLabelMap[provider]
}

type AIModelType string

const (
	AIModelTypeLLM       AIModelType = "llm"
	AIModelTypeEmbedding AIModelType = "embedding"
	AIModelTypeRerank    AIModelType = "rerank"
	AIModelTypeVision    AIModelType = "vision"
	AIModelTypeASR       AIModelType = "asr"
	AIModelTypeTTS       AIModelType = "tts"
)

var aiModelTypeLabelMap = map[AIModelType]string{
	AIModelTypeLLM:       "大语言模型",
	AIModelTypeEmbedding: "向量模型",
	AIModelTypeRerank:    "重排序模型",
	AIModelTypeVision:    "视觉/多模态模型",
	AIModelTypeASR:       "语音识别模型",
	AIModelTypeTTS:       "语音合成模型",
}

func GetAIModelTypeLabel(modelType AIModelType) string {
	return aiModelTypeLabelMap[modelType]
}
