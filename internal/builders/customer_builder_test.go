package builders

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestBuildCustomerUsesOnlyProvidedContext(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 30, 0, 0, time.Local)
	customer := &models.Customer{
		ID: 1, Name: "测试客户", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	relation := models.StoreCustomerRelation{
		ID: 3, CustomerID: customer.ID, StoreID: 4, WxWorkInstanceID: 5,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}

	basic := BuildCustomer(customer)
	if basic == nil || basic.StoreRelations != nil {
		t.Fatalf("basic builder must not perform optional lookups: %+v", basic)
	}

	result := BuildCustomerWithContext(customer, &CustomerBuildContext{
		StoreRelationsByCustomerID: map[int64][]models.StoreCustomerRelation{1: {relation}},
		StoresByID: map[int64]*models.Store{
			4: {ID: 4, Name: "测试门店", Status: enums.StatusOk},
		},
		WxWorkInstancesByID: map[int64]*models.WxWorkProtocolInstance{
			5: {ID: 5, EmployeeName: "门店员工号", Status: enums.StatusOk},
		},
	})
	if len(result.StoreRelations) != 1 || result.StoreRelations[0].StoreName != "测试门店" || result.StoreRelations[0].WxWorkInstanceName != "门店员工号" {
		t.Fatalf("expected relation labels from context, got %+v", result.StoreRelations)
	}
}
