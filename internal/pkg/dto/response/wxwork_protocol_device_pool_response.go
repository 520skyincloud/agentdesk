package response

import "time"

type WxWorkProtocolDevicePoolSettingsResponse struct {
	AdminBaseURL  string     `json:"adminBaseUrl"`
	Username      string     `json:"username"`
	PasswordSet   bool       `json:"passwordSet"`
	TokenSet      bool       `json:"tokenSet"`
	TokenExpireAt *time.Time `json:"tokenExpireAt"`
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
