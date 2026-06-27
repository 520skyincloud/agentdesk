package response

import "time"

type HQQiyuRouteResponse struct {
	ID              int64     `json:"id"`
	DefaultGroupID  string    `json:"defaultGroupId"`
	HighRiskGroupID string    `json:"highRiskGroupId"`
	ServiceTime     string    `json:"serviceTime"`
	FallbackMode    string    `json:"fallbackMode"`
	TimeoutMinutes  int       `json:"timeoutMinutes"`
	Status          int       `json:"status"`
	Remark          string    `json:"remark"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
