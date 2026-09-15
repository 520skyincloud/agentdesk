package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestNonHPMSProviderNeverContactsExternalPMS(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, provider := range []string{"sandbox", "unknown"} {
		client := NewClient(config.PMSConfig{Enabled: true, Provider: provider, BaseURL: server.URL, AllowWrite: true})
		if client.Enabled() {
			t.Fatal("non-HPMS provider exposed external client")
		}
		if _, err := client.Query(context.Background(), "inventory", nil); err == nil {
			t.Fatal("non-HPMS query should fail closed")
		}
		if _, err := client.Renew(context.Background(), RenewRequest{ReceptOrderID: 1}); err == nil {
			t.Fatal("non-HPMS write should fail closed")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unexpected external requests: %d", calls.Load())
	}
}
