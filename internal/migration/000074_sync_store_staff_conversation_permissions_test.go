package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSyncStoreStaffConversationPermissionsIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "s74_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Permission{}, &models.Role{}, &models.RolePermission{}); err != nil {
		t.Fatalf("migrate permission fixtures: %v", err)
	}
	role := &models.Role{
		Name:   "门店员工",
		Code:   constants.RoleCodeStoreStaff,
		Scope:  constants.RoleScopeTenant,
		Status: enums.StatusOk,
	}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := syncStoreStaffConversationPermissions(db); err != nil {
			t.Fatalf("sync permissions attempt %d: %v", attempt+1, err)
		}
	}

	for _, permissionCode := range []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	} {
		var count int64
		if err := db.Table("s74_role_permission AS rp").
			Joins("JOIN s74_permission AS p ON p.id = rp.permission_id").
			Where("rp.role_id = ? AND p.code = ?", role.ID, permissionCode).
			Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("permission %s count=%d err=%v", permissionCode, count, err)
		}
	}
}
