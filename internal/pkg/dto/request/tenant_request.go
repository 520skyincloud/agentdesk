package request

type CreateTenantRequest struct {
	IntentProfileID  int64                         `json:"intentProfileId"`
	LegalName        string                        `json:"legalName"`
	ShortName        string                        `json:"shortName"`
	RegistrationType string                        `json:"registrationType"`
	RegistrationNo   string                        `json:"registrationNo"`
	ContactName      string                        `json:"contactName"`
	ContactMobile    string                        `json:"contactMobile"`
	ContactEmail     string                        `json:"contactEmail"`
	Address          string                        `json:"address"`
	Remark           string                        `json:"remark"`
	Supervisor       CreateTenantSupervisorRequest `json:"supervisor"`
}

type CreateTenantSupervisorRequest struct {
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Mobile   string `json:"mobile"`
	Email    string `json:"email"`
}

type UpdateTenantRequest struct {
	ID                    int64  `json:"id"`
	IntentProfileID       int64  `json:"intentProfileId"`
	ConfirmIndustryChange bool   `json:"confirmIndustryChange"`
	IndustryChangeReason  string `json:"industryChangeReason"`
	LegalName             string `json:"legalName"`
	ShortName             string `json:"shortName"`
	RegistrationType      string `json:"registrationType"`
	RegistrationNo        string `json:"registrationNo"`
	ContactName           string `json:"contactName"`
	ContactMobile         string `json:"contactMobile"`
	ContactEmail          string `json:"contactEmail"`
	Address               string `json:"address"`
	Remark                string `json:"remark"`
}

type UpdateTenantStatusRequest struct {
	ID     int64 `json:"id"`
	Status int   `json:"status"`
}
