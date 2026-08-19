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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/channelbreaker"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

var MediaUnderstandingService = newMediaUnderstandingService()

func newMediaUnderstandingService() *mediaUnderstandingService {
	return &mediaUnderstandingService{
		httpClient:          usagex.NewHTTPClient(60 * time.Second),
		analysisWorkerSlots: make(chan struct{}, mediaAnalysisMaxConcurrency),
	}
}

type mediaUnderstandingService struct {
	httpClient          *http.Client
	analysisWorkerSlots chan struct{}
}

type upstreamModelUsage struct {
	RequestID          string
	PromptTokens       int64
	CompletionTokens   int64
	CachedPromptTokens int64
	ReasoningTokens    int64
}

const visionConnectionTestImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAB0klEQVR4nAXBoQ6AIBRAUT/HbCabyWay+SWDMzGD6SUCMzkCiWRwJnYDyS/ynK4XBsEIo2CFSXDCLIiwCrugQhSSUIRH6PqFYcEsjAt2YVpwC/OCLKwL+4IuxIW0UBaeha7fGDbMxrhhN6YNtzFvyMa6sW/oRtxIG2Xj2eh6z+AxntFjPZPHeWaPeFbP7lFP9CRP8Tyerj8YDszBeGAPpgN3MB/IwXqwH+hBPEgH5eA56HplUIwyKlaZFKfMiiirsiuqRCUpRXmUrg8MARMYAzYwBVxgDkhgDewBDcRACpTAE+j6k+HEnIwn9mQ6cSfziZysJ/uJnsSTdFJOnpOuvxguzMV4YS+mC3cxX8jFerFf6EW8SBfl4rno+syQMZkxYzNTxmXmjGTWzJ7RTMykTMk8ma4vDAVTGAu2MBVcYS5IYS3sBS3EQiqUwlPo+pvhxtyMN/ZmunE3843crDf7jd7Em3RTbp6brn8ZXszL+GJfphf3Mr/Iy/qyv+hLfEkv5eV56frKUDGVsWIrU8VV5opU1spe0UqspEqpPJWubwwN0xgbtjE1XGNuSGNt7A1txEZqlMbT6PqP4cN8jB/2Y/pwH/OHfKwf+4d+xI/0UT6ejx/yfeAQHkqo/AAAAABJRU5ErkJggg=="

const (
	visionUnderstandingSystemPrompt = "你是图片内容识别助手。请描述图片中所有清晰可见的关键内容，不因内容与酒店无关而省略。优先说明主体、数量、颜色、形状、位置、场景、动作、界面和清晰文字；报错、问题、求助或操作诉求必须完整保留。只陈述画面证据，不猜测图片外事实，不写客服处理建议。无法确定具体物品时，先描述可见特征，再用“看起来像”或“可能是”给出候选。输出一段简洁但信息完整的中文。"
	visionUnderstandingUserPrompt   = "请识别这张图片并描述所有可见关键内容；如有文字请准确保留，不要只提取酒店服务相关信息。"
)

const (
	mediaAnalysisMaxConcurrency = 4
	mediaAnalysisLeaseDuration  = 2 * time.Minute
	mediaAnalysisRenewInterval  = 30 * time.Second
	mediaAnalysisPollInterval   = 50 * time.Millisecond
	mediaAnalysisAlertAttempt   = 4
)

var mediaAnalysisRetryDelays = []time.Duration{
	time.Second,
	3 * time.Second,
	10 * time.Second,
	30 * time.Second,
	time.Minute,
	3 * time.Minute,
	5 * time.Minute,
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

func (s *mediaUnderstandingService) EnsureInboundMessageAnalysis(messageID int64) (*models.MessageAnalysis, error) {
	message := repositories.MessageRepository.Get(sqls.DB(), messageID)
	if message == nil || message.SenderType != enums.IMSenderTypeCustomer || !isUnderstandableMessageType(message.MessageType) {
		return nil, nil
	}
	return MessageAnalysisService.EnsurePending(message, 1, s.analyzerIdentityFor(message))
}

// UnderstandInboundMessage is the synchronous wake-up API used by both the
// inbound channel and the AI reply worker. The durable MessageAnalysis row is
// the only execution ledger; concurrent callers wait for or reuse its result.
func (s *mediaUnderstandingService) UnderstandInboundMessage(ctx context.Context, messageID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	item, err := s.EnsureInboundMessageAnalysis(messageID)
	if err != nil || item == nil {
		return err
	}
	for {
		current := repositories.MessageAnalysisRepository.GetByRevisionInTenant(sqls.DB(), item.TenantID, item.MessageID, item.SourceRevision)
		if current == nil {
			return fmt.Errorf("message analysis row disappeared")
		}
		now := time.Now()
		switch enums.NormalizeMessageAnalysisStatus(current.AnalysisStatus) {
		case enums.MessageAnalysisStatusReady:
			return nil
		case enums.MessageAnalysisStatusFailedTerminal:
			return fmt.Errorf("media analysis failed terminally: %s", strings.TrimSpace(current.ErrorCode))
		case enums.MessageAnalysisStatusStale:
			return fmt.Errorf("media analysis source became stale")
		case enums.MessageAnalysisStatusFailedRetryable:
			if current.NextRetryAt != nil && current.NextRetryAt.After(now) {
				return fmt.Errorf("media analysis retry scheduled at %s", current.NextRetryAt.UTC().Format(time.RFC3339Nano))
			}
		case enums.MessageAnalysisStatusProcessing:
			if current.LeaseExpiresAt != nil && current.LeaseExpiresAt.After(now) {
				if !sleepContext(ctx, mediaAnalysisPollInterval) {
					return ctx.Err()
				}
				continue
			}
		}

		if !s.acquireAnalysisWorker(ctx) {
			return ctx.Err()
		}
		owner := "media-analysis-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		claimed, claimErr := repositories.MessageAnalysisRepository.TryClaim(
			sqls.DB(), current.ID, current.TenantID, owner, now, now.Add(mediaAnalysisLeaseDuration),
		)
		if claimErr != nil || !claimed {
			s.releaseAnalysisWorker()
			if claimErr != nil {
				return claimErr
			}
			if !sleepContext(ctx, mediaAnalysisPollInterval) {
				return ctx.Err()
			}
			continue
		}
		claimedItem := repositories.MessageAnalysisRepository.GetByRevisionInTenant(sqls.DB(), current.TenantID, current.MessageID, current.SourceRevision)
		if claimedItem == nil {
			s.releaseAnalysisWorker()
			return fmt.Errorf("claimed message analysis row disappeared")
		}
		runErr := s.executeClaimedAnalysis(ctx, claimedItem, owner)
		s.releaseAnalysisWorker()
		return runErr
	}
}

// ProcessDue resumes pending, retryable, or lease-expired media analyses after
// a process restart. It shares the same claim/CAS path as immediate wake-ups.
func (s *mediaUnderstandingService) ProcessDue(limit int) int {
	if limit <= 0 || limit > mediaAnalysisMaxConcurrency {
		limit = mediaAnalysisMaxConcurrency
	}
	now := time.Now()
	candidates := repositories.MessageAnalysisRepository.FindClaimableMedia(sqls.DB(), now, limit)
	claimedCount := 0
	for i := range candidates {
		if !s.tryAcquireAnalysisWorker() {
			break
		}
		owner := "media-analysis-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		claimed, claimErr := repositories.MessageAnalysisRepository.TryClaim(
			sqls.DB(), candidates[i].ID, candidates[i].TenantID, owner, now, now.Add(mediaAnalysisLeaseDuration),
		)
		if claimErr != nil || !claimed {
			s.releaseAnalysisWorker()
			if claimErr != nil {
				slog.Warn("claim media analysis failed", "analysis_id", candidates[i].ID, "error", claimErr)
			}
			continue
		}
		current := repositories.MessageAnalysisRepository.GetByRevisionInTenant(
			sqls.DB(), candidates[i].TenantID, candidates[i].MessageID, candidates[i].SourceRevision,
		)
		if current == nil {
			s.releaseAnalysisWorker()
			continue
		}
		claimedCount++
		go func(item models.MessageAnalysis, leaseOwner string) {
			defer s.releaseAnalysisWorker()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := s.executeClaimedAnalysis(ctx, &item, leaseOwner); err != nil {
				slog.Warn("process media analysis failed", "analysis_id", item.ID, "message_id", item.MessageID, "error", err)
			}
		}(*current, owner)
	}
	return claimedCount
}

func (s *mediaUnderstandingService) executeClaimedAnalysis(ctx context.Context, item *models.MessageAnalysis, owner string) error {
	if item == nil {
		return fmt.Errorf("claimed media analysis is unavailable")
	}
	message := repositories.MessageRepository.GetInTenant(sqls.DB(), item.MessageID, item.TenantID)
	if message == nil || message.SenderType != enums.IMSenderTypeCustomer || !isUnderstandableMessageType(message.MessageType) {
		return s.failClaimedAnalysis(item, owner, fmt.Errorf("media analysis source is invalid"), false, true)
	}
	if !MessageAnalysisService.sourceMatches(item, message) {
		if err := MessageAnalysisService.MarkClaimedMediaStale(item.ID, item.TenantID, owner); err != nil {
			return err
		}
		return fmt.Errorf("media analysis source changed before execution")
	}
	payload, err := parseMessageMediaPayload(message.Payload)
	if err != nil {
		return s.failClaimedAnalysis(item, owner, fmt.Errorf("媒体 payload 解析失败: %w", err), false, true)
	}
	if text := strings.TrimSpace(payload.MediaText); text != "" && strings.TrimSpace(payload.MediaStatus) == "understood" {
		updated, commitErr := MessageAnalysisService.CommitClaimedMediaReady(item.ID, item.TenantID, owner, text)
		if commitErr == nil {
			s.publishMediaMessageUpdated(updated)
		}
		return commitErr
	}

	runCtx, cancel := context.WithCancel(ctx)
	leaseLost := &atomic.Bool{}
	leaseDone := make(chan struct{})
	go s.renewAnalysisLease(runCtx, cancel, leaseDone, leaseLost, item, owner)
	var text string
	switch message.MessageType {
	case enums.IMMessageTypeImage:
		text, err = s.understandImage(runCtx, message, payload)
	case enums.IMMessageTypeVoice:
		text, err = s.transcribeVoice(runCtx, message, payload)
	case enums.IMMessageTypeAttachment:
		text, err = s.extractFileText(runCtx, message, payload)
	}
	cancel()
	<-leaseDone
	if leaseLost.Load() {
		return fmt.Errorf("media analysis lease lost")
	}
	if err != nil {
		return s.failClaimedAnalysis(item, owner, err, false, false)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return s.failClaimedAnalysis(item, owner, fmt.Errorf("媒体理解结果为空"), true, false)
	}
	updated, err := MessageAnalysisService.CommitClaimedMediaReady(item.ID, item.TenantID, owner, text)
	if err == nil {
		s.publishMediaMessageUpdated(updated)
	}
	return err
}

func (s *mediaUnderstandingService) failClaimedAnalysis(item *models.MessageAnalysis, owner string, cause error, empty, terminal bool) error {
	if item == nil {
		return cause
	}
	current := repositories.MessageAnalysisRepository.GetByRevisionInTenant(sqls.DB(), item.TenantID, item.MessageID, item.SourceRevision)
	attempt := 1
	if current != nil && current.AttemptCount > 0 {
		attempt = current.AttemptCount
	}
	status := enums.MessageAnalysisStatusFailedTerminal
	payloadStatus := "failed"
	errorClass := "media_understanding_failed"
	errorCode := "media_understanding_failed"
	var nextRetryAt *time.Time
	if empty {
		payloadStatus = "empty"
		errorClass = "empty_output"
		errorCode = "media_understanding_empty"
	}
	if !terminal {
		status = enums.MessageAnalysisStatusFailedRetryable
		payloadStatus = "retrying"
		if !empty {
			errorClass = "upstream_error"
			errorCode = "media_understanding_retryable"
		}
		retryAt := time.Now().Add(mediaAnalysisRetryDelay(attempt))
		nextRetryAt = &retryAt
		if attempt >= mediaAnalysisAlertAttempt {
			slog.Warn("media analysis remains retryable after repeated failures",
				"analysis_id", item.ID,
				"message_id", item.MessageID,
				"attempt_count", attempt,
				"error_class", errorClass,
				"next_retry_at", retryAt,
			)
		}
	}
	updated, commitErr := MessageAnalysisService.CommitClaimedMediaFailure(
		item.ID, item.TenantID, owner, status, errorClass, errorCode, payloadStatus, nextRetryAt,
	)
	if commitErr != nil {
		return commitErr
	}
	s.publishMediaMessageUpdated(updated)
	return cause
}

func mediaAnalysisRetryDelay(attempt int) time.Duration {
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(mediaAnalysisRetryDelays) {
		index = len(mediaAnalysisRetryDelays) - 1
	}
	return mediaAnalysisRetryDelays[index]
}

func (s *mediaUnderstandingService) renewAnalysisLease(ctx context.Context, cancel context.CancelFunc, done chan<- struct{}, lost *atomic.Bool, item *models.MessageAnalysis, owner string) {
	defer close(done)
	ticker := time.NewTicker(mediaAnalysisRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			ok, err := repositories.MessageAnalysisRepository.RenewLease(sqls.DB(), item.ID, item.TenantID, owner, now.Add(mediaAnalysisLeaseDuration))
			if err != nil || !ok {
				lost.Store(true)
				cancel()
				return
			}
		}
	}
}

func (s *mediaUnderstandingService) acquireAnalysisWorker(ctx context.Context) bool {
	select {
	case s.analysisWorkerSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *mediaUnderstandingService) tryAcquireAnalysisWorker() bool {
	select {
	case s.analysisWorkerSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *mediaUnderstandingService) releaseAnalysisWorker() {
	<-s.analysisWorkerSlots
}

func (s *mediaUnderstandingService) publishMediaMessageUpdated(message *models.Message) {
	if message == nil {
		return
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), message.ConversationID, message.TenantID)
	if conversation == nil {
		return
	}
	WsService.PublishMessageUpdated(conversation, message)
	WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
}

// analyzerIdentityFor 返回当前媒体分析器身份（写入 Analysis row 供审计与过期判断）。
func (s *mediaUnderstandingService) analyzerIdentityFor(message *models.Message) MessageAnalyzerIdentity {
	switch message.MessageType {
	case enums.IMMessageTypeImage:
		return MessageAnalyzerIdentity{Kind: "vision", Name: "media_understanding", Version: "v1"}
	case enums.IMMessageTypeVoice:
		return MessageAnalyzerIdentity{Kind: "asr", Name: "media_understanding", Version: "v1"}
	case enums.IMMessageTypeAttachment:
		return MessageAnalyzerIdentity{Kind: "file_parser", Name: "media_understanding", Version: "v1"}
	default:
		return MessageAnalyzerIdentity{Kind: "rule", Name: "media_understanding", Version: "v1"}
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
	resolved, err := ModelCallResolverService.ResolveForConversation(message.ConversationID, enums.ModelUsageSlotVision)
	if err != nil {
		return "", err
	}
	if resolved.TenantID != message.TenantID {
		return "", fmt.Errorf("图片消息与模型调用租户范围不一致")
	}
	imageURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	startedAt := time.Now()
	modelCtx := usagex.WithScope(ctx, modelCallUsageScope(resolved, message.ConversationID, message.ID, message.RequestID))
	modelCtx, usageCapture := usagex.WithCapture(modelCtx)
	// 视觉理解对体验影响大，超时/网络抖动不该直接判 failed。
	// 重试次数优先读 vision 槽位的 MaxRetryCount（默认 1 次重试 = 初次调用 + 1 次）。
	retryCount := resolved.MaxRetryCount
	if retryCount < 1 {
		retryCount = 1
	}
	if open, retryAt := channelbreaker.IsOpen("media_vision", resolved.ModelName, time.Now()); open {
		return "", fmt.Errorf("vision channel breaker open until %s", retryAt.Format(time.RFC3339))
	}
	text, usage, err := s.callOpenAICompatibleVisionWithUsage(modelCtx, resolved.RuntimeConfig(), imageURL)
	for attempt := 0; attempt < retryCount && err != nil && isRetryableMediaError(err); attempt++ {
		text, usage, err = s.callOpenAICompatibleVisionWithUsage(modelCtx, resolved.RuntimeConfig(), imageURL)
	}
	if err != nil {
		channelbreaker.RecordFailure("media_vision", resolved.ModelName, time.Now())
	} else {
		channelbreaker.RecordSuccess("media_vision", resolved.ModelName)
	}
	s.recordMediaModelUsage(message, resolved, "vision", usage, lastUsageReceipt(usageCapture), time.Since(startedAt).Milliseconds(), err)
	return text, err
}

func isRetryableMediaError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "http 429") ||
		strings.Contains(msg, "http 500") ||
		strings.Contains(msg, "http 502") ||
		strings.Contains(msg, "http 503")
}

func (s *mediaUnderstandingService) transcribeVoice(ctx context.Context, message *models.Message, payload *messageMediaPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("语音 payload 为空")
	}
	var protocolErr error
	// 契约 3.3：企微语音翻译主路径连续失败时熔断，直接使用已配置 ASR，
	// 不再每条语音重复失败（-5103017 场景）。
	voiceStage := "wxwork_voice_translate_" + strconv.FormatInt(message.TenantID, 10)
	if open, _ := channelbreaker.IsOpen(voiceStage, "protocol", time.Now()); open {
		protocolErr = fmt.Errorf("企微语音翻译通道熔断中")
	} else if text, err := s.transcribeWxWorkVoice(ctx, message, payload); err == nil && strings.TrimSpace(text) != "" {
		channelbreaker.RecordSuccess(voiceStage, "protocol")
		return text, nil
	} else if err != nil {
		protocolErr = err
		channelbreaker.RecordFailure(voiceStage, "protocol", time.Now())
	}
	asset := AssetService.GetByAssetIDInTenant(payload.AssetID, message.TenantID)
	data, _, err := s.readAssetBytes(message, asset, payload)
	if err != nil {
		if protocolErr != nil {
			return "", fmt.Errorf("企微语音翻译失败: %v；语音文件下载失败: %w", protocolErr, err)
		}
		return "", err
	}
	resolved, err := ModelCallResolverService.ResolveForConversation(message.ConversationID, enums.ModelUsageSlotASR)
	if err != nil {
		if protocolErr != nil {
			return "", fmt.Errorf("企微语音翻译失败: %v；ASR 模型配置失败: %w", protocolErr, err)
		}
		return "", err
	}
	if resolved.TenantID != message.TenantID {
		return "", fmt.Errorf("语音消息与模型调用租户范围不一致")
	}
	startedAt := time.Now()
	modelCtx := usagex.WithScope(ctx, modelCallUsageScope(resolved, message.ConversationID, message.ID, message.RequestID))
	modelCtx, usageCapture := usagex.WithCapture(modelCtx)
	text, usage, err := s.callOpenAICompatibleASRWithUsage(modelCtx, resolved.RuntimeConfig(), payload.Filename, data)
	s.recordMediaModelUsage(message, resolved, "asr", usage, lastUsageReceipt(usageCapture), time.Since(startedAt).Milliseconds(), err)
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

func (s *mediaUnderstandingService) callOpenAICompatibleASR(ctx context.Context, config modelconfig.Config, filename string, data []byte) (string, error) {
	text, _, err := s.callOpenAICompatibleASRWithUsage(ctx, config, filename, data)
	return text, err
}

func (s *mediaUnderstandingService) callOpenAICompatibleASRWithUsage(ctx context.Context, config modelconfig.Config, filename string, data []byte) (string, *upstreamModelUsage, error) {
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

func (s *mediaUnderstandingService) callOpenAICompatibleVision(ctx context.Context, config modelconfig.Config, imageURL string) (string, error) {
	text, _, err := s.callOpenAICompatibleVisionWithUsage(ctx, config, imageURL)
	return text, err
}

func (s *mediaUnderstandingService) callOpenAICompatibleVisionWithUsage(ctx context.Context, config modelconfig.Config, imageURL string) (string, *upstreamModelUsage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return "", nil, fmt.Errorf("视觉/多模态模型配置不完整")
	}
	body := map[string]any{
		"model": strings.TrimSpace(config.ModelName),
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": visionUnderstandingSystemPrompt,
			},
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": visionUnderstandingUserPrompt},
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

func (s *mediaUnderstandingService) recordMediaModelUsage(message *models.Message, resolved *ModelCallConfig, operationType string, usage *upstreamModelUsage, receipt *usagex.Receipt, latencyMS int64, callErr error) {
	if message == nil || resolved == nil {
		return
	}
	status := "completed"
	errorMessage := ""
	metricSource := AIUsageMetricSourceProviderOperation
	if callErr != nil {
		status = "failed"
		errorMessage = "model_call_failed"
	}
	stage := "media_vision"
	if operationType == "asr" {
		stage = "media_asr"
	}
	event := models.AIUsageEvent{
		EventKey:       fmt.Sprintf("%s:media_understanding:%s", firstNonBlank(message.RequestID, fmt.Sprintf("message-%d", message.ID)), operationType),
		ConversationID: message.ConversationID, MessageID: message.ID, RequestID: message.RequestID,
		Stage: stage, OperationType: operationType, MetricSource: metricSource,
		RequestCount: 1, LatencyMS: latencyMS, Status: status, ErrorClass: errorMessage,
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
	recordResolvedModelCall(event, resolved, receipt)
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
