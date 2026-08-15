package contracts

import "time"

// 多模态契约 7：message_analysis.v2 权威媒体分析。Writer/Reader/Schema 必须
// 成组启用；analyzer.kind 允许 vision/asr/file_parser，V1 只保留只读兼容。

const MessageAnalysisV2SchemaVersion = SchemaMessageAnalysisV2

// MessageAnalysisV2 是媒体分析的权威 JSON 形态。
type MessageAnalysisV2 struct {
	SchemaVersion      string                        `json:"schemaVersion"`
	MessageID          int64                         `json:"messageId"`
	SourceRevision     int                           `json:"sourceRevision"`
	ContentFingerprint string                        `json:"contentFingerprint"`
	Status             string                        `json:"status"`
	MediaType          string                        `json:"mediaType"` // none/image/voice/attachment
	Analyzer           MessageAnalysisAnalyzerV2     `json:"analyzer"`
	NormalizedText     string                        `json:"normalizedText"`
	Quality            MessageAnalysisQualityV2      `json:"quality"`
	Observations       []ObservationV2Item           `json:"observations"`
	Error              *MessageAnalysisErrorV2       `json:"error"`
	AnalyzedAt         *time.Time                    `json:"analyzedAt"`
}

// MessageAnalysisAnalyzerV2 是分析器身份。
type MessageAnalysisAnalyzerV2 struct {
	Kind     string `json:"kind"` // rule/vision/asr/file_parser/import
	Name     string `json:"name"`
	Version  string `json:"version"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// MessageAnalysisQualityV2 是分析质量元数据。
type MessageAnalysisQualityV2 struct {
	OverallConfidence float64                        `json:"overallConfidence"`
	Completeness      string                         `json:"completeness"` // complete/partial/empty
	FallbackUsed      bool                           `json:"fallbackUsed"`
	Warnings          []string                       `json:"warnings"`
	UncertainRanges   []MessageAnalysisUncertainV2   `json:"uncertainRanges"`
}

// MessageAnalysisUncertainV2 是不确定区间（rune offset，end exclusive）。
type MessageAnalysisUncertainV2 struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Reason string `json:"reason"`
}

// ObservationV2Item 是 V2 观察项（schema 内联 observation.v1 形态；权限由
// 服务端 ObservationPolicyProjector 投影后写入）。
type ObservationV2Item struct {
	Ref             string   `json:"ref"`
	SourceMessageID int64    `json:"sourceMessageId"`
	SourceRevision  int      `json:"sourceRevision"`
	Status          string   `json:"status"`
	SourceType      string   `json:"sourceType"`
	ObservationType string   `json:"observationType"`
	Text            string   `json:"text"`
	Confidence      float64  `json:"confidence"`
	AllowedUses     []string `json:"allowedUses"`
	ForbiddenUses   []string `json:"forbiddenUses"`
}

// MessageAnalysisErrorV2 是终态失败信息。
type MessageAnalysisErrorV2 struct {
	Class     string `json:"class"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}
