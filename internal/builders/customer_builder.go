package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"time"
)

type CustomerBuildContext struct {
	StoreRelationsByCustomerID    map[int64][]models.StoreCustomerRelation
	StoresByID                    map[int64]*models.Store
	WxWorkInstancesByID           map[int64]*models.WxWorkProtocolInstance
	CustomerTagsByStoreRelationID map[int64][]response.CustomerTagResponse
}

func BuildCustomer(item *models.Customer) *response.CustomerResponse {
	return BuildCustomerWithContext(item, nil)
}

func BuildCustomerWithContext(item *models.Customer, ctx *CustomerBuildContext) *response.CustomerResponse {
	if item == nil {
		return nil
	}
	ret := &response.CustomerResponse{
		ID:            item.ID,
		Name:          item.Name,
		Avatar:        item.Avatar,
		Gender:        item.Gender,
		LastActiveAt:  utils.FormatTimePtr(item.LastActiveAt),
		PrimaryMobile: item.PrimaryMobile,
		PrimaryEmail:  item.PrimaryEmail,
		Status:        item.Status,
		Remark:        item.Remark,
		CreatedAt:     item.CreatedAt.Format(time.DateTime),
		UpdatedAt:     item.UpdatedAt.Format(time.DateTime),
	}
	if ctx != nil {
		ret.StoreRelations = BuildStoreCustomerRelationListWithContext(ctx.StoreRelationsByCustomerID[item.ID], ctx)
	}
	return ret
}

func BuildCustomerList(list []models.Customer) []response.CustomerResponse {
	return BuildCustomerListWithContext(list, nil)
}

func BuildCustomerListWithContext(list []models.Customer, ctx *CustomerBuildContext) []response.CustomerResponse {
	results := make([]response.CustomerResponse, 0, len(list))
	for _, item := range list {
		if customer := BuildCustomerWithContext(&item, ctx); customer != nil {
			results = append(results, *customer)
		}
	}
	return results
}

func BuildStoreCustomerRelation(item *models.StoreCustomerRelation) *response.StoreCustomerRelationResponse {
	return BuildStoreCustomerRelationWithContext(item, nil)
}

func BuildStoreCustomerRelationWithContext(item *models.StoreCustomerRelation, ctx *CustomerBuildContext) *response.StoreCustomerRelationResponse {
	if item == nil {
		return nil
	}
	storeName := ""
	instanceName := ""
	if ctx != nil {
		if store := ctx.StoresByID[item.StoreID]; store != nil {
			storeName = store.Name
		}
		if instance := ctx.WxWorkInstancesByID[item.WxWorkInstanceID]; instance != nil {
			instanceName = instance.EmployeeName
			if instanceName == "" {
				instanceName = instance.EmployeeUserID
			}
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
		CustomerTags:       customerTagsForStoreRelation(ctx, item.ID),
		Status:             item.Status,
		CreatedAt:          item.CreatedAt.Format(time.DateTime),
		UpdatedAt:          item.UpdatedAt.Format(time.DateTime),
	}
}

func customerTagsForStoreRelation(ctx *CustomerBuildContext, relationID int64) []response.CustomerTagResponse {
	if ctx == nil || relationID <= 0 {
		return []response.CustomerTagResponse{}
	}
	ret := ctx.CustomerTagsByStoreRelationID[relationID]
	if ret == nil {
		return []response.CustomerTagResponse{}
	}
	return ret
}

func BuildStoreCustomerRelationList(list []models.StoreCustomerRelation) []response.StoreCustomerRelationResponse {
	return BuildStoreCustomerRelationListWithContext(list, nil)
}

func BuildStoreCustomerRelationListWithContext(list []models.StoreCustomerRelation, ctx *CustomerBuildContext) []response.StoreCustomerRelationResponse {
	results := make([]response.StoreCustomerRelationResponse, 0, len(list))
	for _, item := range list {
		if relation := BuildStoreCustomerRelationWithContext(&item, ctx); relation != nil {
			results = append(results, *relation)
		}
	}
	return results
}
