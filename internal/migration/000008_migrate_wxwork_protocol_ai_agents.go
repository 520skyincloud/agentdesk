package migration

import "agent-desk/internal/services"

func init() {
	register(8, "migrate wxwork protocol accounts to dedicated ai agents", func() error {
		return services.WxWorkProtocolInstanceService.MigrateDedicatedAIAgents()
	})
}
