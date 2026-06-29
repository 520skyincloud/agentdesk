package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
	"time"
)

func BuildCustomer(item *models.Customer) *response.CustomerResponse {
	if item == nil {
		return nil
	}
	return &response.CustomerResponse{
		ID:             item.ID,
		Name:           item.Name,
		Avatar:         item.Avatar,
		Gender:         item.Gender,
		CompanyID:      item.CompanyID,
		Company:        BuildCompany(services.CompanyService.Get(item.CompanyID)),
		LastActiveAt:   utils.FormatTimePtr(item.LastActiveAt),
		PrimaryMobile:  item.PrimaryMobile,
		PrimaryEmail:   item.PrimaryEmail,
		Status:         item.Status,
		Remark:         item.Remark,
		StoreRelations: BuildStoreCustomerRelationList(services.CustomerService.ListStoreRelations(item.ID)),
		CreatedAt:      item.CreatedAt.Format(time.DateTime),
		UpdatedAt:      item.UpdatedAt.Format(time.DateTime),
	}
}

func BuildCustomerList(list []models.Customer) []response.CustomerResponse {
	results := make([]response.CustomerResponse, 0, len(list))
	for _, item := range list {
		if customer := BuildCustomer(&item); customer != nil {
			results = append(results, *customer)
		}
	}
	return results
}

func BuildStoreCustomerRelation(item *models.StoreCustomerRelation) *response.StoreCustomerRelationResponse {
	if item == nil {
		return nil
	}
	storeName := ""
	if store := services.StoreService.Get(item.StoreID); store != nil {
		storeName = store.Name
	}
	instanceName := ""
	if instance := services.WxWorkProtocolInstanceService.Get(item.WxWorkInstanceID); instance != nil {
		instanceName = instance.EmployeeName
		if instanceName == "" {
			instanceName = instance.EmployeeUserID
		}
	}
	return &response.StoreCustomerRelationResponse{
		ID:                 item.ID,
		CustomerID:         item.CustomerID,
		StoreID:            item.StoreID,
		StoreName:          storeName,
		WxWorkInstanceID:   item.WxWorkInstanceID,
		WxWorkInstanceName: instanceName,
		LastConversationID: item.LastConversationID,
		LastActiveAt:       utils.FormatTimePtr(item.LastActiveAt),
		VisitCount:         item.VisitCount,
		Tags:               item.Tags,
		StableNotes:        item.StableNotes,
		Status:             item.Status,
		CreatedAt:          item.CreatedAt.Format(time.DateTime),
		UpdatedAt:          item.UpdatedAt.Format(time.DateTime),
	}
}

func BuildStoreCustomerRelationList(list []models.StoreCustomerRelation) []response.StoreCustomerRelationResponse {
	results := make([]response.StoreCustomerRelationResponse, 0, len(list))
	for _, item := range list {
		if relation := BuildStoreCustomerRelation(&item); relation != nil {
			results = append(results, *relation)
		}
	}
	return results
}
