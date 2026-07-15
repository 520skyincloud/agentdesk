package newapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestFindUsageByRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/self/" || r.URL.Query().Get("request_id") != "req-1" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("New-Api-User") != "7" {
			t.Fatalf("missing new api auth headers")
		}
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"page":1,"page_size":10,"total":1,"items":[{"id":8,"type":2,"request_id":"req-1","model_name":"deepseek-v4-pro","prompt_tokens":120,"completion_tokens":30,"quota":150}]}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, AccessToken: "access", UserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	item, err := client.FindUsageByRequestID(context.Background(), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if item == nil || item.PromptTokens != 120 || item.CompletionTokens != 30 || item.ModelName != "deepseek-v4-pro" {
		t.Fatalf("unexpected item %#v", item)
	}
}

func TestListAllUsageLogsFollowsPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if r.URL.Query().Get("token_name") != "fastgpt-dedicated" || r.URL.Query().Get("page_size") != "2" {
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
		switch page {
		case 1:
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"request_id":"req-1"},{"request_id":"req-2"}]}}`))
		case 2:
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"request_id":"req-3"}]}}`))
		default:
			t.Fatalf("unexpected page %d", page)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, AccessToken: "access", UserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListAllUsageLogs(context.Background(), UsageLogQuery{TokenName: "fastgpt-dedicated", PageSize: 2}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %s", len(items), fmt.Sprint(items))
	}
}
