package main

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestScoreRecordRequiresEveryMultiQuestionOutcome(t *testing.T) {
	tests := []struct {
		id       string
		partial  record
		complete record
		missing  string
	}{
		{
			id: "C02",
			partial: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30。",
				Intent:       "hotel_info",
				KnowledgeHit: true,
			},
			complete: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30；停车免费；剃须刀可在自助区领取。",
				Intent:       "hotel_info",
				KnowledgeHit: true,
			},
			missing: "停车问题",
		},
		{
			id: "X03",
			partial: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30。",
				Intent:       "service_request",
				KnowledgeHit: true,
			},
			complete: record{
				Status:                "completed",
				ReplyText:             "早餐供应时间是7:00-9:30。",
				Intent:                "service_request",
				KnowledgeHit:          true,
				DeferredHandoff:       true,
				DeferredHandoffReason: "部分酒店业务问题需要门店同事接手；待处理问题：空调坏了",
			},
			missing: "空调故障处理",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			sc := findEvalScenario(t, tt.id)
			partialScore, partialIssues := scoreRecord(sc, tt.partial)
			if partialScore >= 80 {
				t.Fatalf("partial multi-question reply must fail, score=%d issues=%v", partialScore, partialIssues)
			}
			if !issuesContain(partialIssues, tt.missing) {
				t.Fatalf("expected missing outcome %q, got %v", tt.missing, partialIssues)
			}

			completeScore, completeIssues := scoreRecord(sc, tt.complete)
			if completeScore < 80 {
				t.Fatalf("complete multi-question outcome must pass, score=%d issues=%v", completeScore, completeIssues)
			}
		})
	}
}

func TestFillRecordFromRunLogCapturesDeferredHandoffAction(t *testing.T) {
	logItem := &models.AgentRunLog{
		FinalStatus: "completed",
		ReplyText:   "早餐供应时间是7:00-9:30。",
		TraceData:   `{"runtime":{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"部分酒店业务问题需要门店同事接手；待处理问题：空调坏了"}}}}`,
	}
	rec := (&runner{}).fillRecordFromRunLog(record{}, scenario{}, logItem)
	if !rec.DeferredHandoff || !strings.Contains(rec.DeferredHandoffReason, "空调坏了") {
		t.Fatalf("expected deferred handoff action in eval record, got %#v", rec)
	}
}

func findEvalScenario(t *testing.T, id string) scenario {
	t.Helper()
	for _, sc := range buildScenarios(1) {
		if sc.ID == id {
			return sc
		}
	}
	t.Fatalf("scenario %s not found", id)
	return scenario{}
}

func issuesContain(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}
