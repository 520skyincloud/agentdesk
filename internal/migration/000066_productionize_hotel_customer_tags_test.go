package migration

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestProductionizeHotelCustomerTagsRetiresDeprecatedRelationsIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Tag{}, &models.CustomerTagRelation{}, &models.CustomerTagChangeLog{}, &models.StoreCustomerRelation{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	now := time.Now()
	positionCategory := &models.Tag{CompanyID: 0, Name: "位置偏好", SemanticKey: "category.hotel", AIEnabled: true, ReplyEnabled: true, Status: enums.StatusOk, AuditFields: systemTagAuditFields(now)}
	if err := db.Create(positionCategory).Error; err != nil {
		t.Fatal(err)
	}
	deprecatedLocation := &models.Tag{CompanyID: 0, ParentID: positionCategory.ID, Name: "近前台", SemanticKey: "legacy.changed", AIEnabled: true, ReplyEnabled: true, Status: enums.StatusOk, AuditFields: systemTagAuditFields(now)}
	if err := db.Create(deprecatedLocation).Error; err != nil {
		t.Fatal(err)
	}
	deprecated := &models.Tag{CompanyID: 0, Name: "需接送", SemanticKey: "service.transfer", AIEnabled: true, Status: enums.StatusOk, AuditFields: systemTagAuditFields(now)}
	if err := db.Create(deprecated).Error; err != nil {
		t.Fatal(err)
	}
	storeRelation := &models.StoreCustomerRelation{CustomerID: 9, StoreID: 8, LastConversationID: 7, Status: enums.StatusOk, AuditFields: systemTagAuditFields(now)}
	if err := db.Create(storeRelation).Error; err != nil {
		t.Fatal(err)
	}
	relation := &models.CustomerTagRelation{CompanyID: 6, StoreID: 8, CustomerID: 9, StoreCustomerRelationID: storeRelation.ID, TagID: deprecated.ID, RelationStatus: "active", AuditFields: systemTagAuditFields(now)}
	if err := db.Create(relation).Error; err != nil {
		t.Fatal(err)
	}
	locationRelation := &models.CustomerTagRelation{CompanyID: 6, StoreID: 8, CustomerID: 9, StoreCustomerRelationID: storeRelation.ID, TagID: deprecatedLocation.ID, RelationStatus: "active", AuditFields: systemTagAuditFields(now)}
	if err := db.Create(locationRelation).Error; err != nil {
		t.Fatal(err)
	}

	if err := productionizeHotelCustomerTags(); err != nil {
		t.Fatal(err)
	}
	if err := productionizeHotelCustomerTags(); err != nil {
		t.Fatal(err)
	}
	if err := db.First(deprecated, deprecated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if deprecated.Status != enums.StatusDeleted || deprecated.AIEnabled || deprecated.ReplyEnabled {
		t.Fatalf("deprecated tag not retired: %#v", deprecated)
	}
	if err := db.First(relation, relation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if relation.RelationStatus != "inactive" {
		t.Fatalf("deprecated relation status=%q", relation.RelationStatus)
	}
	if err := db.First(locationRelation, locationRelation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if locationRelation.RelationStatus != "inactive" {
		t.Fatalf("deprecated location relation status=%q", locationRelation.RelationStatus)
	}
	for _, item := range []*models.Tag{positionCategory, deprecatedLocation, deprecated} {
		if err := db.First(item, item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if item.Status != enums.StatusDeleted || item.AIEnabled || item.ReplyEnabled || !item.SystemDefined {
			t.Fatalf("deprecated catalog item not retired: %#v", item)
		}
	}
	var logCount int64
	if err := db.Model(&models.CustomerTagChangeLog{}).Where("old_tag_id IN ?", []int64{deprecated.ID, deprecatedLocation.ID}).Count(&logCount).Error; err != nil {
		t.Fatal(err)
	}
	if logCount != 2 {
		t.Fatalf("change log count=%d", logCount)
	}
	var categoryCount, leafCount, replyCount, aiCount int64
	standardTags := func() *gorm.DB {
		return db.Model(&models.Tag{}).Where("company_id = ? AND system_defined = ? AND status = ?", 0, true, enums.StatusOk)
	}
	if err := standardTags().Where("parent_id = ?", 0).Count(&categoryCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := standardTags().Where("parent_id <> ?", 0).Count(&leafCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := standardTags().Where("parent_id <> ? AND reply_enabled = ?", 0, true).Count(&replyCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := standardTags().Where("parent_id <> ? AND ai_enabled = ?", 0, true).Count(&aiCount).Error; err != nil {
		t.Fatal(err)
	}
	if categoryCount != 4 || leafCount != 31 || replyCount != 25 || aiCount != 31 {
		t.Fatalf("effective standard catalog categories=%d leaves=%d reply=%d ai=%d", categoryCount, leafCount, replyCount, aiCount)
	}
	if active := repositories.CustomerTagRelationRepository.CountActiveByRelationID(db, storeRelation.ID); active != 0 {
		t.Fatalf("deprecated relations still count as active tags: %d", active)
	}
}
