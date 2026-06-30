package enums

type VectorDBType string

const (
	VectorDBTypeQdrant VectorDBType = "qdrant"
)

var vectorDBTypeLabelMap = map[VectorDBType]string{
	VectorDBTypeQdrant: "Qdrant",
}

func GetVectorDBTypeLabel(dbType VectorDBType) string {
	return vectorDBTypeLabelMap[dbType]
}

type KnowledgeDocumentContentType string

const (
	KnowledgeDocumentContentTypeHTML     KnowledgeDocumentContentType = "html"
	KnowledgeDocumentContentTypeMarkdown KnowledgeDocumentContentType = "markdown"
)

type KnowledgeBaseType string

const (
	KnowledgeBaseTypeDocument     KnowledgeBaseType = "document"
	KnowledgeBaseTypeFAQ          KnowledgeBaseType = "faq"
	KnowledgeBaseTypeFastGPTCloud KnowledgeBaseType = "fastgpt_cloud"
)

var knowledgeBaseTypeLabelMap = map[KnowledgeBaseType]string{
	KnowledgeBaseTypeDocument:     "文档知识库",
	KnowledgeBaseTypeFAQ:          "FAQ知识库",
	KnowledgeBaseTypeFastGPTCloud: "FastGPT云端知识库",
}

func GetKnowledgeBaseTypeLabel(knowledgeType KnowledgeBaseType) string {
	return knowledgeBaseTypeLabelMap[knowledgeType]
}

var knowledgeDocumentContentTypeLabelMap = map[KnowledgeDocumentContentType]string{
	KnowledgeDocumentContentTypeHTML:     "HTML",
	KnowledgeDocumentContentTypeMarkdown: "Markdown",
}

func GetKnowledgeDocumentContentTypeLabel(contentType KnowledgeDocumentContentType) string {
	return knowledgeDocumentContentTypeLabelMap[contentType]
}

type KnowledgeDocumentIndexStatus string

const (
	KnowledgeDocumentIndexStatusPending KnowledgeDocumentIndexStatus = "pending"
	KnowledgeDocumentIndexStatusIndexed KnowledgeDocumentIndexStatus = "indexed"
	KnowledgeDocumentIndexStatusFailed  KnowledgeDocumentIndexStatus = "failed"
)

var KnowledgeDocumentIndexStatusValues = []KnowledgeDocumentIndexStatus{
	KnowledgeDocumentIndexStatusPending,
	KnowledgeDocumentIndexStatusIndexed,
	KnowledgeDocumentIndexStatusFailed,
}

var knowledgeDocumentIndexStatusLabelMap = map[KnowledgeDocumentIndexStatus]string{
	KnowledgeDocumentIndexStatusPending: "待索引",
	KnowledgeDocumentIndexStatusIndexed: "已索引",
	KnowledgeDocumentIndexStatusFailed:  "索引失败",
}

func GetKnowledgeDocumentIndexStatusLabel(status KnowledgeDocumentIndexStatus) string {
	return knowledgeDocumentIndexStatusLabelMap[status]
}

func IsValidKnowledgeDocumentIndexStatus(status string) bool {
	for _, item := range KnowledgeDocumentIndexStatusValues {
		if string(item) == status {
			return true
		}
	}
	return false
}

type KnowledgeChunkType string

const (
	KnowledgeChunkTypeText  KnowledgeChunkType = "text"
	KnowledgeChunkTypeFAQ   KnowledgeChunkType = "faq"
	KnowledgeChunkTypeTable KnowledgeChunkType = "table"
	KnowledgeChunkTypeCode  KnowledgeChunkType = "code"
)

var knowledgeChunkTypeLabelMap = map[KnowledgeChunkType]string{
	KnowledgeChunkTypeText:  "文本",
	KnowledgeChunkTypeFAQ:   "问答",
	KnowledgeChunkTypeTable: "表格",
	KnowledgeChunkTypeCode:  "代码",
}

func GetKnowledgeChunkTypeLabel(chunkType KnowledgeChunkType) string {
	return knowledgeChunkTypeLabelMap[chunkType]
}

type KnowledgeChunkProvider string

const (
	KnowledgeChunkProviderFixed      KnowledgeChunkProvider = "fixed"
	KnowledgeChunkProviderStructured KnowledgeChunkProvider = "structured"
	KnowledgeChunkProviderFAQ        KnowledgeChunkProvider = "faq"
	KnowledgeChunkProviderSemantic   KnowledgeChunkProvider = "semantic"
	KnowledgeChunkProviderFastGPT    KnowledgeChunkProvider = "fastgpt_cloud"
)

var knowledgeChunkProviderLabelMap = map[KnowledgeChunkProvider]string{
	KnowledgeChunkProviderFixed:      "固定长度",
	KnowledgeChunkProviderStructured: "结构化分块",
	KnowledgeChunkProviderFAQ:        "问答式分块",
	KnowledgeChunkProviderSemantic:   "语义分块",
	KnowledgeChunkProviderFastGPT:    "FastGPT云端",
}

func GetKnowledgeChunkProviderLabel(provider KnowledgeChunkProvider) string {
	return knowledgeChunkProviderLabelMap[provider]
}

type KnowledgeRetrieveChannel string

const (
	KnowledgeRetrieveChannelIM          KnowledgeRetrieveChannel = "im"
	KnowledgeRetrieveChannelAgentAssist KnowledgeRetrieveChannel = "agent_assist"
	KnowledgeRetrieveChannelAPI         KnowledgeRetrieveChannel = "api"
	KnowledgeRetrieveChannelDebug       KnowledgeRetrieveChannel = "debug"
)

var knowledgeRetrieveChannelLabelMap = map[KnowledgeRetrieveChannel]string{
	KnowledgeRetrieveChannelIM:          "客服会话",
	KnowledgeRetrieveChannelAgentAssist: "客服助手",
	KnowledgeRetrieveChannelAPI:         "API接口",
	KnowledgeRetrieveChannelDebug:       "调试",
}

func GetKnowledgeRetrieveChannelLabel(channel KnowledgeRetrieveChannel) string {
	return knowledgeRetrieveChannelLabelMap[channel]
}

type KnowledgeRetrieveScene string

const (
	KnowledgeRetrieveSceneFirstResponse KnowledgeRetrieveScene = "first_response"
	KnowledgeRetrieveSceneAssist        KnowledgeRetrieveScene = "assist"
	KnowledgeRetrieveSceneQA            KnowledgeRetrieveScene = "qa"
)

var knowledgeRetrieveSceneLabelMap = map[KnowledgeRetrieveScene]string{
	KnowledgeRetrieveSceneFirstResponse: "首次回复",
	KnowledgeRetrieveSceneAssist:        "辅助回复",
	KnowledgeRetrieveSceneQA:            "问答",
}

func GetKnowledgeRetrieveSceneLabel(scene KnowledgeRetrieveScene) string {
	return knowledgeRetrieveSceneLabelMap[scene]
}

type KnowledgeAnswerStatus int

const (
	KnowledgeAnswerStatusNormal   KnowledgeAnswerStatus = 1
	KnowledgeAnswerStatusNoAnswer KnowledgeAnswerStatus = 2
	KnowledgeAnswerStatusFallback KnowledgeAnswerStatus = 3
	KnowledgeAnswerStatusBlocked  KnowledgeAnswerStatus = 4
)

var knowledgeAnswerStatusLabelMap = map[KnowledgeAnswerStatus]string{
	KnowledgeAnswerStatusNormal:   "正常",
	KnowledgeAnswerStatusNoAnswer: "无答案",
	KnowledgeAnswerStatusFallback: "兜底回复",
	KnowledgeAnswerStatusBlocked:  "已屏蔽",
}

func GetKnowledgeAnswerStatusLabel(status KnowledgeAnswerStatus) string {
	return knowledgeAnswerStatusLabelMap[status]
}

type KnowledgeFeedbackType int

const (
	KnowledgeFeedbackTypeLike          KnowledgeFeedbackType = 1
	KnowledgeFeedbackTypeDislike       KnowledgeFeedbackType = 2
	KnowledgeFeedbackTypeNotHelpful    KnowledgeFeedbackType = 3
	KnowledgeFeedbackTypeWrongCitation KnowledgeFeedbackType = 4
	KnowledgeFeedbackTypeOther         KnowledgeFeedbackType = 5
)

var knowledgeFeedbackTypeLabelMap = map[KnowledgeFeedbackType]string{
	KnowledgeFeedbackTypeLike:          "点赞",
	KnowledgeFeedbackTypeDislike:       "点踩",
	KnowledgeFeedbackTypeNotHelpful:    "无帮助",
	KnowledgeFeedbackTypeWrongCitation: "引用错误",
	KnowledgeFeedbackTypeOther:         "其他",
}

func GetKnowledgeFeedbackTypeLabel(feedbackType KnowledgeFeedbackType) string {
	return knowledgeFeedbackTypeLabelMap[feedbackType]
}

type KnowledgeAnswerMode int

const (
	KnowledgeAnswerModeStrict KnowledgeAnswerMode = 1
	KnowledgeAnswerModeAssist KnowledgeAnswerMode = 2
)

var knowledgeAnswerModeLabelMap = map[KnowledgeAnswerMode]string{
	KnowledgeAnswerModeStrict: "严格模式",
	KnowledgeAnswerModeAssist: "辅助模式",
}

func GetKnowledgeAnswerModeLabel(mode KnowledgeAnswerMode) string {
	return knowledgeAnswerModeLabelMap[mode]
}

type KnowledgeCandidateSource string

const (
	KnowledgeCandidateSourceQiyuHQ      KnowledgeCandidateSource = "qiyu_hq"
	KnowledgeCandidateSourceAgentDeskHQ KnowledgeCandidateSource = "agentdesk_hq"
	KnowledgeCandidateSourceStoreWecom  KnowledgeCandidateSource = "store_wecom"
	KnowledgeCandidateSourceAINoAnswer  KnowledgeCandidateSource = "ai_no_answer"
)

var knowledgeCandidateSourceLabelMap = map[KnowledgeCandidateSource]string{
	KnowledgeCandidateSourceQiyuHQ:      "总部网页人工",
	KnowledgeCandidateSourceAgentDeskHQ: "总部网页端人工",
	KnowledgeCandidateSourceStoreWecom:  "门店企微人工",
	KnowledgeCandidateSourceAINoAnswer:  "AI未解答",
}

func GetKnowledgeCandidateSourceLabel(source KnowledgeCandidateSource) string {
	return knowledgeCandidateSourceLabelMap[source]
}

type KnowledgeCandidateStatus string

const (
	KnowledgeCandidateStatusPending  KnowledgeCandidateStatus = "pending"
	KnowledgeCandidateStatusApproved KnowledgeCandidateStatus = "approved"
	KnowledgeCandidateStatusRejected KnowledgeCandidateStatus = "rejected"
	KnowledgeCandidateStatusExported KnowledgeCandidateStatus = "exported"
	KnowledgeCandidateStatusImported KnowledgeCandidateStatus = "imported"
)

var knowledgeCandidateStatusLabelMap = map[KnowledgeCandidateStatus]string{
	KnowledgeCandidateStatusPending:  "待审核",
	KnowledgeCandidateStatusApproved: "已通过",
	KnowledgeCandidateStatusRejected: "已驳回",
	KnowledgeCandidateStatusExported: "已导出",
	KnowledgeCandidateStatusImported: "已导入知识库",
}

func GetKnowledgeCandidateStatusLabel(status KnowledgeCandidateStatus) string {
	return knowledgeCandidateStatusLabelMap[status]
}
