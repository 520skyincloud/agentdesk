package services

import (
	"testing"

	"agent-desk/internal/pkg/dto/request"
)

func TestBuildKnowledgeBaseModelUsesLowerDefaultScoreThreshold(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{}, 101)
	if err != nil {
		t.Fatalf("build knowledge base model failed: %v", err)
	}
	if item.DefaultScoreThreshold != 0.2 {
		t.Fatalf("expected default score threshold 0.2, got %v", item.DefaultScoreThreshold)
	}
}

func TestBuildKnowledgeBaseModelDoesNotRequireIntentProfile(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{Name: "独立门店 FastGPT"}, 101)
	if err != nil {
		t.Fatalf("knowledge base without industry profile should be valid: %v", err)
	}
	if item.IntentProfileID != 0 {
		t.Fatalf("intent profile should remain optional, got %d", item.IntentProfileID)
	}
}
