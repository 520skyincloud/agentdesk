package repositories

import (
	"errors"
	"sort"
	"time"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type pmsSandboxRepository struct{}

var PMSSandboxRepository = &pmsSandboxRepository{}

type PMSSandboxRows struct {
	Dataset    *models.PMSSandboxDataset
	RoomTypes  []models.PMSSandboxRoomType
	Rooms      []models.PMSSandboxRoom
	Orders     []models.PMSSandboxOrder
	Grades     []models.PMSSandboxGrade
	Members    []models.PMSSandboxMember
	Rules      []models.PMSSandboxRule
	Resources  []models.PMSSandboxResource
	Bindings   []models.PMSSandboxBinding
	Operations []models.PMSOperation
}

func (r *pmsSandboxRepository) LockStore(db *gorm.DB, storeID int64) (*models.PMSSandboxStore, error) {
	var count int64
	if err := db.Model(&models.Store{}).Where("id = ?", storeID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	item := &models.PMSSandboxStore{StoreID: storeID, UpdatedAt: time.Now()}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error; err != nil {
		return nil, err
	}
	// This write also acquires SQLite's writer lock before any business reads.
	if err := db.Model(&models.PMSSandboxStore{}).Where("store_id = ?", storeID).
		Updates(map[string]any{"lock_version": gorm.Expr("lock_version + 1"), "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ?", storeID).Take(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func (r *pmsSandboxRepository) Store(db *gorm.DB, storeID int64) (*models.PMSSandboxStore, error) {
	item := &models.PMSSandboxStore{}
	err := db.Where("store_id = ?", storeID).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) SetDataset(db *gorm.DB, storeID, datasetID int64) error {
	return db.Model(&models.PMSSandboxStore{}).Where("store_id = ?", storeID).
		Update("active_dataset_id", datasetID).Error
}

func (r *pmsSandboxRepository) BumpDataset(db *gorm.DB, storeID, datasetID int64) error {
	return db.Model(&models.PMSSandboxDataset{}).Where("store_id = ? AND id = ?", storeID, datasetID).
		Update("version", gorm.Expr("version + 1")).Error
}

func (r *pmsSandboxRepository) Create(db *gorm.DB, item any) error {
	return db.Create(item).Error
}

func (r *pmsSandboxRepository) Save(db *gorm.DB, item any, id, storeID, datasetID int64) error {
	if id == 0 {
		return db.Create(item).Error
	}
	var count int64
	if err := db.Model(item).Where("id = ? AND store_id = ? AND dataset_id = ?", id, storeID, datasetID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return gorm.ErrRecordNotFound
	}
	return db.Model(item).Where("id = ? AND store_id = ? AND dataset_id = ?", id, storeID, datasetID).
		Select("*").Updates(item).Error
}

func (r *pmsSandboxRepository) Load(db *gorm.DB, storeID, datasetID int64, history bool) (*PMSSandboxRows, error) {
	rows := &PMSSandboxRows{Dataset: &models.PMSSandboxDataset{}}
	if err := db.Where("store_id = ? AND id = ?", storeID, datasetID).Take(rows.Dataset).Error; err != nil {
		return nil, err
	}
	for _, target := range []any{&rows.RoomTypes, &rows.Rooms, &rows.Orders, &rows.Grades, &rows.Members, &rows.Rules, &rows.Resources, &rows.Bindings} {
		if err := db.Where("store_id = ? AND dataset_id = ?", storeID, datasetID).Order("id ASC").Find(target).Error; err != nil {
			return nil, err
		}
	}
	if history {
		if err := db.Where("store_id = ? AND provider = ?", storeID, "sandbox").Order("id DESC").Limit(200).Find(&rows.Operations).Error; err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (r *pmsSandboxRepository) LockRooms(db *gorm.DB, storeID, datasetID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	ids = append([]int64(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var rooms []models.PMSSandboxRoom
	return db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("store_id = ? AND dataset_id = ? AND id IN ?", storeID, datasetID, ids).
		Order("id ASC").Find(&rooms).Error
}

func (r *pmsSandboxRepository) UpdateOrder(db *gorm.DB, order *models.PMSSandboxOrder, expectedVersion int64) error {
	result := db.Model(&models.PMSSandboxOrder{}).
		Where("id = ? AND store_id = ? AND dataset_id = ? AND version = ?", order.ID, order.StoreID, order.DatasetID, expectedVersion).
		Updates(map[string]any{"room_type_id": order.RoomTypeID, "room_id": order.RoomID,
			"check_in": order.CheckIn, "check_out": order.CheckOut, "payable_cents": order.PayableCents,
			"status": order.Status, "version": expectedVersion + 1, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("订单版本已变化，请重新生成方案")
	}
	return nil
}

func (r *pmsSandboxRepository) ReadOrder(db *gorm.DB, storeID, datasetID, id int64) (*models.PMSSandboxOrder, error) {
	item := &models.PMSSandboxOrder{}
	err := db.Where("id = ? AND store_id = ? AND dataset_id = ?", id, storeID, datasetID).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) Operation(db *gorm.DB, id int64) (*models.PMSOperation, error) {
	item := &models.PMSOperation{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) OperationByKey(db *gorm.DB, key string) (*models.PMSOperation, error) {
	item := &models.PMSOperation{}
	err := db.Where("idempotency_key = ?", key).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) Pending(db *gorm.DB, storeID, conversationID int64) (*models.PMSOperation, error) {
	item := &models.PMSOperation{}
	err := db.Where("provider = ? AND operation_type = ? AND store_id = ? AND conversation_id = ? AND status = ?", "sandbox", "sandbox_change", storeID, conversationID, "pending").
		Order("id DESC").Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) Latest(db *gorm.DB, storeID, datasetID, conversationID int64) (*models.PMSOperation, error) {
	item := &models.PMSOperation{}
	err := db.Where("provider = ? AND operation_type = ? AND store_id = ? AND dataset_id = ? AND conversation_id = ?", "sandbox", "sandbox_change", storeID, datasetID, conversationID).
		Order("id DESC").Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) UpdateOperation(db *gorm.DB, id int64, values map[string]any) error {
	values["updated_at"] = time.Now()
	return db.Model(&models.PMSOperation{}).Where("id = ? AND provider = ?", id, "sandbox").Updates(values).Error
}

func (r *pmsSandboxRepository) Supersede(db *gorm.DB, storeID, conversationID int64) error {
	return db.Model(&models.PMSOperation{}).
		Where("store_id = ? AND conversation_id = ? AND provider = ? AND operation_type = ? AND status = ?", storeID, conversationID, "sandbox", "sandbox_change", "pending").
		Updates(map[string]any{"status": "superseded", "updated_at": time.Now()}).Error
}

func (r *pmsSandboxRepository) InvalidateDataset(db *gorm.DB, storeID, datasetID int64) error {
	return db.Model(&models.PMSOperation{}).
		Where("store_id = ? AND dataset_id = ? AND provider = ? AND operation_type = ? AND status = ?", storeID, datasetID, "sandbox", "sandbox_change", "pending").
		Updates(map[string]any{"status": "superseded", "error_message": "测试数据已重置，旧方案失效", "updated_at": time.Now()}).Error
}

func (r *pmsSandboxRepository) CustomerExists(db *gorm.DB, id int64) (bool, error) {
	var count int64
	err := db.Model(&models.Customer{}).Where("id = ?", id).Count(&count).Error
	return count == 1, err
}

func (r *pmsSandboxRepository) CustomerInStore(db *gorm.DB, customerID, storeID int64) (bool, error) {
	var count int64
	conversations := db.Model(&models.Conversation{}).Select("id").Where("customer_id = ?", customerID)
	err := db.Model(&models.ConversationRouteState{}).
		Where("store_id = ? AND conversation_id IN (?)", storeID, conversations).Count(&count).Error
	return count > 0, err
}

func (r *pmsSandboxRepository) InterveningCustomerMessages(db *gorm.DB, conversationID, afterSeq, beforeSeq int64) (bool, error) {
	var count int64
	err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ? AND seq_no > ? AND seq_no < ?", conversationID, "customer", afterSeq, beforeSeq).Count(&count).Error
	return count > 0, err
}

func (r *pmsSandboxRepository) Conversation(db *gorm.DB, id int64) (*models.Conversation, error) {
	item := &models.Conversation{}
	err := db.Where("id = ?", id).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) ConversationStore(db *gorm.DB, conversationID int64) (int64, error) {
	item := &models.ConversationRouteState{}
	err := db.Where("conversation_id = ?", conversationID).Take(item).Error
	return item.StoreID, err
}

func (r *pmsSandboxRepository) Message(db *gorm.DB, id int64) (*models.Message, error) {
	item := &models.Message{}
	err := db.Where("id = ?", id).Take(item).Error
	return item, err
}

func (r *pmsSandboxRepository) DeliveryStatus(db *gorm.DB, messageID int64) (string, error) {
	status, _, err := r.PreviewDelivery(db, messageID)
	return status, err
}

func (r *pmsSandboxRepository) PreviewDelivery(db *gorm.DB, messageID int64) (string, *time.Time, error) {
	var items []models.ChannelMessageOutbox
	if err := db.Where("message_id = ?", messageID).Find(&items).Error; err != nil {
		return "", nil, err
	}
	if len(items) == 0 {
		return "", nil, nil
	}
	var latest *time.Time
	for _, item := range items {
		if item.SendStatus != "sent" || item.SentAt == nil {
			return item.SendStatus, nil, nil
		}
		if latest == nil || item.SentAt.After(*latest) {
			value := *item.SentAt
			latest = &value
		}
	}
	return "sent", latest, nil
}
