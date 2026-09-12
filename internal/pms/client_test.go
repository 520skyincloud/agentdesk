package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
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
