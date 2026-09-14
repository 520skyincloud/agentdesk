package pms

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestClientQueryUsesConfiguredHeadersAndHotel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != roomStatusPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("hotelId") != "hotel-1" || r.URL.Query().Get("keyword") != "13800000000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("authorization header missing")
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"homeCardList":[{"roomNo":"101"}]}}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{
		Enabled:       true,
		BaseURL:       server.URL,
		Authorization: "Bearer test",
		HotelID:       "hotel-1",
	})
	result, err := client.Query(context.Background(), "room_status", map[string]string{"keyword": "13800000000"})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["homeCardList"] == nil {
		t.Fatalf("expected normalized data")
	}
}

func TestClientRejectsUnsupportedAction(t *testing.T) {
	client := NewClient(config.PMSConfig{BaseURL: "http://example.com"})
	if _, err := client.Query(context.Background(), "write", nil); err == nil {
		t.Fatal("expected unsupported action error")
	}
}

func TestClientDisabledWhenConfigFlagIsFalse(t *testing.T) {
	client := NewClient(config.PMSConfig{BaseURL: "http://example.com"})
	if client.Enabled() {
		t.Fatal("PMS client must honor enabled=false")
	}
	if _, err := client.Query(context.Background(), "room_status", nil); err == nil {
		t.Fatal("disabled PMS client must reject queries")
	}
}

func TestClientQueryByPhoneUsesDocumentedPathAndFiltersArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != receptOrderByPhonePath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("phone") != "13800000000" || r.URL.Query().Get("hotelId") != "hotel-1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("memberId") != "" {
			t.Fatal("unsupported memberId must not be forwarded")
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"receptOrderId":9007199254740993}}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	result, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
		"phone": "13800000000", "memberId": "not-supported",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["receptOrderId"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("long id precision was lost: %#v", result.Data)
	}
}

func TestClientInventoryMapsToolDatesToHPMSParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != inventoryPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("beginTime") != "2026-09-14" || q.Get("endTime") != "2026-09-15" {
			t.Fatalf("unexpected date query: %s", r.URL.RawQuery)
		}
		if q.Get("metrics") != "sold,sellable,occupied,maintenance" {
			t.Fatalf("unexpected metrics query: %s", r.URL.RawQuery)
		}
		if q.Get("startDate") != "" || q.Get("endDate") != "" {
			t.Fatalf("legacy date names must not be forwarded: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	if _, err := client.Query(context.Background(), "inventory", map[string]string{
		"startDate": "2026-09-14",
		"endDate":   "2026-09-15",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClientRenewRequiresExplicitWriteEnablement(t *testing.T) {
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: "http://example.com"})
	_, err := client.Renew(context.Background(), RenewRequest{ReceptOrderID: 1})
	if err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("expected write guard, got %v", err)
	}
}

func TestClientRenewPostsDocumentedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != renewPath {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"receptOrderId":123`) || !strings.Contains(string(body), `"renewType":"ORIGINAL"`) {
			t.Fatalf("unexpected body: %s", body)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":null}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, AllowWrite: true})
	result, err := client.Renew(context.Background(), RenewRequest{ReceptOrderID: 123, RenewType: "ORIGINAL"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != "0" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
