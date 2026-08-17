package contracts

// MediaAnalysisCandidateV1 是媒体 Provider 的非权威候选输出。Provider 只能
// 描述看见/听见的内容；AllowedUses/ForbiddenUses 必须由服务端策略投影。
type MediaAnalysisCandidateV1 struct {
	SchemaVersion       string                          `json:"schemaVersion"`
	NormalizedText      string                          `json:"normalizedText"`
	Quality             MediaAnalysisCandidateQualityV1 `json:"quality"`
	Items               []MediaAnalysisCandidateItemV1  `json:"items"`
	ResponseExpectation *MediaResponseExpectationV1     `json:"responseExpectation,omitempty"`
}

// MediaResponseExpectationV1 是媒体分析器对“当前媒体是否值得进入 Intent”的
// 非权威建议。它只能控制是否继续理解，不能授予门店事实、资源动作或转人工权限。
type MediaResponseExpectationV1 struct {
	Mode       string  `json:"mode"`  // none/reply/uncertain
	Basis      string  `json:"basis"` // explicit_question/visible_error/customer_spoken_input/ordinary_media/unknown
	Confidence float64 `json:"confidence"`
}

type MediaAnalysisCandidateQualityV1 struct {
	OverallConfidence float64                      `json:"overallConfidence"`
	Completeness      string                       `json:"completeness"`
	Warnings          []string                     `json:"warnings"`
	UncertainRanges   []MessageAnalysisUncertainV2 `json:"uncertainRanges"`
}

type MediaAnalysisCandidateItemV1 struct {
	ObservationType string  `json:"observationType"`
	ContentRole     string  `json:"contentRole"`
	Text            string  `json:"text"`
	Confidence      float64 `json:"confidence"`
}
