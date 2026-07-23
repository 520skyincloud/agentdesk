package migration

func init() {
	register(19, "rebuild store ai model setting account company index", func() error {
		// Superseded by StoreModelProfileAssignment and StoreModelCredential.
		return nil
	})
}
