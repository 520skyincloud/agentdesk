package migration

import "agent-desk/internal/services"

func init() {
	register(18, "rebuild store ai model setting scope index with company", func() error {
		return services.StoreAIModelSettingService.RebuildStoreAIModelSettingScopeIndex()
	})
}
