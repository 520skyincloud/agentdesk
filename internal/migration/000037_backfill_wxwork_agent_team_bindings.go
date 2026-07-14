package migration

import "agent-desk/internal/services"

func init() {
	register(37, "backfill wxwork protocol instance agent team bindings", func() error {
		return services.AgentTeamService.BackfillWxWorkInstanceBindings()
	})
}
