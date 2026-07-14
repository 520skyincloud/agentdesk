package dto

import "time"

type TenantOperationalStats struct {
	AgentCount     int64
	StoreCount     int64
	AgentTeamCount int64
	LastActiveAt   *time.Time
}
