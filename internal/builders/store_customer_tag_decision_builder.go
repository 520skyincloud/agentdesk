package builders

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
)

func BuildStoreCustomerTagDecision(item *models.StoreCustomerTagDecision) *response.StoreCustomerTagDecisionResponse {
	if item == nil {
		return nil
	}
	return &response.StoreCustomerTagDecisionResponse{
		ID:                    item.ID,
		TenantID:              item.TenantID,
		CustomerID:            item.CustomerID,
		SourceStoreID:         item.SourceStoreID,
		SourceStoreRelationID: item.SourceStoreRelationID,
		TargetStoreID:         item.TargetStoreID,
		TargetStoreRelationID: item.TargetStoreRelationID,
		Strategy:              item.Strategy,
		SourceTagIDs:          decodeCustomerTagIDs(item.SourceTagIDsJSON),
		TargetBeforeTagIDs:    decodeCustomerTagIDs(item.TargetBeforeTagIDsJSON),
		TargetAfterTagIDs:     decodeCustomerTagIDs(item.TargetAfterTagIDsJSON),
		OperatorID:            item.OperatorID,
		OperatorName:          item.OperatorName,
		CreatedAt:             utils.FormatTime(item.CreatedAt),
	}
}

func decodeCustomerTagIDs(raw string) []int64 {
	ret := make([]int64, 0)
	if err := json.Unmarshal([]byte(raw), &ret); err != nil || ret == nil {
		return []int64{}
	}
	return ret
}
