package migration

import (
	"path/filepath"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestRetireModelDispatchKeepsHistoryAndNormalizesActiveConfiguration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retire-model-dispatch.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.AgentTeam{}, &models.AgentProfile{}, &legacyStoreAIModelSetting{},
		&models.ConversationAssignment{}, &models.DispatchDecisionLog{}, &models.AIUsageEvent{},
	); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}

	team := &models.AgentTeam{TenantID: 101, Name: "历史智能组", DispatchMode: enums.AgentTeamDispatchMode("intelligent"), Status: enums.StatusOk}
	profile := &models.AgentProfile{TenantID: 101, UserID: 11, TeamID: 1, AgentCode: "A-11", AutoAssignEnabled: true, MaxConcurrentCount: 0, Status: enums.StatusOk}
	setting := &legacyStoreAIModelSetting{TenantID: 101, UsageCode: retiredDispatchModelUsageCode, AIConfigID: 9, Status: enums.StatusOk}
	assignment := &models.ConversationAssignment{TenantID: 101, ConversationID: 22, SessionNo: 1, ToUserID: 11, DispatchMode: enums.AgentTeamDispatchMode("intelligent"), DecisionConfidence: 88, WorkloadWeight: 3, Status: enums.IMAssignmentStatusInactive, CreatedAt: time.Now()}
	decision := &models.DispatchDecisionLog{TenantID: 101, DecisionKey: "historical-intelligent", ConversationID: 22, SessionNo: 1, DecisionMode: "intelligent", Status: enums.DispatchDecisionStatusSelected, DecidedAt: time.Now()}
	usage := &models.AIUsageEvent{TenantID: 101, EventKey: "historical-dispatch-usage", ConversationID: 22, Stage: "dispatch_decision", OperationType: "dispatch_decision", Status: "success", CreatedAt: time.Now()}
	for name, item := range map[string]any{
		"team": team, "profile": profile, "setting": setting,
		"assignment": assignment, "decision": decision, "usage": usage,
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if err := retireModelDispatch(db); err != nil {
		t.Fatalf("retire model dispatch: %v", err)
	}
	if err := retireModelDispatch(db); err != nil {
		t.Fatalf("second retire model dispatch: %v", err)
	}

	db.First(team, team.ID)
	db.First(profile, profile.ID)
	db.First(setting, setting.ID)
	db.First(assignment, assignment.ID)
	db.First(decision, decision.ID)
	db.First(usage, usage.ID)
	if team.DispatchMode != enums.AgentTeamDispatchModeRule {
		t.Fatalf("team dispatch mode=%q", team.DispatchMode)
	}
	if profile.MaxConcurrentCount != 0 || profile.AutoAssignEnabled {
		t.Fatalf("invalid capacity must disable auto assignment without guessing a limit: %+v", profile)
	}
	if setting.Status != enums.StatusDeleted {
		t.Fatalf("dispatch model setting status=%d", setting.Status)
	}
	if assignment.DispatchMode != enums.AgentTeamDispatchMode("intelligent") || assignment.DecisionConfidence != 88 {
		t.Fatalf("historical assignment changed: %+v", assignment)
	}
	if decision.DecisionMode != "intelligent" || usage.OperationType != "dispatch_decision" {
		t.Fatalf("historical evidence changed: decision=%+v usage=%+v", decision, usage)
	}
}
