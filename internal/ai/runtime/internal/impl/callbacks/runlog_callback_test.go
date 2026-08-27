package callbacks

import "testing"

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
