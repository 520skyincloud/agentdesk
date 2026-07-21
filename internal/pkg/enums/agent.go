package enums

type AgentTeamDispatchMode string

const (
	AgentTeamDispatchModeManual AgentTeamDispatchMode = "manual"
	AgentTeamDispatchModeRule   AgentTeamDispatchMode = "rule"
)

var AgentTeamDispatchModeValues = []AgentTeamDispatchMode{
	AgentTeamDispatchModeManual,
	AgentTeamDispatchModeRule,
}

var agentTeamDispatchModeLabelMap = map[AgentTeamDispatchMode]string{
	AgentTeamDispatchModeManual: "人工派单",
	AgentTeamDispatchModeRule:   "规则均衡",
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
