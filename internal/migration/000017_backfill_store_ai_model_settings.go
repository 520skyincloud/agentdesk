package migration

import "agent-desk/internal/services"

func init() {
	register(17, "backfill store ai model settings", func() error {
		return services.StoreAIModelSettingService.BackfillStoreAIModelSettings()
	})
}
