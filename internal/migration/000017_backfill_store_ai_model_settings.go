package migration

func init() {
	register(17, "backfill store ai model settings", func() error {
		// The legacy table is no longer part of the runtime or fresh schema.
		return nil
	})
}
