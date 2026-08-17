package observationpolicy

import "agent-desk/internal/ai/runtime/contracts"

// Source 描述媒体观察的持久来源。
type Source struct {
	MessageID      int64
	SourceRevision int
	SourceType     string
}

const (
	UseDescribeMedia    = "describe_media"
	UseResolveReference = "resolve_reference"
	UseBuildQuery       = "build_knowledge_query"
	UseQuoteDocument    = "quote_document"
)

// ForbiddenFactUses 是媒体观察永远不能直接取得的事实与动作权限。
func ForbiddenFactUses() []string {
	return []string{"store_identity", "store_address", "store_phone", "store_policy", "resource_eligibility", "handoff_decision"}
}

// Project 按内容角色投影最低权限。Provider 输出不能携带或提升权限。
func Project(_ Source, candidate contracts.MediaAnalysisCandidateItemV1) (allowed []string, forbidden []string) {
	forbidden = ForbiddenFactUses()
	switch candidate.ContentRole {
	case "visible_scene":
		allowed = []string{UseDescribeMedia, UseResolveReference}
	case "embedded_document":
		allowed = []string{UseDescribeMedia, UseQuoteDocument, UseResolveReference}
	case "embedded_historical_conversation", "embedded_third_party_claim":
		allowed = []string{UseDescribeMedia, UseQuoteDocument}
	case "system_error":
		allowed = []string{UseDescribeMedia, UseResolveReference}
	case "customer_spoken_input":
		// Transcript 会作为当前客户 Utterance 进入 L6；L5 不重复授权正文。
		allowed = []string{}
	default:
		allowed = []string{UseDescribeMedia}
	}
	return allowed, forbidden
}

func SupportsUse(uses []string, target string) bool {
	for _, item := range uses {
		if item == target {
			return true
		}
	}
	return false
}
