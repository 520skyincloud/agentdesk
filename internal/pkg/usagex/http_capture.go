package usagex

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	GatewayNewAPI          = "new_api"
	NewAPIRequestIDHeader  = "X-Oneapi-Request-Id"
	NewAPIUpstreamIDHeader = "X-Upstream-Request-Id"
)

type Receipt struct {
	Gateway           string
	RequestID         string
	UpstreamRequestID string
	StartedAt         time.Time
	FinishedAt        time.Time
	StatusCode        int
}

func (r Receipt) LatencyMS() int64 {
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt).Milliseconds()
}

type Capture struct {
	mu       sync.Mutex
	receipts []Receipt
}

func (c *Capture) add(receipt Receipt) {
	if c == nil || strings.TrimSpace(receipt.RequestID) == "" {
		return
	}
	c.mu.Lock()
	c.receipts = append(c.receipts, receipt)
	c.mu.Unlock()
}

func (c *Capture) Receipts() []Receipt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Receipt(nil), c.receipts...)
}

type captureContextKey struct{}

type Scope struct {
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	ConversationID      int64
	MessageID           int64
	RequestID           string
	ModelProfileID      int64
	ProfileRevision     int64
	UsageSlot           string
	CredentialRevision  int64
	KeyFingerprint      string
	ModelSource         string
}

type scopeContextKey struct{}

func WithCapture(ctx context.Context) (context.Context, *Capture) {
	capture := &Capture{}
	return context.WithValue(ctx, captureContextKey{}, capture), capture
}

func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

func ScopeFromContext(ctx context.Context) Scope {
	if ctx == nil {
		return Scope{}
	}
	scope, _ := ctx.Value(scopeContextKey{}).(Scope)
	return scope
}

func captureFromContext(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(captureContextKey{}).(*Capture)
	return capture
}

type TrackingTransport struct {
	Base http.RoundTripper
}

func (t TrackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	startedAt := time.Now()
	resp, err := base.RoundTrip(req)
	finishedAt := time.Now()
	if resp == nil {
		return resp, err
	}
	requestID := strings.TrimSpace(resp.Header.Get(NewAPIRequestIDHeader))
	if requestID == "" {
		return resp, err
	}
	if capture := captureFromContext(req.Context()); capture != nil {
		capture.add(Receipt{
			Gateway:           GatewayNewAPI,
			RequestID:         requestID,
			UpstreamRequestID: strings.TrimSpace(resp.Header.Get(NewAPIUpstreamIDHeader)),
			StartedAt:         startedAt,
			FinishedAt:        finishedAt,
			StatusCode:        resp.StatusCode,
		})
	}
	return resp, err
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: TrackingTransport{Base: http.DefaultTransport},
		Timeout:   timeout,
	}
}
