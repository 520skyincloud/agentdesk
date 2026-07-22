package migration

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(61, "backfill FastGPT profile template chat and ASR slots", func() error {
		return backfillFastGPTProfileTemplateChatASR(sqls.DB())
	})
}

func backfillFastGPTProfileTemplateChatASR(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(db)
	if template == nil {
		return nil
	}

	updates := map[string]any{}
	if strings.TrimSpace(template.ChatProvider) == "" ||
		strings.TrimSpace(template.ChatBaseURL) == "" ||
		strings.TrimSpace(template.ChatModel) == "" {
		if chat := repositories.AIConfigRepository.GetEnabled(db, enums.AIModelTypeLLM); chat != nil {
			updates["chat_provider"] = strings.TrimSpace(string(chat.Provider))
			updates["chat_base_url"] = strings.TrimRight(strings.TrimSpace(chat.BaseURL), "/")
			updates["chat_model"] = strings.TrimSpace(chat.ModelName)
			updates["chat_api_mode"] = firstNonBlankMigrationValue(strings.TrimSpace(chat.APIMode), "chat_completions")
		}
	}
	if strings.TrimSpace(template.ASRProvider) == "" ||
		strings.TrimSpace(template.ASRBaseURL) == "" ||
		strings.TrimSpace(template.ASRModel) == "" {
		if asr := repositories.AIConfigRepository.GetEnabled(db, enums.AIModelTypeASR); asr != nil {
			updates["asr_provider"] = strings.TrimSpace(string(asr.Provider))
			updates["asr_base_url"] = strings.TrimRight(strings.TrimSpace(asr.BaseURL), "/")
			updates["asr_model"] = strings.TrimSpace(asr.ModelName)
		}
	}
	if len(updates) == 0 {
		return nil
	}

	now := time.Now()
	revision := template.Revision + 1
	if revision <= 0 {
		revision = 1
	}
	updates["revision"] = revision
	updates["status"] = "active"
	updates["updated_at"] = now
	updates["update_user_id"] = constants.SystemAuditUserID
	updates["update_user_name"] = constants.SystemAuditUserName

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

func firstNonBlankMigrationValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
