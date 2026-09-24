package main

import "agent-desk/internal/pkg/enums"

func customerGoalVariationScenarios() []scenario {
	no := false
	customer := func(text string, outcomes ...outcomeRequirement) turn {
		return turn{
			Type: enums.IMMessageTypeText, Content: text, WaitForAI: true,
			RequiredOutcomes: outcomes, NeedsHumanRoute: &no,
			Banned:           []string{"PMS", "接待单ID", "预订单ID", "taskId", "replyParts", "没能整理", "已经换好", "已经锁房"},
			MaxReplyMessages: 3, LatencyWarningMs: 12000, LatencyLimitMs: 30000,
		}
	}
	identity := scenario{
		ID: "GV01", Category: "goal-variations", Name: "补充身份后纠正并追问个人离店时间", RecordEachTurn: true,
		Turns: []turn{
			customer("我这次预订什么时候结束？", textOutcome("必要定位追问", "手机号", "订单号")),
			customer("用18569300806查", textOutcome("查到住宿日期", "9月25日", "退房")),
			customer("刚才号码写错了，换13900000000查", textOutcome("新号码查询或必要说明", "未查到", "没查到", "没有查到", "手机号", "订单")),
			customer("不是13900000000，还是用18569300806，我最晚几点离店？", textOutcome("纠正后查原住宿", "12点", "12:00")),
		},
	}
	identity.Turns[2].Banned = append(identity.Turns[2].Banned, "376", "V05", "9月25日12点")
	cancel := customer("算了，这次不换了")
	cancel.NoTools = true
	cancel.Banned = append(cancel.Banned, "1501", "可选房间", "请提供手机号")
	restart := scenario{
		ID: "GV02", Category: "goal-variations", Name: "换房取消后切换主题再重新提出需求", RecordEachTurn: true,
		Turns: []turn{
			customer("我想换个房，预订电话18569300806", textOutcome("提供换房选择", "沐阳", "橙意", "房型")),
			cancel,
			customer("先告诉我停车怎么进去", textOutcome("新主题停车入口", "昭潭路")),
			customer("现在重新看看能不能换到沐阳", textOutcome("重新查询指定房型", "沐阳")),
		},
	}
	knowledge := scenario{
		ID: "GV03", Category: "goal-variations", Name: "知识指代纠正与混合订单需求", RecordEachTurn: true,
		Turns: []turn{
			customer("想喝杯咖啡，酒店有吗？", textOutcome("咖啡供应", "咖啡")),
			customer("去哪里取呀？", textOutcome("咖啡具体位置", "1313", "洗衣房")),
			customer("不是问咖啡了，我是问停车场入口", textOutcome("纠正对象为停车", "昭潭路")),
			customer("手机号18569300806，帮我看最晚几点退房，外卖地址也告诉我", textOutcome("个人退房时间", "12点", "12:00"), textOutcome("外卖地址", "丽斯未来酒店合肥南七店")),
		},
	}
	knowledge.Turns[2].Banned = append(knowledge.Turns[2].Banned, "1313", "洗衣房")
	return []scenario{identity, restart, knowledge}
}
