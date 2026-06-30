package executor

import (
	"context"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	answerabilityNodeRetrieve = "retrieve_knowledge"
	answerabilityNodeAllow    = "allow_agent"
	answerabilityNodeFallback = "fallback"

	answerabilityStatusSkipped      = "skipped"
	answerabilityStatusNoContext    = "no_context"
	answerabilityStatusHasContext   = "has_context"
	answerabilityStatusUnanswerable = "unanswerable"
)

type knowledgeContextRetriever interface {
	KnowledgeBaseIDs() []int64
	RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error)
}

type answerabilityRetrieverFactory func(aiAgent models.AIAgent) knowledgeContextRetriever

type KnowledgeAnswerabilityGate struct {
	newRetriever answerabilityRetrieverFactory
}

type answerabilityGateInput struct {
	Request   RunInput
	Summary   *RunResult
	Collector *callbacks.RuntimeTraceCollector
	Messages  []*schema.Message
}

type answerabilityGateState struct {
	Input          answerabilityGateInput
	KnowledgeIDs   []int64
	RetrieveResult *retrievers.KnowledgeRetrieveResult
	Decision       knowledgeGuardDecision
	SkipGate       bool
	FallbackReply  string
	ErrorMessage   string
}

func NewKnowledgeAnswerabilityGate() *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(aiAgent models.AIAgent) knowledgeContextRetriever {
			return retrievers.NewKnowledgeRetriever(aiAgent)
		},
	}
}

func (g *KnowledgeAnswerabilityGate) withDefaults() *KnowledgeAnswerabilityGate {
	if g == nil {
		return NewKnowledgeAnswerabilityGate()
	}
	ret := *g
	defaults := NewKnowledgeAnswerabilityGate()
	if ret.newRetriever == nil {
		ret.newRetriever = defaults.newRetriever
	}
	return &ret
}

func (g *KnowledgeAnswerabilityGate) Evaluate(ctx context.Context, input answerabilityGateInput) (*answerabilityGateState, error) {
	gate := g.withDefaults()
	graph := compose.NewGraph[*answerabilityGateState, *answerabilityGateState]()
	if err := graph.AddLambdaNode(answerabilityNodeRetrieve, compose.InvokableLambda(gate.retrieveKnowledge)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(answerabilityNodeAllow, compose.InvokableLambda(allowAnswerabilityPassThrough)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(answerabilityNodeFallback, compose.InvokableLambda(fallbackAnswerabilityPassThrough)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(compose.START, answerabilityNodeRetrieve); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(answerabilityNodeRetrieve, compose.NewGraphBranch(routeAnswerabilityGate, map[string]bool{
		answerabilityNodeAllow:    true,
		answerabilityNodeFallback: true,
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(answerabilityNodeAllow, compose.END); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(answerabilityNodeFallback, compose.END); err != nil {
		return nil, err
	}
	runnable, err := graph.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return runnable.Invoke(ctx, &answerabilityGateState{Input: input})
}

func routeAnswerabilityGate(ctx context.Context, state *answerabilityGateState) (string, error) {
	if state == nil {
		return answerabilityNodeFallback, nil
	}
	if state.SkipGate || strings.TrimSpace(state.FallbackReply) == "" {
		return answerabilityNodeAllow, nil
	}
	return answerabilityNodeFallback, nil
}

func allowAnswerabilityPassThrough(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		return &answerabilityGateState{}, nil
	}
	if len(state.Decision.Instructions) > 0 {
		state.Input.Messages = append(state.Input.Messages, state.Decision.Instructions...)
	}
	if state.RetrieveResult != nil {
		if contextText := strings.TrimSpace(state.RetrieveResult.ContextText); contextText != "" {
			state.Input.Messages = append(state.Input.Messages, schema.SystemMessage(contextText))
		}
	}
	return state, nil
}

func fallbackAnswerabilityPassThrough(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		return &answerabilityGateState{}, nil
	}
	return state, nil
}

func (g *KnowledgeAnswerabilityGate) retrieveKnowledge(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		state = &answerabilityGateState{}
	}
	gate := g.withDefaults()
	req := state.Input.Request
	if isRuntimeActionIntent(req.UserMessage.Content) {
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, "runtime action intent", nil)
		return state, nil
	}
	if skip, reason := shouldSkipKnowledgeGate(req); skip {
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, reason, nil)
		return state, nil
	}
	configuredKnowledgeIDs := utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
	if len(configuredKnowledgeIDs) == 0 {
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, "no knowledge configured", nil)
		return state, nil
	}
	retriever := gate.newRetriever(req.AIAgent)
	state.KnowledgeIDs = append([]int64(nil), configuredKnowledgeIDs...)
	if retriever == nil {
		state.FallbackReply = resolveKnowledgeHumanSupportFallback(req.AIAgent)
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retriever unavailable", nil)
		return state, nil
	}
	knowledgeIDs := retriever.KnowledgeBaseIDs()
	state.KnowledgeIDs = append([]int64(nil), knowledgeIDs...)
	if len(knowledgeIDs) == 0 {
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, "no knowledge configured", nil)
		return state, nil
	}
	query := strings.TrimSpace(req.UserMessage.Content)
	if query == "" {
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.recordAnswerability(answerabilityStatusNoContext, "empty user question", nil)
		return state, nil
	}
	retrieveOptions := retrievers.DefaultKnowledgeRetrieveOptions()
	retrieveOptions.QueryPreview = preview(req.UserMessage.Content, 120)
	result, err := retriever.RetrieveContextByOptions(ctx, retrieveOptions, query)
	if err != nil {
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, knowledgeIDs)
		state.ErrorMessage = err.Error()
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retrieval failed", err)
		return state, nil
	}
	state.RetrieveResult = result
	if state.Input.Summary != nil && result != nil {
		state.Input.Summary.RetrieverCount = len(result.Hits)
	}
	if state.Input.Collector != nil && result != nil {
		state.Input.Collector.SetRetrieverSummary(result.TraceSummary)
		state.Input.Collector.AddRetrieverItems(result.TraceItems)
	}
	if result == nil || len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.recordAnswerability(answerabilityStatusNoContext, "no retrieved context", nil)
		return state, nil
	}
	state.Decision = buildKnowledgeGuardDecision(req.AIAgent, result)
	state.recordAnswerability(answerabilityStatusHasContext, "retrieved context injected", nil)
	return state, nil
}

func shouldSkipKnowledgeGate(req RunInput) (bool, string) {
	content := strings.TrimSpace(req.UserMessage.Content)
	messageType := req.UserMessage.MessageType
	if isMediaOnlyMessage(messageType, content) {
		return true, "media-only message"
	}
	text := normalizeIntentText(content)
	if text == "" {
		return false, ""
	}
	if isConversationalIntent(text) {
		return true, "conversational intent"
	}
	if isOperationalResourceIntent(text) {
		return true, "operational resource intent"
	}
	if isServiceActionIntent(text) {
		return true, "service action intent"
	}
	if isMediaFollowUpIntent(text) && !isExplicitHotelKnowledgeQuestion(text) {
		return true, "media follow-up intent"
	}
	return false, ""
}

func normalizeIntentText(content string) string {
	text := strings.ToLower(strings.TrimSpace(content))
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "", "～", "", "~", "").Replace(text)
}

func isMediaOnlyMessage(messageType enums.IMMessageType, content string) bool {
	if strings.TrimSpace(content) != "" {
		return false
	}
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF, enums.IMMessageTypeLocation, enums.IMMessageTypeMiniProgram:
		return true
	default:
		return false
	}
}

func isConversationalIntent(text string) bool {
	if text == "" {
		return false
	}
	shortExact := []string{
		"你好", "您好", "在吗", "在不在", "有人吗", "哈喽", "hello", "hi", "谢谢", "多谢", "感谢", "不客气", "好的", "好", "嗯", "嗯嗯", "可以", "行", "收到", "明白", "知道了", "确认", "确认确认", "对", "是的", "不是", "不用了", "没事了", "算了", "拜拜", "再见",
	}
	for _, value := range shortExact {
		if text == value {
			return true
		}
	}
	if len([]rune(text)) <= 8 && containsAny(text, []string{"谢谢", "感谢", "好的", "收到", "确认", "可以", "行", "嗯"}) {
		return true
	}
	return false
}

func isOperationalResourceIntent(text string) bool {
	if containsAny(text, []string{"小程序", "安心宿", "自助入住", "自助办理", "办理入住", "办入住", "入住办理", "入住小程序", "扫码入住"}) {
		return true
	}
	if containsAny(text, []string{"发定位", "发个定位", "定位发", "酒店定位", "门店定位", "导航", "怎么去", "怎么过去", "到酒店", "去酒店", "酒店地址", "地址发", "位置发", "在哪里", "在哪儿"}) &&
		containsAny(text, []string{"酒店", "门店", "你们", "这家", "位置", "地址", "定位", "导航", "过去", "到"}) {
		return true
	}
	return false
}

func isServiceActionIntent(text string) bool {
	servicePhrases := []string{
		"送水", "拿水", "矿泉水", "送拖鞋", "拖鞋", "牙刷", "牙膏", "纸巾", "浴巾", "毛巾", "被子", "枕头", "充电器", "打扫", "保洁", "清理房间", "维修", "修一下", "漏水", "堵了", "马桶", "空调不冷", "空调坏", "电视坏", "门锁", "房卡", "叫醒", "退房", "续住", "换房", "投诉", "赔偿", "发票", "开发票",
	}
	if containsAny(text, servicePhrases) && containsAny(text, []string{"帮", "要", "送", "拿", "来", "处理", "修", "开", "换", "安排", "麻烦", "需要", "可以"}) {
		return true
	}
	return false
}

func isMediaFollowUpIntent(text string) bool {
	return containsAny(text, []string{"图片", "照片", "图里", "图上", "这个", "这是啥", "这是什么", "看下", "帮我看", "识别", "语音", "听下", "文件", "附件", "截图", "表情"})
}

func isExplicitHotelKnowledgeQuestion(text string) bool {
	if isOperationalResourceIntent(text) || isServiceActionIntent(text) {
		return false
	}
	knowledgeWords := []string{"早餐", "停车", "发票", "押金", "退房", "入住时间", "价格", "多少钱", "费用", "收费", "规则", "政策", "会员", "权益", "取消", "退款", "报销", "洗衣", "健身房", "餐厅", "wifi", "无线网", "宠物", "加床", "延迟退房"}
	questionWords := []string{"几点", "多久", "多少", "怎么", "如何", "能不能", "可不可以", "可以吗", "有没有", "是否", "什么", "哪", "哪里", "收费吗"}
	return containsAny(text, knowledgeWords) && (containsAny(text, questionWords) || len([]rune(text)) <= 18)
}

func isRuntimeActionIntent(content string) bool {
	text := strings.ToLower(strings.TrimSpace(content))
	if text == "" {
		return false
	}
	compact := normalizeIntentText(text)
	handoffPhrases := []string{
		"我要转人工",
		"帮我转人工",
		"转人工",
		"接人工",
		"找人工",
		"真人客服",
		"humanagent",
		"liveagent",
	}
	for _, phrase := range handoffPhrases {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	if containsAny(compact, []string{"人工客服", "人工服务", "人工处理"}) &&
		!containsAny(compact, []string{"是什么", "怎么", "如何", "多少", "几", "吗", "?"}) &&
		(isShortActionPhrase(compact) || containsAny(compact, []string{"我要", "帮我", "请", "联系", "需要"})) {
		return true
	}
	ticketPhrases := []string{
		"创建工单",
		"新建工单",
		"提交工单",
		"发起工单",
		"建工单",
		"开工单",
		"我要建单",
		"帮我建单",
		"创建ticket",
		"createticket",
	}
	for _, phrase := range ticketPhrases {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	if strings.Contains(compact, "工单") {
		for _, action := range []string{"创建", "新建", "提交", "发起", "建", "开", "帮我", "我要", "请"} {
			if strings.Contains(compact, action) {
				return true
			}
		}
	}
	return false
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func isShortActionPhrase(text string) bool {
	return len([]rune(text)) <= 8
}

func (s *answerabilityGateState) recordAnswerability(status string, reason string, err error) {
	s.recordAnswerabilityWithLatency(status, reason, err, time.Time{})
}

func (s *answerabilityGateState) recordAnswerabilityWithLatency(status string, reason string, err error, started time.Time) {
	if s == nil || s.Input.Collector == nil {
		return
	}
	errorMessage := strings.TrimSpace(s.ErrorMessage)
	if err != nil {
		errorMessage = err.Error()
	}
	data := callbacks.AnswerabilityTraceData{
		Status:       status,
		Reason:       strings.TrimSpace(reason),
		ErrorMessage: errorMessage,
	}
	if !started.IsZero() {
		data.LatencyMs = time.Since(started).Milliseconds()
	}
	s.Input.Collector.SetAnswerability(data)
}
