package contracts

import "testing"

func TestEmbeddedSchemasAreValidDraft202012(t *testing.T) {
	if err := ValidateEmbeddedSchemas(); err != nil {
		t.Fatal(err)
	}
	if got, want := len(SchemaNames()), 36; got != want {
		t.Fatalf("schema count=%d want=%d", got, want)
	}
}
