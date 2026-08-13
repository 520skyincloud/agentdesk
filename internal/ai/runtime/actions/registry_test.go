package actions

import (
	"testing"
)

func TestRegistryRegistersBuiltinActions(t *testing.T) {
	expected := map[string]Kind{
		"human_handoff":        KindBuiltin,
		"create_ticket":        KindBuiltin,
		"provide_location":     KindBuiltin,
		"provide_mini_program": KindBuiltin,
		"provide_phone":        KindBuiltin,
		"query_weather":        KindTool,
		"query_room_status":    KindExternal,
		"query_member_level":   KindExternal,
	}
	for code, kind := range expected {
		def, ok := Get(code)
		if !ok {
			t.Fatalf("expected action %q to be registered", code)
		}
		if def.Kind != kind {
			t.Fatalf("action %q kind = %q, want %q", code, def.Kind, kind)
		}
	}
}

func TestExternalActionNotProvisioned(t *testing.T) {
	if Provisioned("query_room_status") {
		t.Fatalf("external action query_room_status must not be provisioned")
	}
	if _, _, err := Resolve("query_room_status"); err == nil {
		t.Fatalf("expected ErrActionNotProvisioned for unprovisioned external action")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected duplicate registration to panic")
		}
	}()
	Register(Definition{Code: "human_handoff"})
}
