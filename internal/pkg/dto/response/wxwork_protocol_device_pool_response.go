package response

import "time"

type WxWorkProtocolDevicePoolSettingsResponse struct {
	AdminBaseURL    string     `json:"adminBaseUrl"`
	CallbackBaseURL string     `json:"callbackBaseUrl"`
	Username        string     `json:"username"`
	PasswordSet     bool       `json:"passwordSet"`
	TokenSet        bool       `json:"tokenSet"`
	TokenExpireAt   *time.Time `json:"tokenExpireAt"`
}

type WxWorkProtocolDevicePoolInstanceResponse struct {
	ID                            int64      `json:"id"`
	ProviderInstanceID            int64      `json:"providerInstanceId"`
	Guid                          string     `json:"guid"`
	Uin                           string     `json:"uin"`
	ProviderUserID                int64      `json:"providerUserId"`
	ClientType                    int        `json:"clientType"`
	SeatName                      string     `json:"seatName"`
	BridgeID                      string     `json:"bridgeId"`
	State                         string     `json:"state"`
	ExpiredAt                     *time.Time `json:"expiredAt"`
	SyncStatus                    string     `json:"syncStatus"`
	LastSyncedAt                  *time.Time `json:"lastSyncedAt"`
	BoundWxWorkProtocolInstanceID int64      `json:"boundWxWorkProtocolInstanceId"`
	BoundEmployeeName             string     `json:"boundEmployeeName"`
	BoundStoreName                string     `json:"boundStoreName"`
	Available                     bool       `json:"available"`
	Adoptable                     bool       `json:"adoptable"`
	MessageSyncSeq                string     `json:"messageSyncSeq"`
	MessageGapFromSeq             string     `json:"messageGapFromSeq"`
	MessageGapToSeq               string     `json:"messageGapToSeq"`
	MessageGapDetectedAt          *time.Time `json:"messageGapDetectedAt"`
	MessageRepairLastAt           *time.Time `json:"messageRepairLastAt"`
	MessageRepairLastError        string     `json:"messageRepairLastError"`
	Status                        int        `json:"status"`
	Remark                        string     `json:"remark"`
	CreatedAt                     time.Time  `json:"createdAt"`
	UpdatedAt                     time.Time  `json:"updatedAt"`
}

type WxWorkProtocolDevicePoolSyncResponse struct {
	SyncedCount int `json:"syncedCount"`
	IdleCount   int `json:"idleCount"`
	BoundCount  int `json:"boundCount"`
}

type WxWorkProtocolAdoptionOptionResponse struct {
	TenantID            int64  `json:"tenantId"`
	TenantName          string `json:"tenantName"`
	StoreID             int64  `json:"storeId"`
	StoreName           string `json:"storeName"`
	StoreStaffBindingID int64  `json:"storeStaffBindingId"`
	StoreStaffUserID    int64  `json:"storeStaffUserId"`
	StoreStaffUserName  string `json:"storeStaffUserName"`
}

type WxWorkProtocolAdoptionResponse struct {
	DevicePoolID        int64  `json:"devicePoolId"`
	InstanceID          int64  `json:"instanceId"`
	TenantID            int64  `json:"tenantId"`
	StoreID             int64  `json:"storeId"`
	StoreStaffBindingID int64  `json:"storeStaffBindingId"`
	EmployeeName        string `json:"employeeName"`
	StoreName           string `json:"storeName"`
	NotifyConfigured    bool   `json:"notifyConfigured"`
}

type WxWorkProtocolRepairResponse struct {
	InstanceID int64  `json:"instanceId"`
	SyncKey    string `json:"syncKey"`
	Limit      int    `json:"limit"`
}
