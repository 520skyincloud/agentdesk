package migration

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestRepairStandardHotelTagCategoryFlagsIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Tag{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)

	if err := seedStandardHotelCustomerTags(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Tag{}).Where("parent_id = ?", 0).Update("ai_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := repairStandardHotelTagCategoryFlags(); err != nil {
		t.Fatal(err)
	}
	if err := repairStandardHotelTagCategoryFlags(); err != nil {
		t.Fatal(err)
	}

	var enabledParents int64
	if err := db.Model(&models.Tag{}).
		Where("company_id = ? AND parent_id = ? AND ai_enabled = ?", 0, 0, true).
		Count(&enabledParents).Error; err != nil {
		t.Fatal(err)
	}
	if enabledParents != 0 {
		t.Fatalf("AI-enabled category count=%d", enabledParents)
	}

	var enabledChildren int64
	if err := db.Model(&models.Tag{}).
		Where("company_id = ? AND parent_id <> ? AND ai_enabled = ?", 0, 0, true).
		Count(&enabledChildren).Error; err != nil {
		t.Fatal(err)
	}
	if enabledChildren != 31 {
		t.Fatalf("AI-enabled child tag count=%d", enabledChildren)
	}
}
