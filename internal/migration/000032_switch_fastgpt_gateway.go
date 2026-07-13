package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(32, "switch FastGPT knowledge bases to the platform gateway", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return ctx.Tx.Model(&models.KnowledgeBase{}).
				Where("status <> ? AND (knowledge_type = ? OR chunk_provider = ?)", enums.StatusDeleted, string(enums.KnowledgeBaseTypeFastGPTCloud), string(enums.KnowledgeChunkProviderFastGPT)).
				Updates(map[string]any{
					"retrieval_mode":   enums.KnowledgeRetrievalModeFastGPT,
					"updated_at":       time.Now(),
					"update_user_id":   constants.SystemAuditUserID,
					"update_user_name": constants.SystemAuditUserName,
				}).Error
		})
	})
}
