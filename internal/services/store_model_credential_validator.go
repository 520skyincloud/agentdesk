package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
)

type storeCredentialSlotValidator interface {
	Validate(context.Context, *models.ModelProfileTemplate, []models.ModelProfileSlot, string) error
}

type newAPIStoreCredentialValidator struct{}

type storeCredentialValidationError struct {
	UsageCode enums.ModelUsageSlot
	Class     string
	retryable bool
}

func (e *storeCredentialValidationError) Error() string {
	return fmt.Sprintf("model slot %s validation failed (%s)", e.UsageCode, e.Class)
}

func (v *newAPIStoreCredentialValidator) Validate(ctx context.Context, template *models.ModelProfileTemplate, slots []models.ModelProfileSlot, apiKey string) error {
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) > 0 {
		return &storeCredentialValidationError{Class: "profile_invalid"}
	}
	slotByUsage := make(map[enums.ModelUsageSlot]models.ModelProfileSlot, len(slots))
	for _, slot := range slots {
		slotByUsage[slot.UsageCode] = slot
	}
	for _, spec := range RequiredModelUsageSlotSpecs() {
		slot, ok := slotByUsage[spec.UsageCode]
		if !ok {
			return &storeCredentialValidationError{UsageCode: spec.UsageCode, Class: "slot_missing"}
		}
		if !slot.Enabled {
			if spec.Optional {
				continue
			}
			return &storeCredentialValidationError{UsageCode: spec.UsageCode, Class: "slot_disabled"}
		}
		attempts := slot.MaxRetryCount + 1
		if attempts < 1 {
			attempts = 1
		}
		if attempts > 3 {
			attempts = 3
		}
		var err error
		for attempt := 0; attempt < attempts; attempt++ {
			err = v.validateSlot(ctx, template.GatewayBaseURL, slot, apiKey)
			if err == nil {
				break
			}
			var validationErr *storeCredentialValidationError
			if !errors.As(err, &validationErr) || !validationErr.retryable || attempt == attempts-1 {
				return err
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "timeout"}
			case <-timer.C:
			}
		}
	}
	return nil
}

func (v *newAPIStoreCredentialValidator) validateSlot(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string) error {
	timeout := time.Duration(slot.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch slot.ModelType {
	case enums.AIModelTypeLLM:
		return v.validateTextModel(callCtx, baseURL, slot, apiKey, slot.UsageCode == enums.ModelUsageSlotDocumentParser)
	case enums.AIModelTypeVision:
		return v.validateVision(callCtx, baseURL, slot, apiKey)
	case enums.AIModelTypeASR:
		return v.validateASR(callCtx, baseURL, slot, apiKey)
	case enums.AIModelTypeEmbedding:
		return v.validateEmbedding(callCtx, baseURL, slot, apiKey)
	case enums.AIModelTypeRerank:
		return v.validateRerank(callCtx, baseURL, slot, apiKey)
	default:
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "model_type_unsupported"}
	}
}

func (v *newAPIStoreCredentialValidator) validateTextModel(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string, documentParser bool) error {
	prompt := "只回复 OK。"
	if documentParser {
		prompt = "从文本中提取酒店名称：合成验收酒店。只回复酒店名称。"
	}
	if strings.EqualFold(slot.APIMode, "responses") {
		return v.doJSON(ctx, baseURL, "/responses", slot, apiKey, map[string]any{
			"model": slot.ModelName, "instructions": "这是连接验证。", "input": prompt, "max_output_tokens": 16,
		}, validateResponsesPayload)
	}
	payload := map[string]any{
		"model":      slot.ModelName,
		"messages":   []map[string]any{{"role": "system", "content": "这是连接验证。"}, {"role": "user", "content": prompt}},
		"max_tokens": 16,
	}
	if modelconfig.IsDeepSeekV4Model(slot.ModelName) {
		payload["thinking"] = map[string]any{"type": "disabled"}
	}
	return v.doJSON(ctx, baseURL, "/chat/completions", slot, apiKey, payload, validateChatPayload)
}

func (v *newAPIStoreCredentialValidator) validateVision(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string) error {
	if strings.EqualFold(slot.APIMode, "responses") {
		return v.doJSON(ctx, baseURL, "/responses", slot, apiKey, map[string]any{
			"model": slot.ModelName,
			"input": []map[string]any{{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "简短描述图片。"},
				{"type": "input_image", "image_url": visionConnectionTestImage},
			}}},
			"max_output_tokens": 16,
		}, validateResponsesPayload)
	}
	return v.doJSON(ctx, baseURL, "/chat/completions", slot, apiKey, map[string]any{
		"model": slot.ModelName,
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "text", "text": "简短描述图片。"},
			{"type": "image_url", "image_url": map[string]any{"url": visionConnectionTestImage}},
		}}},
		"max_tokens": 16,
	}, validateChatPayload)
}

func (v *newAPIStoreCredentialValidator) validateASR(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("model", slot.ModelName); err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	part, err := writer.CreateFormFile("file", "credential-test.wav")
	if err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	if _, err := part.Write(storeCredentialSilentWAV()); err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	if err := writer.Close(); err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, modelGatewayEndpoint(baseURL, "/audio/transcriptions"), body)
	if err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	raw, err := executeCredentialValidationRequest(req, slot.UsageCode)
	if err != nil {
		return err
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "invalid_response"}
	}
	for _, key := range []string{"text", "content", "transcription"} {
		if _, exists := payload[key]; exists {
			return nil
		}
	}
	return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "invalid_response"}
}

func (v *newAPIStoreCredentialValidator) validateEmbedding(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string) error {
	return v.doJSON(ctx, baseURL, "/embeddings", slot, apiKey, map[string]any{
		"model": slot.ModelName, "input": "酒店前台连接验证",
	}, func(raw []byte) bool {
		var payload struct {
			Data []json.RawMessage `json:"data"`
		}
		return json.Unmarshal(raw, &payload) == nil && len(payload.Data) > 0
	})
}

func (v *newAPIStoreCredentialValidator) validateRerank(ctx context.Context, baseURL string, slot models.ModelProfileSlot, apiKey string) error {
	return v.doJSON(ctx, baseURL, "/rerank", slot, apiKey, map[string]any{
		"model": slot.ModelName, "query": "早餐时间", "documents": []string{"早餐七点开始", "停车场在负一层"}, "top_n": 1,
	}, func(raw []byte) bool {
		var payload struct {
			Results []json.RawMessage `json:"results"`
			Data    []json.RawMessage `json:"data"`
		}
		return json.Unmarshal(raw, &payload) == nil && (len(payload.Results) > 0 || len(payload.Data) > 0)
	})
}

func (v *newAPIStoreCredentialValidator) doJSON(ctx context.Context, baseURL, path string, slot models.ModelProfileSlot, apiKey string, payload any, validate func([]byte) bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, modelGatewayEndpoint(baseURL, path), bytes.NewReader(raw))
	if err != nil {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "request_build_failed"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	responseBody, err := executeCredentialValidationRequest(req, slot.UsageCode)
	if err != nil {
		return err
	}
	if !validate(responseBody) {
		return &storeCredentialValidationError{UsageCode: slot.UsageCode, Class: "invalid_response"}
	}
	return nil
}

func executeCredentialValidationRequest(req *http.Request, usageCode enums.ModelUsageSlot) ([]byte, error) {
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		class := "network_error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(req.Context().Err(), context.DeadlineExceeded) {
			class = "timeout"
		}
		return nil, &storeCredentialValidationError{UsageCode: usageCode, Class: class, retryable: true}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return nil, &storeCredentialValidationError{UsageCode: usageCode, Class: "invalid_response", retryable: true}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	class := "upstream_error"
	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		class = "credential_rejected"
	case http.StatusNotFound:
		class = "endpoint_not_found"
	case http.StatusBadRequest:
		class = "model_or_payload_rejected"
	case http.StatusTooManyRequests:
		class = "rate_limited"
	}
	return nil, &storeCredentialValidationError{UsageCode: usageCode, Class: class, retryable: retryable}
}

func validateChatPayload(raw []byte) bool {
	var payload struct {
		Choices []json.RawMessage `json:"choices"`
	}
	return json.Unmarshal(raw, &payload) == nil && len(payload.Choices) > 0
}

func validateResponsesPayload(raw []byte) bool {
	var payload struct {
		OutputText string            `json:"output_text"`
		Output     []json.RawMessage `json:"output"`
	}
	return json.Unmarshal(raw, &payload) == nil && (strings.TrimSpace(payload.OutputText) != "" || len(payload.Output) > 0)
}

func modelGatewayEndpoint(baseURL, path string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/" + strings.TrimLeft(path, "/")
}

func storeCredentialSilentWAV() []byte {
	const sampleRate = 16000
	const samples = sampleRate / 4
	dataSize := samples * 2
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}
