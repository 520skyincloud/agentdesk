package request

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SendEmailCodeRequest struct {
	Email string `json:"email"`
}

type EmailCodeLoginRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type WxWorkExchangeRequest struct {
	Ticket string `json:"ticket"`
}

type OIDCExchangeRequest struct {
	Ticket string `json:"ticket"`
}
