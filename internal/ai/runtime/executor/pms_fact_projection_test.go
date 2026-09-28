package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestPMSProgramFactsAnswerOnlyRequestedMembershipAspect(t *testing.T) {
	grade := map[string]any{
		"gradeName": "钻石会员", "gradeAvailable": true, "validityText": "12个月",
		"upgradeRuleSummary":   "累计入住房夜 >= 12 且 等级成长积分 >= 2000",
		"keepGradeRuleSummary": "等级成长积分 >= 2000 或 新增入住房夜 >= 12",
		"benefits": []any{
			map[string]any{"label": "会员价", "contentText": "8.5折"},
			map[string]any{"label": "延迟退房", "contentText": "15:00", "benefitDescription": "仅适用会员渠道全天房"},
		},
	}
	data := map[string]any{"grades": []any{
		map[string]any{"gradeName": "普通会员", "gradeAvailable": true, "benefits": []any{map[string]any{"label": "会员价", "contentText": "9.9折"}}},
		grade,
	}}
	for _, question := range []string{"你们有哪些会员？", "酒店会员分几档啊？"} {
		task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, SubjectScope: runtimeSubjectPublicMembership, RequestedAspects: []string{"member_level_names"}}
		text := runtimePMSFactStatementsForTest(runtimePMSProgramFacts(task, data))
		for _, want := range []string{"普通会员", "钻石会员"} {
			if !strings.Contains(text, want) {
				t.Fatalf("catalog lost requested name: %s", text)
			}
		}
		for _, unwanted := range []string{"8.5", "2000", "15:00", "12个月"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("catalog dumped unasked fields: %s", text)
			}
		}
	}
	for _, question := range []string{"钻石会员最晚几点退？", "钻石卡能住到下午几点离店？"} {
		task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, SubjectScope: runtimeSubjectPublicMembership, RequestedAspects: []string{"checkout_time"}}
		facts := runtimePMSProgramFacts(task, data)
		text := runtimePMSFactStatementsForTest(facts)
		if !strings.Contains(text, "15:00") || !strings.Contains(text, "仅适用会员渠道全天房") {
			t.Fatalf("checkout benefit or its condition lost: %s", text)
		}
		for _, unwanted := range []string{"8.5", "2000", "普通会员", "12个月"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("checkout question got unrelated member details: %s", text)
			}
		}
	}
	for _, question := range []string{"怎么升级成钻石会员？", "达到钻石卡要满足什么条件？"} {
		task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, RequestedAspects: []string{"member_upgrade_conditions"}}
		text := runtimePMSFactStatementsForTest(runtimePMSProgramFacts(task, data))
		if !strings.Contains(text, "12 且 等级成长积分 >= 2000") || strings.Contains(text, "保级") || strings.Contains(text, "15:00") {
			t.Fatalf("upgrade condition semantics changed or unrelated rules included: %s", text)
		}
	}
}

func TestPMSHistoryFactsPreserveObjectButReplaceRequestedAspect(t *testing.T) {
	data := map[string]any{"rows": []any{map[string]any{
		"receptOrderId": "9007199254740993", "homeName": "1304", "roomName": "儿童房",
		"checkInTime": "2026-09-23 10:13:00", "checkOutTime": "2026-09-28 10:09:00",
		"orderStatus": "0015003", "payAmount": "376.00",
	}}}
	for _, test := range []struct {
		questions []string
		aspect    string
		want      string
		unwanted  []string
	}{
		{[]string{"上次住的时候是什么时候退的？", "那一单我几点离店的？"}, "checkout_time", "9月28日10:09", []string{"376", "10:13", "儿童房", "1304"}},
		{[]string{"那次一共花了多少钱？", "上次的订单金额多少？"}, "order_amount", "376.00元", []string{"9月28日", "10:13", "儿童房", "1304"}},
	} {
		for _, question := range test.questions {
			task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, SubjectScope: runtimeSubjectHistoricalOrder, RequestedAspects: []string{test.aspect}}
			text := runtimePMSFactStatementsForTest(runtimePMSHistoryFacts(task, data))
			if !strings.Contains(text, test.want) {
				t.Fatalf("requested historical field missing: %s", text)
			}
			for _, unwanted := range append(test.unwanted, "9007199254740993") {
				if strings.Contains(text, unwanted) {
					t.Fatalf("unrequested historical field leaked: %s", text)
				}
			}
		}
	}
	for _, question := range []string{"我当前房间几点退？", "我今晚的订单是什么房型？"} {
		task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, SubjectScope: runtimeSubjectCurrentOrder, RequestedAspects: []string{"checkout_time"}}
		text := runtimePMSFactStatementsForTest(runtimePMSHistoryFacts(task, data))
		if strings.Contains(text, "10:09") || !strings.Contains(text, "不能以历史住宿替代当前订单") {
			t.Fatalf("historical record was treated as current: %s", text)
		}
	}
}

func TestPMSPersonalMembershipFactsDoNotRepeatOtherBenefits(t *testing.T) {
	data := map[string]any{
		"member": map[string]any{"gradeName": "银卡会员", "statusName": "启用", "gradeAvailable": true},
		"grade": map[string]any{"benefits": []any{
			map[string]any{"label": "会员价", "contentText": "9.5折"},
			map[string]any{"label": "早餐", "contentText": "1份"},
			map[string]any{"label": "延迟退房", "contentText": "13:00"},
		}},
	}
	for _, question := range []string{"我的卡也能到刚才说的15点吗？", "帮我查查本人会员能几点退？"} {
		task := callbacks.ReplyTaskPlanTraceData{OriginalText: question, SubjectScope: runtimeSubjectPersonalMembership, RequestedAspects: []string{"checkout_time"}}
		text := runtimePMSFactStatementsForTest(runtimePMSPersonalMemberFacts(task, data))
		if !strings.Contains(text, "银卡会员") || !strings.Contains(text, "13:00") || strings.Contains(text, "9.5") || strings.Contains(text, "早餐") {
			t.Fatalf("personal checkout facts are incomplete or unfocused: %s", text)
		}
	}
}

func TestPMSRoomSelectionBindsAgainstEntireRealCatalog(t *testing.T) {
	catalog := make([]any, 0, 9)
	for _, name := range []string{"儿童房", "星旗", "云漫", "橙意", "亲子", "双床", "家庭", "沐阳"} {
		catalog = append(catalog, map[string]any{"roomTypeId": "id-" + name, "roomTypeName": name})
	}
	for _, question := range []string{"那就看看沐阳吧", "你刚说的沐阳我想换到那个"} {
		id, name, status := resolveRuntimePMSTargetRoomType(catalog, question)
		if status != pmsReadStepOK || id != "id-沐阳" || name != "沐阳" {
			t.Fatalf("selection not bound to catalog: %s => %s %s %s", question, id, name, status)
		}
	}
	for _, question := range []string{"沐阳和云漫我选哪个好", "不存在的豪华套间"} {
		if _, _, status := resolveRuntimePMSTargetRoomType(catalog, question); status == pmsReadStepOK {
			t.Fatalf("ambiguous or absent room was guessed: %s", question)
		}
	}
}

func TestPMSPriceFactsKeepTargetDatesAndNullableAmounts(t *testing.T) {
	rows := []any{}
	for _, name := range []string{"儿童房", "星旗", "云漫", "橙意", "亲子", "双床", "家庭", "沐阳"} {
		rows = append(rows, map[string]any{
			"roomTypeId": "id-" + name, "roomTypeName": name,
			"bookings": map[string]any{
				"2026-09-28": map[string]any{"price": "268.50"},
				"2026-09-29": map[string]any{"price": nil},
				"2026-09-30": map[string]any{"price": "999.00"},
			},
		})
	}
	for _, targetInPlan := range []bool{true, false} {
		plan := pmsReadPlan{}
		if targetInPlan {
			plan.Steps = []pmsReadPlanStep{{ID: "inventory.stay", Args: map[string]string{"roomTypeId": "id-沐阳"}}}
		}
		step := pmsReadStepResult{
			StepID: "price.board", Status: pmsReadStepOK, Data: rows,
			Args: map[string]string{"beginTime": "2026-09-28", "endTime": "2026-09-30", "roomTypeId": "id-沐阳"},
		}
		facts := runtimePMSPriceBoardFacts(plan, step)
		text := runtimePMSFactStatementsForTest(facts)
		if len(facts) != 1 || !strings.Contains(text, "沐阳在9月28日晚的挂牌售价为268.50元") {
			t.Fatalf("target price beyond default display limit was omitted: %s", text)
		}
		for _, unwanted := range []string{"儿童房", "星旗", "9月29日", "9月30日", "999", "为0元", "为0.00元"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("price facts leaked unrelated room, nullable price, or checkout night: %s", text)
			}
		}
		if !strings.Contains(text, "不是完成会员或渠道结算后的最终补退金额") {
			t.Fatalf("listed price lost settlement boundary: %s", text)
		}
	}
}

func TestPMSInventoryFactsRequireEveryStayNightAndRespectResolvedTarget(t *testing.T) {
	for _, complete := range []bool{true, false} {
		bookings := map[string]any{
			"2026-09-28": map[string]any{"available": "2"},
			"2026-09-30": map[string]any{"available": "0"},
		}
		if complete {
			bookings["2026-09-29"] = map[string]any{"available": "1"}
		}
		step := pmsReadStepResult{
			StepID: "inventory.stay", Status: pmsReadStepOK,
			Args: map[string]string{"beginTime": "2026-09-28", "endTime": "2026-09-30", "roomTypeId": "TARGET"},
			Data: []any{
				map[string]any{"roomTypeId": "OTHER", "roomTypeName": "别的房型", "bookings": bookings},
				map[string]any{"roomTypeId": "TARGET", "roomTypeName": "沐阳", "bookings": bookings},
			},
		}
		// Renewal IDs may only become available after the old order is read.
		plan := pmsReadPlan{Steps: []pmsReadPlanStep{{ID: "inventory.stay", Args: map[string]string{}}}}
		facts := runtimePMSInventoryFacts(plan, step)
		text := runtimePMSFactStatementsForTest(facts)
		if len(facts) != 1 || !strings.Contains(text, "沐阳在9月28日入住至9月30日离店区间") || strings.Contains(text, "别的房型") {
			t.Fatalf("resolved order room type or stay dates not honored: %s", text)
		}
		if complete {
			if !strings.Contains(text, "最低可售库存为1间") || strings.Contains(text, "库存为0间") {
				t.Fatalf("checkout day was included in the inventory calculation: %s", text)
			}
		} else if !strings.Contains(text, "不能确认全程可售") || strings.Contains(text, "最低可售库存") {
			t.Fatalf("incomplete date coverage was reported as full availability: %s", text)
		}
		if !strings.Contains(text, "不代表同一房号全程可用") || !strings.Contains(text, "没有锁房") {
			t.Fatalf("inventory became an allocation promise: %s", text)
		}
	}
}

func TestPMSRoomAvailabilityFactsKeepCoverageAndTargetBoundaries(t *testing.T) {
	plan := pmsReadPlan{Steps: []pmsReadPlanStep{{ID: "inventory.stay", Args: map[string]string{"roomTypeId": "TARGET"}}}}
	for _, complete := range []bool{true, false} {
		step := pmsReadStepResult{Data: map[string]any{
			"coverageComplete": complete, "startDate": "2026-09-28", "endDate": "2026-09-30",
			"reason": "订单分页尚未完整返回",
			"candidates": []any{
				map[string]any{"roomTypeId": "OTHER", "roomTypeName": "其他房型", "homeName": "0801", "readyNow": true},
				map[string]any{"roomTypeId": "TARGET", "roomTypeName": "沐阳", "homeName": "0901", "readyNow": true},
			},
		}}
		text := runtimePMSFactStatementsForTest(runtimePMSStayRoomFacts(plan, step))
		if !strings.Contains(text, "9月28日至9月30日") || strings.Contains(text, "0801") {
			t.Fatalf("room facts included another target or lost dates: %s", text)
		}
		if complete {
			if !strings.Contains(text, "0901房") || !strings.Contains(text, "当前为空净房") || !strings.Contains(text, "并未分配或锁定") {
				t.Fatalf("complete room facts lost readiness or read-only boundary: %s", text)
			}
		} else if !strings.Contains(text, "资料未完整覆盖") || strings.Contains(text, "0901") {
			t.Fatalf("incomplete occupancy coverage exposed confident candidates: %s", text)
		}
	}
}
