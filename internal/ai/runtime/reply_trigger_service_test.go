package runtime

import "testing"

func TestIsPMSRenewCancellation(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"取消", true},
		{"不用了？", true},
		{"不续了!", true},
		{"我想续住", false},
	}
	for _, tt := range tests {
		if got := isPMSRenewCancellation(tt.text); got != tt.want {
			t.Fatalf("isPMSRenewCancellation(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
