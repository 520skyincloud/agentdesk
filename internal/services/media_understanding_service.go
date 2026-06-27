package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var MediaUnderstandingService = newMediaUnderstandingService()

func newMediaUnderstandingService() *mediaUnderstandingService {
	return &mediaUnderstandingService{httpClient: &http.Client{Timeout: 60 * time.Second}}
}

type mediaUnderstandingService struct {
	httpClient *http.Client
}

type messageMediaPayload struct {
	AssetID      string         `json:"assetId"`
	Filename     string         `json:"filename"`
	MimeType     string         `json:"mimeType"`
	URL          string         `json:"url"`
	MediaText    string         `json:"mediaText,omitempty"`
	MediaSummary string         `json:"mediaSummary,omitempty"`
	MediaStatus  string         `json:"mediaUnderstandingStatus,omitempty"`
	MediaError   string         `json:"mediaUnderstandingError,omitempty"`
	WxMedia      map[string]any `json:"wxMedia,omitempty"`
}

func (s *mediaUnderstandingService) UnderstandInboundMessageAsync(messageID int64) {
	if messageID <= 0 {
		return
	}
	go func() {
		if err := s.UnderstandInboundMessage(context.Background(), messageID); err != nil {
			slog.Warn("understand inbound media failed", "message_id", messageID, "error", err)
		}
	}()
}

func (s *mediaUnderstandingService) UnderstandInboundMessage(ctx context.Context, messageID int64) error {
	message := repositories.MessageRepository.Get(sqls.DB(), messageID)
	if message == nil || message.SenderType != enums.IMSenderTypeCustomer {
		return nil
	}
	if !isUnderstandableMessageType(message.MessageType) {
		return nil
	}
	payload, err := parseMessageMediaPayload(message.Payload)
	if err != nil {
		return s.markMediaUnderstanding(message, nil, "failed", "媒体 payload 解析失败: "+err.Error())
	}
	if strings.TrimSpace(payload.MediaText) != "" || payload.MediaStatus == "understood" {
		return nil
	}

	var text string
	switch message.MessageType {
	case enums.IMMessageTypeImage:
		text, err = s.understandImage(ctx, payload)
	case enums.IMMessageTypeVoice:
		text, err = s.transcribeVoice(ctx, payload)
	case enums.IMMessageTypeAttachment:
		text, err = s.extractFileText(ctx, payload)
	default:
		return nil
	}
	if err != nil {
		return s.markMediaUnderstanding(message, payload, "failed", err.Error())
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return s.markMediaUnderstanding(message, payload, "empty", "媒体理解结果为空")
	}
	payload.MediaText = text
	payload.MediaSummary = limitText(text, 500)
	payload.MediaStatus = "understood"
	payload.MediaError = ""
	if err := s.updateMessagePayload(message.ID, payload); err != nil {
		return err
	}
	updated := repositories.MessageRepository.Get(sqls.DB(), message.ID)
	conversation := ConversationService.Get(message.ConversationID)
	if updated != nil && conversation != nil && TriggerAIReplyAsyncHook != nil && s.canTriggerAIForMedia(conversation.ID) {
		TriggerAIReplyAsyncHook(*conversation, *updated)
	}
	return nil
}

func isUnderstandableMessageType(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeAttachment:
		return true
	default:
		return false
	}
}

func parseMessageMediaPayload(raw string) (*messageMediaPayload, error) {
	payload := &messageMediaPayload{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *mediaUnderstandingService) understandImage(ctx context.Context, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("图片 payload 为空")
	}
	asset := AssetService.GetByAssetID(payload.AssetID)
	data, mimeType, err := s.readAssetBytes(asset, payload)
	if err != nil {
		return "", err
	}
	config, err := ai.GetEnabledAIConfig(enums.AIModelTypeVision)
	if err != nil {
		return "", err
	}
	imageURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	return s.callOpenAICompatibleVision(ctx, *config, imageURL)
}

func (s *mediaUnderstandingService) transcribeVoice(ctx context.Context, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("语音 payload 为空")
	}
	asset := AssetService.GetByAssetID(payload.AssetID)
	data, _, err := s.readAssetBytes(asset, payload)
	if err != nil {
		return "", err
	}
	config, err := ai.GetEnabledAIConfig(enums.AIModelTypeASR)
	if err != nil {
		return "", err
	}
	return s.callOpenAICompatibleASR(ctx, *config, payload.Filename, data)
}

func (s *mediaUnderstandingService) extractFileText(ctx context.Context, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("文件 payload 为空")
	}
	asset := AssetService.GetByAssetID(payload.AssetID)
	data, mimeType, err := s.readAssetBytes(asset, payload)
	if err != nil {
		return "", err
	}
	filename := strings.ToLower(strings.TrimSpace(payload.Filename))
	if strings.HasPrefix(mimeType, "text/") || strings.HasSuffix(filename, ".txt") || strings.HasSuffix(filename, ".md") || strings.HasSuffix(filename, ".csv") || strings.HasSuffix(filename, ".json") {
		return limitText(string(data), 4000), nil
	}
	return "", fmt.Errorf("当前文件类型 %s 只做展示和审计，尚未启用解析器", mimeType)
}

func (s *mediaUnderstandingService) readAssetBytes(asset *models.Asset, payload *messageMediaPayload) ([]byte, string, error) {
	if asset != nil && strings.TrimSpace(asset.StorageKey) != "" {
		reader, err := AssetService.OpenReader(asset)
		if err == nil {
			defer reader.Close()
			data, readErr := io.ReadAll(io.LimitReader(reader, 20<<20))
			if readErr != nil {
				return nil, "", readErr
			}
			mimeType := strings.TrimSpace(asset.MimeType)
			if mimeType == "" {
				mimeType = detectMimeType(payload.Filename, data)
			}
			return data, mimeType, nil
		}
	}
	downloadURL := mediaDownloadURL(payload)
	if payload == nil || downloadURL == "" {
		return nil, "", fmt.Errorf("媒体文件没有可读取的 asset 或 URL")
	}
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("下载媒体失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = detectMimeType(payload.Filename, data)
	}
	return data, strings.Split(mimeType, ";")[0], nil
}

func mediaDownloadURL(payload *messageMediaPayload) string {
	if payload == nil {
		return ""
	}
	if value := strings.TrimSpace(payload.URL); strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	for _, key := range []string{"url", "download_url", "file_url", "cdn_url", "file_id", "fileid", "fileId"} {
		if value := strings.TrimSpace(fmt.Sprint(payload.WxMedia[key])); strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
	}
	return ""
}

func detectMimeType(filename string, data []byte) string {
	if ext := strings.TrimSpace(filepath.Ext(filename)); ext != "" {
		if value := mime.TypeByExtension(ext); value != "" {
			return strings.Split(value, ";")[0]
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

func (s *mediaUnderstandingService) callOpenAICompatibleASR(ctx context.Context, config models.AIConfig, filename string, data []byte) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return "", fmt.Errorf("ASR 模型配置不完整")
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", strings.TrimSpace(config.ModelName))
	part, err := writer.CreateFormFile("file", defaultUploadFilename(filename, "voice.wav"))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/transcriptions", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.APIKey))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ASR 调用失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	for _, key := range []string{"text", "content", "transcription"} {
		if value := strings.TrimSpace(fmt.Sprint(parsed[key])); value != "" && value != "<nil>" {
			return value, nil
		}
	}
	return "", fmt.Errorf("ASR 返回中没有 text 字段")
}

func (s *mediaUnderstandingService) callOpenAICompatibleVision(ctx context.Context, config models.AIConfig, imageURL string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return "", fmt.Errorf("视觉/多模态模型配置不完整")
	}
	body := map[string]any{
		"model": strings.TrimSpace(config.ModelName),
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "你是酒店前台同事的图片理解助手。只描述图片中能确定的信息，不猜测，不写固定表情。输出一句简洁中文，必要时说明需要人工确认。",
			},
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "请识别这张客人发来的图片，提取与酒店服务相关的信息。"},
					{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
				},
			},
		},
	}
	if config.MaxOutputTokens > 0 {
		body["max_tokens"] = config.MaxOutputTokens
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("视觉模型调用失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("视觉模型没有返回结果")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func defaultUploadFilename(filename, fallback string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return fallback
	}
	return filename
}

func (s *mediaUnderstandingService) markMediaUnderstanding(message *models.Message, payload *messageMediaPayload, status string, errText string) error {
	if message == nil {
		return nil
	}
	if payload == nil {
		parsed, _ := parseMessageMediaPayload(message.Payload)
		payload = parsed
	}
	if payload == nil {
		payload = &messageMediaPayload{}
	}
	payload.MediaStatus = strings.TrimSpace(status)
	payload.MediaError = limitText(errText, 500)
	return s.updateMessagePayload(message.ID, payload)
}

func (s *mediaUnderstandingService) updateMessagePayload(messageID int64, payload *messageMediaPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return repositories.MessageRepository.Updates(sqls.DB(), messageID, map[string]any{
		"payload":          string(data),
		"updated_at":       time.Now(),
		"update_user_name": "media_understanding",
	})
}

func (s *mediaUnderstandingService) canTriggerAIForMedia(conversationID int64) bool {
	state := ConversationRouteService.GetByConversationID(conversationID)
	if state == nil {
		return true
	}
	switch state.RouteStatus {
	case enums.ConversationRouteStatusStoreWecomManual,
		enums.ConversationRouteStatusHQQiyuPending,
		enums.ConversationRouteStatusHQQiyuServing,
		enums.ConversationRouteStatusHQAgentDeskPending,
		enums.ConversationRouteStatusHQAgentDeskServing:
		return false
	}
	if state.WxWorkInstanceID > 0 {
		instance := WxWorkProtocolInstanceService.Get(state.WxWorkInstanceID)
		if instance != nil && !instance.AIReplyEnabled {
			return false
		}
	}
	return true
}
