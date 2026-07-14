package repositories

import (
	"testing"
	"time"
)

func TestParseTenantAggregateTimeSupportsSQLiteAndMySQLDriverValues(t *testing.T) {
	expected := time.Date(2026, time.July, 14, 12, 34, 56, 123456000, time.FixedZone("CST", 8*60*60))
	tests := []struct {
		name  string
		value any
	}{
		{name: "mysql time", value: expected},
		{name: "sqlite text", value: "2026-07-14 12:34:56.123456+08:00"},
		{name: "sqlite bytes", value: []byte("2026-07-14 12:34:56.123456+08:00")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parseTenantAggregateTime(tt.value)
			if err != nil {
				t.Fatalf("parse aggregate time: %v", err)
			}
			if parsed == nil || !parsed.Equal(expected) {
				t.Fatalf("parsed time = %v, want %v", parsed, expected)
			}
		})
	}
	parsed, err := parseTenantAggregateTime(nil)
	if err != nil || parsed != nil {
		t.Fatalf("parse nil = %v, %v", parsed, err)
	}
	if _, err := parseTenantAggregateTime("not-a-time"); err == nil {
		t.Fatal("expected invalid aggregate time to fail")
	}
}
