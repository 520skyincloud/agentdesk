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
	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

var MediaUnderstandingService = newMediaUnderstandingService()

func newMediaUnderstandingService() *mediaUnderstandingService {
	return &mediaUnderstandingService{httpClient: usagex.NewHTTPClient(60 * time.Second)}
}

type mediaUnderstandingService struct {
	httpClient *http.Client
}

type upstreamModelUsage struct {
	RequestID          string
	PromptTokens       int64
	CompletionTokens   int64
	CachedPromptTokens int64
	ReasoningTokens    int64
}

const visionConnectionTestImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9JPGcAAAAASUVORK5CYII="

// TestVisionConfig verifies the same OpenAI-compatible image path used by media understanding.
func (s *mediaUnderstandingService) TestVisionConfig(ctx context.Context, config models.AIConfig) error {
	config.MaxOutputTokens = 32
	content, err := s.callOpenAICompatibleVision(ctx, config, visionConnectionTestImage)
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("视觉模型返回为空")
	}
	return nil
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
		s.triggerAIForUnderstoodMedia(message)
		return nil
	}
	if payload.MediaStatus == "failed" || payload.MediaStatus == "empty" {
		payload.MediaStatus = "retrying"
		payload.MediaError = ""
		if err := s.updateMessagePayload(message.ID, message.TenantID, payload); err != nil {
			return err
		}
	}

	var text string
	switch message.MessageType {
	case enums.IMMessageTypeImage:
		text, err = s.understandImage(ctx, message, payload)
	case enums.IMMessageTypeVoice:
		text, err = s.transcribeVoice(ctx, message, payload)
	case enums.IMMessageTypeAttachment:
		text, err = s.extractFileText(ctx, message, payload)
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
	if err := s.updateMessagePayload(message.ID, message.TenantID, payload); err != nil {
		return err
	}
	updated := repositories.MessageRepository.GetInTenant(sqls.DB(), message.ID, message.TenantID)
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), message.ConversationID, message.TenantID)
	if updated != nil && conversation != nil {
		WsService.PublishMessageUpdated(conversation, updated)
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if updated != nil && conversation != nil && TriggerAIReplyAsyncHook != nil && s.canTriggerAIForMedia(conversation.ID, conversation.TenantID) {
		if followUp := s.latestCustomerFollowUp(*updated); followUp != nil {
			TriggerAIReplyAsyncHook(*conversation, *followUp)
		} else if s.mediaUnderstandingShouldTriggerAI(*updated) {
			TriggerAIReplyAsyncHook(*conversation, *updated)
		}
	}
	return nil
}

func (s *mediaUnderstandingService) triggerAIForUnderstoodMedia(message *models.Message) {
	if message == nil || TriggerAIReplyAsyncHook == nil {
		return
	}
	updated := repositories.MessageRepository.GetInTenant(sqls.DB(), message.ID, message.TenantID)
	if updated == nil {
		updated = message
	}
	conversation := ConversationService.GetByTenantID(updated.ConversationID, updated.TenantID)
	if conversation == nil || !s.canTriggerAIForMedia(conversation.ID, conversation.TenantID) {
		return
	}
	if followUp := s.latestCustomerFollowUp(*updated); followUp != nil {
		TriggerAIReplyAsyncHook(*conversation, *followUp)
		return
	}
	if s.mediaUnderstandingShouldTriggerAI(*updated) {
		TriggerAIReplyAsyncHook(*conversation, *updated)
	}
}

func (s *mediaUnderstandingService) latestCustomerFollowUp(mediaMessage models.Message) *models.Message {
	if mediaMessage.ConversationID <= 0 || mediaMessage.ID <= 0 || mediaMessage.SentAt == nil {
		return nil
	}
	latest, err := MessageService.FindLatestByConversationIDInTenant(mediaMessage.ConversationID, mediaMessage.TenantID)
	if err != nil || latest == nil || latest.ID <= mediaMessage.ID || latest.SenderType != enums.IMSenderTypeCustomer {
		return nil
	}
	if latest.SentAt == nil || latest.SentAt.Sub(*mediaMessage.SentAt) > 8*time.Second {
		return nil
	}
	switch latest.MessageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
		return latest
	default:
		return nil
	}
}

func (s *mediaUnderstandingService) mediaUnderstandingLooksActionable(message models.Message) bool {
	mediaText, mediaSummary, mediaStatus := replyengine.MediaUnderstandingFromPayload(message.Payload)
	if strings.TrimSpace(mediaStatus) != "understood" {
		return false
	}
	return replyengine.MediaUnderstandingHasActionableIntent(strings.Join([]string{mediaText, mediaSummary}, " "))
}

func (s *mediaUnderstandingService) mediaUnderstandingShouldTriggerAI(message models.Message) bool {
	mediaText, mediaSummary, mediaStatus := replyengine.MediaUnderstandingFromPayload(message.Payload)
	if strings.TrimSpace(mediaStatus) != "understood" {
		return false
	}
	if message.MessageType == enums.IMMessageTypeVoice {
		return strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != ""
	}
	return replyengine.MediaUnderstandingHasActionableIntent(strings.Join([]string{mediaText, mediaSummary}, " "))
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

func (s *mediaUnderstandingService) understandImage(ctx context.Context, message *models.Message, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("图片 payload 为空")
	}
	asset := AssetService.GetByAssetIDInTenant(payload.AssetID, message.TenantID)
	data, mimeType, err := s.readAssetBytes(message, asset, payload)
	if err != nil {
		return "", err
	}
	resolved, err := StoreAIModelSettingService.ResolveForMessage(message, StoreAIModelUsageMediaUnderstanding)
	if err != nil {
		return "", err
	}
	imageURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	startedAt := time.Now()
	modelCtx, usageCapture := usagex.WithCapture(ctx)
	text, usage, err := s.callOpenAICompatibleVisionWithUsage(modelCtx, resolved.Config, imageURL)
	s.recordMediaModelUsage(message, resolved.Config, resolved.Source, "vision", usage, lastUsageReceipt(usageCapture), time.Since(startedAt).Milliseconds(), err)
	return text, err
}

func (s *mediaUnderstandingService) transcribeVoice(ctx context.Context, message *models.Message, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("语音 payload 为空")
	}
	var protocolErr error
	if text, err := s.transcribeWxWorkVoice(ctx, message, payload); err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	} else if err != nil {
		protocolErr = err
	}
	asset := AssetService.GetByAssetIDInTenant(payload.AssetID, message.TenantID)
	data, _, err := s.readAssetBytes(message, asset, payload)
	if err != nil {
		if protocolErr != nil {
			return "", fmt.Errorf("企微语音翻译失败: %v；语音文件下载失败: %w", protocolErr, err)
		}
		return "", err
	}
	config, err := ai.GetEnabledAIConfig(enums.AIModelTypeASR)
	if err != nil {
		if protocolErr != nil {
			return "", fmt.Errorf("企微语音翻译失败: %v；ASR 模型配置失败: %w", protocolErr, err)
		}
		return "", err
	}
	startedAt := time.Now()
	modelCtx, usageCapture := usagex.WithCapture(ctx)
	text, usage, err := s.callOpenAICompatibleASRWithUsage(modelCtx, *config, payload.Filename, data)
	s.recordMediaModelUsage(message, *config, StoreAIModelSourceGlobalDefault, "asr", usage, lastUsageReceipt(usageCapture), time.Since(startedAt).Milliseconds(), err)
	if err != nil && protocolErr != nil {
		return "", fmt.Errorf("企微语音翻译失败: %v；ASR 调用失败: %w", protocolErr, err)
	}
	return text, err
}

func (s *mediaUnderstandingService) transcribeWxWorkVoice(ctx context.Context, message *models.Message, payload *messageMediaPayload) (string, error) {
	if message == nil || payload == nil {
		return "", fmt.Errorf("语音消息为空")
	}
	state := ConversationRouteService.GetByConversationIDInTenant(message.ConversationID, message.TenantID)
	if state == nil || state.WxWorkInstanceID <= 0 {
		return "", fmt.Errorf("会话缺少企微员工号实例绑定")
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(state.WxWorkInstanceID, message.TenantID)
	if instance == nil {
		return "", fmt.Errorf("企微员工号实例不存在")
	}
	channel := ChannelService.Get(instance.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return "", fmt.Errorf("企微协议渠道不存在")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return "", err
	}
	conversationID := strings.TrimSpace(wxWorkProtocolVoiceConversationID(message.Payload, message.ConversationID, message.TenantID))
	msgID := strings.TrimSpace(wxWorkProtocolVoiceMsgID(message.Payload))
	if msgID == "" && len(payload.WxMedia) > 0 {
		msgID = nonNilString(payload.WxMedia["msg_id"])
	}
	if conversationID == "" || msgID == "" {
		refConversationID, refMsgID := waitWxWorkProtocolVoiceRefIDs(ctx, message.ID, message.TenantID, 3*time.Second)
		if conversationID == "" || msgID == "" {
			if conversationID == "" {
				conversationID = refConversationID
			}
			if msgID == "" {
				msgID = refMsgID
			}
		}
	}
	if conversationID == "" || msgID == "" {
		return "", fmt.Errorf("语音消息缺少企微 conversation_id 或 msgid")
	}
	guid := strings.TrimSpace(instance.Guid)
	applyResp, err := WxWorkProtocolService.postJSON(cfg, "/msg/apply_voice_id", map[string]any{
		"guid":            guid,
		"conversation_id": conversationID,
		"msgid":           msgID,
	})
	if err != nil {
		return "", err
	}
	voiceID := extractProtocolDataString(applyResp, "voiceid", "voice_id", "id")
	if voiceID == "" {
		return "", fmt.Errorf("企微语音翻译申请未返回 voiceid")
	}
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	seqID := "0"
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		queryResp, queryErr := s.queryWxWorkVoiceText(cfg, guid, conversationID, msgID, voiceID, seqID)
		if queryErr != nil {
			lastErr = queryErr
		} else if text := extractProtocolDataString(queryResp, "text", "content"); text != "" {
			return text, nil
		} else {
			if nextSeqID := extractProtocolDataString(queryResp, "seqid", "seq_id"); nextSeqID != "" {
				seqID = nextSeqID
			}
			lastErr = fmt.Errorf("企微语音翻译结果为空")
		}
		if !sleepContext(ctx, time.Second) {
			return "", ctx.Err()
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("企微语音翻译超时")
}

func (s *mediaUnderstandingService) queryWxWorkVoiceText(cfg *dto.WxWorkProtocolChannelConfig, guid string, conversationID string, msgID string, voiceID string, seqID string) (string, error) {
	base := map[string]any{
		"guid":    guid,
		"voiceid": voiceID,
		"seqid":   seqID,
	}
	candidates := wxWorkVoiceTextQueryCandidates(conversationID, msgID)
	var lastErr error
	for _, candidate := range candidates {
		body := map[string]any{}
		for key, value := range base {
			body[key] = value
		}
		for key, value := range candidate {
			body[key] = value
		}
		resp, err := WxWorkProtocolService.postJSON(cfg, "/msg/query_voice_text", body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func wxWorkVoiceTextQueryCandidates(conversationID string, msgID string) []map[string]any {
	conversationID = strings.TrimSpace(conversationID)
	msgID = strings.TrimSpace(msgID)
	seen := map[string]bool{}
	candidates := make([]map[string]any, 0, 4)
	appendCandidate := func(cid string, mid string) {
		cid = strings.TrimSpace(cid)
		mid = strings.TrimSpace(mid)
		key := cid + "\x00" + mid
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, map[string]any{"conversation_id": cid, "msgid": mid})
	}
	appendCandidate(conversationID, msgID)
	if strings.HasPrefix(conversationID, "S:") || strings.HasPrefix(conversationID, "R:") {
		appendCandidate(strings.TrimPrefix(strings.TrimPrefix(conversationID, "S:"), "R:"), msgID)
	}
	appendCandidate("", msgID)
	return candidates
}

func (s *mediaUnderstandingService) extractFileText(ctx context.Context, message *models.Message, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("文件 payload 为空")
	}
	asset := AssetService.GetByAssetIDInTenant(payload.AssetID, message.TenantID)
	data, mimeType, err := s.readAssetBytes(message, asset, payload)
	if err != nil {
		return "", err
	}
	filename := strings.ToLower(strings.TrimSpace(payload.Filename))
	if strings.HasPrefix(mimeType, "text/") || strings.HasSuffix(filename, ".txt") || strings.HasSuffix(filename, ".md") || strings.HasSuffix(filename, ".csv") || strings.HasSuffix(filename, ".json") {
		return limitText(string(data), 4000), nil
	}
	return "", fmt.Errorf("当前文件类型 %s 只做展示和审计，尚未启用解析器", mimeType)
}

func (s *mediaUnderstandingService) readAssetBytes(message *models.Message, asset *models.Asset, payload *messageMediaPayload) ([]byte, string, error) {
	var recoverErr error
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
		var recovered *models.Asset
		recovered, recoverErr = s.recoverWxWorkMediaAsset(message, payload)
		if recoverErr == nil && recovered != nil {
			return s.readAssetBytes(nil, recovered, payload)
		}
	}
	var recovered *models.Asset
	recovered, recoverErr = s.recoverWxWorkMediaAsset(message, payload)
	if recoverErr == nil && recovered != nil {
		return s.readAssetBytes(nil, recovered, payload)
	}
	downloadURL := mediaDownloadURL(payload)
	if payload == nil || downloadURL == "" {
		if recoverErr != nil && len(payload.WxMedia) > 0 {
			return nil, "", fmt.Errorf("企微媒体二次下载失败: %w", recoverErr)
		}
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

func (s *mediaUnderstandingService) recoverWxWorkMediaAsset(message *models.Message, payload *messageMediaPayload) (*models.Asset, error) {
	if message == nil || payload == nil || len(payload.WxMedia) == 0 {
		return nil, fmt.Errorf("没有可用于二次下载的企微媒体参数")
	}
	state := ConversationRouteService.GetByConversationIDInTenant(message.ConversationID, message.TenantID)
	if state == nil || state.WxWorkInstanceID <= 0 {
		return nil, fmt.Errorf("会话缺少企微员工号实例绑定")
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(state.WxWorkInstanceID, message.TenantID)
	if instance == nil {
		return nil, fmt.Errorf("企微员工号实例不存在")
	}
	media := request.WxProtocolMediaPayload{}
	fillMediaPayloadFromMap(&media, payload.WxMedia)
	if strings.TrimSpace(media.FileID) == "" {
		return nil, fmt.Errorf("企微媒体参数缺少 file_id")
	}
	asset, err := WxWorkProtocolService.downloadInboundMediaToAsset(instance, message.MessageType, request.WxProtocolChatMsg{}, media, payload.Filename, payload.MimeType)
	if err != nil {
		return nil, err
	}
	payload.AssetID = asset.AssetID
	payload.Filename = asset.Filename
	payload.MimeType = asset.MimeType
	payload.MediaError = ""
	if message.ID > 0 {
		if err := s.updateMessagePayload(message.ID, message.TenantID, payload); err != nil {
			slog.Warn("update recovered wxwork media asset payload failed", "message_id", message.ID, "error", err)
		}
	}
	return asset, nil
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

func wxWorkProtocolVoiceMsgID(rawPayload string) string {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawPayload)), &root); err != nil {
		return ""
	}
	for _, key := range []string{"msg_id", "msgid", "id"} {
		if value := strings.TrimSpace(fmt.Sprint(root[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func wxWorkProtocolVoiceConversationID(rawPayload string, conversationID, tenantID int64) string {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawPayload)), &root); err == nil {
		if value := strings.TrimSpace(fmt.Sprint(root["conversation_id"])); value != "" && value != "<nil>" {
			return value
		}
		chatroom := strings.TrimSpace(fmt.Sprint(root["chatroom"]))
		if chatroom != "" && chatroom != "<nil>" {
			return "R:" + chatroom
		}
		fromUsername := strings.TrimSpace(fmt.Sprint(root["from_username"]))
		toUsername := strings.TrimSpace(fmt.Sprint(root["to_username"]))
		state := ConversationRouteService.GetByConversationIDInTenant(conversationID, tenantID)
		if state != nil && state.WxWorkInstanceID > 0 {
			if instance := WxWorkProtocolInstanceService.GetByTenantID(state.WxWorkInstanceID, tenantID); instance != nil {
				employeeID := strings.TrimSpace(instance.EmployeeUserID)
				switch {
				case employeeID != "" && fromUsername == employeeID && toUsername != "":
					return "S:" + toUsername
				case employeeID != "" && toUsername == employeeID && fromUsername != "":
					return "S:" + fromUsername
				}
			}
		}
		if fromUsername != "" && fromUsername != "<nil>" {
			return "S:" + fromUsername
		}
	}
	return ""
}

func waitWxWorkProtocolVoiceRefIDs(ctx context.Context, messageID, tenantID int64, timeout time.Duration) (conversationID string, msgID string) {
	deadline := time.Now().Add(timeout)
	for {
		conversationID, msgID = wxWorkProtocolVoiceRefIDs(messageID, tenantID)
		if conversationID != "" && msgID != "" {
			return conversationID, msgID
		}
		if time.Now().After(deadline) || !sleepContext(ctx, 100*time.Millisecond) {
			return conversationID, msgID
		}
	}
}

func wxWorkProtocolVoiceRefIDs(messageID, tenantID int64) (conversationID string, msgID string) {
	if messageID <= 0 || tenantID <= 0 {
		return "", ""
	}
	ref := WxWorkKFMessageRefService.FindOne(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("message_id", messageID).Eq("direction", string(enums.WxWorkKFMessageDirectionIn)))
	if ref == nil {
		return "", ""
	}
	msgID = strings.TrimPrefix(strings.TrimSpace(ref.WxMsgID), "wx_protocol:")
	if idx := strings.LastIndex(msgID, ":"); idx >= 0 {
		msgID = strings.TrimSpace(msgID[idx+1:])
	}
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ref.RawPayload)), &raw); err == nil {
		data := raw
		if nested, ok := raw["data"].(map[string]any); ok {
			data = nested
		}
		if value := strings.TrimSpace(fmt.Sprint(data["conversation_id"])); value != "" && value != "<nil>" {
			conversationID = value
		}
		chatroom := strings.TrimSpace(fmt.Sprint(data["chatroom"]))
		roomID := strings.TrimSpace(fmt.Sprint(data["roomid"]))
		if conversationID == "" && chatroom != "" && chatroom != "<nil>" {
			conversationID = "R:" + chatroom
		}
		if conversationID == "" && roomID != "" && roomID != "0" && roomID != "<nil>" {
			conversationID = "R:" + roomID
		}
	}
	if conversationID == "" && strings.TrimSpace(ref.ExternalUserID) != "" {
		conversationID = "S:" + strings.TrimSpace(ref.ExternalUserID)
	}
	return conversationID, msgID
}

func extractNestedString(raw string, keys ...string) string {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil {
		return ""
	}
	scopes := []map[string]any{root}
	if data, ok := nestedStringMap(root["data"]); ok {
		scopes = append(scopes, data)
	}
	for _, scope := range scopes {
		for _, key := range keys {
			if value := strings.TrimSpace(fmt.Sprint(scope[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func extractProtocolDataString(raw string, keys ...string) string {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil {
		return ""
	}
	if data, ok := root["data"].(map[string]any); ok {
		for _, key := range keys {
			if value := strings.TrimSpace(fmt.Sprint(data[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	for _, key := range keys {
		if value := strings.TrimSpace(fmt.Sprint(root[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func nonNilString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func nestedStringMap(value any) (map[string]any, bool) {
	if item, ok := value.(map[string]any); ok {
		return item, true
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" || !strings.HasPrefix(text, "{") {
		return nil, false
	}
	item := map[string]any{}
	if err := json.Unmarshal([]byte(text), &item); err != nil {
		return nil, false
	}
	return item, true
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
	text, _, err := s.callOpenAICompatibleASRWithUsage(ctx, config, filename, data)
	return text, err
}

func (s *mediaUnderstandingService) callOpenAICompatibleASRWithUsage(ctx context.Context, config models.AIConfig, filename string, data []byte) (string, *upstreamModelUsage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return "", nil, fmt.Errorf("ASR 模型配置不完整")
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", strings.TrimSpace(config.ModelName))
	part, err := writer.CreateFormFile("file", defaultUploadFilename(filename, "voice.wav"))
	if err != nil {
		return "", nil, err
	}
	if _, err := part.Write(data); err != nil {
		return "", nil, err
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/transcriptions", body)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.APIKey))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("ASR 调用失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", nil, err
	}
	usage := parseUpstreamModelUsage(parsed)
	for _, key := range []string{"text", "content", "transcription"} {
		if value := strings.TrimSpace(fmt.Sprint(parsed[key])); value != "" && value != "<nil>" {
			return value, usage, nil
		}
	}
	return "", usage, fmt.Errorf("ASR 返回中没有 text 字段")
}

func (s *mediaUnderstandingService) callOpenAICompatibleVision(ctx context.Context, config models.AIConfig, imageURL string) (string, error) {
	text, _, err := s.callOpenAICompatibleVisionWithUsage(ctx, config, imageURL)
	return text, err
}

func (s *mediaUnderstandingService) callOpenAICompatibleVisionWithUsage(ctx context.Context, config models.AIConfig, imageURL string) (string, *upstreamModelUsage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return "", nil, fmt.Errorf("视觉/多模态模型配置不完整")
	}
	body := map[string]any{
		"model": strings.TrimSpace(config.ModelName),
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "你是酒店前台同事的图片理解助手。只提取图片中能确定的信息，不猜测图片外事实，不写客服处理建议，不写“需要人工确认”。如果图片里有清晰文字、报错、问题、求助或操作诉求，要把这些内容保留下来；如果只是普通物品/餐食/环境照片，只描述画面。输出一句简洁中文。",
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
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("视觉模型调用失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed struct {
		ID      string         `json:"id"`
		Usage   map[string]any `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", nil, err
	}
	if len(parsed.Choices) == 0 {
		return "", parseUpstreamModelUsage(map[string]any{"id": parsed.ID, "usage": parsed.Usage}), fmt.Errorf("视觉模型没有返回结果")
	}
	usage := parseUpstreamModelUsage(map[string]any{"id": parsed.ID, "usage": parsed.Usage})
	return strings.TrimSpace(parsed.Choices[0].Message.Content), usage, nil
}

func parseUpstreamModelUsage(payload map[string]any) *upstreamModelUsage {
	if len(payload) == 0 {
		return nil
	}
	usageMap, _ := payload["usage"].(map[string]any)
	result := &upstreamModelUsage{RequestID: strings.TrimSpace(fmt.Sprint(payload["id"]))}
	result.PromptTokens = jsonNumberToInt64(usageMap["prompt_tokens"])
	result.CompletionTokens = jsonNumberToInt64(usageMap["completion_tokens"])
	if details, ok := usageMap["prompt_tokens_details"].(map[string]any); ok {
		result.CachedPromptTokens = jsonNumberToInt64(details["cached_tokens"])
	}
	if details, ok := usageMap["completion_tokens_details"].(map[string]any); ok {
		result.ReasoningTokens = jsonNumberToInt64(details["reasoning_tokens"])
	}
	if result.RequestID == "<nil>" {
		result.RequestID = ""
	}
	if result.RequestID == "" && result.PromptTokens == 0 && result.CompletionTokens == 0 && result.CachedPromptTokens == 0 && result.ReasoningTokens == 0 {
		return nil
	}
	return result
}

func jsonNumberToInt64(value any) int64 {
	switch item := value.(type) {
	case float64:
		return int64(item)
	case int:
		return int64(item)
	case int64:
		return item
	case json.Number:
		result, _ := item.Int64()
		return result
	default:
		return 0
	}
}

func (s *mediaUnderstandingService) recordMediaModelUsage(message *models.Message, config models.AIConfig, modelSource string, operationType string, usage *upstreamModelUsage, receipt *usagex.Receipt, latencyMS int64, callErr error) {
	if message == nil {
		return
	}
	status := "completed"
	errorMessage := ""
	metricSource := AIUsageMetricSourceProviderOperation
	if callErr != nil {
		status = "failed"
		errorMessage = callErr.Error()
	}
	event := models.AIUsageEvent{
		EventKey:       fmt.Sprintf("%s:media_understanding:%s", firstNonBlank(message.RequestID, fmt.Sprintf("message-%d", message.ID)), operationType),
		ConversationID: message.ConversationID, MessageID: message.ID, RequestID: message.RequestID,
		Stage: "media_understanding", Provider: string(config.Provider), Model: config.ModelName,
		AIConfigID: config.ID, ModelSource: modelSource, OperationType: operationType,
		MetricSource: metricSource, RequestCount: 1, LatencyMS: latencyMS, Status: status, ErrorMessage: errorMessage,
	}
	if usage != nil {
		event.UpstreamRequestID = usage.RequestID
		event.PromptTokens = usage.PromptTokens
		event.CompletionTokens = usage.CompletionTokens
		event.CachedPromptTokens = usage.CachedPromptTokens
		event.ReasoningTokens = usage.ReasoningTokens
		if usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.CachedPromptTokens > 0 || usage.ReasoningTokens > 0 {
			event.MetricSource = AIUsageMetricSourceUpstreamActual
		}
	}
	if receipt != nil {
		event.Gateway = receipt.Gateway
		event.GatewayRequestID = receipt.RequestID
		event.GatewayUpstreamID = receipt.UpstreamRequestID
		event.CallStartedAt = &receipt.StartedAt
		event.CallFinishedAt = &receipt.FinishedAt
		if receipt.LatencyMS() > 0 {
			event.LatencyMS = receipt.LatencyMS()
		}
	}
	_ = AIUsageEventService.Record(event)
}

func lastUsageReceipt(capture *usagex.Capture) *usagex.Receipt {
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
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
	if err := s.updateMessagePayload(message.ID, message.TenantID, payload); err != nil {
		return err
	}
	if message.MessageType == enums.IMMessageTypeVoice && (payload.MediaStatus == "failed" || payload.MediaStatus == "empty") {
		s.sendVoiceTranscriptionFailedReply(message)
	}
	return nil
}

func (s *mediaUnderstandingService) sendVoiceTranscriptionFailedReply(message *models.Message) {
	if message == nil || !s.canTriggerAIForMedia(message.ConversationID, message.TenantID) {
		return
	}
	conversation := ConversationService.GetByTenantID(message.ConversationID, message.TenantID)
	if conversation == nil {
		return
	}
	_, err := MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		conversation.AIAgentID,
		"voice_transcription_failed_"+strs.UUID(),
		enums.IMMessageTypeText,
		"这条语音我没听清，方便打字发我一下吗？",
		"",
		systemOperator(),
		message.RequestID,
	)
	if err != nil {
		slog.Warn("send voice transcription failed reply failed", "message_id", message.ID, "error", err)
	}
}

func (s *mediaUnderstandingService) updateMessagePayload(messageID, tenantID int64, payload *messageMediaPayload) error {
	if messageID <= 0 || tenantID <= 0 {
		return fmt.Errorf("媒体理解消息缺少租户归属")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return repositories.MessageRepository.UpdatesInTenant(sqls.DB(), messageID, tenantID, map[string]any{
		"payload":          string(data),
		"updated_at":       time.Now(),
		"update_user_name": "media_understanding",
	})
}

func (s *mediaUnderstandingService) canTriggerAIForMedia(conversationID, tenantID int64) bool {
	state := ConversationRouteService.GetByConversationIDInTenant(conversationID, tenantID)
	if state == nil {
		return true
	}
	if routeStatusBlocksAIReply(state.RouteStatus) {
		return false
	}
	if state.WxWorkInstanceID > 0 {
		instance := WxWorkProtocolInstanceService.GetByTenantID(state.WxWorkInstanceID, tenantID)
		if instance != nil && !instance.AIReplyEnabled {
			return false
		}
	}
	return true
}
