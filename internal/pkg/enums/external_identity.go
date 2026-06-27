package enums

// ExternalSource 外部身份来源。
//
// 与 ExternalID 组合即可唯一标识某渠道下的访客身份。
type ExternalSource string

const (
	ExternalSourceGuest          ExternalSource = "guest"           // 访客
	ExternalSourceWxWorkKF       ExternalSource = "wxwork_kf"       // 企业微信客服
	ExternalSourceWxWorkCLI      ExternalSource = "wxwork_cli"      // 企业微信 CLI
	ExternalSourceWxWorkProtocol ExternalSource = "wxwork_protocol" // 企业微信协议 SAAS
	ExternalSourceUser           ExternalSource = "user"            // 用户信息
)

var externalSourceLabelMap = map[ExternalSource]string{
	ExternalSourceGuest:          "访客",
	ExternalSourceWxWorkKF:       "企业微信客服",
	ExternalSourceWxWorkCLI:      "企业微信CLI",
	ExternalSourceWxWorkProtocol: "企业微信协议",
	ExternalSourceUser:           "用户",
}

func GetExternalSourceLabel(v ExternalSource) string {
	if s, ok := externalSourceLabelMap[v]; ok {
		return s
	}
	return string(v)
}
