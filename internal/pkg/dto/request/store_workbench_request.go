package request

type UpdateStoreWorkbenchRequest struct {
	ManagedMode             string `json:"managedMode"`
	ServiceHours            string `json:"serviceHours"`
	StoreRoomConversationID string `json:"storeRoomConversationId"`
	StoreRoomNotifyEnabled  bool   `json:"storeRoomNotifyEnabled"`
	StoreRoomAtList         string `json:"storeRoomAtList"`
	ManualTimeoutMinutes    int    `json:"manualTimeoutMinutes"`
	StoreAddress            string `json:"storeAddress"`
	StoreNavigationName     string `json:"storeNavigationName"`
	StoreLongitude          string `json:"storeLongitude"`
	StoreLatitude           string `json:"storeLatitude"`
	StoreMapProvider        string `json:"storeMapProvider"`
}

type StoreWorkbenchRoomListRequest struct {
	StartIndex int `json:"startIndex"`
	Limit      int `json:"limit"`
}

type StoreWorkbenchRoomMemberListRequest struct {
	RoomID string `json:"roomId"`
}
