package migration

import (
	"encoding/json"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(31, "backfill store-owned knowledge and reusable resources", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			knowledgeBases := repositories.KnowledgeBaseRepository.Find(ctx.Tx, sqls.NewCnd().Eq("store_id", 0).Where("status <> ?", enums.StatusDeleted))
			for i := range knowledgeBases {
				instances := repositories.WxWorkProtocolInstanceRepository.Find(ctx.Tx, sqls.NewCnd().Eq("knowledge_base_id", knowledgeBases[i].ID).Gt("store_id", 0).Where("status <> ?", enums.StatusDeleted))
				storeID, companyID, unique := uniqueKnowledgeStore(instances)
				if !unique {
					continue
				}
				updates := map[string]any{
					"store_id": storeID, "company_id": companyID, "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}
				datasetID, datasetName := legacyFastGPTDatasetBinding(knowledgeBases[i].Remark)
				if knowledgeBases[i].DatasetID == "" && datasetID != "" {
					updates["dataset_id"] = datasetID
					updates["dataset_name"] = firstNonEmptyMigrationValue(datasetName, knowledgeBases[i].Name)
					updates["connection_id"] = "platform"
				}
				if err := repositories.KnowledgeBaseRepository.Updates(ctx.Tx, knowledgeBases[i].ID, updates); err != nil {
					return err
				}
			}
			legacyCloudBases := repositories.KnowledgeBaseRepository.Find(ctx.Tx, sqls.NewCnd().Eq("dataset_id", "").Eq("knowledge_type", string(enums.KnowledgeBaseTypeFastGPTCloud)).Where("status <> ?", enums.StatusDeleted))
			for i := range legacyCloudBases {
				datasetID, datasetName := legacyFastGPTDatasetBinding(legacyCloudBases[i].Remark)
				if datasetID == "" {
					continue
				}
				if err := repositories.KnowledgeBaseRepository.Updates(ctx.Tx, legacyCloudBases[i].ID, map[string]any{
					"dataset_id": datasetID, "dataset_name": firstNonEmptyMigrationValue(datasetName, legacyCloudBases[i].Name), "connection_id": "platform", "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}); err != nil {
					return err
				}
			}

			groups := repositories.KnowledgeResourceGroupRepository.Find(ctx.Tx, sqls.NewCnd().Eq("store_id", 0).Gt("wx_work_instance_id", 0))
			for i := range groups {
				instance := repositories.WxWorkProtocolInstanceRepository.Get(ctx.Tx, groups[i].WxWorkInstanceID)
				if instance == nil || instance.StoreID <= 0 {
					continue
				}
				if err := repositories.KnowledgeResourceGroupRepository.Updates(ctx.Tx, groups[i].ID, map[string]any{
					"store_id": instance.StoreID, "company_id": instance.CompanyID, "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func legacyFastGPTDatasetBinding(remark string) (string, string) {
	payload := map[string]any{}
	if json.Unmarshal([]byte(strings.TrimSpace(remark)), &payload) != nil {
		return "", ""
	}
	datasetID := firstNonEmptyMigrationValue(stringMigrationValue(payload["datasetId"]), stringMigrationValue(payload["dataset_id"]), stringMigrationValue(payload["knowledgeBaseId"]))
	datasetName := firstNonEmptyMigrationValue(stringMigrationValue(payload["datasetName"]), stringMigrationValue(payload["dataset_name"]))
	return datasetID, datasetName
}

func stringMigrationValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstNonEmptyMigrationValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func uniqueKnowledgeStore(instances []models.WxWorkProtocolInstance) (int64, int64, bool) {
	storeID := int64(0)
	companyID := int64(0)
	for i := range instances {
		if storeID == 0 {
			storeID = instances[i].StoreID
			companyID = instances[i].CompanyID
			continue
		}
		if instances[i].StoreID != storeID {
			return 0, 0, false
		}
		if companyID == 0 {
			companyID = instances[i].CompanyID
		}
	}
	return storeID, companyID, storeID > 0
}
