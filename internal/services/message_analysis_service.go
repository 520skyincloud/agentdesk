package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
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
	var item *models.MessageAnalysis
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.MessageAnalysisRepository.MarkStaleByMessageInTenant(ctx.Tx, message.TenantID, message.ID, fingerprint, now); err != nil {
			return err
		}
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

func (s *messageAnalysisService) CompleteReady(id, tenantID int64, analysis contracts.MessageAnalysisV1) error {
	if id <= 0 || tenantID <= 0 {
		return fmt.Errorf("message analysis scope is invalid")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.MessageAnalysisRepository.GetForUpdateInTenant(ctx.Tx, id, tenantID)
		if err != nil {
			return err
		}
		if item == nil || item.MessageID != analysis.MessageID || item.SourceRevision != analysis.SourceRevision || item.ContentFingerprint != analysis.ContentFingerprint {
			return fmt.Errorf("message analysis evidence no longer matches source")
		}
		analyzedAt := time.Now().UTC()
		if item.AnalysisStatus == messageAnalysisStatusReady {
			if item.AnalyzedAt == nil || strings.TrimSpace(item.AnalysisJSON) == "" {
				return fmt.Errorf("ready message analysis is missing evidence")
			}
			analyzedAt = item.AnalyzedAt.UTC()
		}
		raw, err := encodeReadyMessageAnalysis(analysis, analyzedAt)
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
	decoded, err := strictjson.DecodeObject[contracts.MessageAnalysisV1]([]byte(item.AnalysisJSON), strictjson.DecodeOptions{
		MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaMessageAnalysisV1),
	})
	if err != nil {
		return nil, err
	}
	if decoded.MessageID != message.ID || decoded.SourceRevision != item.SourceRevision ||
		decoded.ContentFingerprint != item.ContentFingerprint || decoded.Status != messageAnalysisStatusReady {
		return nil, fmt.Errorf("message analysis JSON does not match authoritative row")
	}
	return &decoded, nil
}

func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
