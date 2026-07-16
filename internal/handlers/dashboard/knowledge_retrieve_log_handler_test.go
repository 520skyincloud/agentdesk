package dashboard

import (
	"testing"

	"agent-desk/internal/pkg/dto/response"
)

func TestHideKnowledgeModelDiagnostics(t *testing.T) {
	tenantResponse := response.KnowledgeRetrieveLogResponse{
		PromptTokens: 11, CompletionTokens: 7, ModelName: "platform-model", TraceData: `{"model":"platform-model"}`,
	}
	hideKnowledgeModelDiagnostics(&tenantResponse, false)
	if tenantResponse.PromptTokens != 0 || tenantResponse.CompletionTokens != 0 || tenantResponse.ModelName != "" || tenantResponse.TraceData != "" {
		t.Fatalf("tenant response leaked model diagnostics: %+v", tenantResponse)
	}

	platformResponse := response.KnowledgeRetrieveLogResponse{
		PromptTokens: 11, CompletionTokens: 7, ModelName: "platform-model", TraceData: `{"model":"platform-model"}`,
	}
	hideKnowledgeModelDiagnostics(&platformResponse, true)
	if platformResponse.PromptTokens != 11 || platformResponse.CompletionTokens != 7 || platformResponse.ModelName != "platform-model" || platformResponse.TraceData == "" {
		t.Fatalf("platform response lost model diagnostics: %+v", platformResponse)
	}
}
