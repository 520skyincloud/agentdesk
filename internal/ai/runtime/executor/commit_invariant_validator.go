package executor

import (
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func validateReplyCommitInvariants(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	if input.Plan.TurnVersion <= 0 || input.ActionLedger.TurnVersion != input.Plan.TurnVersion {
		issues = append(issues, validationIssue("turn_version_mismatch", "$", "reply plan and action ledger turn versions differ"))
	}
	if input.Evidence.ScopeFingerprint == "" {
		issues = append(issues, validationIssue("evidence_scope_missing", "$.evidence", "evidence scope fingerprint is missing"))
	}
	if len(input.Output.Parts) > input.Plan.GlobalConstraints.MaxReplyParts {
		issues = append(issues, validationIssue("too_many_reply_parts", "$.parts", "reply exceeds the plan part limit"))
	}
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	for _, part := range input.Output.Parts {
		if part.Content == "" || len(part.TaskKeys) == 0 {
			issues = append(issues, validationIssue("empty_reply_part", "$.parts", "reply part must contain content and task keys"))
		}
		if limit := input.Plan.GlobalConstraints.MaxQuestionsPerPart; limit > 0 && len(part.TaskKeys) > limit {
			issues = append(issues, validationIssue("too_many_questions_in_part", "$.parts", "reply part exceeds the plan question limit"))
		}
		if replyPartMissesDistinctTaskAnswers(part, planByTask) {
			issues = append(issues, validationIssue("task_answer_obligation_missing", "$.parts", "reply part content does not answer every distinct task key"))
		}
	}
	return issues
}

func replyPartMissesDistinctTaskAnswers(part contracts.ReplyPartV2, planByTask map[string]contracts.ReplyPlanTaskV2) bool {
	if len(part.TaskKeys) < 2 || strings.TrimSpace(part.Content) == "" {
		return false
	}
	tasks := make([]contracts.ReplyPlanTaskV2, 0, len(part.TaskKeys))
	for _, taskKey := range part.TaskKeys {
		task, ok := planByTask[taskKey]
		if !ok {
			continue
		}
		tasks = append(tasks, task)
	}
	if len(tasks) < 2 {
		return true
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Sequence < tasks[j].Sequence })
	units := splitReplyAnswerUnits(part.Content)
	if len(units) == 0 {
		return true
	}
	usedImplicitUnits := make([]bool, len(units))
	for _, task := range tasks {
		matched := false
		for _, unit := range units {
			if replyAnswerUnitExplicitlyNamesTask(unit, task) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for index, unit := range units {
			if usedImplicitUnits[index] || !replyAnswerUnitImplicitlySupportsTask(unit, task) {
				continue
			}
			usedImplicitUnits[index] = true
			matched = true
			break
		}
		if !matched {
			return true
		}
	}
	return false
}

func splitReplyAnswerUnits(content string) []string {
	parts := strings.FieldsFunc(content, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '。' || r == '！' || r == '!' || r == '？' || r == '?' || r == '；' || r == ';' || r == '，' || r == ','
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func replyAnswerUnitExplicitlyNamesTask(unit string, task contracts.ReplyPlanTaskV2) bool {
	compact := compactRuntimeProtocolText(unit)
	markers := replyTaskObjectiveMarkers(task)
	if len(markers) > 0 {
		return containsAny(compact, markers) && replyUnitHasAnswerPayload(compact)
	}
	topic := replyTaskTopicClass(task)
	if topic == "" || !replyUnitHasAnswerPayload(compact) {
		return false
	}
	if _, ok := detectKnowledgeTopicClasses(unit)[topic]; ok {
		return true
	}
	return false
}

func replyTaskObjectiveMarkers(task contracts.ReplyPlanTaskV2) []string {
	objective := compactRuntimeProtocolText(task.Objective)
	candidates := []string{
		"入住", "退房", "地址", "定位", "停车", "早餐", "咖啡", "发票", "wifi", "无线网", "网络", "密码", "洗衣", "烘干", "行李", "寄存",
		"外卖", "送餐", "换房", "升房", "房型", "牙刷", "拖鞋", "剃须刀", "草稿纸", "纸张", "纸笔", "洗漱用品", "毛巾", "浴巾", "矿泉水",
		"电视", "投屏", "空调", "开门", "门锁", "保洁", "打扫", "优惠", "折扣", "酒店名", "门店名", "电话", "小程序",
	}
	ret := make([]string, 0, 2)
	for _, marker := range candidates {
		if strings.Contains(objective, marker) {
			ret = append(ret, marker)
		}
	}
	return ret
}

func replyUnitHasAnswerPayload(compact string) bool {
	if containsAny(compact, []string{
		"是", "在", "从", "到", "有", "没有", "可以", "不能", "无法", "免费", "收费", "开放", "提供", "需要", "使用", "办理", "填写", "下单",
		"当前资料", "没写明", "入口", "出口", "楼", "层", "路", "街", "号", "点", "时", "密码", "自取", "柜", "前", "后",
	}) {
		return true
	}
	for _, r := range compact {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func replyAnswerUnitImplicitlySupportsTask(unit string, task contracts.ReplyPlanTaskV2) bool {
	topic := replyTaskTopicClass(task)
	if topic == "" {
		return false
	}
	compact := compactRuntimeProtocolText(unit)
	switch topic {
	case "checkin":
		return containsAny(compact, []string{"身份证", "小程序", "门锁", "房门", "办理", "登记", "验证码"})
	case "checkout", "breakfast":
		return containsAny(compact, []string{"点", "时", ":", "：", "之前", "开始", "结束", "供应", "餐厅", "楼", "层"})
	case "address":
		return containsAny(compact, []string{"省", "市", "区", "县", "路", "街", "巷", "号", "楼", "层", "门牌", "导航"})
	case "parking":
		return containsAny(compact, []string{"入口", "出口", "东侧", "西侧", "南侧", "北侧", "地下", "地面", "车库", "车位", "免费", "收费", "导航"})
	case "coffee":
		return containsAny(compact, []string{"咖啡机", "大堂", "楼", "层", "免费", "收费", "供应", "开放"})
	case "invoice":
		return containsAny(compact, []string{"抬头", "税号", "邮箱", "平台", "电子", "开具", "订单"})
	case "wifi":
		return containsAny(compact, []string{"密码", "连接", "扫码", "名称", "账号", "房号后", "八个8", "8个8"})
	case "laundry":
		return containsAny(compact, []string{"洗衣机", "烘干机", "楼", "层", "免费", "收费", "开放"})
	case "luggage":
		return containsAny(compact, []string{"寄存", "柜", "楼", "层", "免费", "收费"})
	case "takeaway":
		return containsAny(compact, []string{"下单", "骑手", "配送", "送到", "收货", "备注", "门口", "自取"})
	case "room_change":
		return containsAny(compact, []string{"不能", "无法", "房态", "订单", "房型", "办理", "修改"})
	default:
		return false
	}
}

func repairableReplyCommitInvariantIssues(issues []contracts.ValidationIssueV1) bool {
	if len(issues) == 0 {
		return false
	}
	for _, issue := range issues {
		switch issue.Code {
		case "empty_reply_part", "too_many_reply_parts", "too_many_questions_in_part", "task_answer_obligation_missing":
		default:
			return false
		}
	}
	return true
}

func replyTaskTopicClass(task contracts.ReplyPlanTaskV2) string {
	if topics := detectKnowledgeTopicClasses(task.Objective); len(topics) == 1 {
		for topic := range topics {
			return topic
		}
	}
	switch strings.TrimSpace(task.SubIntent) {
	case "checkin_process", "check_in":
		return "checkin"
	case "checkout_process", "check_out":
		return "checkout"
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return "address"
	case "parking":
		return "parking"
	case "breakfast":
		return "breakfast"
	case "coffee":
		return "coffee"
	case "invoice":
		return "invoice"
	case "network_wifi", "wifi", "network":
		return "wifi"
	case "laundry":
		return "laundry"
	case "luggage", "luggage_storage":
		return "luggage"
	case "order_food_delivery", "food_delivery", "takeaway":
		return "takeaway"
	case "room_change", "room_upgrade":
		return "room_change"
	default:
		return ""
	}
}
