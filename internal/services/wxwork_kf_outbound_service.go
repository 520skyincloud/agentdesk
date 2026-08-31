package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/wxwork"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
	workkf "github.com/silenceper/wechat/v2/work/kf"
	"github.com/silenceper/wechat/v2/work/kf/sendmsg"
)

const (
	wxWorkKFOutboxBatchSize = 20
	wxWorkKFOutboxMaxRetry  = 6
)

var WxWorkKFOutboundService = newWxWorkKFOutboundService()

func newWxWorkKFOutboundService() *wxWorkKFOutboundService {
	return &wxWorkKFOutboundService{}
}

type wxWorkKFOutboundService struct {
}

type wxWorkKFOutboundChunk struct {
	MessageType enums.IMMessageType
	Content     string
	AssetID     string
}

type wxWorkKFOutboundChunkSender func(chunk wxWorkKFOutboundChunk, chunkIndex int) (string, error)

func (s *wxWorkKFOutboundService) DispatchPendingOutbox() int {
	if !wxwork.Enabled() {
		return 0
	}

	var totalCount int = 0
	for {
		count := s.doDispatchPendingOutbox(wxWorkKFOutboxBatchSize)

		totalCount += count
		slog.Info("wxwork kf outbound dispatch loop",
			"batch_count", count,
			"total_count", totalCount,
		)

		if count == 0 {
			break
		}
	}
	return totalCount
}

func (s *wxWorkKFOutboundService) doDispatchPendingOutbox(limit int) int {
	if !wxwork.Enabled() {
		return 0
	}
	if limit <= 0 {
		limit = wxWorkKFOutboxBatchSize
	}

	items := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkKF, limit)
	if len(items) == 0 {
		return 0
	}

	successCount := 0
	for i := range items {
		if err := s.processOutbox(items[i].ID); err != nil {
			slog.Warn("process wxwork kf outbox failed",
				"outbox_id", items[i].ID,
				"conversation_id", items[i].ConversationID,
				"message_id", items[i].MessageID,
				"error", err,
			)
			continue
		}
		successCount++
	}
	return successCount
}

func (s *wxWorkKFOutboundService) processOutbox(outboxID int64) error {
	outbox := ChannelMessageOutboxService.Get(outboxID)
	if outbox == nil {
		return nil
	}
	if outbox.ChannelType != enums.ChannelTypeWxWorkKF {
		return nil
	}
	if outbox.SendStatus == string(enums.ChannelMessageOutboxStatusSent) {
		return nil
	}
	if outbox.NextRetryAt != nil && outbox.NextRetryAt.After(time.Now()) {
		return nil
	}

	slog.Info("processing wxwork kf outbox",
		"outbox_id", outbox.ID,
		"conversation_id", outbox.ConversationID,
		"message_id", outbox.MessageID,
		"send_status", outbox.SendStatus,
		"retry_count", outbox.RetryCount,
	)

	message := MessageService.Get(outbox.MessageID)
	if message == nil {
		return s.markOutboxFailed(outbox, "平台消息不存在")
	}
	conversation := ConversationService.Get(outbox.ConversationID)
	if conversation == nil {
		return s.markOutboxFailed(outbox, "平台会话不存在")
	}
	mapping := WxWorkKFConversationService.Take("conversation_id = ?", conversation.ID)
	if mapping == nil {
		return s.markOutboxFailed(outbox, "企业微信会话映射不存在")
	}
	if mapping.ChannelID <= 0 {
		return s.markOutboxFailed(outbox, "企业微信会话映射缺少渠道ID")
	}
	channel := ChannelService.Get(mapping.ChannelID)
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkKF {
		return s.markOutboxFailed(outbox, "企业微信接入渠道不存在、未启用或类型不匹配")
	}
	if strings.TrimSpace(mapping.OpenKfID) == "" || strings.TrimSpace(mapping.ExternalUserID) == "" {
		return s.markOutboxFailed(outbox, "企业微信会话映射缺少发送必要参数")
	}
	chunks, buildErr := s.buildOutboundChunks(message)
	if buildErr != nil {
		return s.markOutboxFailed(outbox, buildErr.Error())
	}
	if len(chunks) == 0 {
		return s.markOutboxFailed(outbox, "当前消息无法转换为企业微信下行消息")
	}
	claimed, err := ChannelMessageOutboxService.ClaimForDispatch(*outbox, message)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	slog.Info("built wxwork outbound chunks",
		"outbox_id", outbox.ID,
		"conversation_id", conversation.ID,
		"message_id", message.ID,
		"sender_type", message.SenderType,
		"message_type", message.MessageType,
		"chunk_count", len(chunks),
		"open_kfid", mapping.OpenKfID,
		"external_userid", mapping.ExternalUserID,
	)

	wxMsgIDs, chunksCompleted, sendErr := s.sendClaimedOutboundChunks(outbox, message, chunks, func(chunk wxWorkKFOutboundChunk, chunkIndex int) (string, error) {
		wxMsgID, err := s.sendOutboundChunk(mapping, message, chunk, chunkIndex)
		if err != nil {
			return "", err
		}
		if _, err := s.persistAcceptedOutboundChunk(outbox, message, conversation, mapping, chunk, chunkIndex, wxMsgID); err != nil {
			return "", markExternalDispatchResultUncertain(fmt.Errorf("persist accepted wxwork kf message ref: %w", err))
		}
		return wxMsgID, nil
	})
	if sendErr != nil {
		return sendErr
	}
	if !chunksCompleted {
		return nil
	}

	completed := false
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		var err error
		completed, err = ChannelMessageOutboxService.completeClaimedDispatchWithDB(ctx.Tx, *outbox, now)
		if err != nil {
			return err
		}
		if !completed {
			return nil
		}
		return ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeWxWorkKFOutbound, message.SenderType, message.SenderID, fmt.Sprintf("企业微信消息发送成功，共%d条", len(wxMsgIDs)), "")
	})
	if err != nil || !completed {
		return err
	}
	return s.reconcileAcceptedChunkFailures(outbox)
}

func (s *wxWorkKFOutboundService) persistAcceptedOutboundChunk(
	outbox *models.ChannelMessageOutbox,
	message *models.Message,
	conversation *models.Conversation,
	mapping *models.WxWorkKFConversation,
	chunk wxWorkKFOutboundChunk,
	chunkIndex int,
	wxMsgID string,
) (*models.WxWorkKFMessageRef, error) {
	if outbox == nil || message == nil || conversation == nil || mapping == nil {
		return nil, fmt.Errorf("accepted wxwork kf chunk is missing delivery context")
	}
	wxMsgID = strings.TrimSpace(wxMsgID)
	if wxMsgID == "" {
		return nil, fmt.Errorf("accepted wxwork kf chunk is missing wx message id")
	}
	rawPayload := strings.TrimSpace(outbox.Payload)
	if payload, err := json.Marshal(map[string]any{
		"messageId":       message.ID,
		"dispatchAttempt": outbox.RetryCount,
		"chunkIndex":      chunkIndex,
		"chunkType":       chunk.MessageType,
		"chunkText":       strings.TrimSpace(chunk.Content),
		"chunkAssetId":    strings.TrimSpace(chunk.AssetID),
	}); err == nil {
		rawPayload = string(payload)
	}
	now := time.Now()
	created := &models.WxWorkKFMessageRef{
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		WxMsgID:        wxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		Origin:         0,
		OpenKfID:       strings.TrimSpace(mapping.OpenKfID),
		ExternalUserID: strings.TrimSpace(mapping.ExternalUserID),
		SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
		RawPayload:     rawPayload,
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   outbox.UpdateUserID,
			CreateUserName: outbox.UpdateUserName,
			UpdatedAt:      now,
			UpdateUserID:   outbox.UpdateUserID,
			UpdateUserName: outbox.UpdateUserName,
		},
	}
	if err := repositories.WxWorkKFMessageRefRepository.Create(sqls.DB(), created); err == nil {
		return created, nil
	} else if existing := repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), "wx_msg_id = ?", wxMsgID); existing == nil {
		return nil, err
	}

	existing := repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), "wx_msg_id = ?", wxMsgID)
	if existing == nil {
		return nil, fmt.Errorf("accepted wxwork kf message ref disappeared: %s", wxMsgID)
	}
	if strings.TrimSpace(existing.Direction) != string(enums.WxWorkKFMessageDirectionOut) ||
		(existing.MessageID > 0 && existing.MessageID != message.ID) ||
		(existing.ConversationID > 0 && existing.ConversationID != conversation.ID) {
		return nil, fmt.Errorf("wxwork kf message id collision: %s", wxMsgID)
	}
	if existing.MessageID > 0 {
		attempt, hasAttempt := wxWorkKFMessageRefDispatchAttempt(*existing)
		if (hasAttempt && attempt != outbox.RetryCount) || (!hasAttempt && outbox.RetryCount > 0) {
			return nil, fmt.Errorf("wxwork kf message id belongs to another dispatch attempt: %s", wxMsgID)
		}
		return existing, nil
	}

	updates := map[string]any{
		"conversation_id":  conversation.ID,
		"message_id":       message.ID,
		"direction":        string(enums.WxWorkKFMessageDirectionOut),
		"origin":           0,
		"open_kf_id":       strings.TrimSpace(mapping.OpenKfID),
		"external_user_id": strings.TrimSpace(mapping.ExternalUserID),
		"raw_payload":      rawPayload,
		"status":           enums.StatusOk,
		"updated_at":       now,
		"update_user_id":   outbox.UpdateUserID,
		"update_user_name": outbox.UpdateUserName,
	}
	if strings.TrimSpace(existing.SendStatus) != string(enums.WxWorkKFMessageSendStatusFailed) {
		updates["send_status"] = string(enums.WxWorkKFMessageSendStatusSent)
		updates["fail_reason"] = ""
	}
	if err := repositories.WxWorkKFMessageRefRepository.Updates(sqls.DB(), existing.ID, updates); err != nil {
		return nil, err
	}
	return repositories.WxWorkKFMessageRefRepository.Get(sqls.DB(), existing.ID), nil
}

func (s *wxWorkKFOutboundService) recordSendFailureCallback(
	conversationID int64,
	wxMsgID string,
	openKfID string,
	externalUserID string,
	failReason string,
) (*models.WxWorkKFMessageRef, error) {
	wxMsgID = strings.TrimSpace(wxMsgID)
	if wxMsgID == "" {
		return nil, fmt.Errorf("wxwork kf send failure callback is missing wx message id")
	}
	now := time.Now()
	created := &models.WxWorkKFMessageRef{
		ConversationID: conversationID,
		MessageID:      0,
		WxMsgID:        wxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		Origin:         0,
		OpenKfID:       strings.TrimSpace(openKfID),
		ExternalUserID: strings.TrimSpace(externalUserID),
		SendStatus:     string(enums.WxWorkKFMessageSendStatusFailed),
		FailReason:     strings.TrimSpace(failReason),
		RawPayload:     strings.TrimSpace(failReason),
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserName: wxWorkKFSystemOperatorName,
			UpdatedAt:      now,
			UpdateUserName: wxWorkKFSystemOperatorName,
		},
	}
	if err := repositories.WxWorkKFMessageRefRepository.Create(sqls.DB(), created); err == nil {
		return created, nil
	} else if existing := repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), "wx_msg_id = ?", wxMsgID); existing == nil {
		return nil, err
	}

	existing := repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), "wx_msg_id = ?", wxMsgID)
	if existing == nil {
		return nil, fmt.Errorf("wxwork kf failed message ref disappeared: %s", wxMsgID)
	}
	if strings.TrimSpace(existing.Direction) != string(enums.WxWorkKFMessageDirectionOut) ||
		(existing.ConversationID > 0 && conversationID > 0 && existing.ConversationID != conversationID) ||
		(strings.TrimSpace(existing.OpenKfID) != "" && strings.TrimSpace(openKfID) != "" && strings.TrimSpace(existing.OpenKfID) != strings.TrimSpace(openKfID)) ||
		(strings.TrimSpace(existing.ExternalUserID) != "" && strings.TrimSpace(externalUserID) != "" && strings.TrimSpace(existing.ExternalUserID) != strings.TrimSpace(externalUserID)) {
		return nil, fmt.Errorf("wxwork kf failed message callback target mismatch: %s", wxMsgID)
	}
	updates := map[string]any{
		"send_status":      string(enums.WxWorkKFMessageSendStatusFailed),
		"fail_reason":      strings.TrimSpace(failReason),
		"updated_at":       now,
		"update_user_name": wxWorkKFSystemOperatorName,
	}
	if existing.MessageID == 0 {
		updates["conversation_id"] = conversationID
		updates["open_kf_id"] = strings.TrimSpace(openKfID)
		updates["external_user_id"] = strings.TrimSpace(externalUserID)
	}
	if err := repositories.WxWorkKFMessageRefRepository.Updates(sqls.DB(), existing.ID, updates); err != nil {
		return nil, err
	}
	return repositories.WxWorkKFMessageRefRepository.Get(sqls.DB(), existing.ID), nil
}

func (s *wxWorkKFOutboundService) reconcileAcceptedChunkFailures(outbox *models.ChannelMessageOutbox) error {
	if outbox == nil {
		return nil
	}
	current := ChannelMessageOutboxService.Get(outbox.ID)
	if current == nil || strings.TrimSpace(current.SendStatus) != string(enums.ChannelMessageOutboxStatusSent) {
		return nil
	}
	refs := repositories.WxWorkKFMessageRefRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("message_id", current.MessageID).
		Eq("direction", string(enums.WxWorkKFMessageDirectionOut)).
		Asc("id"))
	for i := range refs {
		if strings.TrimSpace(refs[i].SendStatus) != string(enums.WxWorkKFMessageSendStatusFailed) ||
			!wxWorkKFMessageRefBelongsToOutboxAttempt(refs[i], *current) {
			continue
		}
		reason := strings.TrimSpace(refs[i].FailReason)
		if reason == "" {
			reason = "wxwork kf asynchronous delivery failure"
		}
		_, err := s.markOutboxFailedFromCallback(current, &refs[i], reason)
		return err
	}
	return nil
}

func (s *wxWorkKFOutboundService) sendClaimedOutboundChunks(outbox *models.ChannelMessageOutbox, message *models.Message, chunks []wxWorkKFOutboundChunk, sendChunk wxWorkKFOutboundChunkSender) ([]string, bool, error) {
	if outbox == nil || message == nil || len(chunks) == 0 || sendChunk == nil {
		return nil, false, nil
	}
	allowed, err := ChannelMessageOutboxService.RevalidateClaimedForDispatch(*outbox, message)
	if err != nil || !allowed {
		return nil, false, err
	}

	wxMsgIDs := make([]string, 0, len(chunks))
	for i := range chunks {
		wxMsgID, sendErr := sendChunk(chunks[i], i)
		if sendErr == nil {
			wxMsgIDs = append(wxMsgIDs, wxMsgID)
			continue
		}
		if len(wxMsgIDs) == 0 && !isExternalDispatchResultUncertain(sendErr) {
			return nil, false, s.markClaimedOutboxFailed(outbox, sendErr.Error())
		}
		reason := fmt.Sprintf("wxwork kf delivery result uncertain after %d/%d confirmed chunks: %v", len(wxMsgIDs), len(chunks), sendErr)
		_, markErr := ChannelMessageOutboxService.cancelClaimedDispatchUncertainWithDB(sqls.DB(), *outbox, reason)
		slog.Warn("wxwork kf outbound delivery is partial or uncertain",
			"outbox_id", outbox.ID,
			"conversation_id", outbox.ConversationID,
			"message_id", outbox.MessageID,
			"confirmed_chunk_count", len(wxMsgIDs),
			"total_chunk_count", len(chunks),
			"failed_chunk_index", i,
			"delivery_state", "partial_or_uncertain",
			"error", sendErr,
			"state_write_error", markErr,
		)
		return wxMsgIDs, false, markErr
	}
	return wxMsgIDs, true, nil
}

func (s *wxWorkKFOutboundService) sendOutboundChunk(mapping *models.WxWorkKFConversation, message *models.Message, chunk wxWorkKFOutboundChunk, chunkIndex int) (string, error) {
	switch chunk.MessageType {
	case enums.IMMessageTypeText:
		return s.sendTextMessage(mapping, message, chunk.Content, chunkIndex)
	case enums.IMMessageTypeImage:
		return s.sendImageMessage(mapping, message, chunk, chunkIndex)
	default:
		return "", fmt.Errorf("不支持的企业微信下行消息类型: %s", chunk.MessageType)
	}
}

func (s *wxWorkKFOutboundService) sendTextMessage(mapping *models.WxWorkKFConversation, message *models.Message, content string, chunkIndex int) (string, error) {
	cli, err := wxwork.GetWorkCli().GetKF()
	if err != nil {
		return "", err
	}

	req := sendmsg.Text{}
	req.Message.ToUser = strings.TrimSpace(mapping.ExternalUserID)
	req.Message.OpenKFID = strings.TrimSpace(mapping.OpenKfID)
	req.Message.MsgID = s.buildOutboundClientMsgID(message.ID, chunkIndex)
	req.MsgType = "text"
	req.Text.Content = strings.TrimSpace(content)

	slog.Info("sending wxwork text message",
		"conversation_id", message.ConversationID,
		"message_id", message.ID,
		"chunk_index", chunkIndex,
		"client_msg_id", req.Message.MsgID,
		"open_kfid", req.Message.OpenKFID,
		"external_userid", req.Message.ToUser,
		"content_length", len([]rune(req.Text.Content)),
	)

	resp, err := cli.SendMsg(req)
	if err != nil {
		if isKnownWxWorkKFSendFailure(err) {
			return "", err
		}
		return "", markExternalDispatchResultUncertain(err)
	}
	if strings.TrimSpace(resp.MsgID) == "" {
		return "", markExternalDispatchResultUncertain(fmt.Errorf("企业微信返回的消息ID为空"))
	}
	slog.Info("wxwork text message accepted",
		"conversation_id", message.ConversationID,
		"message_id", message.ID,
		"chunk_index", chunkIndex,
		"client_msg_id", req.Message.MsgID,
		"wx_msg_id", strings.TrimSpace(resp.MsgID),
		"open_kfid", req.Message.OpenKFID,
		"external_userid", req.Message.ToUser,
	)
	return strings.TrimSpace(resp.MsgID), nil
}

func (s *wxWorkKFOutboundService) sendImageMessage(mapping *models.WxWorkKFConversation, message *models.Message, chunk wxWorkKFOutboundChunk, chunkIndex int) (string, error) {
	if strings.TrimSpace(chunk.AssetID) == "" {
		return "", fmt.Errorf("图片消息缺少 assetId")
	}

	asset := AssetService.GetByAssetID(chunk.AssetID)
	if asset == nil {
		return "", fmt.Errorf("图片资源不存在")
	}
	fileReader, err := AssetService.OpenReader(asset)
	if err != nil {
		return "", err
	}
	defer func() {
		if fileReader != nil {
			_ = fileReader.Close()
		}
	}()

	slog.Info("sending wxwork image message",
		"conversation_id", message.ConversationID,
		"message_id", message.ID,
		"chunk_index", chunkIndex,
		"asset_id", asset.AssetID,
		"filename", asset.Filename,
		"storage_key", asset.StorageKey,
		"open_kfid", mapping.OpenKfID,
		"external_userid", mapping.ExternalUserID,
	)

	materialCli := wxwork.GetWorkCli().GetMaterial()
	uploadResp, err := materialCli.UploadTempFileFromReader(asset.Filename, "image", fileReader)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(uploadResp.MediaID) == "" {
		return "", fmt.Errorf("企业微信返回的图片 media_id 为空")
	}

	kfCli, err := wxwork.GetWorkCli().GetKF()
	if err != nil {
		return "", err
	}
	req := sendmsg.Image{
		Message: sendmsg.Message{
			ToUser:   strings.TrimSpace(mapping.ExternalUserID),
			OpenKFID: strings.TrimSpace(mapping.OpenKfID),
			MsgID:    s.buildOutboundClientMsgID(message.ID, chunkIndex),
		},
		MsgType: "image",
	}
	req.Image.MediaID = strings.TrimSpace(uploadResp.MediaID)

	resp, err := kfCli.SendMsg(req)
	if err != nil {
		if isKnownWxWorkKFSendFailure(err) {
			return "", err
		}
		return "", markExternalDispatchResultUncertain(err)
	}
	if strings.TrimSpace(resp.MsgID) == "" {
		return "", markExternalDispatchResultUncertain(fmt.Errorf("企业微信返回的消息ID为空"))
	}
	slog.Info("wxwork image message accepted",
		"conversation_id", message.ConversationID,
		"message_id", message.ID,
		"chunk_index", chunkIndex,
		"client_msg_id", req.Message.MsgID,
		"wx_msg_id", strings.TrimSpace(resp.MsgID),
		"media_id", req.Image.MediaID,
		"asset_id", asset.AssetID,
		"open_kfid", req.Message.OpenKFID,
		"external_userid", req.Message.ToUser,
	)
	return strings.TrimSpace(resp.MsgID), nil
}

func isKnownWxWorkKFSendFailure(err error) bool {
	var sdkErr workkf.Error
	return errors.As(err, &sdkErr)
}

func (s *wxWorkKFOutboundService) markOutboxFailed(outbox *models.ChannelMessageOutbox, errMsg string) error {
	if outbox == nil {
		return nil
	}
	slog.Warn("mark wxwork kf outbox failed",
		"outbox_id", outbox.ID,
		"conversation_id", outbox.ConversationID,
		"message_id", outbox.MessageID,
		"retry_count", outbox.RetryCount+1,
		"error", strings.TrimSpace(errMsg),
	)
	retryCount := outbox.RetryCount + 1
	nextRetryAt := s.nextRetryAt(retryCount)
	if retryCount >= wxWorkKFOutboxMaxRetry {
		nextRetryAt = nil
	}
	_, err := ChannelMessageOutboxService.failUnclaimedDispatchWithDB(sqls.DB(), *outbox, nextRetryAt, errMsg)
	return err
}

func (s *wxWorkKFOutboundService) markOutboxFailedFromCallback(outbox *models.ChannelMessageOutbox, failedRef *models.WxWorkKFMessageRef, errMsg string) (bool, error) {
	if outbox == nil || failedRef == nil {
		return false, nil
	}
	applied := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ChannelMessageOutboxRepository.Get(ctx.Tx, outbox.ID)
		if current == nil || strings.TrimSpace(current.SendStatus) != string(enums.ChannelMessageOutboxStatusSent) ||
			!wxWorkKFMessageRefBelongsToOutboxAttempt(*failedRef, *current) {
			return nil
		}
		refs := repositories.WxWorkKFMessageRefRepository.Find(ctx.Tx, sqls.NewCnd().
			Eq("message_id", current.MessageID).
			Eq("direction", string(enums.WxWorkKFMessageDirectionOut)).
			Asc("id"))
		currentAttemptRefs := make([]models.WxWorkKFMessageRef, 0, len(refs))
		for i := range refs {
			if wxWorkKFMessageRefBelongsToOutboxAttempt(refs[i], *current) {
				currentAttemptRefs = append(currentAttemptRefs, refs[i])
			}
		}
		if len(currentAttemptRefs) == 0 {
			return nil
		}
		now := time.Now()
		updates := map[string]any{
			"sent_at":          nil,
			"next_retry_at":    nil,
			"last_error":       strings.TrimSpace(errMsg),
			"updated_at":       now,
			"update_user_id":   current.UpdateUserID,
			"update_user_name": current.UpdateUserName,
		}
		if len(currentAttemptRefs) == 1 {
			retryCount := current.RetryCount + 1
			updates["send_status"] = string(enums.ChannelMessageOutboxStatusFailed)
			updates["retry_count"] = retryCount
			if retryCount < wxWorkKFOutboxMaxRetry {
				updates["next_retry_at"] = s.nextRetryAt(retryCount)
			}
		} else {
			updates["send_status"] = string(enums.ChannelMessageOutboxStatusCancelled)
			updates["last_error"] = channelMessageOutboxDispatchUncertainReasonPrefix +
				"wxwork kf asynchronous failure after multi-chunk acceptance: " + strings.TrimSpace(errMsg)
		}
		result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status = ? AND retry_count = ?", current.ID, string(enums.ChannelMessageOutboxStatusSent), current.RetryCount).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		applied = result.RowsAffected > 0
		return nil
	})
	return applied, err
}

func wxWorkKFMessageRefBelongsToOutboxAttempt(ref models.WxWorkKFMessageRef, outbox models.ChannelMessageOutbox) bool {
	if attempt, ok := wxWorkKFMessageRefDispatchAttempt(ref); ok {
		return attempt == outbox.RetryCount
	}
	if outbox.SentAt == nil || ref.CreatedAt.IsZero() {
		return true
	}
	return !ref.CreatedAt.Before(*outbox.SentAt)
}

func wxWorkKFMessageRefDispatchAttempt(ref models.WxWorkKFMessageRef) (int, bool) {
	var payload struct {
		DispatchAttempt *int `json:"dispatchAttempt"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(ref.RawPayload)), &payload) != nil || payload.DispatchAttempt == nil || *payload.DispatchAttempt < 0 {
		return 0, false
	}
	return *payload.DispatchAttempt, true
}

func (s *wxWorkKFOutboundService) markClaimedOutboxFailed(outbox *models.ChannelMessageOutbox, errMsg string) error {
	if outbox == nil {
		return nil
	}
	now := time.Now()
	retryCount := outbox.RetryCount + 1
	nextRetryAt := s.nextRetryAt(retryCount)
	if retryCount >= wxWorkKFOutboxMaxRetry {
		nextRetryAt = nil
	}
	return sqls.DB().Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status = ?", outbox.ID, string(enums.ChannelMessageOutboxStatusSending)).
		Updates(map[string]any{
			"send_status":      string(enums.ChannelMessageOutboxStatusFailed),
			"retry_count":      retryCount,
			"next_retry_at":    nextRetryAt,
			"last_error":       strings.TrimSpace(errMsg),
			"updated_at":       now,
			"update_user_id":   outbox.UpdateUserID,
			"update_user_name": outbox.UpdateUserName,
		}).Error
}

func (s *wxWorkKFOutboundService) nextRetryAt(retryCount int) *time.Time {
	delay := time.Minute
	switch {
	case retryCount <= 1:
		delay = 30 * time.Second
	case retryCount == 2:
		delay = time.Minute
	case retryCount == 3:
		delay = 2 * time.Minute
	default:
		delay = 5 * time.Minute
	}
	t := time.Now().Add(delay)
	return &t
}

func (s *wxWorkKFOutboundService) buildOutboundClientMsgID(messageID int64, chunkIndex int) string {
	return fmt.Sprintf("outbox_wxwork_kf_%d_%d", messageID, chunkIndex)
}

type wxWorkKFOutboundPayload struct {
	ConversationID int64               `json:"conversationId"`
	MessageID      int64               `json:"messageId"`
	MessageType    enums.IMMessageType `json:"messageType"`
	Content        string              `json:"content"`
	Payload        string              `json:"payload"`
	SenderID       int64               `json:"senderId"`
}

func (s *wxWorkKFOutboundService) parseOutboxPayload(raw string) (*wxWorkKFOutboundPayload, error) {
	payload := &wxWorkKFOutboundPayload{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *wxWorkKFOutboundService) buildOutboundChunks(message *models.Message) ([]wxWorkKFOutboundChunk, error) {
	if message == nil {
		return nil, fmt.Errorf("平台消息不存在")
	}
	switch message.MessageType {
	case enums.IMMessageTypeText:
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return nil, fmt.Errorf("文本消息内容为空")
		}
		return []wxWorkKFOutboundChunk{{MessageType: enums.IMMessageTypeText, Content: content}}, nil
	case enums.IMMessageTypeHTML:
		return s.buildHTMLChunks(message.Content)
	default:
		return nil, fmt.Errorf("当前暂不支持企业微信下行消息类型: %s", message.MessageType)
	}
}

func (s *wxWorkKFOutboundService) buildHTMLChunks(content string) ([]wxWorkKFOutboundChunk, error) {
	contentChunks, err := utils.SplitHTMLContentChunks(content)
	if err != nil {
		return nil, err
	}
	chunks := make([]wxWorkKFOutboundChunk, 0, len(contentChunks))
	for _, chunk := range contentChunks {
		switch chunk.Type {
		case utils.ContentChunkTypeText:
			if strs.IsNotBlank(chunk.Content) {
				chunks = append(chunks, wxWorkKFOutboundChunk{
					MessageType: enums.IMMessageTypeText,
					Content:     chunk.Content,
				})
			}
		case utils.ContentChunkTypeImage:
			if strs.IsBlank(chunk.AssetID) {
				chunks = append(chunks, wxWorkKFOutboundChunk{
					MessageType: enums.IMMessageTypeText,
					Content:     "[图片]",
				})
			} else {
				chunks = append(chunks, wxWorkKFOutboundChunk{
					MessageType: enums.IMMessageTypeImage,
					AssetID:     chunk.AssetID,
				})
			}
		}
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("HTML 消息内容为空")
	}
	return chunks, nil
}
