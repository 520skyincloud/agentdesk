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
	IndustryName                   string       `json:"industryName"`
	StoreID                        int64        `json:"storeId"`
	StoreStaffBindingID            int64        `json:"storeStaffBindingId"`
	StoreStaffUserID               int64        `json:"storeStaffUserId"`
	StoreStaffUserName             string       `json:"storeStaffUserName"`
	ReplacesInstanceID             int64        `json:"replacesInstanceId"`
	ReplacedByInstanceID           int64        `json:"replacedByInstanceId"`
	ReplacedAt                     *time.Time   `json:"replacedAt"`
	ManagedMode                    string       `json:"managedMode"`
	StoreCode                      string       `json:"storeCode"`
	StoreName                      string       `json:"storeName"`
	StoreAddress                   string       `json:"storeAddress"`
	StoreNavigationName            string       `json:"storeNavigationName"`
	StoreLongitude                 string       `json:"storeLongitude"`
	StoreLatitude                  string       `json:"storeLatitude"`
	StoreMapProvider               string       `json:"storeMapProvider"`
	StoreContactPhone              string       `json:"storeContactPhone"`
	DefaultMiniProgramPayload      string       `json:"defaultMiniProgramPayload"`
	WelcomeEnabled                 bool         `json:"welcomeEnabled"`
	WelcomeMessage                 string       `json:"welcomeMessage"`
	WelcomeImageAssetID            string       `json:"welcomeImageAssetId"`
	WelcomeImageURL                string       `json:"welcomeImageUrl"`
	WelcomeSendMiniProgram         bool         `json:"welcomeSendMiniProgram"`
	WelcomeAskLocation             bool         `json:"welcomeAskLocation"`
	KnowledgeBaseID                int64        `json:"knowledgeBaseId"`
	KnowledgeBaseName              string       `json:"knowledgeBaseName"`
	CustomerCount                  int64        `json:"customerCount"`
	ManualAttentionCount           int64        `json:"manualAttentionCount"`
	UrgentManualAttentionCount     int64        `json:"urgentManualAttentionCount"`
	NotifyURL                      string       `json:"notifyUrl"`
	Proxy                          string       `json:"proxy"`
	BridgeID                       string       `json:"bridgeId"`
	StaffUserIDs                   string       `json:"staffUserIds"`
	ServiceHours                   string       `json:"serviceHours"`
	FrontDeskMode                  string       `json:"frontDeskMode"`
	FrontDeskHours                 string       `json:"frontDeskHours"`
	StoreRoomConversationID        string       `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled         bool         `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList                string       `json:"storeRoomAtList"`
	FallbackToHQ                   bool         `json:"fallbackToHQ"`
	ManualTimeoutMinutes           int          `json:"manualTimeoutMinutes"`
	AIReplyEnabled                 bool         `json:"aiReplyEnabled"`
	PersonaPrompt                  string       `json:"personaPrompt"`
	AutoAcceptFriendRequest        bool         `json:"autoAcceptFriendRequest"`
	AutoAcceptFriendRemarkTemplate string       `json:"autoAcceptFriendRemarkTemplate"`
	ContactAutomationLastAt        *time.Time   `json:"contactAutomationLastAt"`
	ContactAutomationLastError     string       `json:"contactAutomationLastError"`
	ContextMaxMessages             int          `json:"contextMaxMessages"`
	ContextMaxTokens               int          `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool         `json:"contextCompressionEnabled"`
	RemoteSetupToken               string       `json:"remoteSetupToken"`
	RemoteSetupURL                 string       `json:"remoteSetupUrl"`
	RemoteSetupExpiresAt           *time.Time   `json:"remoteSetupExpiresAt"`
	RemoteSetupSubmittedAt         *time.Time   `json:"remoteSetupSubmittedAt"`
	KnowledgeProvisionStatus       string       `json:"knowledgeProvisionStatus"`
	KnowledgeProvisionError        string       `json:"knowledgeProvisionError"`
	HealthStatus                   string       `json:"healthStatus"`
	ProtocolExpiresAt              *time.Time   `json:"protocolExpiresAt"`
	ProtocolExpired                bool         `json:"protocolExpired"`
	LoginAvailable                 bool         `json:"loginAvailable"`
	LoginUnavailableReason         string       `json:"loginUnavailableReason"`
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
	QRCode        string                         `json:"qrcode"`
	QRCodeContent string                         `json:"qrcodeContent"`
}

type WxWorkProtocolLoginQRCodeResponse struct {
	QRCode        string `json:"qrcode"`
	QRCodeContent string `json:"qrcodeContent"`
}

type WxWorkProtocolLoginStatusResponse struct {
	Status       string `json:"status"`
	StatusCode   int    `json:"statusCode"`
	RequiresCode bool   `json:"requiresCode"`
	Message      string `json:"message"`
}

type WxWorkProtocolRoomOptionResponse struct {
	RoomID         string `json:"roomId"`
	ConversationID string `json:"conversationId"`
	Name           string `json:"name"`
	Owner          string `json:"owner"`
	MemberCount    int    `json:"memberCount"`
	Raw            any    `json:"raw,omitempty"`
}

type WxWorkProtocolRoomMemberOptionResponse struct {
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	RealName    string `json:"realName"`
	RoomRemark  string `json:"roomRemark"`
	AccountID   string `json:"accountId"`
	Avatar      string `json:"avatar"`
	Raw         any    `json:"raw,omitempty"`
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
		StoreStaffBindingID:            item.StoreStaffBindingID,
		ReplacesInstanceID:             item.ReplacesInstanceID,
		ReplacedByInstanceID:           item.ReplacedByInstanceID,
		ReplacedAt:                     item.ReplacedAt,
		ManagedMode:                    "semi",
		DefaultMiniProgramPayload:      utils.RepairMojibakeText(item.DefaultMiniProgramPayload),
		WelcomeEnabled:                 item.WelcomeEnabled,
		WelcomeMessage:                 utils.RepairMojibakeText(item.WelcomeMessage),
		WelcomeImageAssetID:            item.WelcomeImageAssetID,
		WelcomeSendMiniProgram:         item.WelcomeSendMiniProgram,
		WelcomeAskLocation:             item.WelcomeAskLocation,
		NotifyURL:                      item.NotifyURL,
		Proxy:                          "",
		BridgeID:                       item.BridgeID,
		StaffUserIDs:                   item.StaffUserIDs,
		FrontDeskMode:                  item.FrontDeskMode,
		FrontDeskHours:                 item.FrontDeskHours,
		AIReplyEnabled:                 item.AIReplyEnabled,
		PersonaPrompt:                  utils.RepairMojibakeText(item.PersonaPrompt),
		AutoAcceptFriendRequest:        item.AutoAcceptFriendRequest,
		AutoAcceptFriendRemarkTemplate: utils.RepairMojibakeText(item.AutoAcceptFriendRemarkTemplate),
		ContactAutomationLastAt:        item.ContactAutomationLastAt,
		ContactAutomationLastError:     utils.RepairMojibakeText(item.ContactAutomationLastError),
		ContextMaxMessages:             item.ContextMaxMessages,
		ContextMaxTokens:               item.ContextMaxTokens,
		ContextCompressionEnabled:      item.ContextCompressionEnabled,
		RemoteSetupToken:               item.RemoteSetupToken,
		RemoteSetupExpiresAt:           item.RemoteSetupExpiresAt,
		RemoteSetupSubmittedAt:         item.RemoteSetupSubmittedAt,
		HealthStatus:                   item.HealthStatus,
		LoginAvailable:                 true,
		LastHeartbeatAt:                item.LastHeartbeatAt,
		Status:                         item.Status,
		Remark:                         utils.RepairMojibakeText(item.Remark),
		CreatedAt:                      item.CreatedAt,
		UpdatedAt:                      item.UpdatedAt,
		CreateUserName:                 item.CreateUserName,
		UpdateUserName:                 item.UpdateUserName,
	}
}
