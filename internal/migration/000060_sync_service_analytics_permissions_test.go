package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
)

func TestSyncServiceAnalyticsPermissions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := syncServiceAnalyticsPermissions(db); err != nil {
		t.Fatalf("sync permissions: %v", err)
	}

	permissions := []constants.Permission{
		constants.PermissionServiceAnalyticsView,
		constants.PermissionServiceAnalyticsExport,
		constants.PermissionServiceAnalyticsManagePolicy,
		constants.PermissionConversationRecordView,
		constants.PermissionConversationRecordAnnotate,
		constants.PermissionConversationRecordExport,
		constants.PermissionQualityInspectionView,
		constants.PermissionQualityInspectionManage,
		constants.PermissionQualitySamplingCreate,
		constants.PermissionQualityTemplateManage,
		constants.PermissionConversationEvaluationView,
		constants.PermissionConversationEvaluationInvite,
		constants.PermissionReportViewPresetManage,
		constants.PermissionAgentPresenceUpdate,
	}
	for _, permission := range permissions {
		var count int64
		if err := db.Model(&models.Permission{}).Where("code = ?", permission.Code).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("permission %s count=%d err=%v", permission.Code, count, err)
		}
	}

	var manage models.Permission
	if err := db.Where("code = ?", constants.PermissionQualityInspectionManage.Code).Take(&manage).Error; err != nil {
		t.Fatalf("find quality manage permission: %v", err)
	}
	assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, manage.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, manage.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsUser, manage.ID, 0)

	var analytics models.Permission
	if err := db.Where("code = ?", constants.PermissionServiceAnalyticsView.Code).Take(&analytics).Error; err != nil {
		t.Fatalf("find analytics view permission: %v", err)
	}
	assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, analytics.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, analytics.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsUser, analytics.ID, 1)
}
