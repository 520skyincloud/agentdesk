package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
)

func BuildHQQiyuRoute(item *models.HQQiyuRoute) response.HQQiyuRouteResponse {
	if item == nil {
		return response.HQQiyuRouteResponse{}
	}
	return response.HQQiyuRouteResponse{
		ID:              item.ID,
		DefaultGroupID:  item.DefaultGroupID,
		HighRiskGroupID: item.HighRiskGroupID,
		ServiceTime:     item.ServiceTime,
		FallbackMode:    item.FallbackMode,
		TimeoutMinutes:  item.TimeoutMinutes,
		Status:          int(item.Status),
		Remark:          item.Remark,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}
