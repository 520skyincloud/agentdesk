package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var KnowledgeEvidenceMetadataRepository = newKnowledgeEvidenceMetadataRepository()

type knowledgeEvidenceMetadataRepository struct{}

func newKnowledgeEvidenceMetadataRepository() *knowledgeEvidenceMetadataRepository {
	return &knowledgeEvidenceMetadataRepository{}
}

// FindBySourceRecords 按 (tenantID, storeID, knowledgeBaseID) + SourceRecordID 批量取元数据，
// 供 Evidence Judge 一次 join，不逐条查询。
func (r *knowledgeEvidenceMetadataRepository) FindBySourceRecords(db *gorm.DB, tenantID, storeID, knowledgeBaseID int64, sourceRecordIDs []string) []models.KnowledgeEvidenceMetadata {
	if db == nil || tenantID <= 0 || storeID <= 0 || knowledgeBaseID <= 0 || len(sourceRecordIDs) == 0 {
		return nil
	}
	var list []models.KnowledgeEvidenceMetadata
	err := db.Where("tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_record_id IN ?",
		tenantID, storeID, knowledgeBaseID, sourceRecordIDs).Find(&list).Error
	if err != nil {
		return nil
	}
	return list
}

// UpsertBatch 幂等写入元数据：按唯一键存在则仅在 digest 变化时更新，不存在则创建。
func (r *knowledgeEvidenceMetadataRepository) UpsertBatch(db *gorm.DB, items []models.KnowledgeEvidenceMetadata) error {
	if db == nil || len(items) == 0 {
		return nil
	}
	for _, item := range items {
		existing := &models.KnowledgeEvidenceMetadata{}
		err := db.Where("tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_record_id = ?",
			item.TenantID, item.StoreID, item.KnowledgeBaseID, item.SourceRecordID).First(existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&item).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if existing.SourceDigest != "" && existing.SourceDigest == item.SourceDigest {
			// 内容未变化时仍允许把旧的 unknown/pending 自动分类升级为更具体的
			// imported_faq/derived_qa。人工 approved/rejected 结论永不覆盖。
			if existing.ReviewStatus == "approved" || existing.ReviewStatus == "rejected" ||
				(existing.SourceClass != "unknown" && existing.ClaimType != "fact") {
				continue
			}
			updates := map[string]any{
				"source_class": item.SourceClass, "fact_scope": item.FactScope,
				"claim_type": item.ClaimType, "trust_level": item.TrustLevel,
				"freshness": item.Freshness, "topic_labels": item.TopicLabels,
				"resource_purpose": item.ResourcePurpose, "auto_attach_resource": item.AutoAttachResource,
				"updated_at": item.UpdatedAt,
			}
			if err := db.Model(existing).Updates(updates).Error; err != nil {
				return err
			}
			continue
		}
		updates := map[string]any{
			"source_class":         item.SourceClass,
			"fact_scope":           item.FactScope,
			"claim_type":           item.ClaimType,
			"trust_level":          item.TrustLevel,
			"freshness":            item.Freshness,
			"topic_labels":         item.TopicLabels,
			"resource_purpose":     item.ResourcePurpose,
			"auto_attach_resource": item.AutoAttachResource,
			"review_status":        "pending",
			"source_digest":        item.SourceDigest,
			"metadata_revision":    gorm.Expr("metadata_revision + 1"),
			"updated_at":           item.UpdatedAt,
		}
		if err := db.Model(existing).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
