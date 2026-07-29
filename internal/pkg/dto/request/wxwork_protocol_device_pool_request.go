package request

type UpdateWxWorkProtocolDevicePoolSettingsRequest struct {
	AdminBaseURL    string `json:"adminBaseUrl"`
	CallbackBaseURL string `json:"callbackBaseUrl"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type WxWorkProtocolDevicePoolActionRequest struct {
	ID int64 `json:"id"`
}

type AdoptWxWorkProtocolDevicePoolRequest struct {
	DevicePoolID        int64 `json:"devicePoolId"`
	TenantID            int64 `json:"tenantId"`
	StoreStaffBindingID int64 `json:"storeStaffBindingId"`
}

type RepairWxWorkProtocolMessagesRequest struct {
	ID    int64 `json:"id"`
	Limit int   `json:"limit"`
}
