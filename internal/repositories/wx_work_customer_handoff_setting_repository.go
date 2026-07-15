package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var WxWorkCustomerHandoffSettingRepository = newWxWorkCustomerHandoffSettingRepository()

type wxWorkCustomerHandoffSettingRepository struct{}

func newWxWorkCustomerHandoffSettingRepository() *wxWorkCustomerHandoffSettingRepository {
	return &wxWorkCustomerHandoffSettingRepository{}
}

func (r *wxWorkCustomerHandoffSettingRepository) Take(db *gorm.DB, where ...any) *models.WxWorkCustomerHandoffSetting {
	ret := &models.WxWorkCustomerHandoffSetting{}
	result := db.Take(ret, where...)
	if result.Error != nil {
		return nil
	}
	return ret
}

func (r *wxWorkCustomerHandoffSettingRepository) Create(db *gorm.DB, item *models.WxWorkCustomerHandoffSetting) error {
	if item == nil {
		return nil
	}
	// A map keeps an explicit false from being omitted by GORM's default:true
	// handling. The setting is semantically unsafe if false silently becomes true.
	return db.Model(&models.WxWorkCustomerHandoffSetting{}).Create(map[string]any{
		"tenant_id":            item.TenantID,
		"customer_id":          item.CustomerID,
		"wx_work_instance_id":  item.WxWorkInstanceID,
		"auto_handoff_enabled": item.AutoHandoffEnabled,
		"remark":               item.Remark,
		"created_at":           item.CreatedAt,
		"create_user_id":       item.CreateUserID,
		"create_user_name":     item.CreateUserName,
		"updated_at":           item.UpdatedAt,
		"update_user_id":       item.UpdateUserID,
		"update_user_name":     item.UpdateUserName,
	}).Error
}

func (r *wxWorkCustomerHandoffSettingRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.WxWorkCustomerHandoffSetting{}).Where("id = ?", id).Updates(columns).Error
}

func (r *wxWorkCustomerHandoffSettingRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.WxWorkCustomerHandoffSetting{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
