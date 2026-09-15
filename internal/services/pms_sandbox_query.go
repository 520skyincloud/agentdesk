package services

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func (s *pmsSandboxService) validateScope(db *gorm.DB, scope sandbox.Scope, needSource bool) error {
	if scope.StoreID <= 0 || scope.ConversationID <= 0 || scope.CustomerID <= 0 {
		return errors.New("测试 PMS 缺少当前门店、会话或客户")
	}
	conversation, err := repositories.PMSSandboxRepository.Conversation(db, scope.ConversationID)
	if err != nil || conversation.CustomerID != scope.CustomerID {
		return errors.New("客户与当前会话不匹配")
	}
	storeID, err := repositories.PMSSandboxRepository.ConversationStore(db, scope.ConversationID)
	if err != nil || storeID != scope.StoreID {
		return errors.New("当前会话不属于指定测试门店")
	}
	if needSource {
		message, err := repositories.PMSSandboxRepository.Message(db, scope.SourceMessageID)
		if err != nil || message.ConversationID != scope.ConversationID || message.SenderType != enums.IMSenderTypeCustomer {
			return errors.New("办理请求缺少真实的当前客户消息")
		}
	}
	return nil
}

func boundItems(rows *repositories.PMSSandboxRows, customerID int64) (*models.PMSSandboxOrder, *models.PMSSandboxMember) {
	for _, binding := range rows.Bindings {
		if binding.CustomerID == customerID {
			return findOrder(rows, binding.OrderID), findMember(rows, binding.MemberID)
		}
	}
	return nil, nil
}

func (s *pmsSandboxService) Query(ctx context.Context, scope sandbox.Scope) (*sandbox.CustomerState, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	db := sqls.DB().WithContext(ctx)
	if err := s.validateScope(db, scope, false); err != nil {
		return nil, err
	}
	rows, err := s.rows(db, scope.StoreID, false)
	if err != nil {
		return nil, err
	}
	return s.customerState(rows, scope.CustomerID), nil
}

func (s *pmsSandboxService) customerState(rows *repositories.PMSSandboxRows, customerID int64) *sandbox.CustomerState {
	result := &sandbox.CustomerState{Label: sandbox.Label, Dataset: sandbox.Dataset{ID: rows.Dataset.ID, StoreID: rows.Dataset.StoreID, Version: rows.Dataset.Version, CreatedAt: rows.Dataset.CreatedAt}, Rules: []sandbox.Rule{}, Availability: []sandbox.RoomAvailability{}}
	order, member := boundItems(rows, customerID)
	if order != nil {
		value := sandboxOrder(rows, order)
		value.Phone = ""
		value.GuestName = ""
		result.Order = &value
		result.Availability = s.availability(rows, order.CheckIn, order.CheckOut, 0)
	}
	if member != nil {
		value := sandboxMember(rows, *member)
		value.Phone = ""
		result.Member = &value
		if grade := findGrade(rows, member.GradeID); grade != nil && grade.Enabled && member.Enabled && s.now().Before(member.ValidUntil) {
			value := sandboxGrade(*grade)
			result.Grade = &value
		}
	}
	for _, rule := range rows.Rules {
		if s.ruleApplies(&rule, member) {
			result.Rules = append(result.Rules, sandboxRule(rule))
		}
	}
	for _, resource := range rows.Resources {
		if resource.Code == "pillow" && resource.Enabled {
			value := sandboxResource(resource)
			result.Resource = &value
		}
	}
	return result
}

func (s *pmsSandboxService) availability(rows *repositories.PMSSandboxRows, start, end time.Time, orderID int64) []sandbox.RoomAvailability {
	result := []sandbox.RoomAvailability{}
	types := append([]models.PMSSandboxRoomType{}, rows.RoomTypes...)
	sort.SliceStable(types, func(i, j int) bool {
		if types[i].Rank == types[j].Rank {
			return types[i].ID < types[j].ID
		}
		return types[i].Rank < types[j].Rank
	})
	for _, roomType := range types {
		if !roomType.Enabled {
			continue
		}
		item := sandbox.RoomAvailability{RoomType: sandboxRoomType(roomType), Rooms: []sandbox.Room{}}
		for _, room := range rows.Rooms {
			if room.Enabled && room.CleanStatus == "clean" && room.RoomTypeID == roomType.ID && !roomConflicts(rows, room.ID, orderID, start, end) {
				item.Rooms = append(item.Rooms, sandboxRoom(room))
			}
		}
		item.Available = len(item.Rooms)
		result = append(result, item)
	}
	return result
}

func (s *pmsSandboxService) locate(ctx context.Context, scope sandbox.Scope, phone, number string) error {
	phone, number = strings.TrimSpace(phone), strings.TrimSpace(number)
	if phone == "" && number == "" {
		return nil
	}
	return s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if err := s.validateScope(tx, scope, true); err != nil {
			return err
		}
		rows, err := repositories.PMSSandboxRepository.Load(tx, scope.StoreID, store.ActiveDatasetID, false)
		if err != nil {
			return errors.New("当前门店没有测试订单")
		}
		current, _ := boundItems(rows, scope.CustomerID)
		source, err := repositories.PMSSandboxRepository.Message(tx, scope.SourceMessageID)
		if err != nil {
			return err
		}
		// Supplied identifiers must be present in this message or already bound
		// to this customer; the model cannot choose an arbitrary test booking.
		if phone != "" && !strings.Contains(source.Content, phone) && (current == nil || current.Phone != phone) {
			return errors.New("请在当前会话提供需要查询的测试手机号")
		}
		if number != "" && !strings.Contains(source.Content, number) && (current == nil || current.Number != number) {
			return errors.New("请在当前会话提供需要查询的测试订单号")
		}
		var matches []*models.PMSSandboxOrder
		for i := range rows.Orders {
			order := &rows.Orders[i]
			if orderOccupies(order.Status) && (phone == "" || order.Phone == phone) && (number == "" || order.Number == number) {
				matches = append(matches, order)
			}
		}
		if len(matches) == 0 {
			return errors.New("测试 PMS 未查到符合当前查询条件的订单")
		}
		if len(matches) > 1 {
			return errors.New("测试 PMS 查到多个订单，请补充测试订单号")
		}
		match := matches[0]
		memberID := int64(0)
		for _, member := range rows.Members {
			if member.Phone == match.Phone {
				if memberID > 0 {
					memberID = 0
					break
				}
				memberID = member.ID
			}
		}
		binding := &models.PMSSandboxBinding{StoreID: scope.StoreID, DatasetID: store.ActiveDatasetID, CustomerID: scope.CustomerID, OrderID: match.ID, MemberID: memberID}
		for _, item := range rows.Bindings {
			if item.CustomerID == scope.CustomerID {
				binding.ID = item.ID
			}
		}
		return repositories.PMSSandboxRepository.Save(tx, binding, binding.ID, scope.StoreID, store.ActiveDatasetID)
	})
}

func (s *pmsSandboxService) locateMember(ctx context.Context, scope sandbox.Scope, phone string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return nil
	}
	return s.transaction(ctx, scope.StoreID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if err := s.validateScope(tx, scope, true); err != nil {
			return err
		}
		rows, err := repositories.PMSSandboxRepository.Load(tx, scope.StoreID, store.ActiveDatasetID, false)
		if err != nil {
			return errors.New("当前门店没有测试会员数据")
		}
		order, existing := boundItems(rows, scope.CustomerID)
		source, err := repositories.PMSSandboxRepository.Message(tx, scope.SourceMessageID)
		if err != nil {
			return err
		}
		if !strings.Contains(source.Content, phone) && (existing == nil || existing.Phone != phone) {
			return errors.New("请在当前会话提供需要查询的测试会员手机号")
		}
		var match *models.PMSSandboxMember
		for i := range rows.Members {
			if rows.Members[i].Phone == phone {
				if match != nil {
					return errors.New("测试 PMS 查到多个同手机号会员，请先在后台明确绑定")
				}
				match = &rows.Members[i]
			}
		}
		if match == nil {
			return errors.New("测试 PMS 未查到该手机号对应的会员")
		}
		binding := &models.PMSSandboxBinding{StoreID: scope.StoreID, DatasetID: store.ActiveDatasetID, CustomerID: scope.CustomerID, MemberID: match.ID}
		if order != nil && order.Phone == phone {
			binding.OrderID = order.ID
		}
		for _, old := range rows.Bindings {
			if old.CustomerID == scope.CustomerID {
				binding.ID = old.ID
			}
		}
		return repositories.PMSSandboxRepository.Save(tx, binding, binding.ID, scope.StoreID, store.ActiveDatasetID)
	})
}
