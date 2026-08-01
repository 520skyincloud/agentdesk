package response

import (
	"agent-desk/internal/pkg/enums"
	"time"
)

type StoreResponse struct {
	ID                   int64        `json:"id"`
	TenantID             int64        `json:"tenantId"`
	StoreCode            string       `json:"storeCode"`
	Name                 string       `json:"name"`
	BrandName            string       `json:"brandName"`
	Address              string       `json:"address"`
	NavigationName       string       `json:"navigationName"`
	Longitude            string       `json:"longitude"`
	Latitude             string       `json:"latitude"`
	MapProvider          string       `json:"mapProvider"`
	ContactPhone         string       `json:"contactPhone"`
	KnowledgeBaseID      int64        `json:"knowledgeBaseId"`
	ActiveStaffCount     int64        `json:"activeStaffCount"`
	CurrentInstanceCount int64        `json:"currentInstanceCount"`
	Status               enums.Status `json:"status"`
	Remark               string       `json:"remark"`
	CreatedAt            time.Time    `json:"createdAt"`
	UpdatedAt            time.Time    `json:"updatedAt"`
}

type StoreOptionResponse struct {
	ID        int64  `json:"id"`
	StoreCode string `json:"storeCode"`
	Name      string `json:"name"`
}
