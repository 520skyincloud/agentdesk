package executor

import (
	"errors"
	"testing"
)

func TestIsRetryableRuntimeIntentModelError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "server overloaded", err: errors.New("Server Overloaded"), want: true},
		{name: "http 503", err: errors.New("channel error, status code: 503"), want: true},
		{name: "rate limited", err: errors.New("responses api status 429"), want: true},
		{name: "temporary network failure", err: errors.New("connection reset by peer"), want: true},
		{name: "bad request", err: errors.New("status code: 400"), want: false},
		{name: "unauthorized", err: errors.New("unauthorized"), want: false},
		{name: "ordinary protocol error", err: errors.New("invalid JSON response"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableRuntimeIntentModelError(test.err); got != test.want {
				t.Fatalf("isRetryableRuntimeIntentModelError(%q)=%v, want %v", test.err, got, test.want)
			}
		})
	}
}
