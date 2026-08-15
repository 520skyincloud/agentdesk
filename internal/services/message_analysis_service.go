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
	_, _ = h.Write([]byte(message.Payload))
	return hex.EncodeToString(h.Sum(nil))
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
		item = repositories.MessageAnalysisRepository.GetByRevisionInTenant(ctx.Tx, message.TenantID, message.ID, sourceRevision)
		if item != nil {
			if item.ContentFingerprint == fingerprint && item.AnalyzerKind == analyzer.Kind && item.AnalyzerName == analyzer.Name && item.AnalyzerVersion == analyzer.Version {
				return nil
			}
			return fmt.Errorf("message analysis revision %d already belongs to another source", sourceRevision)
		}
		item = &models.MessageAnalysis{
			TenantID: message.TenantID, MessageID: message.ID, SourceRevision: sourceRevision,
			ContentFingerprint: fingerprint, AnalysisStatus: messageAnalysisStatusPending,
			SchemaVersion: contracts.MessageAnalysisV1SchemaVersion,
			AnalyzerKind:  strings.TrimSpace(analyzer.Kind), AnalyzerName: strings.TrimSpace(analyzer.Name), AnalyzerVersion: strings.TrimSpace(analyzer.Version),
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "message_analysis", UpdateUserName: "message_analysis"},
		}
		created, err := repositories.MessageAnalysisRepository.CreateIfAbsent(ctx.Tx, item)
		if err != nil {
			return err
		}
		if !created {
			item = repositories.MessageAnalysisRepository.GetByRevisionInTenant(ctx.Tx, message.TenantID, message.ID, sourceRevision)
		}
		return nil
	})
	return item, err
}

// CompleteReadyV1 兼容入口：按 message_analysis.v1 编码并完成。
func (s *messageAnalysisService) CompleteReady(id, tenantID int64, analysis contracts.MessageAnalysisV1) error {
	encode := func(analyzedAt time.Time) ([]byte, error) { return encodeReadyMessageAnalysis(analysis, analyzedAt) }
	return s.completeReadyEncoded(id, tenantID, analysis.MessageID, analysis.SourceRevision, analysis.ContentFingerprint, encode)
}

// CompleteReadyV2 权威入口：按 message_analysis.v2 编码并完成。
func (s *messageAnalysisService) CompleteReadyV2(id, tenantID int64, analysis contracts.MessageAnalysisV2) error {
	encode := func(analyzedAt time.Time) ([]byte, error) { return encodeReadyMessageAnalysisV2(analysis, analyzedAt) }
	return s.completeReadyEncoded(id, tenantID, analysis.MessageID, analysis.SourceRevision, analysis.ContentFingerprint, encode)
}

func (s *messageAnalysisService) completeReadyEncoded(id, tenantID, messageID int64, sourceRevision int, contentFingerprint string, encode func(time.Time) ([]byte, error)) error {
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
			"analysis_status": messageAnalysisStatusReady, "analysis_json": string(raw), "error_code": "", "analyzed_at": analyzedAt,
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
	if message == nil || message.ID <= 0 || message.TenantID <= 0 {
		return fmt.Errorf("message analysis scope is invalid")
	}
	if strings.TrimSpace(normalizedText) == "" {
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
	mediaType := messageAnalysisMediaType(message.MessageType)
	analysis := contracts.MessageAnalysisV2{
		SchemaVersion: contracts.MessageAnalysisV2SchemaVersion,
		MessageID:     message.ID, SourceRevision: item.SourceRevision,
		ContentFingerprint: item.ContentFingerprint, Status: "ready",
		MediaType: mediaType,
		Analyzer: contracts.MessageAnalysisAnalyzerV2{
			Kind: strings.TrimSpace(analyzer.Kind), Name: strings.TrimSpace(analyzer.Name), Version: strings.TrimSpace(analyzer.Version),
		},
		NormalizedText: limitText(normalizedText, 4000),
		Quality: contracts.MessageAnalysisQualityV2{
			OverallConfidence: 0.9, Completeness: "complete", FallbackUsed: false,
			Warnings: []string{}, UncertainRanges: []contracts.MessageAnalysisUncertainV2{},
		},
		Observations: []contracts.ObservationV2Item{},
		Error:        nil,
	}
	return s.CompleteReadyV2(item.ID, item.TenantID, analysis)
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
