package executor

import (
	"strings"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type runtimeKnowledgeEvidenceFilterStats struct {
	droppedMeta     int
	droppedMismatch int
}

// judgeRuntimeTaskKnowledgeEvidence 在知识结果进入 EvidenceBundle、资源绑定和
// AnswerGroup 之前完成一次统一裁决。这样被判定为错题的正文、图片和分组键都会
// 一起退出链路，不会出现“文字过滤了但洗衣房图片仍跟着地址任务发出”的半过滤。
func judgeRuntimeTaskKnowledgeEvidence(req RunInput, item *runtimeTaskKnowledgeItem) {
	if item == nil || item.Result == nil || item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed || !gateEnabled(gateEvidenceQuality, req) {
		return
	}
	combined := append([]rag.RetrieveResult(nil), item.Result.ContextResults...)
	combined = appendUniqueRuntimeRetrieveResults(combined, item.Result.Hits, map[string]bool{})
	if len(combined) == 0 {
		return
	}
	kept, stats := filterKnowledgeEvidenceForTask(req, *item, combined)
	if len(kept) == len(combined) {
		return
	}
	allowed := make(map[string]struct{}, len(kept))
	for _, result := range kept {
		allowed[runtimeEvidenceResultKey(result)] = struct{}{}
	}
	item.Result.Hits = retainJudgedKnowledgeResults(item.Result.Hits, allowed)
	item.Result.ContextResults = retainJudgedKnowledgeResults(item.Result.ContextResults, allowed)
	if len(item.Result.ContextResults) == 0 && len(item.Result.Hits) > 0 {
		limit := item.Result.Options.MaxContextItems
		if limit <= 0 || limit > len(item.Result.Hits) {
			limit = len(item.Result.Hits)
		}
		item.Result.ContextResults = append([]rag.RetrieveResult(nil), item.Result.Hits[:limit]...)
	}
	item.Result.ContextText = judgedKnowledgeContextText(item.Result.ContextResults)
	item.Result.TopScore = 0
	if len(item.Result.Hits) > 0 {
		item.Result.TopScore = float64(item.Result.Hits[0].Score)
	}
	item.Result.TraceSummary.HitCount = len(item.Result.Hits)
	item.Result.TraceSummary.ContextCount = len(item.Result.ContextResults)
	if len(item.Result.Hits) == 0 && len(item.Result.ContextResults) == 0 {
		item.Status = enums.AIReplyTurnTaskKnowledgeStatusNoHit
		if stats.droppedMismatch > 0 {
			item.FilterReason = "knowledge_context_not_relevant"
		} else if stats.droppedMeta > 0 {
			item.FilterReason = "knowledge_meta_content"
		}
	}
}

func filterKnowledgeEvidenceForTask(req RunInput, item runtimeTaskKnowledgeItem, results []rag.RetrieveResult) ([]rag.RetrieveResult, runtimeKnowledgeEvidenceFilterStats) {
	stats := runtimeKnowledgeEvidenceFilterStats{}
	kept, droppedMeta := filterKnowledgeMetaEvidence(req, results)
	stats.droppedMeta = droppedMeta
	filtered := make([]rag.RetrieveResult, 0, len(kept))
	for _, result := range kept {
		if knowledgeEvidenceMismatchesTask(item, result) {
			stats.droppedMismatch++
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered, stats
}

// knowledgeEvidenceMismatchesTask 只拒绝可确定的错配：正常问题命中异常处理 FAQ，
// 或任务与候选分别落入两个明确且不相交的酒店业务主题。无法确定时保留候选，
// 避免用模糊语义去重吞掉合法知识。
func knowledgeEvidenceMismatchesTask(item runtimeTaskKnowledgeItem, result rag.RetrieveResult) bool {
	query := strings.TrimSpace(item.Query)
	title := strings.TrimSpace(strings.Join([]string{result.Title, result.DocumentTitle, result.SectionPath}, "\n"))
	candidate := strings.TrimSpace(strings.Join([]string{title, result.Content}, "\n"))
	if candidate == "" {
		return true
	}
	if knowledgeEvidenceIsExceptionSpecific(result) && !knowledgeTextHasExceptionContext(query) {
		return true
	}
	// 正常入住只保留登记和开门步骤。入口/电梯属于独立路线问题；客户
	// 没有询问路线时不能因为知识库相似度较高而混进入住流程。
	if isNormalCheckinKnowledgeItem(item) && knowledgeEvidenceIsEntranceOnly(candidate) {
		return true
	}
	if isNormalCheckinKnowledgeItem(item) && knowledgeEvidenceSupportsNormalCheckinStep(candidate) {
		return false
	}
	taskTopics := detectKnowledgeTopicClasses(query + " " + item.SubIntent)
	strongTopics := detectKnowledgeTopicClasses(title)
	if len(taskTopics) > 0 && len(strongTopics) > 0 {
		if !knowledgeTopicSetsIntersect(taskTopics, strongTopics) {
			return true
		}
		// 地址是通用属性，不能因为候选标题同时出现“地址”就把洗衣房、餐厅等
		// 其他对象误当成门店地址。反方向不成立：问洗衣房时，标题写“洗衣房地址”
		// 仍然是合法知识。
		if _, addressTask := taskTopics["address"]; addressTask && knowledgeHasForeignSpecificTopic(taskTopics, strongTopics) {
			return true
		}
		return false
	}
	candidateTopics := detectKnowledgeTopicClasses(result.Content)
	return len(taskTopics) > 0 && len(candidateTopics) > 0 && !knowledgeTopicSetsIntersect(taskTopics, candidateTopics)
}

func isNormalCheckinKnowledgeItem(item runtimeTaskKnowledgeItem) bool {
	return isCheckinProcessSubIntent(item.SubIntent) &&
		!knowledgeTextHasExceptionContext(item.Query) &&
		runtimeTextHasCheckinContext(item.Query)
}

func knowledgeEvidenceSupportsNormalCheckinStep(text string) bool {
	compact := compactRuntimeProtocolText(text)
	doorStep := containsAny(compact, []string{"入住登记", "登记入住", "完成登记", "办理入住"}) &&
		strings.Contains(compact, "刷脸") && strings.Contains(compact, "开门")
	registrationStep := strings.Contains(compact, "小程序") &&
		containsAny(compact, []string{"登记", "实名", "住客信息", "订单信息", "办理入住"})
	return doorStep || registrationStep
}

func knowledgeEvidenceIsEntranceOnly(text string) bool {
	compact := compactRuntimeProtocolText(text)
	entry := containsAny(compact, []string{"酒店入口", "门店入口", "大楼入口", "入口位置"}) &&
		containsAny(compact, []string{"电梯", "大楼", "大厅", "停车场"})
	return entry && !knowledgeEvidenceSupportsNormalCheckinStep(text)
}

func knowledgeEvidenceIsExceptionSpecific(result rag.RetrieveResult) bool {
	title := strings.TrimSpace(strings.Join([]string{result.Title, result.DocumentTitle, result.SectionPath}, "\n"))
	if knowledgeTextHasExceptionContext(title) {
		return true
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return false
	}
	first := content
	if index := strings.IndexAny(first, "。！？!?\n"); index >= 0 {
		first = first[:index+1]
	}
	// 正常流程正文偶尔会在末尾附带异常兜底，不能因此整篇删除。只有标题、
	// 首句或整段短 FAQ 明确以异常为主题时，才视为异常知识。
	return knowledgeTextHasExceptionContext(first) && (len([]rune(content)) <= 240 || startsWithExceptionCondition(first))
}

func startsWithExceptionCondition(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return strings.HasPrefix(compact, "如果") || strings.HasPrefix(compact, "若") ||
		strings.HasPrefix(compact, "如遇") || strings.HasPrefix(compact, "出现") ||
		strings.HasPrefix(compact, "遇到")
}

func knowledgeHasForeignSpecificTopic(taskTopics, candidateTopics map[string]struct{}) bool {
	generic := map[string]struct{}{"address": {}, "takeaway": {}, "store_name": {}}
	for topic := range candidateTopics {
		if _, isGeneric := generic[topic]; isGeneric {
			continue
		}
		if _, belongsToTask := taskTopics[topic]; !belongsToTask {
			return true
		}
	}
	return false
}

func retainJudgedKnowledgeResults(results []rag.RetrieveResult, allowed map[string]struct{}) []rag.RetrieveResult {
	ret := make([]rag.RetrieveResult, 0, len(results))
	for _, result := range results {
		if _, ok := allowed[runtimeEvidenceResultKey(result)]; ok {
			ret = append(ret, result)
		}
	}
	return ret
}

func judgedKnowledgeContextText(results []rag.RetrieveResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if content := strings.TrimSpace(result.Content); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func runtimeTaskHasAuthoritativeStoreFact(req RunInput, plan callbacks.ReplyTaskPlanTraceData) bool {
	if isStoreIdentitySubIntent(plan.SubIntent) {
		return authoritativeStoreIdentity(req) != ""
	}
	if !runtimeTaskUsesOnlyAuthoritativeStoreAddress(plan.SubIntent) {
		return false
	}
	return authoritativeStoreAddress(req) != ""
}

func runtimeTaskUsesOnlyAuthoritativeStoreAddress(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return true
	default:
		// 外卖/收货复合问题仍需检索门店规则，只把最终地址事实绑定到 Store。
		return false
	}
}

func taskRequestsStoreAddress(subIntent, query string) bool {
	switch strings.TrimSpace(subIntent) {
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return true
	case "order_food_delivery", "takeaway", "food_delivery":
		compact := compactRuntimeProtocolText(query)
		return containsAny(compact, []string{"地址", "填哪里", "写哪里", "送到哪", "收货", "门牌", "定位", "位置"})
	default:
		return false
	}
}

func isStoreIdentitySubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "store_identity", "hotel_identity", "store_name", "hotel_name":
		return true
	default:
		return false
	}
}

func authoritativeStoreIdentity(req RunInput) string {
	instance := findRuntimeWxWorkInstance(req)
	if instance == nil {
		return ""
	}
	if name := strings.TrimSpace(instance.StoreNavigationName); name != "" {
		return name
	}
	if instance.StoreID <= 0 || sqls.DB() == nil {
		return ""
	}
	store := repositories.StoreRepository.Get(sqls.DB(), instance.StoreID)
	if store == nil || store.TenantID != req.Conversation.TenantID {
		return ""
	}
	return firstNonEmpty(store.NavigationName, store.Name, store.BrandName)
}

func runtimeKnowledgeTopicLabel(subIntent string) string {
	switch strings.TrimSpace(subIntent) {
	case "checkin_process", "check_in", "checkin", "check_in_process", "checkin_steps", "check_in_steps", "checkin_guide":
		return "入住流程"
	case "checkout_process", "check_out", "checkout":
		return "退房流程"
	case "parking":
		return "停车"
	case "breakfast":
		return "早餐"
	case "invoice":
		return "发票"
	case "network_wifi", "wifi", "network":
		return "WiFi"
	case "coffee":
		return "咖啡"
	case "laundry":
		return "洗衣"
	case "luggage", "luggage_storage":
		return "行李寄存"
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return "门店地址"
	case "order_food_delivery", "food_delivery", "takeaway":
		return "外卖"
	case "discount", "promotion":
		return "优惠"
	case "tv_cast", "tvcast", "tv_screen_mirror":
		return "电视投屏"
	case "air_conditioner":
		return "空调"
	case "supplies_self_help", "supplies":
		return "客用品"
	case "store_identity", "hotel_identity", "store_name", "hotel_name":
		return "门店名称"
	default:
		return ""
	}
}

func knowledgeTextHasExceptionContext(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{
		"不能", "无法", "失败", "故障", "异常", "坏了", "用不了", "进不去", "打不开",
		"删掉", "删除", "取消", "退订", "退款", "换房", "另一间", "两间房", "改错", "不对",
		"投诉", "差评", "提前退房", "延迟退房", "没有收到", "没收到",
	})
}

func detectKnowledgeTopicClasses(text string) map[string]struct{} {
	compact := strings.ToLower(compactRuntimeProtocolText(text))
	ret := make(map[string]struct{})
	topics := map[string][]string{
		"checkin":      {"入住", "checkin", "check_in"},
		"checkout":     {"退房", "离店", "checkout", "check_out"},
		"address":      {"地址", "定位", "门牌", "收货"},
		"parking":      {"停车", "车位", "停车场"},
		"breakfast":    {"早餐", "早饭"},
		"coffee":       {"咖啡", "拿铁", "美式"},
		"invoice":      {"发票", "开票"},
		"wifi":         {"wifi", "无线网", "网络", "密码"},
		"laundry":      {"洗衣", "烘干", "洗衣房"},
		"luggage":      {"行李", "寄存"},
		"housekeeping": {"保洁", "打扫", "阿姨", "清洁"},
		"room_change":  {"换房", "升级房", "升级房间", "另一间房"},
		"door_access":  {"开门", "门锁", "刷脸", "房门"},
		"tv":           {"投屏", "电视"},
		"aircon":       {"空调", "制冷", "制热"},
		"supplies":     {"牙刷", "拖鞋", "剃须刀", "客用品", "洗漱用品", "草稿纸", "纸张", "纸笔"},
		"discount":     {"优惠", "折扣", "会员价", "便宜"},
		"nearby_food":  {"附近吃", "吃的", "饿了", "推荐吃", "美食", "餐厅", "饭店", "小吃"},
		"nearby_fun":   {"附近玩", "好玩", "景点", "游玩"},
		"takeaway":     {"外卖", "送餐"},
		"store_name":   {"酒店名", "门店名", "公寓", "宾馆", "民宿", "hotel_identity", "store_identity"},
	}
	for topic, markers := range topics {
		if containsAny(compact, markers) {
			ret[topic] = struct{}{}
		}
	}
	return ret
}

func knowledgeTopicSetsIntersect(left, right map[string]struct{}) bool {
	for topic := range left {
		if _, ok := right[topic]; ok {
			return true
		}
	}
	return false
}
