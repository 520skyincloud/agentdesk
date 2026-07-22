package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenClientUsesTokenEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_display_type":"CNY","quota_per_unit":500000,"usd_exchange_rate":7.3,"price":7.3}}`))
		case "/api/usage/token/":
			_, _ = w.Write([]byte(`{"code":true,"data":{"name":"store-a","total_granted":500000,"total_used":125000,"total_available":375000,"expires_at":0}}`))
		case "/api/log/token":
			if r.URL.Query().Get("start_timestamp") != "100" || r.URL.Query().Get("end_timestamp") != "200" {
				t.Fatalf("query=%q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"created_at":2,"model_name":"chat","quota":100,"prompt_tokens":8,"completion_tokens":4,"use_time":1,"request_id":"req-1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewTokenClient(server.URL+"/v1", "sk-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := client.GetBillingSettings(context.Background())
	if err != nil || settings.QuotaPerUnit != 500000 || settings.USDExchangeRate != 7.3 {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	summary, err := client.GetUsageSummary(context.Background())
	if err != nil || summary.TotalAvailable != 375000 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	logs, err := client.ListUsageLogs(context.Background(), 100, 200)
	if err != nil || len(logs) != 1 || logs[0].RequestID != "req-1" {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
}
