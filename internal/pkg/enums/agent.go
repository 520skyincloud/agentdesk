package enums

type ServiceStatus int

const (
	ServiceStatusIdle ServiceStatus = 0
	ServiceStatusBusy ServiceStatus = 1
)

var ServiceStatusValues = []ServiceStatus{
	ServiceStatusIdle,
	ServiceStatusBusy,
}

var serviceStatusLabelMap = map[ServiceStatus]string{
	ServiceStatusIdle: "空闲",
	ServiceStatusBusy: "忙碌",
}

func IsValidServiceStatus(status ServiceStatus) bool {
	for _, item := range ServiceStatusValues {
		if item == status {
			return true
		}
	}
	return false
}

func GetServiceStatusLabel(status ServiceStatus) string {
	return serviceStatusLabelMap[status]
}

type AgentTeamDispatchMode string

const (
	AgentTeamDispatchModeManual      AgentTeamDispatchMode = "manual"
	AgentTeamDispatchModeRule        AgentTeamDispatchMode = "rule"
	AgentTeamDispatchModeIntelligent AgentTeamDispatchMode = "intelligent"
)

var AgentTeamDispatchModeValues = []AgentTeamDispatchMode{
	AgentTeamDispatchModeManual,
	AgentTeamDispatchModeRule,
	AgentTeamDispatchModeIntelligent,
}

var agentTeamDispatchModeLabelMap = map[AgentTeamDispatchMode]string{
	AgentTeamDispatchModeManual:      "人工派单",
	AgentTeamDispatchModeRule:        "规则均衡",
	AgentTeamDispatchModeIntelligent: "智能均衡",
}

func IsValidAgentTeamDispatchMode(mode AgentTeamDispatchMode) bool {
	for _, item := range AgentTeamDispatchModeValues {
		if item == mode {
			return true
		}
	}
	return false
}

func GetAgentTeamDispatchModeLabel(mode AgentTeamDispatchMode) string {
	return agentTeamDispatchModeLabelMap[mode]
}
