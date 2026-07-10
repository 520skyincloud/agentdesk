package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestTeamCanServeRouteUsesTeamScopeOnly(t *testing.T) {
	team := &models.AgentTeam{
		ID:                     1,
		Name:                   "测试客服组",
		StoreScopeIDs:          "11",
		WxWorkInstanceScopeIDs: "22",
		Status:                 enums.StatusOk,
	}
	tests := []struct {
		name  string
		route *models.ConversationRouteState
		want  bool
	}{
		{name: "team store", route: &models.ConversationRouteState{StoreID: 11}, want: true},
		{name: "team instance", route: &models.ConversationRouteState{WxWorkInstanceID: 22}, want: true},
		{name: "store outside team", route: &models.ConversationRouteState{StoreID: 99}, want: false},
		{name: "instance outside team", route: &models.ConversationRouteState{WxWorkInstanceID: 88}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := teamCanServeRoute(team, tt.route); got != tt.want {
				t.Fatalf("teamCanServeRoute() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTeamCanServeRouteRequiresEnabledTeam(t *testing.T) {
	team := &models.AgentTeam{ID: 1, Name: "停用客服组", Status: enums.StatusDisabled}
	if teamCanServeRoute(team, &models.ConversationRouteState{}) {
		t.Fatal("disabled team must not grant conversation scope")
	}
}

func TestTeamCanServeRouteRejectsEmptyTeamScope(t *testing.T) {
	team := &models.AgentTeam{ID: 1, Name: "未配置范围客服组", Status: enums.StatusOk}
	if teamCanServeRoute(team, &models.ConversationRouteState{StoreID: 99, WxWorkInstanceID: 88}) {
		t.Fatal("team without configured accounts must not grant unrestricted conversation scope")
	}
}
