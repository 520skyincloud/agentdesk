package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/services"
)

type runtimeSandboxService interface {
	ExecuteScene(context.Context, sandbox.Scope, sandbox.SceneInput) (*sandbox.SceneResult, error)
	Prepare(context.Context, sandbox.Scope, sandbox.ChangeRequest) (*sandbox.Operation, error)
}

func executeSandboxSceneTasks(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if collector == nil || !runtimeIntentHasSandboxScene(collector.Data.Pipeline.Intent) {
		return
	}
	scope, err := services.PMSSandboxScopeForConversation(req.Conversation.ID, req.UserMessage.ID)
	if err != nil {
		scope = sandbox.Scope{}
	}
	executeSandboxSceneTasksWithService(ctx, scope, summary, collector, services.PMSSandboxService)
}

func executeSandboxSceneTasksWithService(ctx context.Context, scope sandbox.Scope, summary *RunResult, collector *callbacks.RuntimeTraceCollector, service runtimeSandboxService) {
	if !config.PMSSandboxEnabled() || collector == nil || service == nil {
		return
	}
	plan := collector.Data.Pipeline.ReplyPlan
	writeIndexes := make([]int, 0, 3)
	for index := range plan.TaskPlans {
		task := &plan.TaskPlans[index]
		if !isSandboxScene(task.SandboxScene) {
			continue
		}
		if scope.StoreID <= 0 || scope.ConversationID <= 0 || scope.CustomerID <= 0 || scope.SourceMessageID <= 0 {
			setSandboxFixedReply(task, "【测试 PMS】当前会话还未绑定有效测试门店，暂时不能查询或办理。")
			continue
		}
		switch task.SandboxScene {
		case "B", "C", "D":
			writeIndexes = append(writeIndexes, index)
			continue
		}
		input := sandboxInputForTask(*task)
		started := time.Now()
		result, err := service.ExecuteScene(ctx, scope, input)
		recordSandboxTaskExecution(summary, collector, task.TaskID, task.SandboxScene, started, result, err)
		if err != nil {
			setSandboxFixedReply(task, sandboxRuntimeFailure(err))
			continue
		}
		if result == nil || strings.TrimSpace(result.Reply) == "" {
			setSandboxFixedReply(task, sandboxRuntimeFailure(nil))
			continue
		}
		setSandboxFixedReply(task, result.Reply)
		if task.SandboxScene == "F" && result.Resource != nil && result.State != nil &&
			result.State.Dataset.ID > 0 && result.State.Dataset.StoreID == scope.StoreID && result.Resource.ID > 0 {
			collector.Data.SandboxResources = append(collector.Data.SandboxResources, callbacks.SandboxResourceTraceData{
				TaskID: task.TaskID, StoreID: scope.StoreID,
				DatasetID: result.State.Dataset.ID, ResourceID: result.Resource.ID,
			})
		}
	}
	if len(writeIndexes) > 0 {
		executeSandboxChangeTasks(ctx, scope, &plan, writeIndexes, summary, collector, service)
	}
	collector.Data.Pipeline.ReplyPlan = plan
}

func sandboxInputForTask(task callbacks.ReplyTaskPlanTraceData) sandbox.SceneInput {
	input := sandbox.SceneInput{Scene: task.SandboxScene, Question: firstNonEmptyReplyTaskText(task.ResolvedText, task.Text)}
	if task.SandboxParams != nil {
		input.Phone = strings.TrimSpace(task.SandboxParams.Phone)
		input.OrderNumber = strings.TrimSpace(task.SandboxParams.OrderNumber)
		input.Topics = append([]string(nil), task.SandboxParams.Topics...)
	}
	return input
}

func executeSandboxChangeTasks(ctx context.Context, scope sandbox.Scope, plan *callbacks.ReplyPlanTraceData, indexes []int, summary *RunResult, collector *callbacks.RuntimeTraceCollector, service runtimeSandboxService) {
	setAll := func(reply string) {
		for _, index := range indexes {
			setSandboxFixedReply(&plan.TaskPlans[index], reply)
		}
	}
	phone, orderNumber := "", ""
	for _, index := range indexes {
		if params := plan.TaskPlans[index].SandboxParams; params != nil && strings.TrimSpace(params.Phone) != "" {
			value := strings.TrimSpace(params.Phone)
			if phone != "" && phone != value {
				setAll("【测试 PMS】本轮涉及不同手机号，请先明确要办理哪一笔测试订单。")
				return
			}
			phone = value
		}
		if params := plan.TaskPlans[index].SandboxParams; params != nil && strings.TrimSpace(params.OrderNumber) != "" {
			value := strings.TrimSpace(params.OrderNumber)
			if orderNumber != "" && orderNumber != value {
				setAll("【测试 PMS】本轮涉及不同订单，请先明确要办理哪一笔测试订单。")
				return
			}
			orderNumber = value
		}
	}
	started := time.Now()
	query, err := service.ExecuteScene(ctx, scope, sandbox.SceneInput{Scene: "A", Phone: phone, OrderNumber: orderNumber, Topics: []string{"order"}})
	if err != nil || query == nil || query.State == nil || query.State.Order == nil {
		reply := sandboxRuntimeFailure(err)
		if query != nil && strings.TrimSpace(query.Reply) != "" {
			reply = query.Reply
		}
		setAll(reply)
		recordSandboxTaskExecution(summary, collector, plan.TaskPlans[indexes[0]].TaskID, "prepare_query", started, query, err)
		return
	}
	change := sandbox.ChangeRequest{OrderID: query.State.Order.ID}
	for _, index := range indexes {
		task := plan.TaskPlans[index]
		if err := mergeSandboxTaskChange(&change, task, query.State); err != nil {
			setAll("【测试 PMS】" + err.Error() + "本轮尚未生成可确认的办理方案。")
			return
		}
	}
	operation, err := service.Prepare(ctx, scope, change)
	result := &sandbox.SceneResult{Scene: plan.TaskPlans[indexes[0]].SandboxScene, State: query.State, Operation: operation}
	if err != nil || operation == nil || strings.TrimSpace(operation.PreviewText) == "" {
		setAll(sandboxRuntimeFailure(err))
		recordSandboxTaskExecution(summary, collector, plan.TaskPlans[indexes[0]].TaskID, "prepare", started, result, err)
		return
	}
	if operation.Provider != sandbox.Provider || operation.StoreID != scope.StoreID ||
		operation.ConversationID != scope.ConversationID || operation.SourceMessageID != scope.SourceMessageID ||
		operation.DatasetID != query.State.Dataset.ID || operation.ID <= 0 || operation.Status != "pending" {
		setAll("【测试 PMS】这项办理方案当前不可确认，请重新提出办理需求；未执行订单修改。")
		return
	}
	result.Reply, result.Completed = operation.PreviewText, true
	collector.Data.SandboxOperationIDs = append(collector.Data.SandboxOperationIDs, operation.ID)
	for offset, index := range indexes {
		if offset == 0 {
			setSandboxFixedReply(&plan.TaskPlans[index], operation.PreviewText)
		} else {
			label := map[string]string{"B": "升房", "C": "换房/排房", "D": "服务补救或延退"}[plan.TaskPlans[index].SandboxScene]
			setSandboxFixedReply(&plan.TaskPlans[index], "【测试 PMS】这项"+label+"要求已包含在上述同一份方案中，确认后统一办理，现在尚未修改订单。")
		}
		recordSandboxTaskExecution(summary, collector, plan.TaskPlans[index].TaskID, plan.TaskPlans[index].SandboxScene, started, result, nil)
	}
}

func mergeSandboxTaskChange(change *sandbox.ChangeRequest, task callbacks.ReplyTaskPlanTraceData, state *sandbox.CustomerState) error {
	upgrade, roomChange, lateCheckout, recovery := false, false, false, false
	switch task.SandboxScene {
	case "B":
		upgrade = true
	case "C":
		roomChange = true
	case "D":
		recovery = true
	}
	mergeActions := func() {
		change.Upgrade = change.Upgrade || upgrade
		change.ChangeRoom = change.ChangeRoom || roomChange
		change.LateCheckout = change.LateCheckout || lateCheckout
		change.Recovery = change.Recovery || recovery
	}
	if task.SandboxParams == nil {
		mergeActions()
		return nil
	}
	params := task.SandboxParams
	if len(params.Actions) > 0 {
		recovery = false
		for _, action := range params.Actions {
			switch action {
			case "upgrade":
				upgrade = true
			case "room_change":
				roomChange = true
			case "late_checkout":
				lateCheckout = true
			case "commitment":
				recovery = true
			default:
				return errors.New("请明确需要升房、换房、延退还是补救承诺。")
			}
		}
	}
	if name := strings.TrimSpace(params.RoomTypeName); name != "" {
		id := int64(0)
		for _, available := range state.Availability {
			if available.RoomType.Name == name {
				id = available.RoomType.ID
			}
		}
		if id <= 0 {
			return errors.New("当前可用房型中没有您选的房型，请先明确目标房型。")
		}
		if change.TargetRoomTypeID > 0 && change.TargetRoomTypeID != id {
			return errors.New("本轮选择了不同目标房型，请先确认一个房型。")
		}
		change.TargetRoomTypeID = id
	}
	if number := strings.TrimSpace(params.RoomNumber); number != "" {
		id, roomTypeID := int64(0), int64(0)
		for _, available := range state.Availability {
			for _, room := range available.Rooms {
				if room.Number == number {
					id, roomTypeID = room.ID, room.RoomTypeID
				}
			}
		}
		if id <= 0 {
			return errors.New("所选房号不在当前入住区间的可用房间中，请重新选择。")
		}
		if change.TargetRoomID > 0 && change.TargetRoomID != id {
			return errors.New("本轮选择了不同房号，请先确认一个房间。")
		}
		if change.TargetRoomTypeID > 0 && change.TargetRoomTypeID != roomTypeID {
			return errors.New("所选房号与所选房型不一致，请确认目标房间。")
		}
		change.TargetRoomID = id
		change.TargetRoomTypeID = roomTypeID
		if state.Order != nil && state.Order.RoomTypeID != roomTypeID {
			upgrade = true
		}
	}
	if value := strings.TrimSpace(params.CheckoutAt); value != "" {
		checkout, err := parseSandboxCheckout(value, state.Order)
		if err != nil {
			return errors.New("请提供明确的延迟退房日期和时间。")
		}
		if change.CheckOut != nil && !change.CheckOut.Equal(checkout) {
			return errors.New("本轮提出了不同退房时间，请先确认一个时间。")
		}
		change.CheckOut, lateCheckout = &checkout, true
		if task.SandboxScene == "D" && params.RemedyCode == "" && len(params.Actions) == 0 {
			recovery = false
		}
	}
	if code := strings.TrimSpace(params.RemedyCode); code != "" {
		id := int64(0)
		for _, rule := range state.Rules {
			if rule.Enabled && rule.Code == code {
				id = rule.ID
			}
		}
		if id <= 0 {
			return errors.New("没有找到所选的有效测试补救规则，请明确可选方案。")
		}
		if change.RuleID > 0 && change.RuleID != id {
			return errors.New("本轮选择了不同补救规则，请先确认一项。")
		}
		change.RuleID = id
	}
	mergeActions()
	return nil
}

func parseSandboxCheckout(value string, order *sandbox.Order) (time.Time, error) {
	location := time.Local
	if order != nil {
		location = order.CheckOut.Location()
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed, nil
		}
	}
	if order != nil {
		if parsed, err := time.ParseInLocation("15:04", value, location); err == nil {
			return time.Date(order.CheckOut.Year(), order.CheckOut.Month(), order.CheckOut.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location), nil
		}
	}
	return time.Time{}, errors.New("invalid checkout time")
}

func setSandboxFixedReply(task *callbacks.ReplyTaskPlanTraceData, reply string) {
	reply = sandbox.CustomerReplyText(reply)
	task.FixedReply, task.AnswerText = true, &reply
	task.OutputKind, task.Output, task.ReplyRequired = "text", "text_reply", true
	task.NeedsKnowledge, task.NeedsTool, task.NeedsResource, task.NeedsHumanRoute = false, false, false, false
	task.SelectedLayer, task.SelectedCandidateIDs, task.SupportedFacts = "", nil, nil
}

func completeSandboxFixedReply(summary *RunResult, collector *callbacks.RuntimeTraceCollector) bool {
	if !config.PMSSandboxEnabled() || summary == nil || collector == nil {
		return false
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) == 0 {
		return false
	}
	for _, task := range plan.TaskPlans {
		if !task.FixedReply || !isReplyRequiredTextTask(task) {
			return false
		}
	}
	parts := make([]string, 0, len(plan.TaskPlans))
	for _, group := range buildTextReplyTaskGroups(plan) {
		content, err := renderLockedReplyContent(group)
		if err != nil {
			return false
		}
		parts = append(parts, content)
	}
	summary.Status = "completed"
	summary.ReplyText = composeGeneratedReplyContents(parts, 3)
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	collector.Data.Output.FinishReason = "sandbox_fixed_reply"
	collector.Data.Pipeline.Generate.Status = "skipped"
	collector.Data.Pipeline.Generate.Reason = "server executed all sandbox scene tasks and preserved fixed business results"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "fixed task results passed protocol and send-safety validation"
	summary.TraceData = collector.Marshal()
	return true
}

func sandboxRuntimeFailure(err error) string {
	if err != nil {
		for _, allowed := range []string{
			"当前没有可升级的测试房型", "当前没有符合方案的可用测试净房", "当前没有适用的测试升房规则",
			"当前没有适用的测试换房规则", "当前没有适用的测试延迟退房规则", "当前没有适用的测试补救承诺规则",
			"延迟退房只能使用当前有效规则允许的同日时间", "目标房间在整个入住区间内已被占用，请重新选择",
			"延迟后房间存在入住区间冲突，不能提交", "没有可办理的当前测试订单", "当前客户未绑定该测试订单",
			"该测试订单已超过离店时间，请先在后台维护有效测试订单",
		} {
			if err.Error() == allowed {
				return "【测试 PMS】" + allowed + "；未执行订单修改。"
			}
		}
	}
	return "【测试 PMS】这项查询或方案准备暂时未完成，请稍后重试；未执行订单修改。"
}

func recordSandboxTaskExecution(summary *RunResult, collector *callbacks.RuntimeTraceCollector, taskID, scene string, started time.Time, result *sandbox.SceneResult, err error) {
	if summary != nil {
		summary.ToolCodes = appendIfMissing(summary.ToolCodes, "pms_sandbox")
		summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, "pms_sandbox")
		summary.ToolCallCount++
	}
	status := "completed"
	if err != nil || result == nil || !result.Completed {
		status = "incomplete"
	}
	collector.Data.Pipeline.ToolKnowledge.ToolTriggered = true
	collector.Data.Pipeline.ToolKnowledge.ExpectedResources = appendIfMissing(
		collector.Data.Pipeline.ToolKnowledge.ExpectedResources,
		fmt.Sprintf("测试 PMS %s:%s:%s (%dms)", taskID, scene, status, time.Since(started).Milliseconds()),
	)
}
