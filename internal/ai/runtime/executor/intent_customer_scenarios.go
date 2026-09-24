package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func applyRuntimeCustomerScenarioIntentCorrections(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	changed := false
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		text := strings.TrimSpace(task.Text)
		switch {
		case runtimeExternalProxyActionRequest(text, *task):
			task.Intent = "service_request"
			task.SubIntent = "external_proxy_action"
			task.Objective = "action_request"
			task.NeedsKnowledge = false
			task.NeedsResource = false
			task.NeedsTool = false
			task.NeedsHumanRoute = false
			task.ResourceAction = ""
			task.Reason = appendIntentReason(task.Reason, "current customer message explicitly delegates an external order")
			changed = true
		case runtimePillowRoomServiceRequest(text):
			task.Intent = "service_request"
			task.SubIntent = "room_supplies"
			task.Objective = "action_request"
			task.NeedsKnowledge = true
			task.NeedsResource = false
			task.NeedsTool = false
			task.NeedsHumanRoute = false
			task.ResourceAction = ""
			task.Reason = appendIntentReason(task.Reason, "pillow room-service request must not become a product card")
			changed = true
		case runtimePillowProductPurchaseRequest(text, *task):
			task.Intent = "hotel_variable"
			task.SubIntent = "pillow_product"
			task.Objective = "action_request"
			task.NeedsKnowledge = false
			task.NeedsResource = true
			task.NeedsTool = false
			task.NeedsHumanRoute = false
			task.ResourceAction = "provide_pillow_product"
			task.Reason = appendIntentReason(task.Reason, "explicit hotel pillow purchase request uses the controlled shop product")
			changed = true
		case task.SubIntent == "checkout_process" && runtimePersonalizedCheckoutQuestion(text):
			task.Intent = "hotel_info"
			task.SubIntent = "order_detail"
			task.NeedsKnowledge = false
			task.NeedsTool = true
			task.Reason = appendIntentReason(task.Reason, "personal checkout time requires the customer's live order")
			changed = true
		case isMemberRuntimeSubIntent(task.SubIntent) && runtimeGenericMembershipProgramQuestion(text):
			task.Intent = "hotel_info"
			task.SubIntent = "store_knowledge"
			task.NeedsKnowledge = true
			task.NeedsTool = false
			task.Reason = appendIntentReason(task.Reason, "general membership program question does not identify a customer")
			changed = true
		case !isMemberRuntimeSubIntent(task.SubIntent) && runtimePersonalMemberBenefitsQuestion(text):
			task.Intent = "hotel_info"
			task.SubIntent = "member_benefits"
			task.NeedsKnowledge = false
			task.NeedsTool = true
			task.Reason = appendIntentReason(task.Reason, "personal member benefits require the customer's verified membership")
			changed = true
		}
	}
	if !changed {
		return intent
	}
	return deriveModelIntentFromTasks(intent)
}

func runtimeExternalProxyActionRequest(text string, task callbacks.IntentTaskTraceData) bool {
	compact := compactRuntimePillowIntentText(text)
	if compact == "" || !containsAny(compact, []string{
		"帮我下单", "替我下单", "帮我点", "替我点", "帮我买", "替我买",
		"帮我叫车", "替我叫车", "帮我联系", "替我联系",
	}) {
		return false
	}
	externalContext := containsAny(compact, []string{
		"外卖", "美团", "饿了么", "餐", "商品", "出租车", "网约车", "商家",
	})
	switch strings.TrimSpace(task.SubIntent) {
	case "food_delivery", "external_proxy_action":
		externalContext = true
	}
	return externalContext
}

func runtimePillowProductPurchaseRequest(text string, task callbacks.IntentTaskTraceData) bool {
	compact := compactRuntimePillowIntentText(text)
	if compact == "" || runtimePillowRoomServiceRequest(compact) {
		return false
	}
	productContext := strings.Contains(compact, "枕头") || strings.Contains(compact, "同款") ||
		strings.TrimSpace(task.SubIntent) == "pillow_product" || strings.TrimSpace(task.ResourceAction) == "provide_pillow_product"
	if !productContext && !strings.Contains(compact, "购买链接") {
		return false
	}
	return containsAny(compact, []string{
		"同款怎么买", "怎么买", "哪里买", "在哪买", "购买", "购买链接", "链接", "下单", "多少钱", "价格",
		"卖吗", "能买吗", "可以买", "想买", "我要买", "怎么卖",
	})
}

func runtimePillowRoomServiceRequest(text string) bool {
	compact := compactRuntimePillowIntentText(text)
	if !strings.Contains(compact, "枕头") {
		return false
	}
	return containsAny(compact, []string{
		"送个枕头", "送一个枕头", "送两个枕头", "送枕头到", "枕头送到", "送来枕头",
		"拿个枕头", "拿一个枕头", "拿来枕头", "换个枕头", "换一个枕头", "更换枕头",
		"加个枕头", "加一个枕头", "加两个枕头", "补个枕头", "补一个枕头", "多要个枕头",
		"枕头脏", "枕头坏", "枕头破", "枕头不舒服", "枕头不合适", "枕头太高", "枕头太低",
	})
}

func compactRuntimePillowIntentText(text string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "，", "", ",", "", "。", "", "！", "", "!", "", "？", "", "?", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

func runtimePersonalizedCheckoutQuestion(text string) bool {
	compact := compactRuntimePMSPhoneContext(strings.ToLower(text))
	if !containsAny(compact, []string{"退房", "离店"}) {
		return false
	}
	if strings.Contains(compact, "我的退房") || strings.Contains(compact, "我的离店") ||
		strings.Contains(compact, "我原本退房") || strings.Contains(compact, "我原定退房") ||
		strings.Contains(compact, "我最晚退房") || strings.Contains(compact, "我实际退房") {
		return true
	}
	for _, phrase := range []string{
		"我几点退房", "我什么时候退房", "我哪天退房", "我几号退房",
		"我几点离店", "我什么时候离店", "我哪天离店", "我几号离店",
	} {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func runtimeGenericMembershipProgramQuestion(text string) bool {
	compact := compactRuntimePMSPhoneContext(strings.ToLower(text))
	if !strings.Contains(compact, "会员") || runtimePersonalMembershipQuestion(compact) {
		return false
	}
	return containsAny(compact, []string{
		"你们有会员吗", "你们酒店有会员吗", "酒店有会员吗", "这里有会员吗",
		"有会员体系吗", "有会员制度吗", "怎么办会员", "怎么成为会员", "怎么注册会员",
		"会员体系是什么", "会员制度是什么", "会员都有什么优惠", "会员有哪些优惠",
	})
}

func runtimePersonalMemberBenefitsQuestion(text string) bool {
	compact := compactRuntimePMSPhoneContext(strings.ToLower(text))
	if !strings.Contains(compact, "会员") || !runtimePersonalMembershipQuestion(compact) {
		return false
	}
	return containsAny(compact, []string{
		"优惠", "权益", "福利", "折扣", "早餐", "延迟退房", "升房", "免差价", "保级", "升级",
	})
}

func runtimePersonalMembershipQuestion(compact string) bool {
	return containsAny(compact, []string{
		"我是会员", "我已经是会员", "我也是会员", "我的会员", "我会员等级", "我这个会员",
		"查我的会员", "看看我的会员", "我能享受", "我可以享受", "我有什么会员", "我有啥会员",
	})
}
