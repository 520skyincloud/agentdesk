package migration

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(62, "normalize FastGPT profile template to one model gateway", func() error {
		return normalizeFastGPTProfileTemplateGateway(sqls.DB())
	})
}

func normalizeFastGPTProfileTemplateGateway(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(db)
	if template == nil {
		return nil
	}

	gateway, ok := commonFastGPTProfileGateway(
		template.EmbeddingBaseURL,
		template.DocumentParserBaseURL,
		template.VisionBaseURL,
		template.RerankBaseURL,
	)
	if !ok {
		return nil
	}
	if fastGPTProfileTemplateUsesGateway(template, gateway) {
		return nil
	}

	now := time.Now()
	revision := template.Revision + 1
	if revision <= 0 {
		revision = 1
	}
	updates := map[string]any{
		"chat_base_url":            gateway,
		"asr_base_url":             gateway,
		"embedding_base_url":       gateway,
		"document_parser_base_url": gateway,
		"vision_base_url":          gateway,
		"rerank_base_url":          gateway,
		"revision":                 revision,
		"status":                   "active",
		"updated_at":               now,
		"update_user_id":           constants.SystemAuditUserID,
		"update_user_name":         constants.SystemAuditUserName,
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.FastGPTProfileTemplate{}).
			Where("id = ?", template.ID).
			Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&models.FastGPTStoreTenant{}).
			Where("status = ?", "active").
			Updates(map[string]any{
				"profile_template_target_revision": revision,
				"profile_template_sync_status":     "pending",
				"profile_template_attempt_count":   0,
				"profile_template_next_retry_at":   now,
				"profile_template_last_error":      "",
				"updated_at":                       now,
				"update_user_id":                   constants.SystemAuditUserID,
				"update_user_name":                 constants.SystemAuditUserName,
			}).Error
	})
}

func commonFastGPTProfileGateway(values ...string) (string, bool) {
	gateway := ""
	for _, value := range values {
		normalized := strings.TrimRight(strings.TrimSpace(value), "/")
		if normalized == "" {
			return "", false
		}
		if gateway == "" {
			gateway = normalized
			continue
		}
		if normalized != gateway {
			return "", false
		}
	}
	return gateway, gateway != ""
}

func fastGPTProfileTemplateUsesGateway(template *models.FastGPTProfileTemplate, gateway string) bool {
	if template == nil || strings.TrimSpace(gateway) == "" {
		return false
	}
	for _, value := range []string{
		template.ChatBaseURL,
		template.ASRBaseURL,
		template.EmbeddingBaseURL,
		template.DocumentParserBaseURL,
		template.VisionBaseURL,
		template.RerankBaseURL,
	} {
		if strings.TrimRight(strings.TrimSpace(value), "/") != gateway {
			return false
		}
	}
	return true
}
