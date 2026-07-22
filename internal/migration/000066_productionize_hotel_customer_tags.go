package migration

import (
	"sort"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(66, "productionize standard hotel customer tags", productionizeHotelCustomerTags)
}

func productionizeHotelCustomerTags() error {
	db := sqls.DB()
	if db == nil {
		return nil
	}
	if err := seedStandardHotelCustomerTags(db); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var deprecated []models.Tag
		if err := tx.Where("company_id = ? AND (semantic_key LIKE ? OR semantic_key = ? OR name = ?)",
			0, "location.%", "service.transfer", "位置偏好").Find(&deprecated).Error; err != nil {
			return err
		}
		deprecatedByID := make(map[int64]models.Tag, len(deprecated))
		positionCategoryIDs := make([]int64, 0, 1)
		for i := range deprecated {
			deprecatedByID[deprecated[i].ID] = deprecated[i]
			if deprecated[i].ParentID == 0 && deprecated[i].Name == "位置偏好" {
				positionCategoryIDs = append(positionCategoryIDs, deprecated[i].ID)
			}
		}
		for len(positionCategoryIDs) > 0 {
			var children []models.Tag
			if err := tx.Where("parent_id IN ?", positionCategoryIDs).Find(&children).Error; err != nil {
				return err
			}
			positionCategoryIDs = positionCategoryIDs[:0]
			for i := range children {
				if _, exists := deprecatedByID[children[i].ID]; exists {
					continue
				}
				deprecatedByID[children[i].ID] = children[i]
				positionCategoryIDs = append(positionCategoryIDs, children[i].ID)
			}
		}
		deprecated = deprecated[:0]
		for _, item := range deprecatedByID {
			deprecated = append(deprecated, item)
		}
		sort.Slice(deprecated, func(i, j int) bool { return deprecated[i].ID < deprecated[j].ID })
		deprecatedIDs := make([]int64, 0, len(deprecated))
		for i := range deprecated {
			deprecatedIDs = append(deprecatedIDs, deprecated[i].ID)
		}
		if len(deprecatedIDs) == 0 {
			return nil
		}

		now := time.Now()
		var activeRelations []models.CustomerTagRelation
		if err := tx.Where("tag_id IN ? AND relation_status = ?", deprecatedIDs, "active").Find(&activeRelations).Error; err != nil {
			return err
		}
		for i := range activeRelations {
			relation := &activeRelations[i]
			storeRelation := &models.StoreCustomerRelation{}
			_ = tx.Take(storeRelation, "id = ?", relation.StoreCustomerRelationID).Error
			if err := tx.Create(&models.CustomerTagChangeLog{
				CompanyID: relation.CompanyID, StoreID: relation.StoreID, CustomerID: relation.CustomerID,
				StoreCustomerRelationID: relation.StoreCustomerRelationID,
				ConversationID:          storeRelation.LastConversationID,
				Action:                  "remove", OldTagID: relation.TagID, EvidenceMessageIDs: "[]",
				Source: "system", OperatorType: "system", OperatorID: constants.SystemAuditUserID,
				OperatorName: constants.SystemAuditUserName, CreatedAt: now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.CustomerTagRelation{}).Where("id = ?", relation.ID).Updates(map[string]any{
				"relation_status": "inactive", "inactivated_at": now, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.Tag{}).Where("id IN ?", deprecatedIDs).Updates(map[string]any{
			"system_defined": true, "ai_enabled": false, "reply_enabled": false,
			"status": enums.StatusDeleted, "update_user_id": constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName, "updated_at": now,
		}).Error
	})
}
