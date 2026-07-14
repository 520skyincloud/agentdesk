package services

import (
	"go/ast"
	"regexp"
	"testing"
)

var agentTeamScheduleTablePattern = regexp.MustCompile(`\bt_agent_team_schedule\b`)

func TestAgentTeamScheduleRuntimeWritesStayBehindDomainServices(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"agent_team_schedule_service.go": {
			"CreateAgentTeamSchedule": {},
			"UpdateAgentTeamSchedule": {},
			"DeleteAgentTeamSchedule": {},
			"BatchGenerate":           {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "AgentTeamSchedule", isAgentTeamScheduleMutationCall, allowed)
}

func TestIsAgentTeamScheduleMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.AgentTeamScheduleRepository.Create(db, item)", want: true},
		{name: "batch create", expression: "repositories.AgentTeamScheduleRepository.CreateBatch(db, items)", want: true},
		{name: "tenant update", expression: "repositories.AgentTeamScheduleRepository.UpdatesInTenant(db, id, tenantID, values)", want: true},
		{name: "tenant delete", expression: "repositories.AgentTeamScheduleRepository.DeleteInTenant(db, id, tenantID)", want: true},
		{name: "generic service update", expression: "AgentTeamScheduleService.Update(item)", want: true},
		{name: "gorm update", expression: "db.Model(&models.AgentTeamSchedule{}).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"DELETE FROM t_agent_team_schedule WHERE id = ?\", id)", want: true},
		{name: "repository read", expression: "repositories.AgentTeamScheduleRepository.GetInTenant(db, id, tenantID)", want: false},
		{name: "team write", expression: "db.Create(&models.AgentTeam{})", want: false},
	}
	assertMutationDetectorCases(t, tests, isAgentTeamScheduleMutationCall)
}

func isAgentTeamScheduleMutationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if receiver, receiverOK := selector.X.(*ast.SelectorExpr); receiverOK && receiver.Sel.Name == "AgentTeamScheduleRepository" {
			switch selector.Sel.Name {
			case "CreateBatch", "UpdatesInTenant", "DeleteInTenant":
				return true
			}
		}
	}
	return isDefinitionMutationCall(call, "AgentTeamScheduleRepository", "AgentTeamScheduleService", "AgentTeamSchedule", agentTeamScheduleTablePattern)
}
