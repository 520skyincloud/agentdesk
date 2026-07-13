package migration

import "agent-desk/internal/services"

func init() {
	register(26, "backfill store staff agent team bindings", func() error {
		return services.AgentTeamService.BackfillStoreStaffAgentTeamBindings()
	})
}
