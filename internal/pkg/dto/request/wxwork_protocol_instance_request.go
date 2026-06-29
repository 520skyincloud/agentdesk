package request

type CreateWxWorkProtocolInstanceRequest struct {
	Guid                           string `json:"guid"`
	ChannelID                      int64  `json:"channelId"`
	EmployeeUserID                 string `json:"employeeUserId"`
	EmployeeName                   string `json:"employeeName"`
	EmployeeAvatar                 string `json:"employeeAvatar"`
	StoreID                        int64  `json:"storeId"`
	StoreAddress                   string `json:"storeAddress"`
	StoreNavigationName            string `json:"storeNavigationName"`
	StoreLongitude                 string `json:"storeLongitude"`
	StoreLatitude                  string `json:"storeLatitude"`
	StoreMapProvider               string `json:"storeMapProvider"`
	DefaultMiniProgramPayload      string `json:"defaultMiniProgramPayload"`
	WelcomeMessage                 string `json:"welcomeMessage"`
	WelcomeSendMiniProgram         bool   `json:"welcomeSendMiniProgram"`
	WelcomeAskLocation             bool   `json:"welcomeAskLocation"`
	KnowledgeBaseID                int64  `json:"knowledgeBaseId"`
	AIAgentID                      int64  `json:"aiAgentId"`
	NotifyURL                      string `json:"notifyUrl"`
	Proxy                          string `json:"proxy"`
	BridgeID                       string `json:"bridgeId"`
	StaffUserIDs                   string `json:"staffUserIds"`
	ManagedMode                    string `json:"managedMode"`
	ServiceHours                   string `json:"serviceHours"`
	StoreRoomConversationID        string `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled         bool   `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList                string `json:"storeRoomAtList"`
	FallbackToHQ                   bool   `json:"fallbackToHQ"`
	ManualTimeoutMinutes           int    `json:"manualTimeoutMinutes"`
	AIReplyEnabled                 bool   `json:"aiReplyEnabled"`
	PersonaPrompt                  string `json:"personaPrompt"`
	AutoAcceptFriendRequest        bool   `json:"autoAcceptFriendRequest"`
	AutoAcceptFriendRemarkTemplate string `json:"autoAcceptFriendRemarkTemplate"`
	ContextMaxMessages             int    `json:"contextMaxMessages"`
	ContextMaxTokens               int    `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool   `json:"contextCompressionEnabled"`
	Status                         int    `json:"status"`
	Remark                         string `json:"remark"`
}

type UpdateWxWorkProtocolInstanceRequest struct {
	ID int64 `json:"id"`
	CreateWxWorkProtocolInstanceRequest
}

type StartWxWorkProtocolLoginRequest struct {
	ChannelID int64  `json:"channelId"`
	Guid      string `json:"guid"`
}

type CreateWxWorkProtocolRemoteSetupRequest struct {
	ChannelID int64  `json:"channelId"`
	Guid      string `json:"guid"`
	Remark    string `json:"remark"`
}

type WxWorkProtocolRemoteSetupTokenRequest struct {
	Token string `json:"token"`
}

type UpdateWxWorkProtocolRemoteSetupRequest struct {
	Token                   string `json:"token"`
	Guid                    string `json:"guid"`
	EmployeeName            string `json:"employeeName"`
	StoreID                 int64  `json:"storeId"`
	StoreName               string `json:"storeName"`
	StoreAddress            string `json:"storeAddress"`
	StoreNavigationName     string `json:"storeNavigationName"`
	StoreLongitude          string `json:"storeLongitude"`
	StoreLatitude           string `json:"storeLatitude"`
	StoreMapProvider        string `json:"storeMapProvider"`
	KnowledgeBaseID         int64  `json:"knowledgeBaseId"`
	ManagedMode             string `json:"managedMode"`
	ServiceHours            string `json:"serviceHours"`
	StoreRoomConversationID string `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled  bool   `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList         string `json:"storeRoomAtList"`
	FallbackToHQ            bool   `json:"fallbackToHQ"`
	ManualTimeoutMinutes    int    `json:"manualTimeoutMinutes"`
	AutoAcceptFriendRequest bool   `json:"autoAcceptFriendRequest"`
}

type DeleteWxWorkProtocolInstanceRequest struct {
	ID int64 `json:"id"`
}

type SetWxWorkProtocolNotifyURLRequest struct {
	ID        int64  `json:"id"`
	NotifyURL string `json:"notifyUrl"`
}

type SetWxWorkProtocolAIReplyEnabledRequest struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}

type UpdateWxWorkProtocolAISettingsRequest struct {
	ID                             int64  `json:"id"`
	AIReplyEnabled                 bool   `json:"aiReplyEnabled"`
	AutoAcceptFriendRequest        bool   `json:"autoAcceptFriendRequest"`
	AutoAcceptFriendRemarkTemplate string `json:"autoAcceptFriendRemarkTemplate"`
	ServiceHours                   string `json:"serviceHours"`
	ManualTimeoutMinutes           int    `json:"manualTimeoutMinutes"`
	StaffUserIDs                   string `json:"staffUserIds"`
	ManagedMode                    string `json:"managedMode"`
	FallbackToHQ                   bool   `json:"fallbackToHQ"`
	StoreRoomConversationID        string `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled         bool   `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList                string `json:"storeRoomAtList"`
	PersonaPrompt                  string `json:"personaPrompt"`
	StoreID                        int64  `json:"storeId"`
	StoreAddress                   string `json:"storeAddress"`
	StoreNavigationName            string `json:"storeNavigationName"`
	StoreLongitude                 string `json:"storeLongitude"`
	StoreLatitude                  string `json:"storeLatitude"`
	StoreMapProvider               string `json:"storeMapProvider"`
	DefaultMiniProgramPayload      string `json:"defaultMiniProgramPayload"`
	WelcomeMessage                 string `json:"welcomeMessage"`
	WelcomeSendMiniProgram         bool   `json:"welcomeSendMiniProgram"`
	WelcomeAskLocation             bool   `json:"welcomeAskLocation"`
	KnowledgeBaseID                int64  `json:"knowledgeBaseId"`
	AIAgentID                      int64  `json:"aiAgentId"`
	ContextMaxMessages             int    `json:"contextMaxMessages"`
	ContextMaxTokens               int    `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool   `json:"contextCompressionEnabled"`
}

type WxWorkProtocolInstanceActionRequest struct {
	ID int64 `json:"id"`
}

type WxWorkProtocolRoomListRequest struct {
	ID         int64 `json:"id"`
	StartIndex int   `json:"startIndex"`
	Limit      int   `json:"limit"`
}

type WxWorkProtocolRoomMemberDetailRequest struct {
	ID       int64    `json:"id"`
	RoomID   string   `json:"roomId"`
	UserList []string `json:"userList"`
}

type WxWorkProtocolRoomDetailRequest struct {
	ID       int64    `json:"id"`
	RoomList []string `json:"roomList"`
}

type WxWorkProtocolSyncRoomInfoRequest struct {
	ID      int64  `json:"id"`
	RoomID  string `json:"roomId"`
	Version int    `json:"version"`
}

type UpdateWxWorkProtocolAIAgentRequest struct {
	ID int64 `json:"id"`
	CreateAIAgentRequest
}

type WxWorkProtocolSetProxyRequest struct {
	ID    int64  `json:"id"`
	Proxy string `json:"proxy"`
}

type CheckWxWorkProtocolLoginQRCodeRequest struct {
	ID     int64  `json:"id"`
	QrCode string `json:"qrcode"`
}

type VerifyWxWorkProtocolLoginRequest struct {
	ID     int64  `json:"id"`
	Ticket string `json:"ticket"`
	Code   string `json:"code"`
}

type AcceptWxWorkProtocolFriendRequest struct {
	ID       int64  `json:"id"`
	ApplyID  string `json:"applyId"`
	Scene    string `json:"scene"`
	Remark   string `json:"remark"`
	Username string `json:"username"`
}

type InviteWxWorkProtocolRoomMemberRequest struct {
	ID       int64    `json:"id"`
	RoomID   string   `json:"roomId"`
	UserList []string `json:"userList"`
}
