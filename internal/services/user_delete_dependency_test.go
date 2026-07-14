package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestUserServiceDeleteRejectsActiveBusinessDependencies(t *testing.T) {
	tests := []struct {
		name        string
		wantMessage string
		seed        func(t *testing.T, fixture userDeleteFixture)
	}{
		{
			name:        "unfinished conversation",
			wantMessage: "未完成会话",
			seed: func(t *testing.T, fixture userDeleteFixture) {
				t.Helper()
				if err := fixture.db.Create(&models.Conversation{
					TenantID:          fixture.tenantID,
					CurrentAssigneeID: fixture.target.ID,
					Status:            enums.IMConversationStatusActive,
				}).Error; err != nil {
					t.Fatalf("create active conversation: %v", err)
				}
			},
		},
		{
			name:        "comprehensive team leader",
			wantMessage: "综合客服组组长",
			seed: func(t *testing.T, fixture userDeleteFixture) {
				t.Helper()
				if err := fixture.db.Create(&models.AgentTeam{
					TenantID:     fixture.tenantID,
					Name:         "delete-guard-team",
					LeaderUserID: fixture.target.ID,
					Status:       enums.StatusOk,
				}).Error; err != nil {
					t.Fatalf("create agent team: %v", err)
				}
			},
		},
		{
			name:        "squad leader",
			wantMessage: "客服小组组长",
			seed: func(t *testing.T, fixture userDeleteFixture) {
				t.Helper()
				if err := fixture.db.Create(&models.AgentTeamSquad{
					TenantID:     fixture.tenantID,
					TeamID:       1,
					Name:         "delete-guard-squad",
					LeaderUserID: fixture.target.ID,
					Status:       enums.StatusOk,
				}).Error; err != nil {
					t.Fatalf("create agent squad: %v", err)
				}
			},
		},
		{
			name:        "agent profile",
			wantMessage: "客服档案",
			seed: func(t *testing.T, fixture userDeleteFixture) {
				t.Helper()
				if err := fixture.db.Create(&models.AgentProfile{
					TenantID:    fixture.tenantID,
					UserID:      fixture.target.ID,
					TeamID:      1,
					AgentCode:   "delete-guard-agent",
					DisplayName: "待删除客服",
					Status:      enums.StatusOk,
				}).Error; err != nil {
					t.Fatalf("create agent profile: %v", err)
				}
			},
		},
		{
			name:        "store staff binding",
			wantMessage: "门店员工身份",
			seed: func(t *testing.T, fixture userDeleteFixture) {
				t.Helper()
				if err := fixture.db.Create(&models.StoreStaffBinding{
					TenantID: fixture.tenantID,
					UserID:   fixture.target.ID,
					StoreID:  1,
					Status:   enums.StatusOk,
				}).Error; err != nil {
					t.Fatalf("create store staff binding: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupUserDeleteFixture(t)
			test.seed(t, fixture)
			err := UserService.DeleteUser(fixture.target.ID, fixture.operator)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("DeleteUser() error = %v, want message containing %q", err, test.wantMessage)
			}
			current := UserService.Get(fixture.target.ID)
			if current == nil || current.DeletedAt != nil || current.Status != enums.StatusOk {
				t.Fatalf("blocked deletion changed user: %+v", current)
			}
		})
	}
}

func TestUserServiceDeleteRemovesDependencyFreeAccount(t *testing.T) {
	fixture := setupUserDeleteFixture(t)
	if err := UserService.DeleteUser(fixture.target.ID, fixture.operator); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	current := UserService.Get(fixture.target.ID)
	if current == nil || current.DeletedAt == nil || current.Status != enums.StatusDisabled {
		t.Fatalf("deleted account state = %+v", current)
	}
}

type userDeleteFixture struct {
	db       *gorm.DB
	tenantID int64
	target   *models.User
	operator *dto.AuthPrincipal
}

func setupUserDeleteFixture(t *testing.T) userDeleteFixture {
	t.Helper()
	db := setupAuthServiceTestDB(t)
	if err := db.AutoMigrate(
		&models.AgentProfile{},
		&models.AgentTeam{},
		&models.AgentTeamSquad{},
		&models.StoreStaffBinding{},
		&models.Conversation{},
	); err != nil {
		t.Fatalf("migrate user delete dependencies: %v", err)
	}
	roles := seedAuthorityRoles(t, db)
	tenantID := authorityLegacyTenantID(t, db)
	now := time.Now()
	operatorUser := &models.User{
		TenantID:    tenantID,
		Username:    "delete-guard-operator",
		Nickname:    "delete-guard-operator",
		Password:    "unused",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(operatorUser).Error; err != nil {
		t.Fatalf("create operator: %v", err)
	}
	target := &models.User{
		TenantID:    tenantID,
		Username:    "delete-guard-target",
		Nickname:    "delete-guard-target",
		Password:    "unused",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	assignAuthorityRole(t, db, operatorUser.ID, roles[constants.RoleCodeTenantAdmin].ID)
	assignAuthorityRole(t, db, target.ID, roles[constants.RoleCodeCsUser].ID)
	return userDeleteFixture{
		db:       db,
		tenantID: tenantID,
		target:   target,
		operator: &dto.AuthPrincipal{
			UserID:         operatorUser.ID,
			Username:       operatorUser.Username,
			TenantID:       tenantID,
			ActiveTenantID: tenantID,
			Roles:          []string{constants.RoleCodeTenantAdmin},
		},
	}
}
