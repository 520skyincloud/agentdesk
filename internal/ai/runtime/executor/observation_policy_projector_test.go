package executor

import "testing"

func TestProjectObservationMinimumPrivilege(t *testing.T) {
	source := MediaSource{MessageID: 1350, SourceRevision: 1, SourceType: "image"}

	allowed, forbidden := ProjectObservation(source, MediaObservationCandidate{
		ObservationType: "scene_description", ContentRole: "visible_scene", Text: "一张自拍", Confidence: 0.95,
	})
	if !ObservationSupportsUse(allowed, "describe_media") {
		t.Fatal("visible_scene must allow describe_media")
	}
	if ObservationSupportsUse(allowed, "build_knowledge_query") {
		t.Fatal("visible_scene must not allow knowledge query")
	}
	if !ObservationSupportsUse(forbidden, "store_address") {
		t.Fatal("forbidden list must contain store_address")
	}

	// 截图里的历史对话：只可描述/引用，禁止作为知识查询对象。
	allowed, _ = ProjectObservation(source, MediaObservationCandidate{
		ObservationType: "historical_statement", ContentRole: "embedded_historical_conversation", Text: "图中升房对话", Confidence: 0.9,
	})
	if ObservationSupportsUse(allowed, "build_knowledge_query") {
		t.Fatal("embedded historical conversation must not feed knowledge query")
	}
	if !ObservationSupportsUse(allowed, "quote_document") {
		t.Fatal("embedded historical conversation must allow quoting")
	}

	// 当前语音输入：L5 不重复注入 transcript，allowed 为空。
	allowed, _ = ProjectObservation(MediaSource{SourceType: "voice"}, MediaObservationCandidate{
		ObservationType: "transcript", ContentRole: "customer_spoken_input", Text: "有拖鞋吗", Confidence: 0.98,
	})
	if len(allowed) != 0 {
		t.Fatalf("current voice transcript must not re-enter L5, got %v", allowed)
	}

	// unknown：只保留 describe_media。
	allowed, _ = ProjectObservation(source, MediaObservationCandidate{ContentRole: "unknown", Text: "?"})
	if len(allowed) != 1 || allowed[0] != "describe_media" {
		t.Fatalf("unknown role must be describe_media only, got %v", allowed)
	}
}
