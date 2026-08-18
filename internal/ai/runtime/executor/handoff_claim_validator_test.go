package executor

import "testing"

func TestUnauthorizedHandoffClaimBoundaryKeepsConditionalAdvice(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "observed malformed claim", text: "办理入住得人工接手", want: true},
		{name: "completed claim", text: "我帮你转人工，稍等一下", want: true},
		{name: "conditional knowledge advice", text: "如需人工可以联系前台", want: false},
		{name: "conditional failure advice", text: "如果无法办理，请联系客服", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := explicitUnauthorizedHandoffClaim(tt.text); got != tt.want {
				t.Fatalf("explicitUnauthorizedHandoffClaim(%q)=%v want %v", tt.text, got, tt.want)
			}
		})
	}
}
