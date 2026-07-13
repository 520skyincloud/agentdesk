package builders

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/services"
)

func TestBuildAgentTeamSquadListUsesEmptyMemberArray(t *testing.T) {
	result := BuildAgentTeamSquadList([]services.AgentTeamSquadOverview{
		{Squad: models.AgentTeamSquad{ID: 1, TeamID: 2, Name: "测试小组"}},
	})
	if len(result) != 1 || result[0].MemberProfileIDs == nil {
		t.Fatalf("expected non-nil empty member profile ids, got %+v", result)
	}
	payload, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("marshal response error = %v", err)
	}
	if !strings.Contains(string(payload), `"memberProfileIds":[]`) {
		t.Fatalf("expected memberProfileIds to serialize as [], got %s", payload)
	}
}
