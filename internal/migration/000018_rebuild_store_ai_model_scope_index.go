package migration

func init() {
	register(18, "rebuild store ai model setting scope index with company", func() error {
		// Superseded by StoreModelProfileAssignment and StoreModelCredential.
		return nil
	})
}
