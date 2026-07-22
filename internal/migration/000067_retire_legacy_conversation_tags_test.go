package migration

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRetireLegacyConversationTagsPreservesPermissionIdentityAndBindings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Permission{}, &models.RolePermission{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	permission := &models.Permission{
		ID: 20, Name: "管理会话标签", Code: constants.PermissionCustomerTag.Code,
		Type: "api", GroupName: "conversation", Method: "POST",
		APIPath: "/api/dashboard/conversation/add_tag", SortNo: 470,
		Status: enums.StatusOk, IsBuiltin: true,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(permission).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.RolePermission{
		RoleID: 7, PermissionID: permission.ID,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := retireLegacyConversationTags(db); err != nil {
		t.Fatal(err)
	}
	if err := retireLegacyConversationTags(db); err != nil {
		t.Fatal(err)
	}

	var updated models.Permission
	if err := db.Take(&updated, "code = ?", constants.PermissionCustomerTag.Code).Error; err != nil {
		t.Fatal(err)
	}
	if updated.ID != permission.ID || updated.Name != constants.PermissionCustomerTag.Name || updated.APIPath != constants.PermissionCustomerTag.APIPath {
		t.Fatalf("permission identity or metadata changed unexpectedly: %#v", updated)
	}
	var bindingCount int64
	if err := db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&bindingCount).Error; err != nil {
		t.Fatal(err)
	}
	if bindingCount != 1 {
		t.Fatalf("role permission binding count=%d", bindingCount)
	}
}
