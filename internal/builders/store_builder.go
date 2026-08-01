package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
)

func BuildStore(item *models.Store, activeStaffCount, currentInstanceCount int64) *response.StoreResponse {
	if item == nil {
		return nil
	}
	return &response.StoreResponse{
		ID:                   item.ID,
		TenantID:             item.TenantID,
		StoreCode:            item.StoreCode,
		Name:                 item.Name,
		BrandName:            item.BrandName,
		Address:              item.Address,
		NavigationName:       item.NavigationName,
		Longitude:            item.Longitude,
		Latitude:             item.Latitude,
		MapProvider:          item.MapProvider,
		ContactPhone:         item.ContactPhone,
		KnowledgeBaseID:      item.KnowledgeBaseID,
		ActiveStaffCount:     activeStaffCount,
		CurrentInstanceCount: currentInstanceCount,
		Status:               item.Status,
		Remark:               item.Remark,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
	}
}

func BuildStoreList(list []models.Store, activeStaffCounts, currentInstanceCounts map[int64]int64) []response.StoreResponse {
	ret := make([]response.StoreResponse, 0, len(list))
	for i := range list {
		ret = append(ret, *BuildStore(&list[i], activeStaffCounts[list[i].ID], currentInstanceCounts[list[i].ID]))
	}
	return ret
}

func BuildStoreOptions(list []models.Store) []response.StoreOptionResponse {
	ret := make([]response.StoreOptionResponse, 0, len(list))
	for i := range list {
		ret = append(ret, response.StoreOptionResponse{ID: list[i].ID, StoreCode: list[i].StoreCode, Name: list[i].Name})
	}
	return ret
}
