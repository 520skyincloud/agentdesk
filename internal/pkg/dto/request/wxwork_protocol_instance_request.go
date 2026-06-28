package request

type CreateWxWorkProtocolInstanceRequest struct {
	Guid                           string `json:"guid"`
	ChannelID                      int64  `json:"channelId"`
	EmployeeUserID                 string `json:"employeeUserId"`
	EmployeeName                   string `json:"employeeName"`
	StoreID                        int64  `json:"storeId"`
	StoreAddress                   string `json:"storeAddress"`
	StoreNavigationName            string `json:"storeNavigationName"`
	StoreLongitude                 string `json:"storeLongitude"`
	StoreLatitude                  string `json:"storeLatitude"`
	StoreMapProvider               string `json:"storeMapProvider"`
	KnowledgeBaseID                int64  `json:"knowledgeBaseId"`
	AIAgentID                      int64  `json:"aiAgentId"`
	NotifyURL                      string `json:"notifyUrl"`
	Proxy                          string `json:"proxy"`
	BridgeID                       string `json:"bridgeId"`
	StaffUserIDs                   string `json:"staffUserIds"`
	ServiceHours                   string `json:"serviceHours"`
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
	ChannelID int64 `json:"channelId"`
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
	FallbackToHQ                   bool   `json:"fallbackToHQ"`
	PersonaPrompt                  string `json:"personaPrompt"`
	StoreID                        int64  `json:"storeId"`
	StoreAddress                   string `json:"storeAddress"`
	StoreNavigationName            string `json:"storeNavigationName"`
	StoreLongitude                 string `json:"storeLongitude"`
	StoreLatitude                  string `json:"storeLatitude"`
	StoreMapProvider               string `json:"storeMapProvider"`
	KnowledgeBaseID                int64  `json:"knowledgeBaseId"`
	AIAgentID                      int64  `json:"aiAgentId"`
	ContextMaxMessages             int    `json:"contextMaxMessages"`
	ContextMaxTokens               int    `json:"contextMaxTokens"`
	ContextCompressionEnabled      bool   `json:"contextCompressionEnabled"`
}

type WxWorkProtocolInstanceActionRequest struct {
	ID int64 `json:"id"`
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
