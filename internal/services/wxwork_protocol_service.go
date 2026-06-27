package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const wxWorkProtocolSystemOperatorName = "wxwork_protocol"

const (
	wxProtocolNotifyUserLogin      = 1003
	wxProtocolNotifyUserLogout     = 1004
	wxProtocolNotifyNewMsg         = 1010
	wxProtocolNotifyBatchNewMsg    = 1011
	wxProtocolNotifyNewMsgAlt      = 11010
	wxProtocolNotifyBatchNewMsgAlt = 11011
	wxProtocolMsgText              = 1
	wxProtocolMsgImage             = 3
	wxProtocolMsgVoice             = 34
	wxProtocolMsgVideo             = 43
	wxProtocolMsgAttachment        = 49
	wxProtocolMsgFile              = 50
	wxProtocolMsgLocation          = 48
	wxProtocolMsgSystem            = 10000
	wxProtocolConfigErrorNotice    = "当前门店配置异常，已通知人工处理。"
)

var wxProtocolURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

var WxWorkProtocolService = newWxWorkProtocolService()

func newWxWorkProtocolService() *wxWorkProtocolService {
	return &wxWorkProtocolService{httpClient: &http.Client{Timeout: 45 * time.Second}}
}

type wxWorkProtocolService struct {
	httpClient *http.Client
}

func (s *wxWorkProtocolService) HandleCallback(req request.WxWorkProtocolCallbackRequest, raw string) error {
	externalMsgID := strings.TrimSpace(req.Guid)
	_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", externalMsgID, enums.MessageSyncStatusPending, raw, fmt.Sprintf("notify_type=%d", req.NotifyType))
	instance := WxWorkProtocolInstanceService.Take("guid = ?", strings.TrimSpace(req.Guid))
	if instance == nil && req.NotifyType == wxProtocolNotifyUserLogin {
		var err error
		instance, err = WxWorkProtocolInstanceService.CreatePendingFromLogin(strings.TrimSpace(req.Guid), req.Data)
		if err != nil {
			return err
		}
	}
	if instance == nil || instance.Status != enums.StatusOk {
		return errorsx.InvalidParam("企微员工号实例不存在或未启用")
	}
	now := time.Now()
	switch req.NotifyType {
	case wxProtocolNotifyUserLogin:
		return s.handleLogin(instance, req.Data, now)
	case wxProtocolNotifyUserLogout:
		return s.handleLogout(instance, req.Data, now)
	case wxProtocolNotifyNewMsg, wxProtocolNotifyNewMsgAlt:
		return s.handleMessage(instance, req.Data, raw)
	case wxProtocolNotifyBatchNewMsg, wxProtocolNotifyBatchNewMsgAlt:
		return s.handleBatchMessages(instance, req.Data, raw)
	default:
		_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", externalMsgID, enums.MessageSyncStatusSkipped, raw, fmt.Sprintf("skip notify_type=%d", req.NotifyType))
		slog.Info("skip wxwork protocol callback", "guid", req.Guid, "notify_type", req.NotifyType)
		return nil
	}
}

func (s *wxWorkProtocolService) DispatchPendingOutbox(limit int) int {
	items := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, limit)
	now := time.Now()
	count := 0
	for i := range items {
		if items[i].NextRetryAt != nil && items[i].NextRetryAt.After(now) {
			continue
		}
		if err := s.dispatchOutbox(items[i]); err != nil {
			slog.Warn("dispatch wxwork protocol outbox failed", "outbox_id", items[i].ID, "error", err)
			continue
		}
		count++
	}
	return count
}

func (s *wxWorkProtocolService) SetNotifyURL(instanceID int64, notifyURL string) error {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if instance == nil {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	channel := ChannelService.Get(instance.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return errorsx.InvalidParam("企微协议渠道不存在")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return errorsx.InvalidParam("企微协议渠道配置不合法")
	}
	body := map[string]string{
		"guid":       strings.TrimSpace(instance.Guid),
		"notify_url": strings.TrimSpace(notifyURL),
	}
	if _, err = s.postJSON(cfg, "/client/set_notify_url", body); err != nil {
		return err
	}
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
		"notify_url":       strings.TrimSpace(notifyURL),
		"updated_at":       time.Now(),
		"update_user_name": wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolService) GetLoginQRCode(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/login/get_login_qrcode", map[string]any{"verify_login": false}, nil)
}

func (s *wxWorkProtocolService) CheckLoginQRCode(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/login/check_login_qrcode", nil, nil)
}

func (s *wxWorkProtocolService) VerifyLoginQRCode(instanceID int64, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", errorsx.InvalidParam("验证码不能为空")
	}
	return s.callInstanceAPI(instanceID, "/login/verify_login_qrcode", map[string]any{"code": code}, nil)
}

func (s *wxWorkProtocolService) SyncProfile(instanceID int64) (string, error) {
	var raw string
	resp, err := s.callInstanceAPI(instanceID, "/user/get_profile", nil, func(instance *models.WxWorkProtocolInstance, response string) error {
		raw = response
		updates := s.profileUpdatesFromResponse(response)
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now()
		updates["update_user_name"] = wxWorkProtocolSystemOperatorName
		return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, updates)
	})
	if err != nil {
		return resp, err
	}
	if raw != "" {
		return raw, nil
	}
	return resp, nil
}

func (s *wxWorkProtocolService) GetCorpInfo(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/user/get_corp_info", nil, nil)
}

func (s *wxWorkProtocolService) Logout(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/user/logout", nil, func(instance *models.WxWorkProtocolInstance, _ string) error {
		return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
			"health_status":    "offline",
			"updated_at":       time.Now(),
			"update_user_name": wxWorkProtocolSystemOperatorName,
		})
	})
}

func (s *wxWorkProtocolService) StopClient(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/client/stop_client", nil, func(instance *models.WxWorkProtocolInstance, _ string) error {
		return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
			"health_status":    "stopped",
			"updated_at":       time.Now(),
			"update_user_name": wxWorkProtocolSystemOperatorName,
		})
	})
}

func (s *wxWorkProtocolService) RestoreClient(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/client/restore_client", map[string]any{
		"proxy":            "",
		"bridge":           "",
		"sync_history_msg": true,
		"force_online":     false,
		"auto_start":       true,
	}, func(instance *models.WxWorkProtocolInstance, _ string) error {
		return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
			"health_status":    "recovering",
			"updated_at":       time.Now(),
			"update_user_name": wxWorkProtocolSystemOperatorName,
		})
	})
}

func (s *wxWorkProtocolService) SetProxy(instanceID int64, proxy string) (string, error) {
	proxy = strings.TrimSpace(proxy)
	return s.callInstanceAPI(instanceID, "/client/set_proxy", map[string]any{"proxy": proxy}, func(instance *models.WxWorkProtocolInstance, _ string) error {
		return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
			"proxy":            proxy,
			"updated_at":       time.Now(),
			"update_user_name": wxWorkProtocolSystemOperatorName,
		})
	})
}

func (s *wxWorkProtocolService) SyncFriendRequests(instanceID int64) (string, error) {
	return s.callInstanceAPI(instanceID, "/contact/sync_apply_list", map[string]any{"seq": "", "limit": 50}, nil)
}

func (s *wxWorkProtocolService) AgreeContact(instanceID int64, userID string, corpID string) (string, error) {
	userID = strings.TrimSpace(userID)
	corpID = strings.TrimSpace(corpID)
	if userID == "" {
		return "", errorsx.InvalidParam("user_id 不能为空")
	}
	if corpID == "" {
		corpID = "0"
	}
	return s.callInstanceAPI(instanceID, "/contact/agree_contact", map[string]any{"user_id": userID, "corp_id": corpID}, nil)
}

func (s *wxWorkProtocolService) InviteRoomMember(instanceID int64, roomID string, userList []string) error {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if instance == nil || instance.Status != enums.StatusOk {
		return errorsx.InvalidParam("企微员工号实例不存在或未启用")
	}
	channel := ChannelService.Get(instance.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return errorsx.InvalidParam("企微协议渠道不存在")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return errorsx.InvalidParam("企微协议渠道配置不合法")
	}
	roomID = strings.TrimSpace(strings.TrimPrefix(roomID, "R:"))
	if roomID == "" {
		return errorsx.InvalidParam("群ID不能为空")
	}
	cleanUsers := make([]string, 0, len(userList))
	seen := map[string]struct{}{}
	for _, item := range userList {
		userID := strings.TrimSpace(item)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		cleanUsers = append(cleanUsers, userID)
	}
	if len(cleanUsers) == 0 {
		return errorsx.InvalidParam("被邀请成员ID不能为空")
	}
	body := map[string]any{
		"guid":      strings.TrimSpace(instance.Guid),
		"room_id":   roomID,
		"user_list": cleanUsers,
	}
	_, err = s.postJSON(cfg, "/room/invite_room_member", body)
	return err
}

func (s *wxWorkProtocolService) handleLogin(instance *models.WxWorkProtocolInstance, raw json.RawMessage, now time.Time) error {
	data := struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		NickName string `json:"nickname"`
		RealName string `json:"real_name"`
	}{}
	_ = json.Unmarshal(raw, &data)
	employeeUserID := strings.TrimSpace(data.Username)
	if employeeUserID == "" {
		employeeUserID = strings.TrimSpace(data.UserID)
	}
	employeeName := strings.TrimSpace(data.RealName)
	if employeeName == "" {
		employeeName = strings.TrimSpace(data.Name)
	}
	if employeeName == "" {
		employeeName = strings.TrimSpace(data.NickName)
	}
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
		"employee_user_id":  employeeUserID,
		"employee_name":     employeeName,
		"health_status":     "online",
		"last_heartbeat_at": now,
		"updated_at":        now,
		"update_user_name":  wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolService) handleLogout(instance *models.WxWorkProtocolInstance, raw json.RawMessage, now time.Time) error {
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{
		"health_status":     "offline",
		"last_heartbeat_at": now,
		"updated_at":        now,
		"update_user_name":  wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolService) handleMessage(instance *models.WxWorkProtocolInstance, raw json.RawMessage, rawPayload string) error {
	msg := request.WxProtocolChatMsg{}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return errorsx.InvalidParam("企微协议消息格式不合法")
	}
	return s.handleChatMessage(instance, msg, rawPayload)
}

func (s *wxWorkProtocolService) handleBatchMessages(instance *models.WxWorkProtocolInstance, raw json.RawMessage, rawPayload string) error {
	var list []request.WxProtocolChatMsg
	if err := json.Unmarshal(raw, &list); err != nil {
		wrapper := struct {
			List     []request.WxProtocolChatMsg `json:"list"`
			Messages []request.WxProtocolChatMsg `json:"messages"`
			Msgs     []request.WxProtocolChatMsg `json:"msgs"`
			Items    []request.WxProtocolChatMsg `json:"items"`
		}{}
		if err2 := json.Unmarshal(raw, &wrapper); err2 != nil {
			return errorsx.InvalidParam("微信协议批量消息格式不合法")
		}
		list = wrapper.List
		if len(list) == 0 {
			list = wrapper.Messages
		}
		if len(list) == 0 {
			list = wrapper.Msgs
		}
		if len(list) == 0 {
			list = wrapper.Items
		}
	}
	for i := range list {
		itemRaw, _ := json.Marshal(list[i])
		if err := s.handleChatMessage(instance, list[i], string(itemRaw)); err != nil {
			return err
		}
	}
	return nil
}

func (s *wxWorkProtocolService) handleChatMessage(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg, rawPayload string) error {
	msg.Normalize()
	clientMsgID := s.clientMessageID(instance.Guid, msg)
	if WxWorkKFMessageRefService.Take("wx_msg_id = ?", clientMsgID) != nil {
		_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", clientMsgID, enums.MessageSyncStatusSkipped, rawPayload, "duplicate message")
		return nil
	}
	if strings.TrimSpace(msg.Content) == "" && !s.isSupportedMediaMessage(msg.MsgType) {
		_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", clientMsgID, enums.MessageSyncStatusSkipped, rawPayload, "empty content")
		return nil
	}
	messageType := s.resolveMessageType(msg.MsgType)
	if messageType == "" {
		_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", clientMsgID, enums.MessageSyncStatusSkipped, rawPayload, fmt.Sprintf("unsupported msg_type=%d", msg.MsgType))
		return nil
	}
	if s.isEmployeeOutgoing(instance, msg) {
		_ = s.createEchoMessageRef(instance, msg, rawPayload, clientMsgID)
		_ = MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", clientMsgID, enums.MessageSyncStatusSkipped, rawPayload, "self echo")
		return nil
	}
	externalID := s.externalConversationID(instance, msg)
	if externalID == "" {
		return nil
	}
	conversation, err := s.ensureConversation(instance, msg, externalID, rawPayload)
	if err != nil {
		return err
	}
	if _, _, err := WxWorkProtocolInstanceService.RequireStoreKnowledge(instance); err != nil {
		_, _ = ConversationRouteService.EnterHQAgentDeskPending(conversation.ID, "企微员工号未绑定门店或知识库", time.Now())
		content, payload, buildErr := s.buildInboundMessageContent(messageType, msg)
		if buildErr != nil {
			return buildErr
		}
		message, sendErr := MessageService.SendCustomerMessage(conversation.ID, clientMsgID, messageType, content, payload, s.externalUser(instance, msg, externalID))
		if sendErr != nil {
			return sendErr
		}
		_ = s.createMessageRef(conversation.ID, message.ID, instance, externalID, clientMsgID, rawPayload, enums.WxWorkKFMessageDirectionIn, enums.WxWorkKFMessageSendStatusReceived)
		_ = MessageSyncLogService.Create(conversation.ID, message.ID, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", clientMsgID, enums.MessageSyncStatusFailed, rawPayload, err.Error())
		return s.replyConfigError(conversation.ID, conversation.AIAgentID, clientMsgID)
	}
	if err := s.ensureRouteState(conversation.ID, instance); err != nil {
		return err
	}
	content, payload, err := s.buildInboundMessageContent(messageType, msg)
	if err != nil {
		return err
	}
	message, err := MessageService.SendCustomerMessage(conversation.ID, clientMsgID, messageType, content, payload, s.externalUser(instance, msg, externalID))
	if err != nil {
		return err
	}
	return s.createMessageRef(conversation.ID, message.ID, instance, externalID, clientMsgID, rawPayload, enums.WxWorkKFMessageDirectionIn, enums.WxWorkKFMessageSendStatusReceived)
}

func (s *wxWorkProtocolService) isEmployeeOutgoing(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg) bool {
	sender := strings.TrimSpace(msg.FromUsername)
	employeeID := strings.TrimSpace(instance.EmployeeUserID)
	return employeeID != "" && sender == employeeID
}

func (s *wxWorkProtocolService) resolveMessageType(msgType int) enums.IMMessageType {
	switch msgType {
	case wxProtocolMsgText, 2:
		return enums.IMMessageTypeText
	case wxProtocolMsgImage:
		return enums.IMMessageTypeImage
	case wxProtocolMsgVoice:
		return enums.IMMessageTypeVoice
	case wxProtocolMsgVideo:
		return enums.IMMessageTypeVideo
	case wxProtocolMsgAttachment, wxProtocolMsgFile:
		return s.resolveAttachmentMessageType(msgType)
	case wxProtocolMsgLocation:
		return enums.IMMessageTypeLocation
	default:
		return ""
	}
}

func (s *wxWorkProtocolService) resolveAttachmentMessageType(msgType int) enums.IMMessageType {
	return enums.IMMessageTypeAttachment
}

func (s *wxWorkProtocolService) isSupportedMediaMessage(msgType int) bool {
	messageType := s.resolveMessageType(msgType)
	return messageType == enums.IMMessageTypeImage || messageType == enums.IMMessageTypeVoice || messageType == enums.IMMessageTypeVideo || messageType == enums.IMMessageTypeAttachment || messageType == enums.IMMessageTypeLocation
}

func (s *wxWorkProtocolService) buildInboundMessageContent(messageType enums.IMMessageType, msg request.WxProtocolChatMsg) (string, string, error) {
	if messageType == enums.IMMessageTypeText {
		return s.messageContent(msg), strings.TrimSpace(s.rawMessagePayload(msg)), nil
	}
	media := s.parseMediaPayload(msg)
	filename := strings.TrimSpace(media.Filename)
	if filename == "" {
		filename = s.defaultMediaFilename(messageType, msg)
	}
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(mediaFileExt(filename))
	}
	asset, err := AssetService.RegisterExternal("wx_protocol", filename, media.FileSize, mimeType, media.URL, nil)
	if err != nil {
		return "", "", err
	}
	payloadMap := map[string]any{
		"assetId":    asset.AssetID,
		"provider":   asset.Provider,
		"storageKey": asset.StorageKey,
		"filename":   asset.Filename,
		"fileSize":   asset.FileSize,
		"mimeType":   asset.MimeType,
		"url":        strings.TrimSpace(media.URL),
		"wxMedia":    media,
	}
	payloadBytes, _ := json.Marshal(payloadMap)
	content := filename
	if messageType == enums.IMMessageTypeVoice {
		if text := strings.TrimSpace(msg.Desc); text != "" && text != filename {
			content = text
		}
	}
	return content, string(payloadBytes), nil
}

func (s *wxWorkProtocolService) rawMessagePayload(msg request.WxProtocolChatMsg) string {
	raw, _ := json.Marshal(msg)
	return string(raw)
}

func (s *wxWorkProtocolService) parseMediaPayload(msg request.WxProtocolChatMsg) request.WxProtocolMediaPayload {
	joined := strings.TrimSpace(msg.Content + "\n" + msg.Source)
	mediaPayload := msg.CDN
	if mediaPayload.FileName == "" {
		mediaPayload.FileName = strings.TrimSpace(msg.FileName)
	}
	if mediaPayload.Filename == "" {
		mediaPayload.Filename = strings.TrimSpace(msg.FileName)
	}
	if mediaPayload.Length <= 0 && msg.VoiceTime > 0 {
		mediaPayload.Length = msg.VoiceTime
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(msg.Content)), &mediaPayload)
	if mediaPayload.URL == "" || mediaPayload.FileID == "" {
		var generic map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(msg.Content)), &generic); err == nil {
			fillMediaPayloadFromMap(&mediaPayload, generic)
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(msg.Source)), &generic); err == nil {
			fillMediaPayloadFromMap(&mediaPayload, generic)
		}
	}
	if mediaPayload.URL == "" {
		mediaPayload.URL = firstURL(joined)
	}
	if mediaPayload.Filename == "" {
		mediaPayload.Filename = filenameFromURL(mediaPayload.URL)
	}
	return mediaPayload
}

func fillMediaPayloadFromMap(payload *request.WxProtocolMediaPayload, values map[string]any) {
	if payload == nil || values == nil {
		return
	}
	setString := func(target *string, keys ...string) {
		if strings.TrimSpace(*target) != "" {
			return
		}
		for _, key := range keys {
			if value, ok := values[key]; ok {
				*target = strings.TrimSpace(fmt.Sprint(value))
				if *target != "" {
					return
				}
			}
		}
	}
	setInt64 := func(target *int64, keys ...string) {
		if *target > 0 {
			return
		}
		for _, key := range keys {
			if value, ok := values[key]; ok {
				if parsed, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64); err == nil {
					*target = parsed
					return
				}
			}
		}
	}
	setInt := func(target *int, keys ...string) {
		if *target > 0 {
			return
		}
		for _, key := range keys {
			if value, ok := values[key]; ok {
				if parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value))); err == nil {
					*target = parsed
					return
				}
			}
		}
	}
	setString(&payload.URL, "url", "cdn_url", "download_url", "file_url", "thumb_url")
	setString(&payload.FileID, "file_id", "fileid", "fileId", "id")
	setString(&payload.AesKey, "aes_key", "aeskey", "aesKey")
	setString(&payload.MD5, "md5", "file_md5", "fileMd5")
	setString(&payload.Filename, "filename", "file_name", "name")
	setString(&payload.FileName, "file_name", "filename", "name")
	setString(&payload.MimeType, "mime_type", "mime", "content_type")
	setInt64(&payload.Size, "size", "file_size", "fileSize")
	setInt64(&payload.FileSize, "file_size", "size", "fileSize")
	setInt(&payload.ImageWidth, "image_width", "width", "thumb_width")
	setInt(&payload.ImageHeight, "image_height", "height", "thumb_height")
	setInt64(&payload.Length, "length", "voice_length", "duration")
}

func firstURL(value string) string {
	match := wxProtocolURLPattern.FindString(strings.TrimSpace(value))
	return strings.TrimSpace(match)
}

func filenameFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func mediaFileExt(filename string) string {
	filename = strings.TrimSpace(filename)
	idx := strings.LastIndex(filename, ".")
	if idx < 0 {
		return ""
	}
	return filename[idx:]
}

func (s *wxWorkProtocolService) defaultMediaFilename(messageType enums.IMMessageType, msg request.WxProtocolChatMsg) string {
	ext := ".bin"
	switch messageType {
	case enums.IMMessageTypeImage:
		ext = ".jpg"
	case enums.IMMessageTypeVoice:
		ext = ".mp3"
	case enums.IMMessageTypeVideo:
		ext = ".mp4"
	}
	id := strings.TrimSpace(msg.MsgID)
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "wx_protocol_" + id + ext
}

func (s *wxWorkProtocolService) dispatchOutbox(outbox models.ChannelMessageOutbox) error {
	conversation := ConversationService.Get(outbox.ConversationID)
	if conversation == nil {
		return s.markOutboxFailed(outbox, "会话不存在")
	}
	channel := ChannelService.Get(conversation.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return nil
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return s.markOutboxFailed(outbox, "企微协议渠道配置不合法")
	}
	message := MessageService.Get(outbox.MessageID)
	if message == nil {
		return s.markOutboxFailed(outbox, "消息不存在")
	}
	mapping := WxWorkKFConversationService.Take("conversation_id = ?", conversation.ID)
	if mapping == nil {
		return s.markOutboxFailed(outbox, "企微协议会话映射不存在")
	}
	route := ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return s.markOutboxFailed(outbox, "企微协议实例绑定不存在")
	}
	instance := WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
	if instance == nil || instance.Status != enums.StatusOk {
		return s.markOutboxFailed(outbox, "企微协议实例不存在或未启用")
	}
	protocolConversationID := s.protocolConversationID(mapping)
	if protocolConversationID == "" {
		return s.markOutboxFailed(outbox, "企微协议 conversation_id 为空")
	}
	if err := ChannelMessageOutboxService.Updates(outbox.ID, map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusSending),
		"updated_at":  time.Now(),
	}); err != nil {
		return err
	}
	resp, err := s.sendOutboxMessage(cfg, instance, protocolConversationID, message)
	if err != nil {
		return s.markOutboxFailed(outbox, err.Error())
	}
	now := time.Now()
	if err := ChannelMessageOutboxService.Updates(outbox.ID, map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusSent),
		"sent_at":     now,
		"last_error":  "",
		"updated_at":  now,
	}); err != nil {
		return err
	}
	wxMsgID := s.sentMessageID(instance.Guid, resp, outbox.ID)
	_ = s.createMessageRef(conversation.ID, message.ID, instance, strings.TrimSpace(mapping.ExternalUserID), wxMsgID, resp, enums.WxWorkKFMessageDirectionOut, enums.WxWorkKFMessageSendStatusSent)
	return nil
}

func (s *wxWorkProtocolService) sendOutboxMessage(cfg *dto.WxWorkProtocolChannelConfig, instance *models.WxWorkProtocolInstance, conversationID string, message *models.Message) (string, error) {
	if message == nil {
		return "", errorsx.InvalidParam("消息不存在")
	}
	base := map[string]any{
		"guid":            strings.TrimSpace(instance.Guid),
		"conversation_id": strings.TrimSpace(conversationID),
	}
	switch message.MessageType {
	case enums.IMMessageTypeText:
		base["content"] = strings.TrimSpace(message.Content)
		return s.postJSON(cfg, "/msg/send_text", base)
	case enums.IMMessageTypeHTML:
		base["content"] = utils.BuildHTMLSummary(message.Content)
		return s.postJSON(cfg, "/msg/send_text", base)
	case enums.IMMessageTypeImage:
		media, err := s.outboundMediaPayload(message)
		if err != nil {
			return "", err
		}
		mergeImageSendBody(base, media)
		return s.postJSON(cfg, "/msg/send_image", base)
	case enums.IMMessageTypeVoice:
		media, err := s.outboundMediaPayload(message)
		if err != nil {
			return "", err
		}
		mergeVoiceSendBody(base, media)
		return s.postJSON(cfg, "/msg/send_voice", base)
	case enums.IMMessageTypeAttachment:
		media, err := s.outboundMediaPayload(message)
		if err != nil {
			return "", err
		}
		mergeFileSendBody(base, media)
		return s.postJSON(cfg, "/msg/send_file", base)
	case enums.IMMessageTypeVideo:
		media, err := s.outboundMediaPayload(message)
		if err != nil {
			return "", err
		}
		mergeVideoSendBody(base, media)
		return s.postJSON(cfg, "/msg/send_video", base)
	case enums.IMMessageTypeGIF:
		media, err := s.outboundMediaPayload(message)
		if err != nil {
			return "", err
		}
		mergeGIFSendBody(base, media)
		return s.postJSON(cfg, "/msg/send_gif", base)
	case enums.IMMessageTypeLocation:
		return s.sendRichPayload(cfg, "/msg/send_location", base, message.Payload, []string{"longitude", "latitude", "address", "title", "zoom"})
	case enums.IMMessageTypeContactCard:
		return s.sendRichPayload(cfg, "/msg/send_card", base, message.Payload, nil)
	case enums.IMMessageTypeLink:
		return s.sendRichPayload(cfg, "/msg/send_link", base, message.Payload, nil)
	case enums.IMMessageTypeMiniProgram:
		return s.sendRichPayload(cfg, "/msg/send_mini_program", base, message.Payload, nil)
	case enums.IMMessageTypeFeed:
		return s.sendRichPayload(cfg, "/msg/send_feed", base, message.Payload, nil)
	case enums.IMMessageTypeFeedLive:
		return s.sendRichPayload(cfg, "/msg/send_feed_live", base, message.Payload, []string{"feed_type", "cover_url", "thumb_url", "avatar", "nickname", "desc", "url", "extras", "object_id", "object_nonce_id"})
	case enums.IMMessageTypeQuote:
		return s.sendRichPayload(cfg, "/msg/send_quote_msg", base, message.Payload, []string{"quote", "content", "appinfo", "content_type", "sender", "sender_name", "message"})
	case enums.IMMessageTypeMergedForward:
		return s.sendRichPayload(cfg, "/msg/send_merged_msg", base, message.Payload, []string{"message_list"})
	case enums.IMMessageTypeShopProduct:
		return s.sendRichPayload(cfg, "/msg/send_finder_product", base, message.Payload, []string{"content"})
	default:
		return "", errorsx.InvalidParam("企微协议暂不支持发送该消息类型")
	}
}

func (s *wxWorkProtocolService) sendRichPayload(cfg *dto.WxWorkProtocolChannelConfig, path string, base map[string]any, payload string, required []string) (string, error) {
	body, err := wxProtocolRichPayload(payload)
	if err != nil {
		return "", err
	}
	for key, value := range base {
		body[key] = value
	}
	for _, key := range required {
		if isEmptyProtocolValue(body[key]) {
			return "", errorsx.InvalidParam(fmt.Sprintf("%s 缺少企微协议字段 %s", path, key))
		}
	}
	return s.postJSON(cfg, path, body)
}

func (s *wxWorkProtocolService) protocolConversationID(mapping *models.WxWorkKFConversation) string {
	if mapping == nil {
		return ""
	}
	externalID := strings.TrimSpace(mapping.ExternalUserID)
	if externalID == "" {
		return ""
	}
	if strings.HasPrefix(externalID, "S:") || strings.HasPrefix(externalID, "R:") {
		return externalID
	}
	if strings.Contains(strings.TrimSpace(mapping.OpenKfID), ":room") {
		return "R:" + externalID
	}
	return "S:" + externalID
}

func (s *wxWorkProtocolService) ValidateOutboundMediaReady(conversationID int64, messageType enums.IMMessageType, payload string) error {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeAttachment, enums.IMMessageTypeVideo:
	default:
		return nil
	}
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	channel := ChannelService.Get(conversation.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return nil
	}
	media, err := wxProtocolMediaFromPayload(payload)
	if err != nil {
		return err
	}
	if strings.TrimSpace(media.FileID) == "" || strings.TrimSpace(media.AesKey) == "" {
		return errorsx.InvalidParam("图片/文件缺少微信协议 SAAS 上传凭证 file_id/aes_key，请先接入协议私有化 CDN 文件上传接口")
	}
	return nil
}

func (s *wxWorkProtocolService) outboundMediaPayload(message *models.Message) (request.WxProtocolMediaPayload, error) {
	media, err := wxProtocolMediaFromPayload(message.Payload)
	if err != nil {
		return media, err
	}
	if strings.TrimSpace(media.FileID) == "" {
		return media, errorsx.InvalidParam("图片/文件缺少企微协议 file_id，请先通过协议 CDN 上传")
	}
	return media, nil
}

func wxProtocolMediaFromPayload(payload string) (request.WxProtocolMediaPayload, error) {
	var wrapper struct {
		WxMedia request.WxProtocolMediaPayload `json:"wxMedia"`
		AssetID string                         `json:"assetId"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(payload)), &wrapper)
	media := wrapper.WxMedia
	var assetID string
	if assetPayload, err := parseIMMessageAssetPayload(payload); err == nil && assetPayload != nil {
		assetID = strings.TrimSpace(assetPayload.AssetID)
		if strings.TrimSpace(media.FileID) == "" {
			media = assetPayload.WxMedia
		}
	}
	if strings.TrimSpace(media.FileID) == "" {
		if assetID == "" {
			assetID = strings.TrimSpace(wrapper.AssetID)
		}
		if assetID != "" {
			return media, errorsx.InvalidParam("图片/文件已上传到系统资产库，但缺少微信协议 SAAS 上传凭证 file_id/aes_key，请先接入协议文件上传接口")
		}
		return media, errorsx.InvalidParam("图片/文件缺少企微协议 file_id，请先通过协议 CDN 上传")
	}
	return media, nil
}

func mergeImageSendBody(body map[string]any, media request.WxProtocolMediaPayload) {
	body["file_id"] = strings.TrimSpace(media.FileID)
	body["aes_key"] = strings.TrimSpace(media.AesKey)
	body["size"] = mediaSize(media)
	body["image_width"] = media.ImageWidth
	body["image_height"] = media.ImageHeight
	body["md5"] = mediaMD5(media)
	body["is_hd"] = media.IsHD
}

func mergeFileSendBody(body map[string]any, media request.WxProtocolMediaPayload) {
	body["file_id"] = strings.TrimSpace(media.FileID)
	body["size"] = mediaSize(media)
	body["file_name"] = mediaFilename(media)
	body["aes_key"] = strings.TrimSpace(media.AesKey)
	body["md5"] = mediaMD5(media)
}

func mergeVoiceSendBody(body map[string]any, media request.WxProtocolMediaPayload) {
	body["file_id"] = strings.TrimSpace(media.FileID)
	body["size"] = mediaSize(media)
	body["voice_time"] = media.Length
	body["aes_key"] = strings.TrimSpace(media.AesKey)
	body["md5"] = mediaMD5(media)
}

func mergeVideoSendBody(body map[string]any, media request.WxProtocolMediaPayload) {
	body["file_id"] = strings.TrimSpace(media.FileID)
	body["size"] = mediaSize(media)
	body["file_name"] = mediaFilename(media)
	body["aes_key"] = strings.TrimSpace(media.AesKey)
	body["md5"] = mediaMD5(media)
	body["video_duration"] = media.Length
	body["video_width"] = media.ImageWidth
	body["video_height"] = media.ImageHeight
}

func mergeGIFSendBody(body map[string]any, media request.WxProtocolMediaPayload) {
	body["file_id"] = strings.TrimSpace(media.FileID)
	body["size"] = mediaSize(media)
	body["aes_key"] = strings.TrimSpace(media.AesKey)
	body["md5"] = mediaMD5(media)
	body["url"] = strings.TrimSpace(media.URL)
	body["image_width"] = media.ImageWidth
	body["image_height"] = media.ImageHeight
}

func wxProtocolRichPayload(payload string) (map[string]any, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, errorsx.InvalidParam("富媒体消息缺少 payload")
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return nil, errorsx.InvalidParam("富媒体消息 payload 必须是 JSON 对象")
	}
	if nested, ok := body["wxPayload"].(map[string]any); ok {
		body = nested
	}
	delete(body, "guid")
	delete(body, "conversation_id")
	return body, nil
}

func isEmptyProtocolValue(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func mediaSize(media request.WxProtocolMediaPayload) int64 {
	if media.Size > 0 {
		return media.Size
	}
	return media.FileSize
}

func mediaMD5(media request.WxProtocolMediaPayload) string {
	if strings.TrimSpace(media.MD5) != "" {
		return strings.TrimSpace(media.MD5)
	}
	return strings.TrimSpace(media.FileMD5)
}

func mediaFilename(media request.WxProtocolMediaPayload) string {
	if strings.TrimSpace(media.FileName) != "" {
		return strings.TrimSpace(media.FileName)
	}
	return strings.TrimSpace(media.Filename)
}

func (s *wxWorkProtocolService) sentMessageID(guid string, raw string, outboxID int64) string {
	resp := request.WxWorkProtocolSendTextResponse{}
	if err := json.Unmarshal([]byte(raw), &resp); err == nil {
		id := strings.TrimSpace(resp.Data.MsgData.MsgID)
		if id == "" {
			id = strings.TrimSpace(resp.Data.MsgData.ID)
		}
		if id != "" {
			return "wx_protocol:" + strings.TrimSpace(guid) + ":" + id
		}
	}
	return fmt.Sprintf("wx_protocol_out:%d", outboxID)
}

func (s *wxWorkProtocolService) ensureConversation(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg, externalID string, rawPayload string) (*models.Conversation, error) {
	openKfID := s.mappingOpenKfID(instance, msg)
	if mapping := WxWorkKFConversationService.Take("channel_id = ? AND open_kf_id = ? AND external_user_id = ? AND status = ?", instance.ChannelID, openKfID, externalID, enums.StatusOk); mapping != nil {
		if conversation := ConversationService.Get(mapping.ConversationID); conversation != nil {
			return conversation, nil
		}
	}
	external := s.externalUser(instance, msg, externalID)
	conversation, err := ConversationService.CreateWithoutWelcome(external, instance.ChannelID, s.channelAIAgentID(instance.ChannelID))
	if err != nil {
		return nil, err
	}
	if err := s.upsertConversationMapping(instance, conversation.ID, msg, externalID, rawPayload); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (s *wxWorkProtocolService) ensureRouteState(conversationID int64, instance *models.WxWorkProtocolInstance) error {
	if _, _, err := WxWorkProtocolInstanceService.RequireStoreKnowledge(instance); err != nil {
		return err
	}
	state, err := ConversationRouteService.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"store_id":            instance.StoreID,
		"knowledge_base_id":   instance.KnowledgeBaseID,
		"wx_work_instance_id": instance.ID,
		"updated_at":          time.Now(),
		"update_user_name":    wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolService) replyConfigError(conversationID int64, aiAgentID int64, clientMsgID string) error {
	requestID := "wx_protocol_config_error_" + strings.TrimPrefix(clientMsgID, "wx_protocol:")
	_, err := MessageService.SendAIServiceNoticeWithRequestID(conversationID, aiAgentID, wxProtocolConfigErrorNotice, requestID)
	return err
}

func (s *wxWorkProtocolService) upsertConversationMapping(instance *models.WxWorkProtocolInstance, conversationID int64, msg request.WxProtocolChatMsg, externalID string, rawPayload string) error {
	now := time.Now()
	channelID := instance.ChannelID
	openKfID := s.mappingOpenKfID(instance, msg)
	if existing := WxWorkKFConversationService.Take("channel_id = ? AND open_kf_id = ? AND external_user_id = ?", channelID, openKfID, externalID); existing != nil {
		return WxWorkKFConversationService.Updates(existing.ID, map[string]any{
			"conversation_id":  conversationID,
			"open_kf_id":       openKfID,
			"external_user_id": externalID,
			"session_status":   string(enums.WxWorkKFSessionStatusActive),
			"raw_profile":      rawPayload,
			"status":           enums.StatusOk,
			"updated_at":       now,
		})
	}
	return WxWorkKFConversationService.Create(&models.WxWorkKFConversation{
		ConversationID: conversationID,
		ChannelID:      channelID,
		OpenKfID:       openKfID,
		ExternalUserID: externalID,
		SessionStatus:  string(enums.WxWorkKFSessionStatusActive),
		RawProfile:     rawPayload,
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   0,
			CreateUserName: wxWorkProtocolSystemOperatorName,
			UpdatedAt:      now,
			UpdateUserID:   0,
			UpdateUserName: wxWorkProtocolSystemOperatorName,
		},
	})
}

func (s *wxWorkProtocolService) createMessageRef(conversationID, messageID int64, instance *models.WxWorkProtocolInstance, externalID, wxMsgID, rawPayload string, direction enums.WxWorkKFMessageDirection, sendStatus enums.WxWorkKFMessageSendStatus) error {
	if existing := WxWorkKFMessageRefService.GetByWxMsgID(wxMsgID); existing != nil {
		updates := map[string]any{
			"send_status": string(sendStatus),
			"raw_payload": strings.TrimSpace(rawPayload),
			"updated_at":  time.Now(),
		}
		if messageID > 0 && existing.MessageID <= 0 {
			updates["message_id"] = messageID
		}
		if conversationID > 0 && existing.ConversationID <= 0 {
			updates["conversation_id"] = conversationID
		}
		if sendStatus == enums.WxWorkKFMessageSendStatusSent || sendStatus == enums.WxWorkKFMessageSendStatusReceived {
			updates["fail_reason"] = ""
		}
		return WxWorkKFMessageRefService.Updates(existing.ID, updates)
	}
	now := time.Now()
	return WxWorkKFMessageRefService.Create(&models.WxWorkKFMessageRef{
		ConversationID: conversationID,
		MessageID:      messageID,
		WxMsgID:        strings.TrimSpace(wxMsgID),
		Direction:      string(direction),
		Origin:         0,
		OpenKfID:       "wx_protocol:" + strings.TrimSpace(instance.Guid),
		ExternalUserID: strings.TrimSpace(externalID),
		SendStatus:     string(sendStatus),
		RawPayload:     strings.TrimSpace(rawPayload),
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   0,
			CreateUserName: wxWorkProtocolSystemOperatorName,
			UpdatedAt:      now,
			UpdateUserID:   0,
			UpdateUserName: wxWorkProtocolSystemOperatorName,
		},
	})
}

func (s *wxWorkProtocolService) markOutboxFailed(outbox models.ChannelMessageOutbox, reason string) error {
	retryCount := outbox.RetryCount + 1
	now := time.Now()
	return ChannelMessageOutboxService.Updates(outbox.ID, map[string]any{
		"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count":   retryCount,
		"next_retry_at": now.Add(time.Minute),
		"last_error":    strings.TrimSpace(reason),
		"updated_at":    now,
	})
}

func (s *wxWorkProtocolService) postJSON(cfg *dto.WxWorkProtocolChannelConfig, path string, body any) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"app_key":    cfg.AppKey,
		"app_secret": cfg.AppSecret,
		"path":       path,
		"data":       body,
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(cfg.BaseURL)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(respBody), fmt.Errorf("企微协议接口返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := s.checkProtocolResponse(respBody); err != nil {
		return string(respBody), fmt.Errorf("%w; raw=%s", err, strings.TrimSpace(string(respBody)))
	}
	return string(respBody), nil
}

func (s *wxWorkProtocolService) callInstanceAPI(instanceID int64, path string, extra map[string]any, after func(instance *models.WxWorkProtocolInstance, response string) error) (string, error) {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return "", errorsx.InvalidParam("企微员工号实例不存在")
	}
	channel := ChannelService.Get(instance.ChannelID)
	if channel == nil || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return "", errorsx.InvalidParam("企微协议渠道不存在")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return "", errorsx.InvalidParam("企微协议渠道配置不合法")
	}
	body := map[string]any{"guid": strings.TrimSpace(instance.Guid)}
	for key, value := range extra {
		body[key] = value
	}
	resp, err := s.postJSON(cfg, path, body)
	if err != nil {
		return resp, err
	}
	if after != nil {
		if err := after(instance, resp); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (s *wxWorkProtocolService) profileUpdatesFromResponse(response string) map[string]any {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(response), &root); err != nil {
		return nil
	}
	data := root
	if nested, ok := root["data"].(map[string]any); ok {
		data = nested
	}
	getString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := data[key]; ok {
				text := strings.TrimSpace(fmt.Sprint(value))
				if text != "" && text != "<nil>" {
					return text
				}
			}
		}
		return ""
	}
	employeeUserID := getString("username", "user_name", "userName", "user_id", "userId", "wxid")
	employeeName := getString("real_name", "realName", "name", "nickname", "nickName", "alias")
	updates := map[string]any{"health_status": "online"}
	if employeeUserID != "" {
		updates["employee_user_id"] = employeeUserID
	}
	if employeeName != "" {
		updates["employee_name"] = employeeName
	}
	return updates
}

func (s *wxWorkProtocolService) checkProtocolResponse(respBody []byte) error {
	body := strings.TrimSpace(string(respBody))
	if body == "" || !strings.HasPrefix(body, "{") {
		return nil
	}
	resp := struct {
		ErrCode      *int   `json:"err_code"`
		ErrMsg       string `json:"err_msg"`
		ErrorCode    *int   `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Message      string `json:"message"`
		Success      *bool  `json:"success"`
	}{
		ErrCode:   nil,
		ErrorCode: nil,
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil
	}
	if resp.ErrCode != nil && *resp.ErrCode != 0 {
		msg := strings.TrimSpace(resp.ErrMsg)
		if msg == "" {
			msg = strings.TrimSpace(resp.Message)
		}
		return fmt.Errorf("企微协议接口返回错误 err_code=%d: %s", *resp.ErrCode, msg)
	}
	if resp.ErrorCode != nil && *resp.ErrorCode != 0 {
		msg := strings.TrimSpace(resp.ErrorMessage)
		if msg == "" {
			msg = strings.TrimSpace(resp.Message)
		}
		return fmt.Errorf("企微协议接口返回错误 error_code=%d: %s", *resp.ErrorCode, msg)
	}
	if resp.Success != nil && !*resp.Success {
		msg := strings.TrimSpace(resp.Message)
		if msg == "" {
			msg = strings.TrimSpace(resp.ErrMsg)
		}
		if msg == "" {
			msg = strings.TrimSpace(resp.ErrorMessage)
		}
		return fmt.Errorf("企微协议接口返回失败: %s", msg)
	}
	return nil
}

func (s *wxWorkProtocolService) createEchoMessageRef(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg, rawPayload string, clientMsgID string) error {
	externalID := s.externalConversationID(instance, msg)
	if externalID == "" {
		return nil
	}
	if mapping := WxWorkKFConversationService.Take("channel_id = ? AND open_kf_id = ? AND external_user_id = ? AND status = ?", instance.ChannelID, s.mappingOpenKfID(instance, msg), externalID, enums.StatusOk); mapping != nil {
		return s.createMessageRef(mapping.ConversationID, 0, instance, externalID, clientMsgID, rawPayload, enums.WxWorkKFMessageDirectionOut, enums.WxWorkKFMessageSendStatusSent)
	}
	return nil
}

func (s *wxWorkProtocolService) externalConversationID(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg) string {
	if chatroom := strings.TrimSpace(msg.Chatroom); chatroom != "" {
		return chatroom
	}
	from := strings.TrimSpace(msg.FromUsername)
	to := strings.TrimSpace(msg.ToUsername)
	employeeID := strings.TrimSpace(instance.EmployeeUserID)
	if employeeID != "" && from == employeeID {
		return to
	}
	return from
}

func (s *wxWorkProtocolService) clientMessageID(guid string, msg request.WxProtocolChatMsg) string {
	id := strings.TrimSpace(msg.MsgID)
	if id == "" {
		id = fmt.Sprintf("%s:%s:%d:%s", msg.FromUsername, msg.ToUsername, msg.CreateTime, msg.Content)
	}
	return "wx_protocol:" + strings.TrimSpace(guid) + ":" + id
}

func (s *wxWorkProtocolService) messageContent(msg request.WxProtocolChatMsg) string {
	return strings.TrimSpace(msg.Content)
}

func (s *wxWorkProtocolService) externalUser(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg, externalID string) openidentity.ExternalUser {
	name := strings.TrimSpace(msg.Desc)
	if name == "" {
		name = strings.TrimSpace(msg.ChatroomSender)
	}
	if name == "" {
		name = externalID
	}
	return openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     fmt.Sprintf("wxwork_protocol:%s:%s", strings.TrimSpace(instance.Guid), strings.TrimSpace(externalID)),
		ExternalName:   name,
	}
}

func (s *wxWorkProtocolService) mappingOpenKfID(instance *models.WxWorkProtocolInstance, msg request.WxProtocolChatMsg) string {
	kind := "single"
	if strings.TrimSpace(msg.Chatroom) != "" {
		kind = "room"
	}
	return "wx_protocol:" + strings.TrimSpace(instance.Guid) + ":" + kind
}

func (s *wxWorkProtocolService) channelAIAgentID(channelID int64) int64 {
	channel := ChannelService.Get(channelID)
	if channel == nil {
		return 0
	}
	return channel.AIAgentID
}
