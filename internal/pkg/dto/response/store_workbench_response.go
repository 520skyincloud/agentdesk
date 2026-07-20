package response

import "agent-desk/internal/pkg/enums"

type StoreWorkbenchResponse struct {
	Bound                   bool         `json:"bound"`
	TenantID                int64        `json:"tenantId"`
	TenantName              string       `json:"tenantName"`
	UserID                  int64        `json:"userId"`
	Username                string       `json:"username"`
	Nickname                string       `json:"nickname"`
	Avatar                  string       `json:"avatar"`
	BindingID               int64        `json:"bindingId"`
	BindingStatus           enums.Status `json:"bindingStatus"`
	StoreID                 int64        `json:"storeId"`
	StoreCode               string       `json:"storeCode"`
	StoreName               string       `json:"storeName"`
	BrandName               string       `json:"brandName"`
	AgentTeamID             int64        `json:"agentTeamId"`
	AgentTeamName           string       `json:"agentTeamName"`
	WxWorkInstanceID        int64        `json:"wxWorkInstanceId"`
	WxWorkEmployeeID        string       `json:"wxWorkEmployeeId"`
	WxWorkEmployeeName      string       `json:"wxWorkEmployeeName"`
	WxWorkEmployeeAvatar    string       `json:"wxWorkEmployeeAvatar"`
	WxWorkHealthStatus      string       `json:"wxWorkHealthStatus"`
	WxWorkLastHeartbeatAt   string       `json:"wxWorkLastHeartbeatAt,omitempty"`
	AIReplyEnabled          bool         `json:"aiReplyEnabled"`
	KnowledgeBaseID         int64        `json:"knowledgeBaseId"`
	KnowledgeBaseName       string       `json:"knowledgeBaseName"`
	ManagedMode             string       `json:"managedMode"`
	ServiceHours            string       `json:"serviceHours"`
	StoreRoomConversationID string       `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled  bool         `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList         string       `json:"storeRoomAtList"`
	FallbackToHQ            bool         `json:"fallbackToHQ"`
	ManualTimeoutMinutes    int          `json:"manualTimeoutMinutes"`
	StoreAddress            string       `json:"storeAddress"`
	StoreNavigationName     string       `json:"storeNavigationName"`
	StoreLongitude          string       `json:"storeLongitude"`
	StoreLatitude           string       `json:"storeLatitude"`
	StoreMapProvider        string       `json:"storeMapProvider"`
	UpdatedAt               string       `json:"updatedAt,omitempty"`
}
