package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type pmsSandboxService struct {
	now func() time.Time
}

var PMSSandboxService = &pmsSandboxService{now: time.Now}

var _ sandbox.Service = PMSSandboxService

var sandboxStoreLocks sync.Map

func sandboxLock(storeID int64) func() {
	value, _ := sandboxStoreLocks.LoadOrStore(storeID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (s *pmsSandboxService) available(runtime bool) error {
	if !config.PMSSandboxAvailable() || (runtime && !config.PMSSandboxEnabled()) {
		return errors.New("测试 PMS 当前未启用")
	}
	if sqls.DB() == nil {
		return errors.New("测试 PMS 数据服务不可用")
	}
	return nil
}

func (s *pmsSandboxService) transaction(ctx context.Context, storeID int64, fn func(*gorm.DB, *models.PMSSandboxStore) error) error {
	if storeID <= 0 {
		return errors.New("必须指定当前门店")
	}
	unlock := sandboxLock(storeID)
	defer unlock()
	return sqls.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		store, err := repositories.PMSSandboxRepository.LockStore(tx, storeID)
		if err != nil {
			return errors.New("测试 PMS 门店不可用")
		}
		return fn(tx, store)
	})
}

func (s *pmsSandboxService) rows(db *gorm.DB, storeID int64, history bool) (*repositories.PMSSandboxRows, error) {
	store, err := repositories.PMSSandboxRepository.Store(db, storeID)
	if err != nil || store.ActiveDatasetID <= 0 {
		return nil, errors.New("当前门店尚未初始化测试 PMS 数据")
	}
	return repositories.PMSSandboxRepository.Load(db, storeID, store.ActiveDatasetID, history)
}

func (s *pmsSandboxService) Snapshot(ctx context.Context, storeID int64) (*sandbox.Snapshot, error) {
	if err := s.available(false); err != nil {
		return nil, err
	}
	rows, err := s.rows(sqls.DB().WithContext(ctx), storeID, true)
	if err != nil {
		if _, lookupErr := repositories.PMSSandboxRepository.Store(sqls.DB().WithContext(ctx), storeID); errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return &sandbox.Snapshot{Label: sandbox.Label, RoomTypes: []sandbox.RoomType{}, Rooms: []sandbox.Room{}, Orders: []sandbox.Order{}, Grades: []sandbox.Grade{}, Members: []sandbox.Member{}, Rules: []sandbox.Rule{}, Resources: []sandbox.Resource{}, Bindings: []sandbox.Binding{}, Operations: []sandbox.Operation{}}, nil
		}
		return nil, err
	}
	return s.snapshot(sqls.DB().WithContext(ctx), rows), nil
}

func (s *pmsSandboxService) Initialize(ctx context.Context, storeID, operatorID int64, reset bool) (*sandbox.Snapshot, error) {
	if reset {
		return nil, errors.New("重置必须提交当前数据批次和版本")
	}
	return s.initialize(ctx, storeID, operatorID, false, 0, 0)
}

func (s *pmsSandboxService) Reset(ctx context.Context, storeID, operatorID, datasetID, version int64) (*sandbox.Snapshot, error) {
	return s.initialize(ctx, storeID, operatorID, true, datasetID, version)
}

func (s *pmsSandboxService) initialize(ctx context.Context, storeID, operatorID int64, reset bool, datasetID, version int64) (*sandbox.Snapshot, error) {
	if err := s.available(false); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, storeID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		if store.ActiveDatasetID > 0 && !reset {
			return nil
		}
		if reset {
			current, err := repositories.PMSSandboxRepository.Load(tx, storeID, store.ActiveDatasetID, false)
			if err != nil || datasetID != current.Dataset.ID || version != current.Dataset.Version {
				return errors.New("测试数据已变化，请刷新后再重置")
			}
		}
		previous := store.ActiveDatasetID
		if previous > 0 {
			if err := repositories.PMSSandboxRepository.InvalidateDataset(tx, storeID, previous); err != nil {
				return err
			}
		}
		dataset := &models.PMSSandboxDataset{StoreID: storeID, Version: 1, CreatedAt: s.now()}
		if err := repositories.PMSSandboxRepository.Create(tx, dataset); err != nil {
			return err
		}
		if err := s.seed(tx, storeID, dataset.ID); err != nil {
			return err
		}
		if err := repositories.PMSSandboxRepository.SetDataset(tx, storeID, dataset.ID); err != nil {
			return err
		}
		return s.adminAudit(tx, storeID, dataset.ID, operatorID, "dataset_reset", map[string]any{"previousDatasetId": previous, "datasetId": dataset.ID})
	})
	if err != nil {
		return nil, err
	}
	return s.Snapshot(ctx, storeID)
}

func (s *pmsSandboxService) seed(tx *gorm.DB, storeID, datasetID int64) error {
	create := func(value any) error { return repositories.PMSSandboxRepository.Create(tx, value) }
	standard := &models.PMSSandboxRoomType{StoreID: storeID, DatasetID: datasetID, Name: "测试舒适大床房", Rank: 1, PriceCents: 28000, Enabled: true}
	deluxe := &models.PMSSandboxRoomType{StoreID: storeID, DatasetID: datasetID, Name: "测试高级大床房", Rank: 2, PriceCents: 38000, Enabled: true}
	for _, value := range []*models.PMSSandboxRoomType{standard, deluxe} {
		if err := create(value); err != nil {
			return err
		}
	}
	rooms := []*models.PMSSandboxRoom{
		{StoreID: storeID, DatasetID: datasetID, RoomTypeID: standard.ID, Number: "T101", Floor: "1", CleanStatus: "clean", Description: "测试房间，未配置安静或临街属性", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, RoomTypeID: standard.ID, Number: "T102", Floor: "1", CleanStatus: "clean", Description: "测试配置：远离电梯，房间说明为相对安静；不承诺无噪音", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, RoomTypeID: deluxe.ID, Number: "T201", Floor: "2", CleanStatus: "clean", Description: "测试高级房，可按库存办理升级", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, RoomTypeID: deluxe.ID, Number: "T202", Floor: "2", CleanStatus: "dirty", Description: "测试脏房，清洁完成前不可分配", Enabled: true},
	}
	for _, room := range rooms {
		if err := create(room); err != nil {
			return err
		}
	}
	now := s.now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 14, 0, 0, 0, now.Location())
	order := &models.PMSSandboxOrder{StoreID: storeID, DatasetID: datasetID, Number: fmt.Sprintf("TEST-%d-001", datasetID), GuestName: "演示住客", Phone: "TEST001", RoomTypeID: standard.ID, RoomID: rooms[0].ID, CheckIn: start, CheckOut: start.AddDate(0, 0, 2).Add(-2 * time.Hour), PayableCents: 56000, IncludesBreakfast: true, Status: "checked_in", Version: 1, UpdatedAt: now}
	if err := create(order); err != nil {
		return err
	}
	grade := &models.PMSSandboxGrade{StoreID: storeID, DatasetID: datasetID, Name: "测试金卡会员", BenefitsJSON: `["有效住宿期间可按可用库存免费升级至测试高级大床房","测试会员退房时间权益以当前订单及延退规则为准"]`, BirthdayJSON: `["测试生日福利：生日当月可申请一份欢迎礼，仅为测试配置"]`, FreeUpgradeMaxRank: 2, Enabled: true}
	if err := create(grade); err != nil {
		return err
	}
	member := &models.PMSSandboxMember{StoreID: storeID, DatasetID: datasetID, Name: "演示会员", Phone: "TEST001", GradeID: grade.ID, Birthday: "09-15", ValidUntil: now.AddDate(1, 0, 0), Enabled: true}
	if err := create(member); err != nil {
		return err
	}
	rules := []*models.PMSSandboxRule{
		{StoreID: storeID, DatasetID: datasetID, Code: "child_policy", Name: "测试儿童早餐政策", Text: "测试配置：1.2米以下儿童早餐免费，1.2米及以上每位28元。", Action: "information", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, Code: "upgrade", Name: "测试升房规则", Text: "根据可用净房及有效会员权益办理；不适用免费权益时按剩余住宿晚数补差价。", Action: "upgrade", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, Code: "room_change", Name: "测试换房规则", Text: "同房型可用净房可免费更换；房间描述只以已录入说明为准。", Action: "room_change", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, Code: "late_checkout", Name: "测试延迟退房", Text: "测试配置：房间无后续占用冲突时可免费延迟至离店日14:00。", Action: "late_checkout", CheckoutTime: "14:00", Enabled: true},
		{StoreID: storeID, DatasetID: datasetID, Code: "recovery_commitment", Name: "测试服务补救承诺", Text: "测试配置：服务问题未解决时，可记录一份50元体验补偿承诺，不代表退款、现金支付或已发券。", Action: "commitment", AmountCents: 5000, Enabled: true},
	}
	for _, rule := range rules {
		if err := create(rule); err != nil {
			return err
		}
	}
	return create(&models.PMSSandboxResource{StoreID: storeID, DatasetID: datasetID, Code: "pillow", Name: "丽斯严选同款枕头", Token: "#微信小店://丽斯严选/NxS0zhyvZJmEDEe", Enabled: true})
}

func (s *pmsSandboxService) Bind(ctx context.Context, storeID, operatorID int64, req sandbox.BindingRequest) (*sandbox.Snapshot, error) {
	if err := s.available(false); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, storeID, func(tx *gorm.DB, store *models.PMSSandboxStore) error {
		rows, err := repositories.PMSSandboxRepository.Load(tx, storeID, store.ActiveDatasetID, false)
		if err != nil {
			return errors.New("请先初始化测试数据")
		}
		if req.DatasetID != rows.Dataset.ID || req.Version != rows.Dataset.Version {
			return errors.New("测试数据已变化，请刷新后再绑定")
		}
		exists, err := repositories.PMSSandboxRepository.CustomerExists(tx, req.CustomerID)
		if err != nil || !exists {
			return errors.New("客户不存在")
		}
		inStore, err := repositories.PMSSandboxRepository.CustomerInStore(tx, req.CustomerID, storeID)
		if err != nil || !inStore {
			return errors.New("客户没有属于当前门店的会话，不能绑定")
		}
		if req.OrderID <= 0 && req.MemberID <= 0 {
			return errors.New("至少绑定一个测试订单或测试会员")
		}
		if req.OrderID > 0 && findOrder(rows, req.OrderID) == nil {
			return errors.New("测试订单不属于当前门店和数据批次")
		}
		if req.MemberID > 0 && findMember(rows, req.MemberID) == nil {
			return errors.New("测试会员不属于当前门店和数据批次")
		}
		item := &models.PMSSandboxBinding{StoreID: storeID, DatasetID: store.ActiveDatasetID, CustomerID: req.CustomerID, OrderID: req.OrderID, MemberID: req.MemberID}
		for _, binding := range rows.Bindings {
			if binding.CustomerID == req.CustomerID {
				item.ID = binding.ID
			}
		}
		if err := repositories.PMSSandboxRepository.Save(tx, item, item.ID, storeID, item.DatasetID); err != nil {
			return err
		}
		if err := repositories.PMSSandboxRepository.BumpDataset(tx, storeID, item.DatasetID); err != nil {
			return err
		}
		return s.adminAudit(tx, storeID, item.DatasetID, operatorID, "customer_binding", req)
	})
	if err != nil {
		return nil, err
	}
	return s.Snapshot(ctx, storeID)
}

func (s *pmsSandboxService) adminAudit(tx *gorm.DB, storeID, datasetID, operatorID int64, action string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := s.now()
	return repositories.PMSSandboxRepository.Create(tx, &models.PMSOperation{
		Provider: sandbox.Provider, StoreID: storeID, DatasetID: datasetID, OperatorID: operatorID,
		OperationType: action, Status: "completed", IdempotencyKey: fmt.Sprintf("sandbox:admin:%d:%d:%s", storeID, operatorID, uuid.NewString()),
		RequestData: string(encoded), PreviewText: sandbox.Label + "后台操作", ResultData: string(encoded), CreatedAt: now, UpdatedAt: now,
	})
}

func findOrder(rows *repositories.PMSSandboxRows, id int64) *models.PMSSandboxOrder {
	for i := range rows.Orders {
		if rows.Orders[i].ID == id {
			return &rows.Orders[i]
		}
	}
	return nil
}

func findMember(rows *repositories.PMSSandboxRows, id int64) *models.PMSSandboxMember {
	for i := range rows.Members {
		if rows.Members[i].ID == id {
			return &rows.Members[i]
		}
	}
	return nil
}

func findRoomType(rows *repositories.PMSSandboxRows, id int64) *models.PMSSandboxRoomType {
	for i := range rows.RoomTypes {
		if rows.RoomTypes[i].ID == id {
			return &rows.RoomTypes[i]
		}
	}
	return nil
}

func findRoom(rows *repositories.PMSSandboxRows, id int64) *models.PMSSandboxRoom {
	for i := range rows.Rooms {
		if rows.Rooms[i].ID == id {
			return &rows.Rooms[i]
		}
	}
	return nil
}

func findGrade(rows *repositories.PMSSandboxRows, id int64) *models.PMSSandboxGrade {
	for i := range rows.Grades {
		if rows.Grades[i].ID == id {
			return &rows.Grades[i]
		}
	}
	return nil
}

func (s *pmsSandboxService) activeRule(rows *repositories.PMSSandboxRows, id int64, action string, member *models.PMSSandboxMember) *models.PMSSandboxRule {
	for i := range rows.Rules {
		rule := &rows.Rules[i]
		if (id > 0 && rule.ID != id) || (action != "" && rule.Action != action) || !s.ruleApplies(rule, member) {
			continue
		}
		return rule
	}
	return nil
}

func (s *pmsSandboxService) ruleApplies(rule *models.PMSSandboxRule, member *models.PMSSandboxMember) bool {
	now := s.now()
	return rule.Enabled && (rule.ValidFrom == nil || !now.Before(*rule.ValidFrom)) &&
		(rule.ValidUntil == nil || now.Before(*rule.ValidUntil)) &&
		(rule.MinimumGradeID == 0 || (member != nil && member.Enabled && member.GradeID == rule.MinimumGradeID && now.Before(member.ValidUntil)))
}

func cleanText(value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > limit {
		return "", errors.New("字段内容过长")
	}
	return value, nil
}
