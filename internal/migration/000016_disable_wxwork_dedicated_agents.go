package migration

import "agent-desk/internal/services"

func init() {
	register(16, "disable legacy wxwork dedicated ai agents", func() error {
		return services.StoreAIModelSettingService.DisableLegacyWxWorkDedicatedAgents()
	})
}
