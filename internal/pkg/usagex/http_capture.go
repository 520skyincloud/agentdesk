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
	Attempt           int
	StartedAt         time.Time
	FinishedAt        time.Time
	StatusCode        int
	ErrorClass        string
	ProviderStatus    string
	ProviderCode      string
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
	if receipt.Attempt <= 0 {
		receipt.Attempt = len(c.receipts) + 1
	}
	c.receipts = append(c.receipts, receipt)
	c.mu.Unlock()
}

func (c *Capture) annotateLatest(errorClass, providerStatus, providerCode string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.receipts) == 0 {
		return
	}
	latest := &c.receipts[len(c.receipts)-1]
	latest.ErrorClass = strings.TrimSpace(errorClass)
	latest.ProviderStatus = strings.TrimSpace(providerStatus)
	latest.ProviderCode = strings.TrimSpace(providerCode)
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

func AnnotateLatestReceipt(ctx context.Context, errorClass, providerStatus, providerCode string) {
	if capture := captureFromContext(ctx); capture != nil {
		capture.annotateLatest(errorClass, providerStatus, providerCode)
	}
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
