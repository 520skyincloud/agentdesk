package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/config"
)

func sandboxSceneIntentInstruction() string {
	if !config.PMSSandboxEnabled() {
		return ""
	}
	return `
【测试 PMS 六场景，当前环境专用】
仍由你根据完整语义拆分当前问题、识别同义表达并补全相关上下文，不按关键词创建任务。对属于以下六类的每个 intentTasks 项增加 sandboxScene 与 sandboxParams；其他问题不填，不强制归类。六场景的必要查询和办理预览由程序执行，不能用 FAQ 资料不足代替。
A=订单与适用政策：订单是否含早、儿童收费、几点退房、当前订单/房态/库存。使用 hotel_info，sandboxParams.topics 只选当前实际问题的 order、breakfast、child_policy、checkout、inventory。
B=房间升房：能升级吗、换个更好的房型、会员能免费升房吗。使用 service_request；会员等级升级规则不是房间升房，不属于 B。
C=换房与排房：太吵想换房、换个房间、能安排哪间。使用 service_request；安静、靠电梯等只能作为客户要求，不得推断某一房间具备属性。
D=服务补救：服务仍没解决、空调没有修好、希望补偿、延迟退房。使用 service_request；sandboxParams.actions 仅按客户当前需求填写 commitment（补偿承诺）、late_checkout（延迟退房）、room_change（换房补救）、upgrade（升房补救）；可延迟退房吗即使未给时间也必须填 late_checkout，不能误作为退款或优惠券承诺。普通首次用品请求继续原知识链路，不假装已有补偿授权。
E=会员福利：会员等级、会员升级条件、已有权益、生日福利。使用 hotel_info，sandboxParams.topics 按本题选择 member、benefits、birthday；只被动回答，不新建营销。
F=枕头商品：枕头好舒服、同款怎么买、购买链接发我。使用 hotel_info；程序发独立枕头资源。枕头脏了、硬得不舒服、要求更换枕头不是购买，继续服务/补救问题，不填 F。入住小程序不是枕头资源。
sandboxParams 的可用字段只有 phone、orderNumber、topics、actions、roomTypeName、roomNumber、checkoutAt、remedyCode。只有客户当前或相关历史明确提供的值才能填写，未知留空；不要填门店、Provider、数据批次、订单 ID、会员 ID、价格、费用或臆造规则。日期只能按消息时间及客户原意补全；未明确延退日期/时间留空，由服务端查询当前有效规则提出可确认时间；有多个选项时再询问。房型房号和补救选择只能用客户实际选定的文字，不能替客户选择。
同一当前轮同时要求升房/换房/延退，分别保留 Task 和原有顺序，由后台合并同一订单的方案。客户只补充手机号、选项或日期时，继承相应场景与必要上下文，text 仍保留当前原话；新主题不继承旧场景。缺手机号或订单仍保留真实场景，不能改成泛化闲聊；服务端优先使用本会话已有测试订单绑定，确实缺失才补问。
示例：{"intent":"service_request","subIntent":"room_upgrade","sandboxScene":"B","sandboxParams":{"phone":"","roomTypeName":""},"objective":"action_request","relationToPrevious":"independent","resolutionState":"clear","text":"能升级吗","resolvedText":"能升级吗","sourceRefs":["U1"],"needsTool":true,"needsKnowledge":false}。
所有测试结果必须明确标记“测试 PMS”；生成方案不等于办理成功，客户确认后由服务端事务执行并回查，禁止自行承诺已升房/已换房/已扣款/已退款。
`
}

func isSandboxScene(scene string) bool {
	switch strings.ToUpper(strings.TrimSpace(scene)) {
	case "A", "B", "C", "D", "E", "F":
		return true
	default:
		return false
	}
}

func runtimeIntentHasSandboxScene(intent callbacks.IntentTraceData) bool {
	if !config.PMSSandboxEnabled() {
		return false
	}
	for _, task := range intent.IntentTasks {
		if isSandboxScene(task.SandboxScene) {
			return true
		}
	}
	return false
}

func normalizeSandboxSceneIntent(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if !config.PMSSandboxEnabled() {
		for index := range intent.IntentTasks {
			intent.IntentTasks[index].SandboxScene = ""
			intent.IntentTasks[index].SandboxParams = nil
		}
		return intent
	}
	hasScene := false
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		task.SandboxScene = strings.ToUpper(strings.TrimSpace(task.SandboxScene))
		if !isSandboxScene(task.SandboxScene) {
			task.SandboxScene = ""
			task.SandboxParams = nil
			continue
		}
		// Explicit handoff and unresolved semantic tasks retain their original
		// safety/clarification route; a scene label alone cannot authorize work.
		if task.Intent == "human_complaint_risk" || task.SubIntent == "clarify" ||
			task.ResolutionState == runtimeIntentResolutionAmbiguous ||
			task.ResolutionState == runtimeIntentResolutionUnresolved {
			task.SandboxScene = ""
			task.SandboxParams = nil
			continue
		}
		hasScene = true
		task.NeedsKnowledge = false
		task.NeedsTool = true
		task.NeedsResource = false
		task.NeedsHumanRoute = false
		task.ResourceAction = ""
		switch task.SandboxScene {
		case "A":
			task.Intent, task.SubIntent = "hotel_info", "order_query"
		case "B":
			task.Intent, task.SubIntent = "service_request", "room_upgrade"
		case "C":
			task.Intent, task.SubIntent = "service_request", "room_change"
		case "D":
			task.Intent, task.SubIntent = "service_request", "service_recovery"
		case "E":
			task.Intent, task.SubIntent = "hotel_info", "member_benefits"
		case "F":
			task.Intent, task.SubIntent = "hotel_info", "pillow_product"
		}
	}
	if !hasScene {
		return intent
	}
	return semanticGateRecomputeIntent(intent, nil)
}
