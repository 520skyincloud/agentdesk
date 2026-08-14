package actions

// init 登记动作目录的全部动作定义（只含元数据与开关，执行器由上层注入）。
func init() {
	Register(Definition{
		Code:                "human_handoff",
		Name:                "转人工",
		Kind:                KindBuiltin,
		Description:         "把当前问题转给人工客服接待；执行前向客户二次确认。",
		RequireConfirmation: true,
		ExecutorRef:         "human_handoff",
		DefaultEnabled:      true,
	})
	Register(Definition{
		Code:                "create_ticket",
		Name:                "建工单",
		Kind:                KindBuiltin,
		Description:         "为当前问题创建工单；执行前向客户二次确认。",
		RequireConfirmation: true,
		ExecutorRef:         "create_ticket",
		DefaultEnabled:      true,
	})
	Register(Definition{
		Code:           "provide_location",
		Name:           "发定位",
		Kind:           KindBuiltin,
		Description:    "发送当前门店定位。",
		ExecutorRef:    "provide_location",
		DefaultEnabled: true,
	})
	Register(Definition{
		Code:           "provide_mini_program",
		Name:           "发小程序",
		Kind:           KindBuiltin,
		Description:    "发送入住小程序。",
		ExecutorRef:    "provide_mini_program",
		DefaultEnabled: true,
	})
	Register(Definition{
		Code:           "provide_phone",
		Name:           "发电话",
		Kind:           KindBuiltin,
		Description:    "发送门店联系电话；未配置时返回安全文本。",
		ExecutorRef:    "provide_phone",
		DefaultEnabled: true,
	})
	Register(Definition{
		Code:           "query_room_status",
		Name:           "查房态",
		Kind:           KindExternal,
		Description:    "查询 PMS 实时房态（尚未接入）。",
		ExecutorRef:    "query_room_status",
		DefaultEnabled: false,
	})
	Register(Definition{
		Code:           "query_member_level",
		Name:           "查会员等级",
		Kind:           KindExternal,
		Description:    "查询会员等级与权益（尚未接入）。",
		ExecutorRef:    "query_member_level",
		DefaultEnabled: false,
	})
}
