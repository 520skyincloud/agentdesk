package usagex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrackingTransportCapturesNewAPIRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(NewAPIRequestIDHeader, "req-new-api-1")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, capture := WithCapture(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := NewHTTPClient(2 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	receipts := capture.Receipts()
	if len(receipts) != 1 || receipts[0].Gateway != GatewayNewAPI || receipts[0].RequestID != "req-new-api-1" {
		t.Fatalf("unexpected receipts %#v", receipts)
	}
}
