package newapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTokenClientUsesCredentialScopedOfficialEndpoints(t *testing.T) {
	var mu sync.Mutex
	paths := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer sk-store-billing" {
			t.Errorf("authorization header=%q", req.Header.Get("Authorization"))
		}
		mu.Lock()
		paths = append(paths, req.URL.RequestURI())
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/status":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_display_type":"CNY","quota_per_unit":500000,"usd_exchange_rate":7.2}}`))
		case "/api/usage/token/":
			_, _ = writer.Write([]byte(`{"code":true,"data":{"name":"private-token-name","total_granted":1000,"total_used":250,"total_available":750}}`))
		case "/api/log/token":
			if req.URL.Query().Get("start_timestamp") != "100" || req.URL.Query().Get("end_timestamp") != "200" {
				t.Errorf("unexpected usage log query: %s", req.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"id":1,"created_at":150,"type":2,"token_name":"private-token-name","model_name":"gpt-test","quota":20,"prompt_tokens":3,"completion_tokens":4,"request_id":"req-1"}]}`))
		default:
			http.NotFound(writer, req)
		}
	}))
	defer server.Close()

	client, err := NewTokenClient(server.URL+"/v1/", " sk-store-billing ", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := client.GetBillingSettings(context.Background())
	if err != nil || settings.QuotaDisplayType != "CNY" || settings.QuotaPerUnit != 500000 {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	summary, err := client.GetUsageSummary(context.Background())
	if err != nil || summary.TotalUsed != 250 || summary.Name != "private-token-name" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	logs, err := client.ListUsageLogs(context.Background(), 100, 200)
	if err != nil || len(logs) != 1 || logs[0].RequestID != "req-1" {
		t.Fatalf("logs=%#v err=%v", logs, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 {
		t.Fatalf("official endpoint calls=%v", paths)
	}
}

func TestTokenClientRejectsNonCNYAndDoesNotLeakCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/api/status" {
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000,"usd_exchange_rate":1}}`))
			return
		}
		http.Error(writer, "upstream rejected", http.StatusUnauthorized)
	}))
	defer server.Close()

	const secret = "sk-never-log-this"
	client, err := NewTokenClient(server.URL, secret, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.GetBillingSettings(context.Background()); err == nil || !strings.Contains(err.Error(), "not CNY") {
		t.Fatalf("non-CNY settings error=%v", err)
	}
	if _, err = client.GetUsageSummary(context.Background()); err == nil {
		t.Fatal("unauthorized token query must fail")
	} else if strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("credential leaked in error: %v", err)
	}
}
