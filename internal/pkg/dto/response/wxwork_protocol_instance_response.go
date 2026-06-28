package response

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

type WxWorkProtocolInstanceResponse struct {
	ID                             int64        `json:"id"`
	Guid                           string       `json:"guid"`
	ChannelID                      int64        `json:"channelId"`
	ChannelName                    string       `json:"channelName"`
	EmployeeUserID                 string       `json:"employeeUserId"`
	EmployeeName                   string       `json:"employeeName"`
	StoreID                        int64        `json:"storeId"`
	StoreCode                      string       `json:"storeCode"`
	StoreName                      string       `json:"storeName"`
	StoreAddress                   string       `json:"storeAddress"`
	StoreNavigationName            string       `json:"storeNavigationName"`
	StoreLongitude                 string       `json:"storeLongitude"`
	StoreLatitude                  string       `json:"storeLatitude"`
	StoreMapProvider               string       `json:"storeMapProvider"`
	KnowledgeBaseID                int64        `json:"knowledgeBaseId"`
	KnowledgeBaseName              string       `json:"knowledgeBaseName"`
	AIAgentID                      int64        `json:"aiAgentId"`
	NotifyURL                      string       `json:"notifyUrl"`
	Proxy                          string       `json:"proxy"`
	BridgeID                       string       `json:"bridgeId"`
	StaffUserIDs                   string       `json:"staffUserIds"`
	ServiceHours                   string       `json:"serviceHours"`
	FallbackToHQ                   bool         `json:"fallbackToHQ"`
	ManualTimeoutMinutes           int          `json:"manualTimeoutMinutes"`
	AIReplyEnabled                 bool         `json:"aiReplyEnabled"`
	PersonaPrompt                  string       `json:"personaPrompt"`
	AutoAcceptFriendRequest        bool         `json:"autoAcceptFriendRequest"`
	AutoAcceptFriendRemarkTemplate string       `json:"autoAcceptFriendRemarkTemplate"`
	ContextMaxMessages             int          `json:"contextMaxMessages"`
	ContextMaxTokens               int          `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool         `json:"contextCompressionEnabled"`
	HealthStatus                   string       `json:"healthStatus"`
	LastHeartbeatAt                *time.Time   `json:"lastHeartbeatAt"`
	Status                         enums.Status `json:"status"`
	Remark                         string       `json:"remark"`
	CreatedAt                      time.Time    `json:"createdAt"`
	UpdatedAt                      time.Time    `json:"updatedAt"`
	CreateUserName                 string       `json:"createUserName"`
	UpdateUserName                 string       `json:"updateUserName"`
}

type StartWxWorkProtocolLoginResponse struct {
	Instance      WxWorkProtocolInstanceResponse `json:"instance"`
	RawResponse   string                         `json:"rawResponse"`
	QRCode        string                         `json:"qrcode"`
	QRCodeContent string                         `json:"qrcodeContent"`
	Key           string                         `json:"key"`
}

func BuildWxWorkProtocolInstanceResponse(item *models.WxWorkProtocolInstance) WxWorkProtocolInstanceResponse {
	if item == nil {
		return WxWorkProtocolInstanceResponse{}
	}
	return WxWorkProtocolInstanceResponse{
		ID:                             item.ID,
		Guid:                           item.Guid,
		ChannelID:                      item.ChannelID,
		EmployeeUserID:                 item.EmployeeUserID,
		EmployeeName:                   item.EmployeeName,
		StoreID:                        item.StoreID,
		StoreAddress:                   item.StoreAddress,
		StoreNavigationName:            item.StoreNavigationName,
		StoreLongitude:                 item.StoreLongitude,
		StoreLatitude:                  item.StoreLatitude,
		StoreMapProvider:               item.StoreMapProvider,
		KnowledgeBaseID:                item.KnowledgeBaseID,
		AIAgentID:                      item.AIAgentID,
		NotifyURL:                      item.NotifyURL,
		Proxy:                          item.Proxy,
		BridgeID:                       item.BridgeID,
		StaffUserIDs:                   item.StaffUserIDs,
		ServiceHours:                   item.ServiceHours,
		FallbackToHQ:                   item.FallbackToHQ,
		ManualTimeoutMinutes:           item.ManualTimeoutMinutes,
		AIReplyEnabled:                 item.AIReplyEnabled,
		PersonaPrompt:                  item.PersonaPrompt,
		AutoAcceptFriendRequest:        item.AutoAcceptFriendRequest,
		AutoAcceptFriendRemarkTemplate: item.AutoAcceptFriendRemarkTemplate,
		ContextMaxMessages:             item.ContextMaxMessages,
		ContextMaxTokens:               item.ContextMaxTokens,
		ContextCompressionEnabled:      item.ContextCompressionEnabled,
		HealthStatus:                   item.HealthStatus,
		LastHeartbeatAt:                item.LastHeartbeatAt,
		Status:                         item.Status,
		Remark:                         item.Remark,
		CreatedAt:                      item.CreatedAt,
		UpdatedAt:                      item.UpdatedAt,
		CreateUserName:                 item.CreateUserName,
		UpdateUserName:                 item.UpdateUserName,
	}
}
