package response

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

type WxWorkProtocolInstanceResponse struct {
	ID                             int64        `json:"id"`
	Guid                           string       `json:"guid"`
	ChannelID                      int64        `json:"channelId"`
	ChannelName                    string       `json:"channelName"`
	EmployeeUserID                 string       `json:"employeeUserId"`
	EmployeeName                   string       `json:"employeeName"`
	EmployeeAvatar                 string       `json:"employeeAvatar"`
	StoreID                        int64        `json:"storeId"`
	StoreCode                      string       `json:"storeCode"`
	StoreName                      string       `json:"storeName"`
	StoreAddress                   string       `json:"storeAddress"`
	StoreNavigationName            string       `json:"storeNavigationName"`
	StoreLongitude                 string       `json:"storeLongitude"`
	StoreLatitude                  string       `json:"storeLatitude"`
	StoreMapProvider               string       `json:"storeMapProvider"`
	DefaultMiniProgramPayload      string       `json:"defaultMiniProgramPayload"`
	WelcomeMessage                 string       `json:"welcomeMessage"`
	WelcomeSendMiniProgram         bool         `json:"welcomeSendMiniProgram"`
	WelcomeAskLocation             bool         `json:"welcomeAskLocation"`
	KnowledgeBaseID                int64        `json:"knowledgeBaseId"`
	KnowledgeBaseName              string       `json:"knowledgeBaseName"`
	AIAgentID                      int64        `json:"aiAgentId"`
	AIAgentName                    string       `json:"aiAgentName"`
	AIConfigName                   string       `json:"aiConfigName"`
	AIAgentConfigured              bool         `json:"aiAgentConfigured"`
	NotifyURL                      string       `json:"notifyUrl"`
	Proxy                          string       `json:"proxy"`
	BridgeID                       string       `json:"bridgeId"`
	StaffUserIDs                   string       `json:"staffUserIds"`
	ServiceHours                   string       `json:"serviceHours"`
	StoreRoomConversationID        string       `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled         bool         `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList                string       `json:"storeRoomAtList"`
	FallbackToHQ                   bool         `json:"fallbackToHQ"`
	ManualTimeoutMinutes           int          `json:"manualTimeoutMinutes"`
	AIReplyEnabled                 bool         `json:"aiReplyEnabled"`
	PersonaPrompt                  string       `json:"personaPrompt"`
	AutoAcceptFriendRequest        bool         `json:"autoAcceptFriendRequest"`
	AutoAcceptFriendRemarkTemplate string       `json:"autoAcceptFriendRemarkTemplate"`
	ContextMaxMessages             int          `json:"contextMaxMessages"`
	ContextMaxTokens               int          `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool         `json:"contextCompressionEnabled"`
	RemoteSetupToken               string       `json:"remoteSetupToken"`
	RemoteSetupURL                 string       `json:"remoteSetupUrl"`
	RemoteSetupExpiresAt           *time.Time   `json:"remoteSetupExpiresAt"`
	RemoteSetupSubmittedAt         *time.Time   `json:"remoteSetupSubmittedAt"`
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
		EmployeeName:                   utils.RepairMojibakeText(item.EmployeeName),
		EmployeeAvatar:                 item.EmployeeAvatar,
		StoreID:                        item.StoreID,
		StoreAddress:                   utils.RepairMojibakeText(item.StoreAddress),
		StoreNavigationName:            utils.RepairMojibakeText(item.StoreNavigationName),
		StoreLongitude:                 item.StoreLongitude,
		StoreLatitude:                  item.StoreLatitude,
		StoreMapProvider:               item.StoreMapProvider,
		DefaultMiniProgramPayload:      utils.RepairMojibakeText(item.DefaultMiniProgramPayload),
		WelcomeMessage:                 utils.RepairMojibakeText(item.WelcomeMessage),
		WelcomeSendMiniProgram:         item.WelcomeSendMiniProgram,
		WelcomeAskLocation:             item.WelcomeAskLocation,
		KnowledgeBaseID:                item.KnowledgeBaseID,
		AIAgentID:                      item.AIAgentID,
		AIAgentConfigured:              item.AIAgentID > 0,
		NotifyURL:                      item.NotifyURL,
		Proxy:                          item.Proxy,
		BridgeID:                       item.BridgeID,
		StaffUserIDs:                   item.StaffUserIDs,
		ServiceHours:                   item.ServiceHours,
		StoreRoomConversationID:        item.StoreRoomConversationID,
		StoreRoomNotifyEnabled:         item.StoreRoomNotifyEnabled,
		StoreRoomAtList:                item.StoreRoomAtList,
		FallbackToHQ:                   item.FallbackToHQ,
		ManualTimeoutMinutes:           item.ManualTimeoutMinutes,
		AIReplyEnabled:                 item.AIReplyEnabled,
		PersonaPrompt:                  utils.RepairMojibakeText(item.PersonaPrompt),
		AutoAcceptFriendRequest:        item.AutoAcceptFriendRequest,
		AutoAcceptFriendRemarkTemplate: utils.RepairMojibakeText(item.AutoAcceptFriendRemarkTemplate),
		ContextMaxMessages:             item.ContextMaxMessages,
		ContextMaxTokens:               item.ContextMaxTokens,
		ContextCompressionEnabled:      item.ContextCompressionEnabled,
		RemoteSetupToken:               item.RemoteSetupToken,
		RemoteSetupExpiresAt:           item.RemoteSetupExpiresAt,
		RemoteSetupSubmittedAt:         item.RemoteSetupSubmittedAt,
		HealthStatus:                   item.HealthStatus,
		LastHeartbeatAt:                item.LastHeartbeatAt,
		Status:                         item.Status,
		Remark:                         utils.RepairMojibakeText(item.Remark),
		CreatedAt:                      item.CreatedAt,
		UpdatedAt:                      item.UpdatedAt,
		CreateUserName:                 item.CreateUserName,
		UpdateUserName:                 item.UpdateUserName,
	}
}
