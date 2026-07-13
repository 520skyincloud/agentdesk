package main

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestBuildSimulationScenarios(t *testing.T) {
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	scenarios := buildSimulationScenarios(now)
	if len(scenarios) != expectedSimulationConversationCount {
		t.Fatalf("scenario count = %d, want %d", len(scenarios), expectedSimulationConversationCount)
	}

	kindCounts := map[simulationKind]int{}
	teamCounts := map[int]int{}
	seenStores := map[int]bool{}
	messageCount := 0
	assignmentCount := 0
	needReplyCount := 0
	for _, scenario := range scenarios {
		kindCounts[scenario.Kind]++
		teamCounts[scenario.TeamIndex]++
		messageCount += len(scenario.Messages)
		if seenStores[scenario.StoreIndex] {
			t.Fatalf("store %d is used by more than one scenario", scenario.StoreIndex)
		}
		seenStores[scenario.StoreIndex] = true

		if simulationNeedsReply(scenario.Kind) {
			needReplyCount++
			if scenario.Messages[len(scenario.Messages)-1].SenderType != enums.IMSenderTypeCustomer {
				t.Fatalf("need-reply scenario %s must end with a customer message", scenario.Key)
			}
		}
		if scenario.AssignmentAt != nil {
			assignmentCount++
			if scenario.AssigneeIndex <= 0 {
				t.Fatalf("assigned scenario %s has no assignee", scenario.Key)
			}
		}
	}

	wantKinds := map[simulationKind]int{
		simulationKindAI:         6,
		simulationKindPending:    9,
		simulationKindAssigned:   6,
		simulationKindProcessing: 6,
		simulationKindPriority:   3,
		simulationKindUrgent:     3,
		simulationKindClosed:     3,
	}
	for kind, want := range wantKinds {
		if kindCounts[kind] != want {
			t.Fatalf("kind %s count = %d, want %d", kind, kindCounts[kind], want)
		}
	}
	for teamIndex := 1; teamIndex <= 3; teamIndex++ {
		if teamCounts[teamIndex] != 12 {
			t.Fatalf("team %d scenario count = %d, want 12", teamIndex, teamCounts[teamIndex])
		}
	}
	if messageCount != expectedSimulationMessageCount {
		t.Fatalf("message count = %d, want %d", messageCount, expectedSimulationMessageCount)
	}
	if assignmentCount != expectedSimulationAssignmentCount {
		t.Fatalf("assignment count = %d, want %d", assignmentCount, expectedSimulationAssignmentCount)
	}
	if needReplyCount != expectedSimulationNeedReplyCount {
		t.Fatalf("need reply count = %d, want %d", needReplyCount, expectedSimulationNeedReplyCount)
	}
}

func TestSimulationManualTasksRemainAvailableForDispatchTesting(t *testing.T) {
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	for _, scenario := range buildSimulationScenarios(now) {
		if simulationNeedsReply(scenario.Kind) {
			if scenario.ManualExpireAt == nil || scenario.ManualExpireAt.Sub(now) < 12*time.Hour {
				t.Fatalf("manual scenario %s does not remain available for a test session", scenario.Key)
			}
		}
		if scenario.Kind == simulationKindClosed {
			if scenario.ClosedAt == nil || scenario.AssignmentAt == nil || !scenario.ClosedAt.After(*scenario.AssignmentAt) {
				t.Fatalf("closed scenario %s has invalid lifecycle", scenario.Key)
			}
		}
	}
}

func TestSimulationSenderIDMatchesRealMessageSemantics(t *testing.T) {
	agent := &models.User{}
	agent.ID = 42

	tests := []struct {
		name       string
		senderType enums.IMSenderType
		assignee   *models.User
		want       int64
		wantErr    bool
	}{
		{name: "customer", senderType: enums.IMSenderTypeCustomer, assignee: agent, want: 0},
		{name: "ai", senderType: enums.IMSenderTypeAI, assignee: agent, want: 0},
		{name: "assigned agent", senderType: enums.IMSenderTypeAgent, assignee: agent, want: 42},
		{name: "missing agent", senderType: enums.IMSenderTypeAgent, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := simulationSenderID(tt.senderType, tt.assignee)
			if (err != nil) != tt.wantErr {
				t.Fatalf("simulationSenderID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("simulationSenderID() = %d, want %d", got, tt.want)
			}
		})
	}
}
