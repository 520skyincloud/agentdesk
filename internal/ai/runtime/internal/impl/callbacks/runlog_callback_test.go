package callbacks

import (
	"strings"
	"testing"

	"agent-desk/internal/pkg/toolx"
)

func TestRuntimeTraceCollectorRedactsPMSLookupData(t *testing.T) {
	for _, status := range []string{"ok", "error", "blocked"} {
		t.Run(status, func(t *testing.T) {
			collector := NewRuntimeTraceCollector()
			args := map[string]any{"action": "member_info_by_phone", "phone": "13800138000", "gradeId": "private-grade"}
			collector.AddToolItem(ToolTraceItem{
				ToolCode: toolx.BuiltinPMSQuery.Code, Arguments: args,
				Status: status, Blocked: status == "blocked", LatencyMs: 12,
				ResultPreview: `{"name":"private-name","phone":"13800138000"}`,
				ErrorMessage:  "GET /?phone=13800138000&token=private-token: failed",
			})
			for _, sensitive := range []string{"13800138000", "private-grade", "private-name", "private-token"} {
				if strings.Contains(collector.Marshal(), sensitive) {
					t.Fatalf("PMS trace leaked %q", sensitive)
				}
			}
			item := collector.Data.Tools.Items[0]
			if item.Arguments["action"] != "member_info_by_phone" || item.Status != status || item.LatencyMs != 12 {
				t.Fatalf("lost non-sensitive diagnostics: %#v", item)
			}
			if args["phone"] != "13800138000" {
				t.Fatal("redaction mutated actual tool arguments")
			}
		})
	}
}

func TestRuntimeTraceCollectorPMSNameFallbackAndOtherTools(t *testing.T) {
	collector := NewRuntimeTraceCollector()
	collector.AddToolItem(ToolTraceItem{
		ToolName: toolx.BuiltinPMSQuery.Name, Arguments: map[string]any{"action": "13800138000", "phone": "13800138000"},
	})
	if strings.Contains(collector.Marshal(), "13800138000") {
		t.Fatal("name-only PMS trace leaked invalid arguments")
	}
	collector.AddToolItem(ToolTraceItem{
		ToolCode: "builtin/get_weather", Arguments: map[string]any{"city": "Hefei"}, ResultPreview: "rain",
	})
	item := collector.Data.Tools.Items[1]
	if item.Arguments["city"] != "Hefei" || item.ResultPreview != "rain" {
		t.Fatal("PMS privacy rule changed unrelated tool traces")
	}
}

func TestRuntimeTraceCollectorDeepCopiesEvidenceAndReplyPlanFacts(t *testing.T) {
	collector := NewRuntimeTraceCollector()
	fact := KnowledgeEvidenceFactTraceData{
		FactID:         "F1",
		Aspect:         "quantity",
		Statement:      "房间内有两瓶矿泉水",
		CriticalValues: []string{"两瓶"},
	}
	judge := KnowledgeEvidenceJudgeTraceData{Tasks: []KnowledgeEvidenceJudgeTaskTraceData{{
		TaskID:         "task-1",
		SupportedFacts: []KnowledgeEvidenceFactTraceData{fact},
		MissingAspects: []string{"price"},
		Layers: []KnowledgeEvidenceJudgeLayerTraceData{{
			Layer:          "store",
			SupportedFacts: []KnowledgeEvidenceFactTraceData{fact},
			MissingAspects: []string{"price"},
		}},
	}}}
	collector.SetKnowledgeEvidenceJudge(judge)
	judge.Tasks[0].SupportedFacts[0].CriticalValues[0] = "changed"
	judge.Tasks[0].MissingAspects[0] = "changed"
	judge.Tasks[0].Layers[0].SupportedFacts[0].CriticalValues[0] = "changed"
	judge.Tasks[0].Layers[0].MissingAspects[0] = "changed"

	storedJudge := collector.Data.Pipeline.EvidenceJudge.Tasks[0]
	if storedJudge.SupportedFacts[0].CriticalValues[0] != "两瓶" || storedJudge.MissingAspects[0] != "price" {
		t.Fatalf("judge task trace shares mutable slices: %#v", storedJudge)
	}
	if storedJudge.Layers[0].SupportedFacts[0].CriticalValues[0] != "两瓶" || storedJudge.Layers[0].MissingAspects[0] != "price" {
		t.Fatalf("judge layer trace shares mutable slices: %#v", storedJudge.Layers[0])
	}

	planFact := KnowledgeEvidenceFactTraceData{
		FactID:         "F1",
		Aspect:         "quantity",
		Statement:      "房间内有两瓶矿泉水",
		CriticalValues: []string{"两瓶"},
	}
	plan := ReplyPlanTraceData{TaskPlans: []ReplyTaskPlanTraceData{{
		TaskID:         "task-1",
		SourceRefs:     []string{"U1"},
		SupportedFacts: []KnowledgeEvidenceFactTraceData{planFact},
		MissingAspects: []string{"price"},
	}}}
	collector.SetReplyPlan(plan)
	plan.TaskPlans[0].SourceRefs[0] = "changed"
	plan.TaskPlans[0].SupportedFacts[0].CriticalValues[0] = "changed"
	plan.TaskPlans[0].MissingAspects[0] = "changed"

	storedPlan := collector.Data.Pipeline.ReplyPlan.TaskPlans[0]
	if storedPlan.SourceRefs[0] != "U1" || storedPlan.SupportedFacts[0].CriticalValues[0] != "两瓶" || storedPlan.MissingAspects[0] != "price" {
		t.Fatalf("reply plan trace shares mutable slices: %#v", storedPlan)
	}
}
