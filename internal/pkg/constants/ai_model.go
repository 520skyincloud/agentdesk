package constants

import "agent-desk/internal/pkg/enums"

const (
	AIModelUsageReplyLLM           = "reply_llm"
	AIModelUsageIntentDetectLLM    = "intent_detect_llm"
	AIModelUsageMediaUnderstanding = "media_understanding"
	AIModelUsageSpeechRecognition  = "speech_recognition"
)

const (
	AIModelSourceEmployeeOverride = "employee_override"
	AIModelSourceTenantDefault    = "tenant_default"
	AIModelSourceTenantFallback   = "tenant_authorized_fallback"
	AIModelSourcePlatformDefault  = "platform_default"
)

type AIModelUsageSpec struct {
	Code         string
	Name         string
	ExpectedType enums.AIModelType
}

func AIModelUsageSpecs() []AIModelUsageSpec {
	return []AIModelUsageSpec{
		{Code: AIModelUsageReplyLLM, Name: "回复生成模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: AIModelUsageIntentDetectLLM, Name: "意图识别模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: AIModelUsageMediaUnderstanding, Name: "媒体理解模型", ExpectedType: enums.AIModelTypeVision},
		{Code: AIModelUsageSpeechRecognition, Name: "语音识别模型", ExpectedType: enums.AIModelTypeASR},
	}
}

func AIModelUsageSpecByCode(code string) (AIModelUsageSpec, bool) {
	for _, spec := range AIModelUsageSpecs() {
		if spec.Code == code {
			return spec, true
		}
	}
	return AIModelUsageSpec{}, false
}
