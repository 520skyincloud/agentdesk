package services

import (
	"go/ast"
	"regexp"
	"testing"
)

var agentTeamTablePattern = regexp.MustCompile(`\bt_agent_team\b`)

func TestAgentTeamRuntimeWritesStayBehindDomainServices(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"tenant_management_service.go": {
			"CreateTenant": {},
		},
		"agent_team_service.go": {
			"CreateAgentTeam":              {},
			"UpdateAgentTeam":              {},
			"DeleteAgentTeam":              {},
			"syncTeamScopeFromAssignments": {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "AgentTeam", isAgentTeamMutationCall, allowed)
}

func TestIsAgentTeamMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.AgentTeamRepository.Create(db, item)", want: true},
		{name: "tenant update", expression: "repositories.AgentTeamRepository.UpdatesInTenant(db, id, tenantID, values)", want: true},
		{name: "generic service delete", expression: "AgentTeamService.Delete(id)", want: true},
		{name: "gorm update", expression: "db.Model(&models.AgentTeam{}).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"UPDATE t_agent_team SET status = ?\", status)", want: true},
		{name: "repository read", expression: "repositories.AgentTeamRepository.GetInTenant(db, id, tenantID)", want: false},
		{name: "squad write", expression: "db.Create(&models.AgentTeamSquad{})", want: false},
		{name: "squad SQL", expression: "db.Exec(\"UPDATE t_agent_team_squad SET status = ?\", status)", want: false},
	}
	assertMutationDetectorCases(t, tests, isAgentTeamMutationCall)
}

func isAgentTeamMutationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if receiver, receiverOK := selector.X.(*ast.SelectorExpr); receiverOK && receiver.Sel.Name == "AgentTeamRepository" {
			if selector.Sel.Name == "UpdatesInTenant" {
				return true
			}
		}
	}
	return isDefinitionMutationCall(call, "AgentTeamRepository", "AgentTeamService", "AgentTeam", agentTeamTablePattern)
}
