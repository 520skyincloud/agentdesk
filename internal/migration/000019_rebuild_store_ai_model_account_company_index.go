package migration

import "agent-desk/internal/services"

func init() {
	register(19, "rebuild store ai model setting account company index", func() error {
		return services.StoreAIModelSettingService.RebuildStoreAIModelSettingScopeIndex()
	})
}
