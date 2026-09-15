package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func (s *pmsSandboxService) Save(ctx context.Context, storeID, operatorID int64, req sandbox.SaveRequest) (*sandbox.Snapshot, error) {
	if err := s.available(false); err != nil {
		return nil, err
	}
	count := 0
	for _, present := range []bool{req.RoomType != nil, req.Room != nil, req.Order != nil, req.Grade != nil, req.Member != nil, req.Rule != nil, req.Resource != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return nil, errors.New("每次只能编辑一项测试 PMS 数据")
	}
	err := s.transaction(ctx, storeID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		rows, err := repositories.PMSSandboxRepository.Load(tx, storeID, store.ActiveDatasetID, false)
		if err != nil {
			return errors.New("请先初始化测试数据")
		}
		if req.DatasetID != rows.Dataset.ID || req.Version != rows.Dataset.Version {
			return errors.New("测试数据已变化，请刷新后再编辑")
		}
		var item any
		var id int64
		datasetID := store.ActiveDatasetID
		switch {
		case req.RoomType != nil:
			v := req.RoomType
			if strings.TrimSpace(v.Name) == "" || v.Rank < 1 || v.PriceCents < 0 || v.PriceCents > 100000000 {
				return errors.New("房型名称、等级或价格不合法")
			}
			item = &models.PMSSandboxRoomType{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Name: strings.TrimSpace(v.Name), Rank: v.Rank, PriceCents: v.PriceCents, Enabled: v.Enabled}
			id = v.ID
		case req.Room != nil:
			v := req.Room
			if strings.TrimSpace(v.Number) == "" || findRoomType(rows, v.RoomTypeID) == nil {
				return errors.New("房号或当前门店房型不合法")
			}
			if v.CleanStatus != "clean" && v.CleanStatus != "dirty" && v.CleanStatus != "maintenance" {
				return errors.New("房间状态只能是 clean、dirty 或 maintenance")
			}
			if existing := findRoom(rows, v.ID); existing != nil && existing.RoomTypeID != v.RoomTypeID {
				for _, order := range rows.Orders {
					if order.RoomID == v.ID && orderOccupies(order.Status) {
						return errors.New("已占用房间不能直接修改房型，请通过订单办理")
					}
				}
			}
			item = &models.PMSSandboxRoom{ID: v.ID, StoreID: storeID, DatasetID: datasetID, RoomTypeID: v.RoomTypeID, Number: strings.TrimSpace(v.Number), Floor: strings.TrimSpace(v.Floor), CleanStatus: v.CleanStatus, Description: strings.TrimSpace(v.Description), Enabled: v.Enabled}
			id = v.ID
		case req.Order != nil:
			v := req.Order
			if strings.TrimSpace(v.Number) == "" || strings.TrimSpace(v.GuestName) == "" || !v.CheckOut.After(v.CheckIn) || v.CheckOut.After(v.CheckIn.AddDate(1, 0, 0)) || v.PayableCents < 0 || v.PayableCents > 100000000 {
				return errors.New("测试订单名称、日期区间或金额不合法")
			}
			if v.Status != "reserved" && v.Status != "checked_in" && v.Status != "checked_out" && v.Status != "cancelled" {
				return errors.New("不支持的测试订单状态")
			}
			rt := findRoomType(rows, v.RoomTypeID)
			if rt == nil || !rt.Enabled {
				return errors.New("房型不可用")
			}
			version := int64(1)
			old := findOrder(rows, v.ID)
			if v.ID > 0 && (old == nil || old.Version != v.Version) {
				return errors.New("订单版本已变化，请刷新后编辑")
			}
			if old != nil {
				version = old.Version + 1
			}
			if v.RoomID > 0 {
				if err := repositories.PMSSandboxRepository.LockRooms(tx, storeID, datasetID, []int64{v.RoomID}); err != nil {
					return err
				}
				room := findRoom(rows, v.RoomID)
				allowOccupiedDirty := old != nil && old.RoomID == v.RoomID
				if room == nil || room.RoomTypeID != v.RoomTypeID || !room.Enabled || (!allowOccupiedDirty && room.CleanStatus != "clean") {
					return errors.New("所选房间不属于该房型或当前不可分配")
				}
				if orderOccupies(v.Status) && roomConflicts(rows, v.RoomID, v.ID, v.CheckIn, v.CheckOut) {
					return errors.New("所选房间在该入住区间已被占用")
				}
			}
			item = &models.PMSSandboxOrder{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Number: strings.TrimSpace(v.Number), GuestName: strings.TrimSpace(v.GuestName), Phone: strings.TrimSpace(v.Phone), RoomTypeID: v.RoomTypeID, RoomID: v.RoomID, CheckIn: v.CheckIn, CheckOut: v.CheckOut, PayableCents: v.PayableCents, IncludesBreakfast: v.IncludesBreakfast, Status: v.Status, Version: version, UpdatedAt: s.now()}
			id = v.ID
		case req.Grade != nil:
			v := req.Grade
			if strings.TrimSpace(v.Name) == "" || v.FreeUpgradeMaxRank < 0 {
				return errors.New("会员等级名称或升级规则不合法")
			}
			benefits, _ := json.Marshal(v.Benefits)
			birthday, _ := json.Marshal(v.BirthdayBenefits)
			item = &models.PMSSandboxGrade{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Name: strings.TrimSpace(v.Name), BenefitsJSON: string(benefits), BirthdayJSON: string(birthday), FreeUpgradeMaxRank: v.FreeUpgradeMaxRank, Enabled: v.Enabled}
			id = v.ID
		case req.Member != nil:
			v := req.Member
			if strings.TrimSpace(v.Name) == "" || findGrade(rows, v.GradeID) == nil || v.ValidUntil.IsZero() {
				return errors.New("会员名称、等级或有效期不合法")
			}
			if v.Birthday != "" {
				if _, err := time.Parse("01-02", v.Birthday); err != nil {
					return errors.New("生日必须使用 MM-DD 格式")
				}
			}
			item = &models.PMSSandboxMember{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Name: strings.TrimSpace(v.Name), Phone: strings.TrimSpace(v.Phone), GradeID: v.GradeID, Birthday: v.Birthday, ValidUntil: v.ValidUntil, Enabled: v.Enabled}
			id = v.ID
		case req.Rule != nil:
			v := req.Rule
			if strings.TrimSpace(v.Code) == "" || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Text) == "" || v.AmountCents < 0 || v.AmountCents > 100000000 {
				return errors.New("规则编码、名称、说明或金额不合法")
			}
			switch v.Action {
			case "information", "upgrade", "room_change", "late_checkout", "commitment":
			default:
				return errors.New("不支持的规则办理类型")
			}
			if v.MinimumGradeID > 0 && findGrade(rows, v.MinimumGradeID) == nil {
				return errors.New("规则指定的会员等级不属于当前门店")
			}
			if v.Action == "late_checkout" {
				if _, err := time.Parse("15:04", v.CheckoutTime); err != nil {
					return errors.New("延迟退房时间必须使用 HH:mm 格式")
				}
			}
			if v.ValidFrom != nil && v.ValidUntil != nil && !v.ValidUntil.After(*v.ValidFrom) {
				return errors.New("规则有效时间区间不合法")
			}
			item = &models.PMSSandboxRule{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Code: strings.TrimSpace(v.Code), Name: strings.TrimSpace(v.Name), Text: strings.TrimSpace(v.Text), Action: v.Action, AmountCents: v.AmountCents, CheckoutTime: v.CheckoutTime, MinimumGradeID: v.MinimumGradeID, Enabled: v.Enabled, ValidFrom: v.ValidFrom, ValidUntil: v.ValidUntil}
			id = v.ID
		case req.Resource != nil:
			v := req.Resource
			if v.Code != "pillow" || strings.TrimSpace(v.Name) == "" {
				return errors.New("当前资源仅支持独立的 pillow 商品")
			}
			if v.CardPayload != "" && (!json.Valid([]byte(v.CardPayload)) || (v.MessageType != "mini_program" && v.MessageType != "shop_product") || v.SourceMessageID <= 0) {
				return errors.New("商品卡片必须来自真实消息，并具有有效类型和内容")
			}
			item = &models.PMSSandboxResource{ID: v.ID, StoreID: storeID, DatasetID: datasetID, Code: v.Code, Name: strings.TrimSpace(v.Name), Token: strings.TrimSpace(v.Token), CardPayload: v.CardPayload, MessageType: v.MessageType, SourceMessageID: v.SourceMessageID, Enabled: v.Enabled}
			id = v.ID
		}
		if err := repositories.PMSSandboxRepository.Save(tx, item, id, storeID, datasetID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("编辑对象不属于当前门店和数据批次")
			}
			return errors.New("测试数据保存失败，请检查是否存在重复编号")
		}
		if err := repositories.PMSSandboxRepository.BumpDataset(tx, storeID, datasetID); err != nil {
			return err
		}
		return s.adminAudit(tx, storeID, datasetID, operatorID, "admin_update", req)
	})
	if err != nil {
		return nil, err
	}
	return s.Snapshot(ctx, storeID)
}

func orderOccupies(status string) bool {
	return status == "reserved" || status == "checked_in"
}

func roomConflicts(rows *repositories.PMSSandboxRows, roomID, excludingID int64, start, end time.Time) bool {
	for _, other := range rows.Orders {
		if other.ID != excludingID && other.RoomID == roomID && orderOccupies(other.Status) && start.Before(other.CheckOut) && end.After(other.CheckIn) {
			return true
		}
	}
	return false
}

func sandboxOrder(rows *repositories.PMSSandboxRows, v *models.PMSSandboxOrder) sandbox.Order {
	result := sandbox.Order{ID: v.ID, Number: v.Number, GuestName: v.GuestName, Phone: v.Phone, RoomTypeID: v.RoomTypeID, RoomID: v.RoomID, CheckIn: v.CheckIn, CheckOut: v.CheckOut, PayableCents: v.PayableCents, IncludesBreakfast: v.IncludesBreakfast, Status: v.Status, Version: v.Version}
	if rt := findRoomType(rows, v.RoomTypeID); rt != nil {
		result.RoomTypeName = rt.Name
	}
	if room := findRoom(rows, v.RoomID); room != nil {
		result.RoomNumber = room.Number
	}
	return result
}

func sandboxRoomType(v models.PMSSandboxRoomType) sandbox.RoomType {
	return sandbox.RoomType{ID: v.ID, Name: v.Name, Rank: v.Rank, PriceCents: v.PriceCents, Enabled: v.Enabled}
}

func sandboxRoom(v models.PMSSandboxRoom) sandbox.Room {
	return sandbox.Room{ID: v.ID, RoomTypeID: v.RoomTypeID, Number: v.Number, Floor: v.Floor, CleanStatus: v.CleanStatus, Description: v.Description, Enabled: v.Enabled}
}

func sandboxMember(rows *repositories.PMSSandboxRows, v models.PMSSandboxMember) sandbox.Member {
	result := sandbox.Member{ID: v.ID, Name: v.Name, Phone: v.Phone, GradeID: v.GradeID, Birthday: v.Birthday, ValidUntil: v.ValidUntil, Enabled: v.Enabled}
	if grade := findGrade(rows, v.GradeID); grade != nil {
		result.GradeName = grade.Name
	}
	return result
}

func sandboxGrade(v models.PMSSandboxGrade) sandbox.Grade {
	result := sandbox.Grade{ID: v.ID, Name: v.Name, FreeUpgradeMaxRank: v.FreeUpgradeMaxRank, Enabled: v.Enabled, Benefits: []string{}, BirthdayBenefits: []string{}}
	_ = json.Unmarshal([]byte(v.BenefitsJSON), &result.Benefits)
	_ = json.Unmarshal([]byte(v.BirthdayJSON), &result.BirthdayBenefits)
	return result
}

func sandboxRule(v models.PMSSandboxRule) sandbox.Rule {
	return sandbox.Rule{ID: v.ID, Code: v.Code, Name: v.Name, Text: v.Text, Action: v.Action, AmountCents: v.AmountCents, CheckoutTime: v.CheckoutTime, MinimumGradeID: v.MinimumGradeID, Enabled: v.Enabled, ValidFrom: v.ValidFrom, ValidUntil: v.ValidUntil}
}

func sandboxResource(v models.PMSSandboxResource) sandbox.Resource {
	return sandbox.Resource{ID: v.ID, Code: v.Code, Name: v.Name, Token: v.Token, CardPayload: v.CardPayload, MessageType: v.MessageType, SourceMessageID: v.SourceMessageID, Enabled: v.Enabled}
}

func (s *pmsSandboxService) operation(db *gorm.DB, v *models.PMSOperation) *sandbox.Operation {
	result := &sandbox.Operation{ID: v.ID, Provider: v.Provider, StoreID: v.StoreID, DatasetID: v.DatasetID, ConversationID: v.ConversationID, SourceMessageID: v.SourceMessageID, PreviewMessageID: v.PreviewMessageID, ConfirmationMessageID: v.ConfirmationMessageID, OperatorID: v.OperatorID, OperationType: v.OperationType, Status: v.Status, PreviewText: v.PreviewText, ErrorMessage: v.ErrorMessage, ExpiresAt: v.ExpiresAt, CreatedAt: v.CreatedAt}
	if v.OperationType == "sandbox_change" {
		var plan sandbox.Plan
		if json.Unmarshal([]byte(v.RequestData), &plan) == nil {
			result.Plan = &plan
		}
		var outcome struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(v.ResultData), &outcome) == nil {
			result.ResultText = outcome.Text
		}
	}
	if v.PreviewMessageID > 0 {
		result.DeliveryStatus, _ = repositories.PMSSandboxRepository.DeliveryStatus(db, v.PreviewMessageID)
	}
	return result
}

func (s *pmsSandboxService) snapshot(db *gorm.DB, rows *repositories.PMSSandboxRows) *sandbox.Snapshot {
	result := &sandbox.Snapshot{Label: sandbox.Label, Dataset: &sandbox.Dataset{ID: rows.Dataset.ID, StoreID: rows.Dataset.StoreID, Version: rows.Dataset.Version, CreatedAt: rows.Dataset.CreatedAt}, RoomTypes: []sandbox.RoomType{}, Rooms: []sandbox.Room{}, Orders: []sandbox.Order{}, Grades: []sandbox.Grade{}, Members: []sandbox.Member{}, Rules: []sandbox.Rule{}, Resources: []sandbox.Resource{}, Bindings: []sandbox.Binding{}, Operations: []sandbox.Operation{}}
	for _, v := range rows.RoomTypes {
		result.RoomTypes = append(result.RoomTypes, sandboxRoomType(v))
	}
	for _, v := range rows.Rooms {
		result.Rooms = append(result.Rooms, sandboxRoom(v))
	}
	for i := range rows.Orders {
		result.Orders = append(result.Orders, sandboxOrder(rows, &rows.Orders[i]))
	}
	for _, v := range rows.Grades {
		result.Grades = append(result.Grades, sandboxGrade(v))
	}
	for _, v := range rows.Members {
		result.Members = append(result.Members, sandboxMember(rows, v))
	}
	for _, v := range rows.Rules {
		result.Rules = append(result.Rules, sandboxRule(v))
	}
	for _, v := range rows.Resources {
		result.Resources = append(result.Resources, sandboxResource(v))
	}
	for _, v := range rows.Bindings {
		result.Bindings = append(result.Bindings, sandbox.Binding{ID: v.ID, CustomerID: v.CustomerID, OrderID: v.OrderID, MemberID: v.MemberID})
	}
	for i := range rows.Operations {
		result.Operations = append(result.Operations, *s.operation(db, &rows.Operations[i]))
	}
	return result
}
