package tools

import "testing"

func TestPhoneArgForActionDoesNotUseKeywordForPhoneLookup(t *testing.T) {
	for _, action := range []string{"reserve_order_by_phone", "recept_order_by_phone"} {
		if got := phoneArgForAction(action, "", "ORDER-001"); got != "" {
			t.Fatalf("%s must not use keyword as phone, got %q", action, got)
		}
		if got := phoneArgForAction(action, "13800000000", "ORDER-001"); got != "13800000000" {
			t.Fatalf("%s must keep phone, got %q", action, got)
		}
	}
}

func TestPhoneArgForActionKeepsKeywordFallbackForOtherQueries(t *testing.T) {
	if got := phoneArgForAction("room_status", "", "13800000000"); got != "13800000000" {
		t.Fatalf("expected keyword fallback for non-phone action, got %q", got)
	}
}
