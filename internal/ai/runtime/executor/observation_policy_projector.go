package executor

// ObservationPolicyProjector 按「多模态契约 7.2 最低权限表」把 Provider 候选投影为
// 最终 Observation。Provider（Vision/ASR/文件解析）只输出候选分类，不携带使用权限；
// 权限由本纯函数按 mediaType/observationType/contentRole 决定。模型即使把截图里的
// 历史对话识别为“升房请求”，也没有资格把它升级为门店事实、资源动作或人工动作。

// MediaObservationCandidate 是 Provider 输出的单条候选（不含权限）。
type MediaObservationCandidate struct {
	ObservationType string // scene_description/ocr_text/document_text/error_ui/transcript/audio_quality/uncertain_span/entity/historical_statement
	ContentRole     string // visible_scene/embedded_document/embedded_historical_conversation/embedded_third_party_claim/system_error/customer_spoken_input/unknown
	Text            string
	Confidence      float64
}

// MediaSource 描述观察的媒体来源。
type MediaSource struct {
	MessageID      int64
	SourceRevision int
	SourceType     string // image/voice/video/gif/attachment/text/history
}

// ObservationUse 授权的使用用途（封闭枚举）。
const (
	obsUseDescribeMedia    = "describe_media"
	obsUseResolveReference = "resolve_reference"
	obsUseBuildQuery       = "build_knowledge_query"
	obsUseQuoteDocument    = "quote_document"
)

// forbiddenAll 是默认禁止用途全集（门店身份/地址/电话/政策/资源资格/人工决定）。
func forbiddenAll() []string {
	return []string{"store_identity", "store_address", "store_phone", "store_policy", "resource_eligibility", "handoff_decision"}
}

// ProjectObservation 按最低权限表投影：contentRole 决定 allowedUses，
// forbiddenUses 永远包含全部门店事实与动作决定。
func ProjectObservation(source MediaSource, candidate MediaObservationCandidate) (allowed []string, forbidden []string) {
	forbidden = forbiddenAll()
	switch candidate.ContentRole {
	case "visible_scene":
		allowed = []string{obsUseDescribeMedia, obsUseResolveReference}
	case "embedded_document":
		allowed = []string{obsUseDescribeMedia, obsUseQuoteDocument, obsUseResolveReference}
	case "embedded_historical_conversation", "embedded_third_party_claim":
		// 截图里的历史对话/第三方声明：只可描述与引用原文，禁止作为知识查询对象，
		// 防止 OCR 里的旧业务文字污染当前检索。
		allowed = []string{obsUseDescribeMedia, obsUseQuoteDocument}
	case "system_error":
		allowed = []string{obsUseDescribeMedia, obsUseResolveReference}
	case "customer_spoken_input":
		// 当前语音正文进入 L6 当前输入；L5 观察层只保留质量信息，不重复注入 transcript。
		allowed = []string{}
	default: // unknown
		allowed = []string{obsUseDescribeMedia}
	}
	return allowed, forbidden
}

// ObservationSupportsUse 判定一条 Observation 是否被授权用于某用途。
func ObservationSupportsUse(allowedUses []string, use string) bool {
	for _, item := range allowedUses {
		if item == use {
			return true
		}
	}
	return false
}
