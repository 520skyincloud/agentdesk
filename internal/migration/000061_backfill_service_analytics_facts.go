package migration

import "agent-desk/internal/services"

func init() {
	register(61, "backfill tenant service analytics facts", func() error {
		result, err := services.ServiceAnalyticsCaptureService.BackfillMissingFacts()
		if err == nil {
			services.LogServiceAnalyticsBackfill(result)
		}
		return err
	})
}
