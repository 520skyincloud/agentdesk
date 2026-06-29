package request

type UpdateWxWorkProtocolDevicePoolSettingsRequest struct {
	AdminBaseURL string `json:"adminBaseUrl"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type WxWorkProtocolDevicePoolActionRequest struct {
	ID int64 `json:"id"`
}
