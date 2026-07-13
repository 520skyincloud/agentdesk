package response

import "agent-desk/internal/pkg/enums"

type PermissionResponse struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Code      string       `json:"code"`
	Type      string       `json:"type"`
	Scope     string       `json:"scope"`
	GroupName string       `json:"groupName"`
	Method    string       `json:"method"`
	ApiPath   string       `json:"apiPath"`
	Status    enums.Status `json:"status"`
	SortNo    int          `json:"sortNo"`
}

type RoleResponse struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Code           string       `json:"code"`
	Scope          string       `json:"scope"`
	AuthorityLevel int          `json:"authorityLevel"`
	Status         enums.Status `json:"status"`
	IsSystem       bool         `json:"isSystem"`
	SortNo         int          `json:"sortNo"`
	Assignable     bool         `json:"assignable"`
	Manageable     bool         `json:"manageable"`
	Permissions    []string     `json:"permissions,omitempty"`
}

type UserResponse struct {
	ID          int64                         `json:"id"`
	Username    string                        `json:"username"`
	Nickname    string                        `json:"nickname"`
	Avatar      string                        `json:"avatar"`
	Mobile      string                        `json:"mobile,omitempty"`
	Email       string                        `json:"email,omitempty"`
	Status      enums.Status                  `json:"status"`
	LastLoginAt string                        `json:"lastLoginAt,omitempty"`
	LastLoginIP string                        `json:"lastLoginIp,omitempty"`
	Roles       []RoleResponse                `json:"roles,omitempty"`
	Permissions []string                      `json:"permissions,omitempty"`
	StoreStaff  *StoreStaffAssignmentResponse `json:"storeStaff,omitempty"`
	Manageable  bool                          `json:"manageable"`
}

type StoreStaffAssignmentResponse struct {
	BindingID          int64  `json:"bindingId"`
	CompanyID          int64  `json:"companyId"`
	CompanyName        string `json:"companyName"`
	StoreID            int64  `json:"storeId"`
	StoreName          string `json:"storeName"`
	WxWorkInstanceID   int64  `json:"wxWorkInstanceId"`
	WxWorkEmployeeName string `json:"wxWorkEmployeeName"`
	WxWorkEmployeeID   string `json:"wxWorkEmployeeId"`
	AgentTeamID        int64  `json:"agentTeamId"`
	AgentTeamName      string `json:"agentTeamName"`
}

// CreateUserResultResponse 创建用户成功响应；password 仅在本次响应中返回一次。
type CreateUserResultResponse struct {
	User     *UserResponse `json:"user"`
	Password string        `json:"password"`
}

type SessionResponse struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"userId"`
	Username   string `json:"username"`
	ClientType string `json:"clientType"`
	ClientIP   string `json:"clientIp"`
	UserAgent  string `json:"userAgent"`
	ExpiredAt  string `json:"expiredAt"`
	RevokedAt  string `json:"revokedAt"`
	LastSeenAt string `json:"lastSeenAt"`
}
