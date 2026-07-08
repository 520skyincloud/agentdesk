package migration

import "agent-desk/internal/services"

func init() {
	register(20, "backfill wxwork protocol instance company id", func() error {
		return services.WxWorkProtocolInstanceService.BackfillCompanyIDFromStore()
	})
}
