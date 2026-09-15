package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func (s *pmsSandboxService) Prepare(ctx context.Context, scope sandbox.Scope, req sandbox.ChangeRequest) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	var result *sandbox.Operation
	err := s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if err := s.validateScope(tx, scope, true); err != nil {
			return err
		}
		rows, err := repositories.PMSSandboxRepository.Load(tx, scope.StoreID, store.ActiveDatasetID, false)
		if err != nil {
			return errors.New("请先绑定测试订单")
		}
		order, _ := boundItems(rows, scope.CustomerID)
		if order == nil || (req.OrderID > 0 && req.OrderID != order.ID) {
			return errors.New("当前客户未绑定该测试订单")
		}
		req.OrderID = order.ID
		key := fmt.Sprintf("sandbox:%d:%d:%d:%d:%d", scope.StoreID, store.ActiveDatasetID, scope.ConversationID, scope.SourceMessageID, order.ID)
		existing, lookupErr := repositories.PMSSandboxRepository.OperationByKey(tx, key)
		if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		if existing != nil && existing.ID > 0 {
			if existing.Status != "pending" || existing.PreviewMessageID > 0 {
				result = s.operation(tx, existing)
				return nil
			}
			var previous sandbox.Plan
			if json.Unmarshal([]byte(existing.RequestData), &previous) != nil {
				return errors.New("现有测试办理方案损坏")
			}
			req = mergeChange(previous.Request, req)
		}
		plan, err := s.buildPlan(rows, scope.CustomerID, req)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		now := s.now()
		expires := now.Add(10 * time.Minute)
		preview := sandboxPreview(plan)
		if existing != nil && existing.ID > 0 {
			if err := repositories.PMSSandboxRepository.UpdateOperation(tx, existing.ID, map[string]any{"request_data": string(encoded), "preview_text": preview, "expires_at": expires}); err != nil {
				return err
			}
			existing.RequestData, existing.PreviewText, existing.ExpiresAt = string(encoded), preview, &expires
			result = s.operation(tx, existing)
			return nil
		}
		if err := repositories.PMSSandboxRepository.Supersede(tx, scope.StoreID, scope.ConversationID); err != nil {
			return err
		}
		item := &models.PMSOperation{Provider: sandbox.Provider, StoreID: scope.StoreID, DatasetID: rows.Dataset.ID, OrderID: order.ID, OrderVersion: order.Version, ConversationID: scope.ConversationID, SourceMessageID: scope.SourceMessageID, OperationType: "sandbox_change", Status: "pending", IdempotencyKey: key, RequestData: string(encoded), PreviewText: preview, ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
		if err := repositories.PMSSandboxRepository.Create(tx, item); err != nil {
			return err
		}
		result = s.operation(tx, item)
		return nil
	})
	return result, err
}

func mergeChange(old, current sandbox.ChangeRequest) sandbox.ChangeRequest {
	old.Upgrade = old.Upgrade || current.Upgrade
	old.ChangeRoom = old.ChangeRoom || current.ChangeRoom
	old.LateCheckout = old.LateCheckout || current.LateCheckout
	old.Recovery = old.Recovery || current.Recovery
	if current.TargetRoomTypeID > 0 {
		old.TargetRoomTypeID = current.TargetRoomTypeID
	}
	if current.TargetRoomID > 0 {
		old.TargetRoomID = current.TargetRoomID
	}
	if current.CheckOut != nil {
		old.CheckOut = current.CheckOut
	}
	if current.RuleID > 0 {
		old.RuleID = current.RuleID
	}
	if current.Reason != "" {
		old.Reason = current.Reason
	}
	return old
}

func (s *pmsSandboxService) buildPlan(rows *repositories.PMSSandboxRows, customerID int64, req sandbox.ChangeRequest) (*sandbox.Plan, error) {
	order, member := boundItems(rows, customerID)
	if order == nil || order.ID != req.OrderID || !orderOccupies(order.Status) {
		return nil, errors.New("没有可办理的当前测试订单")
	}
	if !req.Upgrade && !req.ChangeRoom && !req.LateCheckout && !req.Recovery {
		return nil, errors.New("请明确需要升房、换房、延迟退房还是补救方案")
	}
	if !s.now().Before(order.CheckOut) {
		return nil, errors.New("该测试订单已超过离店时间，请先在后台维护有效测试订单")
	}
	currentType := findRoomType(rows, order.RoomTypeID)
	if currentType == nil || !currentType.Enabled {
		return nil, errors.New("订单当前房型配置不可用")
	}
	before := sandboxOrder(rows, order)
	plan := &sandbox.Plan{Before: before, After: before, Request: req, DatasetVersion: rows.Dataset.Version}
	if member != nil {
		plan.MemberID = member.ID
	}
	targetType := currentType
	if req.TargetRoomTypeID > 0 {
		targetType = findRoomType(rows, req.TargetRoomTypeID)
		if targetType == nil || !targetType.Enabled {
			return nil, errors.New("目标房型不属于当前测试门店或已停用")
		}
	}
	if req.Upgrade {
		if s.activeRule(rows, 0, "upgrade", member) == nil {
			return nil, errors.New("当前没有适用的测试升房规则")
		}
		if req.TargetRoomTypeID == 0 {
			for _, available := range s.availability(rows, order.CheckIn, order.CheckOut, order.ID) {
				if available.RoomType.Rank > currentType.Rank && available.Available > 0 {
					targetType = findRoomType(rows, available.RoomType.ID)
					break
				}
			}
		}
		if targetType.Rank <= currentType.Rank {
			return nil, errors.New("当前没有可升级的测试房型")
		}
	}
	if req.ChangeRoom && s.activeRule(rows, 0, "room_change", member) == nil {
		return nil, errors.New("当前没有适用的测试换房规则")
	}
	if targetType.ID != currentType.ID && !req.Upgrade {
		return nil, errors.New("跨房型调整必须通过升房方案重新核对费用")
	}
	if req.LateCheckout {
		ruleID := int64(0)
		if req.RuleID > 0 && !req.Recovery {
			ruleID = req.RuleID
		}
		rule := s.activeRule(rows, ruleID, "late_checkout", member)
		if rule == nil {
			return nil, errors.New("当前没有适用的测试延迟退房规则")
		}
		clock, err := time.Parse("15:04", rule.CheckoutTime)
		if err != nil {
			return nil, errors.New("测试延迟退房规则的时间配置错误")
		}
		desired := time.Date(order.CheckOut.Year(), order.CheckOut.Month(), order.CheckOut.Day(), clock.Hour(), clock.Minute(), 0, 0, order.CheckOut.Location())
		if req.CheckOut != nil {
			desired = *req.CheckOut
		}
		limit := time.Date(order.CheckOut.Year(), order.CheckOut.Month(), order.CheckOut.Day(), clock.Hour(), clock.Minute(), 0, 0, order.CheckOut.Location())
		if !desired.After(order.CheckOut) || desired.After(limit) {
			return nil, errors.New("延迟退房只能使用当前有效规则允许的同日时间")
		}
		plan.After.CheckOut = desired
		plan.AddedCents += rule.AmountCents
	}
	if req.Upgrade || req.ChangeRoom {
		selected := (*models.PMSSandboxRoom)(nil)
		if req.TargetRoomID > 0 {
			selected = findRoom(rows, req.TargetRoomID)
		} else {
			for i := range rows.Rooms {
				room := &rows.Rooms[i]
				if room.ID != order.RoomID && room.RoomTypeID == targetType.ID && room.Enabled && room.CleanStatus == "clean" && !roomConflicts(rows, room.ID, order.ID, order.CheckIn, plan.After.CheckOut) {
					selected = room
					break
				}
			}
		}
		if selected == nil || !selected.Enabled || selected.CleanStatus != "clean" || selected.RoomTypeID != targetType.ID || selected.ID == order.RoomID {
			return nil, errors.New("当前没有符合方案的可用测试净房")
		}
		if roomConflicts(rows, selected.ID, order.ID, order.CheckIn, plan.After.CheckOut) {
			return nil, errors.New("目标房间在整个入住区间内已被占用，请重新选择")
		}
		plan.After.RoomID, plan.After.RoomNumber = selected.ID, selected.Number
		plan.After.RoomTypeID, plan.After.RoomTypeName = targetType.ID, targetType.Name
		plan.RoomDescription = selected.Description
		if targetType.ID != currentType.ID {
			free := false
			if member != nil && member.Enabled && s.now().Before(member.ValidUntil) {
				grade := findGrade(rows, member.GradeID)
				free = grade != nil && grade.Enabled && grade.FreeUpgradeMaxRank >= targetType.Rank
			}
			if !free {
				difference := targetType.PriceCents - currentType.PriceCents
				if difference < 0 {
					return nil, errors.New("目标房型价格配置不一致，无法生成升房报价")
				}
				start := order.CheckIn
				if s.now().After(start) {
					start = s.now()
				}
				// Count remaining hotel nights using local dates rather than DST-sensitive hours.
				nights := 0
				for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()); day.Before(time.Date(order.CheckOut.Year(), order.CheckOut.Month(), order.CheckOut.Day(), 0, 0, 0, 0, order.CheckOut.Location())); day = day.AddDate(0, 0, 1) {
					nights++
				}
				if nights < 1 {
					nights = 1
				}
				plan.AddedCents += int64(nights) * difference
			}
		}
	}
	if plan.After.RoomID > 0 && roomConflicts(rows, plan.After.RoomID, order.ID, plan.After.CheckIn, plan.After.CheckOut) {
		return nil, errors.New("延迟后房间存在入住区间冲突，不能提交")
	}
	if req.Recovery {
		rule := s.activeRule(rows, req.RuleID, "commitment", member)
		if rule == nil {
			return nil, errors.New("当前没有适用的测试补救承诺规则")
		}
		plan.Commitment = rule.Text
	}
	plan.After.PayableCents += plan.AddedCents
	plan.After.Version++
	if plan.After.PayableCents > 100000000 {
		return nil, errors.New("测试方案金额超过允许范围")
	}
	plan.Request.TargetRoomTypeID = plan.After.RoomTypeID
	if req.Upgrade || req.ChangeRoom {
		plan.Request.TargetRoomID = plan.After.RoomID
	}
	return plan, nil
}

func sandboxMoney(cents int64) string {
	return fmt.Sprintf("%d.%02d元", cents/100, cents%100)
}

func sandboxPreview(plan *sandbox.Plan) string {
	parts := []string{fmt.Sprintf("【测试 PMS】办理预览：订单%s，%s/%s", plan.Before.Number, plan.Before.RoomTypeName, plan.Before.RoomNumber)}
	if plan.Before.RoomID != plan.After.RoomID || plan.Before.RoomTypeID != plan.After.RoomTypeID {
		parts = append(parts, fmt.Sprintf("调整为%s/%s", plan.After.RoomTypeName, plan.After.RoomNumber))
		if plan.RoomDescription != "" {
			parts = append(parts, "房间说明："+plan.RoomDescription)
		}
	}
	if !plan.Before.CheckOut.Equal(plan.After.CheckOut) {
		parts = append(parts, "退房时间调整为"+plan.After.CheckOut.Format("2006-01-02 15:04"))
	}
	parts = append(parts, "补差价"+sandboxMoney(plan.AddedCents)+"，测试订单应付"+sandboxMoney(plan.After.PayableCents)+"，不会真实扣款")
	if plan.Commitment != "" {
		parts = append(parts, "记录补救承诺："+plan.Commitment)
	}
	parts = append(parts, "方案10分钟内有效，确认后仅修改测试 PMS 数据。请回复“确认办理”或“取消”。")
	return strings.Join(parts, "；")
}

func (s *pmsSandboxService) Pending(ctx context.Context, scope sandbox.Scope) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	db := sqls.DB().WithContext(ctx)
	if err := s.validateScope(db, scope, false); err != nil {
		return nil, err
	}
	item, err := repositories.PMSSandboxRepository.Pending(db, scope.StoreID, scope.ConversationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	store, err := repositories.PMSSandboxRepository.Store(db, scope.StoreID)
	if err != nil || !operationInScope(item, scope, store.ActiveDatasetID) {
		return nil, nil
	}
	return s.operation(db, item), nil
}

func (s *pmsSandboxService) Latest(ctx context.Context, scope sandbox.Scope) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	db := sqls.DB().WithContext(ctx)
	if err := s.validateScope(db, scope, false); err != nil {
		return nil, err
	}
	store, err := repositories.PMSSandboxRepository.Store(db, scope.StoreID)
	if err != nil {
		return nil, err
	}
	item, err := repositories.PMSSandboxRepository.Latest(db, scope.StoreID, store.ActiveDatasetID, scope.ConversationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.operation(db, item), nil
}

func (s *pmsSandboxService) Result(ctx context.Context, scope sandbox.Scope, operationID int64) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	db := sqls.DB().WithContext(ctx)
	if err := s.validateScope(db, scope, false); err != nil {
		return nil, err
	}
	store, err := repositories.PMSSandboxRepository.Store(db, scope.StoreID)
	if err != nil {
		return nil, err
	}
	item, err := repositories.PMSSandboxRepository.Operation(db, operationID)
	if err != nil || !operationInScope(item, scope, store.ActiveDatasetID) {
		return nil, errors.New("办理记录不属于当前测试会话")
	}
	return s.operation(db, item), nil
}

func (s *pmsSandboxService) MarkPreviewMessage(ctx context.Context, scope sandbox.Scope, operationID, messageID int64) error {
	if err := s.available(true); err != nil {
		return err
	}
	return s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if err := s.validateScope(tx, scope, false); err != nil {
			return err
		}
		item, err := repositories.PMSSandboxRepository.Operation(tx, operationID)
		if err != nil || !operationInScope(item, scope, store.ActiveDatasetID) || item.Status != "pending" {
			return errors.New("测试办理方案不存在或不属于当前会话")
		}
		if item.PreviewMessageID != 0 && item.PreviewMessageID != messageID {
			return errors.New("测试办理方案已绑定另一条预览消息")
		}
		message, err := repositories.PMSSandboxRepository.Message(tx, messageID)
		if err != nil || message.ConversationID != scope.ConversationID || message.SenderType != enums.IMSenderTypeAI ||
			message.ID <= item.SourceMessageID || !strings.Contains(message.Content, item.PreviewText) {
			return errors.New("预览消息必须为当前方案实际提交的完整 AI 回复")
		}
		return repositories.PMSSandboxRepository.UpdateOperation(tx, item.ID, map[string]any{"preview_message_id": messageID})
	})
}

func operationInScope(item *models.PMSOperation, scope sandbox.Scope, datasetID int64) bool {
	return item != nil && item.Provider == sandbox.Provider && item.OperationType == "sandbox_change" &&
		item.StoreID == scope.StoreID && item.ConversationID == scope.ConversationID && item.DatasetID == datasetID
}

func (s *pmsSandboxService) Confirm(ctx context.Context, scope sandbox.Scope, operationID int64) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	if !config.Current().PMS.AllowWrite {
		return nil, errors.New("测试 PMS 自动办理开关尚未开启，订单未修改")
	}
	var result *sandbox.Operation
	var stateErr error
	err := s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if err := s.validateScope(tx, scope, true); err != nil {
			return err
		}
		item, err := repositories.PMSSandboxRepository.Operation(tx, operationID)
		if err != nil || !operationInScope(item, scope, store.ActiveDatasetID) {
			return errors.New("办理方案不属于当前会话、门店或测试数据批次")
		}
		if item.Status == "completed" {
			result = s.operation(tx, item)
			return nil
		}
		if item.Status != "pending" {
			return errors.New("该方案已取消或失效，请重新提出办理请求")
		}
		if item.ExpiresAt == nil || !s.now().Before(*item.ExpiresAt) {
			stateErr = errors.New("测试办理方案已过期，请重新生成")
			return repositories.PMSSandboxRepository.UpdateOperation(tx, item.ID, map[string]any{"status": "expired", "error_message": stateErr.Error()})
		}
		if err := s.validateConfirmation(tx, scope, item); err != nil {
			return err
		}
		rows, err := repositories.PMSSandboxRepository.Load(tx, scope.StoreID, store.ActiveDatasetID, false)
		if err != nil {
			return err
		}
		var plan sandbox.Plan
		if json.Unmarshal([]byte(item.RequestData), &plan) != nil || plan.Before.ID != item.OrderID || plan.Before.Version != item.OrderVersion {
			return errors.New("测试办理方案结构不完整")
		}
		current, _ := boundItems(rows, scope.CustomerID)
		if current == nil || current.ID != item.OrderID {
			return errors.New("当前客户绑定订单已变化，请重新确认方案")
		}
		if current.Version != item.OrderVersion || rows.Dataset.Version != plan.DatasetVersion {
			stateErr = errors.New("订单、价格或规则已变化，请重新生成并确认方案")
			return repositories.PMSSandboxRepository.UpdateOperation(tx, item.ID, map[string]any{"status": "superseded", "error_message": stateErr.Error()})
		}
		ids := []int64{plan.Before.RoomID, plan.After.RoomID}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err := repositories.PMSSandboxRepository.LockRooms(tx, scope.StoreID, store.ActiveDatasetID, ids); err != nil {
			return err
		}
		rechecked, err := s.buildPlan(rows, scope.CustomerID, plan.Request)
		if err != nil {
			return err
		}
		if !samePlan(*rechecked, plan) {
			return errors.New("方案内容或报价已变化，请重新生成并确认")
		}
		updated := *current
		updated.RoomTypeID, updated.RoomID = plan.After.RoomTypeID, plan.After.RoomID
		updated.CheckOut, updated.PayableCents = plan.After.CheckOut, plan.After.PayableCents
		if err := repositories.PMSSandboxRepository.UpdateOrder(tx, &updated, current.Version); err != nil {
			return err
		}
		readback, err := repositories.PMSSandboxRepository.ReadOrder(tx, scope.StoreID, store.ActiveDatasetID, current.ID)
		if err != nil {
			return errors.New("测试订单结果回查失败，本次事务未提交")
		}
		actual := sandboxOrder(rows, readback)
		if !sameOrder(actual, plan.After) {
			return errors.New("测试订单回查与办理方案不一致，本次事务未提交")
		}
		if plan.Commitment != "" {
			payload, _ := json.Marshal(map[string]any{"provider": sandbox.Provider, "datasetId": store.ActiveDatasetID, "operationId": item.ID, "commitment": plan.Commitment, "testOnly": true, "financialExecuted": false})
			recovery := &models.ServiceRecoveryCase{ConversationID: scope.ConversationID, CustomerID: scope.CustomerID, StoreID: scope.StoreID, RecoveryType: "sandbox_commitment", Status: RecoveryStatusApproved, ApprovalStatus: "customer_confirmed", IdempotencyKey: fmt.Sprintf("sandbox:operation:%d:commitment", item.ID), Reason: plan.Request.Reason, Payload: string(payload), RequestedAt: s.now()}
			if err := repositories.ServiceRecoveryCaseRepository.Create(tx, recovery); err != nil {
				return err
			}
		}
		text := fmt.Sprintf("【测试 PMS】已核对并完成测试订单%s办理：%s/%s，退房时间%s，应付%s。仅测试数据已更新，没有真实扣款。", actual.Number, actual.RoomTypeName, actual.RoomNumber, actual.CheckOut.Format("2006-01-02 15:04"), sandboxMoney(actual.PayableCents))
		if plan.Commitment != "" {
			text += "补救承诺已记录，不代表已退款、付款或发券。"
		}
		outcome, _ := json.Marshal(map[string]any{"order": actual, "text": text, "readbackVerified": true})
		if err := repositories.PMSSandboxRepository.UpdateOperation(tx, item.ID, map[string]any{"status": "completed", "confirmation_message_id": scope.SourceMessageID, "result_data": string(outcome)}); err != nil {
			return err
		}
		item.Status, item.ConfirmationMessageID, item.ResultData = "completed", scope.SourceMessageID, string(outcome)
		result = s.operation(tx, item)
		return nil
	})
	if err == nil && stateErr != nil {
		err = stateErr
	}
	return result, err
}

func sameOrder(a, b sandbox.Order) bool {
	if !a.CheckIn.Equal(b.CheckIn) || !a.CheckOut.Equal(b.CheckOut) {
		return false
	}
	a.CheckIn, a.CheckOut, b.CheckIn, b.CheckOut = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func samePlan(a, b sandbox.Plan) bool {
	if !sameOrder(a.Before, b.Before) || !sameOrder(a.After, b.After) {
		return false
	}
	a.Before, a.After, b.Before, b.After = sandbox.Order{}, sandbox.Order{}, sandbox.Order{}, sandbox.Order{}
	if a.Request.CheckOut != nil || b.Request.CheckOut != nil {
		if a.Request.CheckOut == nil || b.Request.CheckOut == nil || !a.Request.CheckOut.Equal(*b.Request.CheckOut) {
			return false
		}
		a.Request.CheckOut, b.Request.CheckOut = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

func (s *pmsSandboxService) validateConfirmation(db *gorm.DB, scope sandbox.Scope, operation *models.PMSOperation) error {
	if operation.PreviewMessageID <= 0 {
		return errors.New("方案尚未发送给客户，不能确认执行")
	}
	preview, err := repositories.PMSSandboxRepository.Message(db, operation.PreviewMessageID)
	if err != nil || preview.ConversationID != scope.ConversationID || preview.SenderType != enums.IMSenderTypeAI || !strings.Contains(preview.Content, operation.PreviewText) {
		return errors.New("未找到完整的已发送办理预览")
	}
	source, err := repositories.PMSSandboxRepository.Message(db, scope.SourceMessageID)
	if err != nil || source.ID <= preview.ID || source.SeqNo <= preview.SeqNo {
		return errors.New("历史确认消息不能执行当前方案")
	}
	status, sentAt, err := repositories.PMSSandboxRepository.PreviewDelivery(db, preview.ID)
	if err != nil {
		return errors.New("无法确认预览是否投递")
	}
	conversation, err := repositories.PMSSandboxRepository.Conversation(db, scope.ConversationID)
	if err != nil || (status != "sent" && !(conversation.ChannelID == 0 && status == "" && preview.SendStatus == enums.IMMessageStatusSent)) {
		return errors.New("办理预览尚未实际送达，不能执行")
	}
	if status == "sent" && (sentAt == nil || source.CreatedAt.Before(*sentAt)) {
		return errors.New("确认消息早于实际预览投递时间，不能执行")
	}
	if status == "" && preview.SentAt != nil && source.CreatedAt.Before(*preview.SentAt) {
		return errors.New("确认消息早于预览投递时间，不能执行")
	}
	intervening, err := repositories.PMSSandboxRepository.InterveningCustomerMessages(db, scope.ConversationID, preview.SeqNo, source.SeqNo)
	if err != nil || intervening {
		return errors.New("预览之后客户消息已变化，请重新生成方案再确认")
	}
	return nil
}

func (s *pmsSandboxService) Cancel(ctx context.Context, scope sandbox.Scope, operationID int64) (*sandbox.Operation, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	var result *sandbox.Operation
	err := s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		// The dashboard permission wrapper cancels without a customer message.
		// Customer requests retain source and history validation.
		if err := s.validateScope(tx, scope, scope.SourceMessageID > 0); err != nil {
			return err
		}
		item, err := repositories.PMSSandboxRepository.Operation(tx, operationID)
		if err != nil || !operationInScope(item, scope, store.ActiveDatasetID) {
			return errors.New("办理方案不属于当前会话或测试数据批次")
		}
		if item.Status == "completed" {
			return errors.New("测试办理已完成，取消不会撤销已完成订单")
		}
		if scope.SourceMessageID > 0 && scope.SourceMessageID <= item.SourceMessageID {
			return errors.New("历史消息不能取消当前方案")
		}
		if item.Status == "pending" {
			outcome, _ := json.Marshal(map[string]any{"text": "【测试 PMS】已取消该办理方案，订单未因取消操作发生变化。"})
			if err := repositories.PMSSandboxRepository.UpdateOperation(tx, item.ID, map[string]any{"status": "cancelled", "result_data": string(outcome)}); err != nil {
				return err
			}
			item.Status, item.ResultData = "cancelled", string(outcome)
		}
		result = s.operation(tx, item)
		return nil
	})
	return result, err
}
