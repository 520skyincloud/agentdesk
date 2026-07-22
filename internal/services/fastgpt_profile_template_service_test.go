package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
)

func TestFastGPTProfileTemplateValidation(t *testing.T) {
	req := request.UpdateFastGPTProfileTemplateRequest{
		Name:           "门店知识库模型模板",
		Chat:           request.FastGPTProfileTemplateCredentialRequest{Provider: "AliCloud", BaseURL: "http://model.example.com/v1", Model: "qwen3.5-flash", APIMode: "chat_completions"},
		ASR:            request.FastGPTProfileTemplateCredentialRequest{Provider: "AliCloud", BaseURL: "http://model.example.com/v1", Model: "qwen3-asr-flash"},
		Embedding:      request.FastGPTProfileTemplateCredentialRequest{Provider: "DashScope", BaseURL: "http://model.example.com/v1", Model: "text-embedding-v4"},
		DocumentParser: request.FastGPTProfileTemplateCredentialRequest{Provider: "AliCloud", BaseURL: "http://model.example.com/v1", Model: "qwen3.5-flash"},
		Vision:         request.FastGPTProfileTemplateCredentialRequest{Provider: "AliCloud", BaseURL: "http://model.example.com/v1", Model: "qwen3.5-flash"},
		Rerank:         request.FastGPTProfileTemplateCredentialRequest{Provider: "DashScope", BaseURL: "http://model.example.com/v1", Model: "qwen3-vl-rerank"},
	}
	normalizeFastGPTProfileTemplateRequest(&req)
	if err := validateFastGPTProfileTemplateRequest(req); err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	req.Rerank.BaseURL = "not-a-url"
	if err := validateFastGPTProfileTemplateRequest(req); err == nil {
		t.Fatal("invalid rerank URL should be rejected")
	}
	req.Rerank.BaseURL = "http://another-gateway.example.com/v1"
	if err := validateFastGPTProfileTemplateRequest(req); err == nil {
		t.Fatal("mixed model gateways should be rejected")
	}
}

func TestBuildFastGPTTemplateProfileInputKeepsStoreKeysRemote(t *testing.T) {
	template := &models.FastGPTProfileTemplate{
		Name:              "门店知识库模型模板",
		EmbeddingProvider: "DashScope", EmbeddingBaseURL: "http://model.example.com/v1", EmbeddingModel: "text-embedding-v4",
		DocumentParserProvider: "AliCloud", DocumentParserBaseURL: "http://model.example.com/v1", DocumentParserModel: "qwen3.5-flash",
		VisionProvider: "AliCloud", VisionBaseURL: "http://model.example.com/v1", VisionModel: "qwen3.5-flash",
		RerankProvider: "DashScope", RerankBaseURL: "http://model.example.com/v1", RerankModel: "qwen3-vl-rerank",
	}
	profile := &FastGPTModelProfile{
		ID: "profile-3", Name: "高铁南站店知识库模型",
		Embedding:      fastgptapi.ModelCredential{KeyConfigured: true},
		DocumentParser: fastgptapi.ModelCredential{KeyConfigured: true},
		Vision:         fastgptapi.ModelCredential{KeyConfigured: true},
		Rerank:         &fastgptapi.ModelCredential{KeyConfigured: true},
	}
	input := buildFastGPTTemplateProfileInput(template, "dataset-3", profile)
	if input.ProfileID != profile.ID || input.DatasetID != "dataset-3" || input.Name != template.Name {
		t.Fatalf("identity fields changed: %#v", input)
	}
	if input.Embedding.APIKey != "" || input.DocumentParser.APIKey != "" || input.Vision.APIKey != "" || input.Rerank == nil || input.Rerank.APIKey != "" {
		t.Fatal("template propagation must not send or store API keys")
	}
	if input.Embedding.Model != "text-embedding-v4" || input.Rerank.Model != "qwen3-vl-rerank" {
		t.Fatalf("template models not applied: %#v", input)
	}
	if !profileHasAllKeys(profile) {
		t.Fatal("configured FastGPT profile should be eligible for template sync")
	}
}
