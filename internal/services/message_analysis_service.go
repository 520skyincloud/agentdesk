package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/observationpolicy"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	messageAnalysisStatusPending = "pending"
	messageAnalysisStatusReady   = "ready"
	messageAnalysisStatusFailed  = "failed"
	messageAnalysisStatusStale   = "stale"
)

var MessageAnalysisService = &messageAnalysisService{}

type messageAnalysisService struct{}

type MessageAnalyzerIdentity struct {
	Kind    string
	Name    string
	Version string
}

func (s *messageAnalysisService) ContentFingerprint(message *models.Message) string {
	if message == nil {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write([]byte(string(message.MessageType)))
	_, _ = h.Write([]byte{'\n'})
	_, _ = h.Write([]byte(message.Content))
	_, _ = h.Write([]byte{'\n'})
	_, _ = h.Write(canonicalMessageSourcePayload(message.Payload))
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalMessageSourcePayload 只保留入站媒体来源，不把媒体理解派生字段算进
// 指纹。否则写回 mediaText/mediaStatus 后，刚完成的 Analysis 会立即失效。
func canonicalMessageSourcePayload(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	root := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return []byte(trimmed)
	}
	for _, key := range []string{
		"mediaText", "mediaSummary", "mediaUnderstandingStatus", "mediaUnderstandingError",
		"responseExpectation",
	} {
		delete(root, key)
	}
	removeEmptyJSONValues(root)
	encoded, err := json.Marshal(root)
	if err != nil {
		return []byte(trimmed)
	}
	return encoded
}

func removeEmptyJSONValues(value map[string]any) {
	for key, item := range value {
		switch typed := item.(type) {
		case nil:
			delete(value, key)
		case string:
			if strings.TrimSpace(typed) == "" {
				delete(value, key)
			}
		case map[string]any:
			removeEmptyJSONValues(typed)
			if len(typed) == 0 {
				delete(value, key)
			}
		case []any:
			if len(typed) == 0 {
				delete(value, key)
			}
		}
	}
}

func (s *messageAnalysisService) EnsurePending(message *models.Message, sourceRevision int, analyzer MessageAnalyzerIdentity) (*models.MessageAnalysis, error) {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 || sourceRevision <= 0 {
		return nil, fmt.Errorf("message analysis scope is invalid")
	}
	fingerprint := s.ContentFingerprint(message)
	now := time.Now()
	// 生产死锁修复（Error 1213）：MarkStale 的 UPDATE 与并发 INSERT 在唯一
	// 索引上互取间隙锁。stale 标记是幂等副词操作，移出插入事务，失败仅告警。
	if err := repositories.MessageAnalysisRepository.MarkStaleByMessageInTenant(sqls.DB(), message.TenantID, message.ID, fingerprint, now); err != nil {
		slog.Warn("message analysis mark stale failed", "message_id", message.ID, "tenant_id", message.TenantID, "error", err)
	}
	var item *models.MessageAnalysis
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		item, err = s.ensurePendingDB(ctx.Tx, message, sourceRevision, fingerprint, analyzer, now)
		return err
	})
	return item, err
}

func (s *messageAnalysisService) ensurePendingDB(db *gorm.DB, message *models.Message, sourceRevision int, fingerprint string, analyzer MessageAnalyzerIdentity, now time.Time) (*models.MessageAnalysis, error) {
	if db == nil || message == nil || message.ID <= 0 || message.TenantID <= 0 || sourceRevision <= 0 || strings.TrimSpace(fingerprint) == "" {
		return nil, fmt.Errorf("message analysis scope is invalid")
	}
	analyzer.Kind = strings.TrimSpace(analyzer.Kind)
	analyzer.Name = strings.TrimSpace(analyzer.Name)
	analyzer.Version = strings.TrimSpace(analyzer.Version)
	item := repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, sourceRevision)
	if item != nil {
		if item.ContentFingerprint == fingerprint && item.AnalyzerKind == analyzer.Kind && item.AnalyzerName == analyzer.Name && item.AnalyzerVersion == analyzer.Version {
			return item, nil
		}
		return nil, fmt.Errorf("message analysis revision %d already belongs to another source", sourceRevision)
	}
	item = &models.MessageAnalysis{
		TenantID: message.TenantID, MessageID: message.ID, SourceRevision: sourceRevision,
		ContentFingerprint: fingerprint, AnalysisStatus: messageAnalysisStatusPending,
		SchemaVersion: messageAnalysisSchemaVersionFor(analyzer),
		AnalyzerKind:  analyzer.Kind, AnalyzerName: analyzer.Name, AnalyzerVersion: analyzer.Version,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "message_analysis", UpdateUserName: "message_analysis"},
	}
	created, err := repositories.MessageAnalysisRepository.CreateIfAbsent(db, item)
	if err != nil {
		return nil, err
	}
	if !created {
		item = repositories.MessageAnalysisRepository.GetByRevisionInTenant(db, message.TenantID, message.ID, sourceRevision)
		if item == nil || item.ContentFingerprint != fingerprint || item.AnalyzerKind != analyzer.Kind || item.AnalyzerName != analyzer.Name || item.AnalyzerVersion != analyzer.Version {
			return nil, fmt.Errorf("message analysis revision %d changed concurrently", sourceRevision)
		}
	}
	return item, nil
}

func messageAnalysisSchemaVersionFor(analyzer MessageAnalyzerIdentity) string {
	if strings.EqualFold(strings.TrimSpace(analyzer.Version), "v2") {
		return contracts.MessageAnalysisV2SchemaVersion
	}
	switch strings.ToLower(strings.TrimSpace(analyzer.Kind)) {
	case "vision", "asr", "file_parser":
		return contracts.MessageAnalysisV2SchemaVersion
	default:
		return contracts.MessageAnalysisV1SchemaVersion
	}
}

// CompleteReadyV1 兼容入口：按 message_analysis.v1 编码并完成。
func (s *messageAnalysisService) CompleteReady(id, tenantID int64, analysis contracts.MessageAnalysisV1) error {
	encode := func(analyzedAt time.Time) ([]byte, error) { return encodeReadyMessageAnalysis(analysis, analyzedAt) }
	return s.completeReadyEncoded(id, tenantID, analysis.MessageID, analysis.SourceRevision, analysis.ContentFingerprint, contracts.MessageAnalysisV1SchemaVersion, encode)
}

// CompleteReadyV2 权威入口：按 message_analysis.v2 编码并完成。
func (s *messageAnalysisService) CompleteReadyV2(id, tenantID int64, analysis contracts.MessageAnalysisV2) error {
	encode := func(analyzedAt time.Time) ([]byte, error) { return encodeReadyMessageAnalysisV2(analysis, analyzedAt) }
	return s.completeReadyEncoded(id, tenantID, analysis.MessageID, analysis.SourceRevision, analysis.ContentFingerprint, contracts.MessageAnalysisV2SchemaVersion, encode)
}

func (s *messageAnalysisService) completeReadyEncoded(id, tenantID, messageID int64, sourceRevision int, contentFingerprint, schemaVersion string, encode func(time.Time) ([]byte, error)) error {
	if id <= 0 || tenantID <= 0 {
		return fmt.Errorf("message analysis scope is invalid")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.MessageAnalysisRepository.GetForUpdateInTenant(ctx.Tx, id, tenantID)
		if err != nil {
			return err
		}
		if item == nil || item.MessageID != messageID || item.SourceRevision != sourceRevision || item.ContentFingerprint != contentFingerprint {
			return fmt.Errorf("message analysis evidence no longer matches source")
		}
		analyzedAt := time.Now().UTC()
		if item.AnalysisStatus == messageAnalysisStatusReady {
			if item.AnalyzedAt == nil || strings.TrimSpace(item.AnalysisJSON) == "" {
				return fmt.Errorf("ready message analysis is missing evidence")
			}
			analyzedAt = item.AnalyzedAt.UTC()
		}
		raw, err := encode(analyzedAt)
		if err != nil {
			return err
		}
		if item.AnalysisStatus == messageAnalysisStatusReady {
			if item.AnalysisJSON != string(raw) {
				return fmt.Errorf("message analysis revision already completed with different evidence")
			}
			return nil
		}
		updated, err := repositories.MessageAnalysisRepository.CASStatusInTenant(ctx.Tx, id, tenantID, []string{messageAnalysisStatusPending, messageAnalysisStatusFailed}, map[string]any{
			"analysis_status": messageAnalysisStatusReady, "schema_version": schemaVersion,
			"analysis_json": string(raw), "error_code": "", "analyzed_at": analyzedAt,
			"updated_at": analyzedAt, "update_user_name": "message_analysis",
		})
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("message analysis state changed concurrently")
		}
		return nil
	})
}

// ReadyV2ForMessages 批量读取 Runtime 的权威 V2 媒体分析。V1、指纹不符、
// 非 ready 或 JSON/行不一致的记录不会进入当前 Turn。
func (s *messageAnalysisService) ReadyV2ForMessages(messages []models.Message) (map[int64]contracts.MessageAnalysisV2, error) {
	ret := make(map[int64]contracts.MessageAnalysisV2)
	if len(messages) == 0 {
		return ret, nil
	}
	tenantID := int64(0)
	messageByID := make(map[int64]models.Message, len(messages))
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message.ID <= 0 || message.TenantID <= 0 {
			continue
		}
		if tenantID == 0 {
			tenantID = message.TenantID
		}
		if message.TenantID != tenantID {
			return nil, fmt.Errorf("message analysis batch crosses tenant boundary")
		}
		if _, exists := messageByID[message.ID]; exists {
			continue
		}
		messageByID[message.ID] = message
		ids = append(ids, message.ID)
	}
	if tenantID <= 0 || len(ids) == 0 {
		return ret, nil
	}
	rows := repositories.MessageAnalysisRepository.FindLatestForMessagesInTenant(sqls.DB(), tenantID, ids)
	seen := make(map[int64]struct{}, len(rows))
	for _, item := range rows {
		if _, exists := seen[item.MessageID]; exists {
			continue
		}
		seen[item.MessageID] = struct{}{}
		message, ok := messageByID[item.MessageID]
		if !ok || item.AnalysisStatus != messageAnalysisStatusReady ||
			item.SchemaVersion != contracts.MessageAnalysisV2SchemaVersion ||
			item.ContentFingerprint != s.ContentFingerprint(&message) || strings.TrimSpace(item.AnalysisJSON) == "" {
			continue
		}
		parsed, err := strictjson.DecodeObject[contracts.MessageAnalysisV2]([]byte(item.AnalysisJSON), strictjson.DecodeOptions{
			MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV2),
		})
		if err != nil {
			return nil, err
		}
		if parsed.MessageID != item.MessageID || parsed.SourceRevision != item.SourceRevision ||
			parsed.ContentFingerprint != item.ContentFingerprint || parsed.Status != messageAnalysisStatusReady {
			return nil, fmt.Errorf("message analysis JSON does not match authoritative row")
		}
		ret[item.MessageID] = parsed
	}
	return ret, nil
}

func encodeReadyMessageAnalysis(analysis contracts.MessageAnalysisV1, analyzedAt time.Time) ([]byte, error) {
	analysis.SchemaVersion = contracts.MessageAnalysisV1SchemaVersion
	analysis.Status = messageAnalysisStatusReady
	analysis.ErrorCode = nil
	analysis.AnalyzedAt = &analyzedAt
	raw, err := json.Marshal(analysis)
	if err != nil {
		return nil, err
	}
	if _, err := strictjson.DecodeObject[contracts.MessageAnalysisV1](raw, strictjson.DecodeOptions{
		MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV1),
	}); err != nil {
		return nil, err
	}
	return raw, nil
}

func encodeReadyMessageAnalysisV2(analysis contracts.MessageAnalysisV2, analyzedAt time.Time) ([]byte, error) {
	analysis.SchemaVersion = contracts.MessageAnalysisV2SchemaVersion
	analysis.Status = messageAnalysisStatusReady
	analysis.Error = nil
	analysis.AnalyzedAt = &analyzedAt
	if analysis.MediaType == "" {
		analysis.MediaType = "none"
	}
	if analysis.Quality.Warnings == nil {
		analysis.Quality.Warnings = []string{}
	}
	if analysis.Quality.UncertainRanges == nil {
		analysis.Quality.UncertainRanges = []contracts.MessageAnalysisUncertainV2{}
	}
	if analysis.Observations == nil {
		analysis.Observations = []contracts.ObservationV2Item{}
	}
	if analysis.Quality.Completeness == "" {
		analysis.Quality.Completeness = "complete"
	}
	raw, err := json.Marshal(analysis)
	if err != nil {
		return nil, err
	}
	if _, err := strictjson.DecodeObject[contracts.MessageAnalysisV2](raw, strictjson.DecodeOptions{
		MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV2),
	}); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *messageAnalysisService) MarkFailed(id, tenantID int64, errorCode string) error {
	errorCode = strings.TrimSpace(errorCode)
	if id <= 0 || tenantID <= 0 || errorCode == "" {
		return fmt.Errorf("message analysis failure is invalid")
	}
	now := time.Now()
	updated, err := repositories.MessageAnalysisRepository.CASStatusInTenant(sqls.DB(), id, tenantID, []string{messageAnalysisStatusPending}, map[string]any{
		"analysis_status": messageAnalysisStatusFailed, "analysis_json": "", "error_code": errorCode,
		"analyzed_at": now, "updated_at": now, "update_user_name": "message_analysis",
	})
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("message analysis is not pending")
	}
	return nil
}

func (s *messageAnalysisService) ReadyForMessage(message *models.Message) (*contracts.MessageAnalysisV1, error) {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 {
		return nil, nil
	}
	item := repositories.MessageAnalysisRepository.GetLatestInTenant(sqls.DB(), message.TenantID, message.ID)
	if item == nil || item.AnalysisStatus != messageAnalysisStatusReady || item.ContentFingerprint != s.ContentFingerprint(message) || strings.TrimSpace(item.AnalysisJSON) == "" {
		return nil, nil
	}
	var decoded contracts.MessageAnalysisV1
	var decodedV2 *contracts.MessageAnalysisV2
	if strings.Contains(item.AnalysisJSON, contracts.MessageAnalysisV2SchemaVersion) {
		parsed, err := strictjson.DecodeObject[contracts.MessageAnalysisV2]([]byte(item.AnalysisJSON), strictjson.DecodeOptions{
			MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV2),
		})
		if err != nil {
			return nil, err
		}
		if parsed.MessageID != message.ID || parsed.SourceRevision != item.SourceRevision ||
			parsed.ContentFingerprint != item.ContentFingerprint || parsed.Status != messageAnalysisStatusReady {
			return nil, fmt.Errorf("message analysis JSON does not match authoritative row")
		}
		decodedV2 = &parsed
		// V1 兼容投影：老调用方继续读 V1 结构。
		decoded = contracts.MessageAnalysisV1{
			SchemaVersion: contracts.MessageAnalysisV1SchemaVersion,
			MessageID:     parsed.MessageID, SourceRevision: parsed.SourceRevision,
			ContentFingerprint: parsed.ContentFingerprint, Status: parsed.Status,
			Analyzer: contracts.MessageAnalysisAnalyzer{Kind: parsed.Analyzer.Kind, Name: parsed.Analyzer.Name, Version: parsed.Analyzer.Version},
			Result: &contracts.MessageAnalysisResult{
				Language: "zh", DialogueAct: "other", RelationToPrior: "unknown",
				NormalizedText: parsed.NormalizedText,
				Entities:       []contracts.MessageAnalysisEntity{}, MentionedTagKeys: []string{},
				RiskSignals: []string{"none"}, Confidence: parsed.Quality.OverallConfidence,
			},
			ErrorCode: nil,
		}
	} else {
		parsed, err := strictjson.DecodeObject[contracts.MessageAnalysisV1]([]byte(item.AnalysisJSON), strictjson.DecodeOptions{
			MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV1),
		})
		if err != nil {
			return nil, err
		}
		if parsed.MessageID != message.ID || parsed.SourceRevision != item.SourceRevision ||
			parsed.ContentFingerprint != item.ContentFingerprint || parsed.Status != messageAnalysisStatusReady {
			return nil, fmt.Errorf("message analysis JSON does not match authoritative row")
		}
		decoded = parsed
	}
	_ = decodedV2
	return &decoded, nil
}

func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// RecordMediaReady 是媒体理解成功后的权威状态写入（多模态契约 7，V2 成组）：
// Ensure pending revision -> CompleteReadyV2。analyzer.kind=vision/asr/file_parser
// 由 message_analysis.v2 Schema 允许；V1 只保留只读兼容。
func (s *messageAnalysisService) RecordMediaReady(message *models.Message, normalizedText string, analyzer MessageAnalyzerIdentity) error {
	candidate := defaultMediaAnalysisCandidate(message, normalizedText)
	return s.RecordMediaCandidateReady(message, candidate, false, analyzer)
}

// RecordMediaCandidateReady 把 Provider 候选经服务端最低权限策略投影后写成
// message_analysis.v2。Provider 无法自行授予门店事实、资源或人工权限。
func (s *messageAnalysisService) RecordMediaCandidateReady(message *models.Message, candidate contracts.MediaAnalysisCandidateV1, fallbackUsed bool, analyzer MessageAnalyzerIdentity) error {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 {
		return fmt.Errorf("message analysis scope is invalid")
	}
	if strings.TrimSpace(candidate.NormalizedText) == "" {
		return fmt.Errorf("normalized text is required for ready analysis")
	}
	item, err := s.EnsurePending(message, 1, analyzer)
	if err != nil {
		return err
	}
	if item == nil {
		return fmt.Errorf("message analysis row unavailable")
	}
	if enums.NormalizeMessageAnalysisStatus(item.AnalysisStatus) == enums.MessageAnalysisStatusReady {
		return nil
	}
	analysis, err := buildMediaAnalysisV2(message, item, candidate, fallbackUsed, analyzer)
	if err != nil {
		return err
	}
	return s.CompleteReadyV2(item.ID, item.TenantID, analysis)
}

// CommitMediaCandidateReady 在一个事务中提交媒体兼容投影和权威 V2 Analysis。
// Runtime 只能在事务成功后看见 understood Payload；因此不存在“Payload 已有
// 转写但 Analysis 仍 pending/V1”的半完成状态。
func (s *messageAnalysisService) CommitMediaCandidateReady(message *models.Message, payloadJSON string, candidate contracts.MediaAnalysisCandidateV1, fallbackUsed bool, analyzer MessageAnalyzerIdentity) (*models.Message, error) {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 {
		return nil, fmt.Errorf("message analysis scope is invalid")
	}
	if strings.TrimSpace(payloadJSON) == "" || strings.TrimSpace(candidate.NormalizedText) == "" {
		return nil, fmt.Errorf("media analysis payload and normalized text are required")
	}
	fingerprint := s.ContentFingerprint(message)
	now := time.Now()
	if err := repositories.MessageAnalysisRepository.MarkStaleByMessageInTenant(sqls.DB(), message.TenantID, message.ID, fingerprint, now); err != nil {
		slog.Warn("message analysis mark stale failed", "message_id", message.ID, "tenant_id", message.TenantID, "error", err)
	}
	var updated *models.Message
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.MessageRepository.GetInTenant(ctx.Tx, message.ID, message.TenantID)
		if current == nil || current.ConversationID != message.ConversationID {
			return fmt.Errorf("media source message is unavailable")
		}
		if s.ContentFingerprint(current) != fingerprint {
			return fmt.Errorf("media source message changed before analysis commit")
		}
		item, err := s.ensurePendingDB(ctx.Tx, current, 1, fingerprint, analyzer, now)
		if err != nil {
			return err
		}
		analysis, err := buildMediaAnalysisV2(current, item, candidate, fallbackUsed, analyzer)
		if err != nil {
			return err
		}
		analyzedAt := time.Now().UTC()
		if item.AnalysisStatus == messageAnalysisStatusReady {
			if item.AnalyzedAt == nil || strings.TrimSpace(item.AnalysisJSON) == "" {
				return fmt.Errorf("ready message analysis is missing evidence")
			}
			analyzedAt = item.AnalyzedAt.UTC()
		}
		raw, err := encodeReadyMessageAnalysisV2(analysis, analyzedAt)
		if err != nil {
			return err
		}
		if item.AnalysisStatus == messageAnalysisStatusReady && (item.SchemaVersion != contracts.MessageAnalysisV2SchemaVersion || item.AnalysisJSON != string(raw)) {
			return fmt.Errorf("message analysis revision already completed with different evidence")
		}
		if err := repositories.MessageRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, map[string]any{
			"payload": payloadJSON, "updated_at": analyzedAt, "update_user_name": "media_understanding",
		}); err != nil {
			return err
		}
		if item.AnalysisStatus != messageAnalysisStatusReady {
			ok, err := repositories.MessageAnalysisRepository.CASStatusInTenant(ctx.Tx, item.ID, item.TenantID, []string{messageAnalysisStatusPending, messageAnalysisStatusFailed}, map[string]any{
				"analysis_status": messageAnalysisStatusReady, "schema_version": contracts.MessageAnalysisV2SchemaVersion,
				"analysis_json": string(raw), "error_code": "", "analyzed_at": analyzedAt,
				"updated_at": analyzedAt, "update_user_name": "message_analysis",
			})
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("message analysis state changed concurrently")
			}
		}
		updated = repositories.MessageRepository.GetInTenant(ctx.Tx, current.ID, current.TenantID)
		if updated == nil {
			return fmt.Errorf("updated media message is unavailable")
		}
		return nil
	})
	return updated, err
}

func buildMediaAnalysisV2(message *models.Message, item *models.MessageAnalysis, candidate contracts.MediaAnalysisCandidateV1, fallbackUsed bool, analyzer MessageAnalyzerIdentity) (contracts.MessageAnalysisV2, error) {
	if message == nil || item == nil || strings.TrimSpace(candidate.NormalizedText) == "" {
		return contracts.MessageAnalysisV2{}, fmt.Errorf("media analysis evidence is incomplete")
	}
	mediaType := messageAnalysisMediaType(message.MessageType)
	observations := make([]contracts.ObservationV2Item, 0, len(candidate.Items))
	source := observationpolicy.Source{MessageID: message.ID, SourceRevision: item.SourceRevision, SourceType: mediaType}
	for _, value := range candidate.Items {
		value.Text = strings.TrimSpace(value.Text)
		if value.Text == "" {
			continue
		}
		allowed, forbidden := observationpolicy.Project(source, value)
		observations = append(observations, contracts.ObservationV2Item{
			Ref: fmt.Sprintf("O%d", len(observations)+1), SourceMessageID: message.ID,
			SourceRevision: item.SourceRevision, Status: "ready", SourceType: mediaType,
			ObservationType: value.ObservationType, Text: limitText(value.Text, 4000),
			Confidence: value.Confidence, AllowedUses: allowed, ForbiddenUses: forbidden,
		})
	}
	if len(observations) == 0 {
		fallback := defaultMediaAnalysisCandidate(message, candidate.NormalizedText)
		fallbackUsed = true
		for _, value := range fallback.Items {
			allowed, forbidden := observationpolicy.Project(source, value)
			observations = append(observations, contracts.ObservationV2Item{
				Ref: "O1", SourceMessageID: message.ID, SourceRevision: item.SourceRevision,
				Status: "ready", SourceType: mediaType, ObservationType: value.ObservationType,
				Text: limitText(value.Text, 4000), Confidence: value.Confidence,
				AllowedUses: allowed, ForbiddenUses: forbidden,
			})
		}
	}
	warnings := append([]string{}, candidate.Quality.Warnings...)
	if fallbackUsed && !containsStringValue(warnings, "provider_fallback_used") {
		warnings = append(warnings, "provider_fallback_used")
	}
	analysis := contracts.MessageAnalysisV2{
		SchemaVersion: contracts.MessageAnalysisV2SchemaVersion,
		MessageID:     message.ID, SourceRevision: item.SourceRevision,
		ContentFingerprint: item.ContentFingerprint, Status: "ready",
		MediaType: mediaType,
		Analyzer: contracts.MessageAnalysisAnalyzerV2{
			Kind: strings.TrimSpace(analyzer.Kind), Name: strings.TrimSpace(analyzer.Name), Version: strings.TrimSpace(analyzer.Version),
		},
		NormalizedText: limitText(candidate.NormalizedText, 4000),
		Quality: contracts.MessageAnalysisQualityV2{
			OverallConfidence: candidate.Quality.OverallConfidence, Completeness: candidate.Quality.Completeness,
			FallbackUsed: fallbackUsed, Warnings: warnings, UncertainRanges: candidate.Quality.UncertainRanges,
		},
		Observations:        observations,
		ResponseExpectation: candidate.ResponseExpectation,
		Error:               nil,
	}
	return analysis, nil
}

func defaultMediaAnalysisCandidate(message *models.Message, normalizedText string) contracts.MediaAnalysisCandidateV1 {
	text := limitText(strings.TrimSpace(normalizedText), 4000)
	item := contracts.MediaAnalysisCandidateItemV1{Text: text, Confidence: 0.9}
	switch {
	case message != nil && message.MessageType == enums.IMMessageTypeVoice:
		item.ObservationType = "transcript"
		item.ContentRole = "customer_spoken_input"
	case message != nil && message.MessageType == enums.IMMessageTypeAttachment:
		item.ObservationType = "document_text"
		item.ContentRole = "embedded_document"
	default:
		item.ObservationType = "scene_description"
		item.ContentRole = "unknown"
	}
	candidate := contracts.MediaAnalysisCandidateV1{
		SchemaVersion: contracts.SchemaMediaAnalysisCandidateV1, NormalizedText: text,
		Quality: contracts.MediaAnalysisCandidateQualityV1{OverallConfidence: 0.9, Completeness: "complete", Warnings: []string{}, UncertainRanges: []contracts.MessageAnalysisUncertainV2{}},
		Items:   []contracts.MediaAnalysisCandidateItemV1{item},
	}
	normalizeMediaResponseExpectation(message, &candidate)
	return candidate
}

func normalizeMediaResponseExpectation(message *models.Message, candidate *contracts.MediaAnalysisCandidateV1) {
	if candidate == nil {
		return
	}
	if candidate.ResponseExpectation != nil {
		candidate.ResponseExpectation.Mode = strings.TrimSpace(candidate.ResponseExpectation.Mode)
		candidate.ResponseExpectation.Basis = strings.TrimSpace(candidate.ResponseExpectation.Basis)
		return
	}
	expectation := &contracts.MediaResponseExpectationV1{
		Mode: "uncertain", Basis: "unknown", Confidence: 0.5,
	}
	if message != nil && message.MessageType == enums.IMMessageTypeVoice {
		expectation.Mode = "reply"
		expectation.Basis = "customer_spoken_input"
		expectation.Confidence = 1
	}
	candidate.ResponseExpectation = expectation
}

func containsStringValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// messageAnalysisMediaType 把消息类型映射到 V2 mediaType 枚举。
func messageAnalysisMediaType(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "image"
	case enums.IMMessageTypeVoice:
		return "voice"
	case enums.IMMessageTypeAttachment:
		return "attachment"
	default:
		return "none"
	}
}
