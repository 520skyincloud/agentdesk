package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientUsesSystemOneProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Error("unexpected authorization header")
		}
		var payload Request
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Model != DefaultModel {
			t.Errorf("invalid request body or default model: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"model":"jev-1.13.0","answers":{"primary":{"type":"choice","choice":"hotel_info","confidence":0.9,"probabilities":{"hotel_info":0.95,"interaction":0.05}},"needs_tool":{"type":"noul","noul":0.1}},"usage":{"input_tokens":12,"output_tokens":4}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/v1", "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Evaluate(context.Background(), Request{
		State: "酒店有没有停车场",
		Questions: map[string]Question{
			"primary":    {Type: "choice", Instructions: "选择意图", Criteria: map[string]any{"hotel_info": "酒店信息", "interaction": "闲聊"}},
			"needs_tool": {Type: "noul", Instructions: "是否需要实时工具"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Answers["primary"].Choice != "hotel_info" || response.Answers["needs_tool"].Noul != 0.1 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestClientRejectsAPIErrorWithoutLeakingAuthorization(t *testing.T) {
	for _, status := range []int{401, 422, 429, 529} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Retry-After", "3")
				http.Error(writer, `{"error":"invalid key secret, private hotel/customer data"}`, status)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "secret", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Evaluate(context.Background(), noulRequest())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.RetryAfter != 3*time.Second {
				t.Fatalf("unexpected API error: %v", err)
			}
			if apiErr.IsRetryable() != (status == 429 || status == 529) {
				t.Fatalf("unexpected retry classification: %v", err)
			}
			if err.Error() != fmt.Sprintf("jev status %d", status) || requests.Load() != 1 {
				t.Fatalf("error leaked body or request was retried: %v, count %d", err, requests.Load())
			}
		})
	}
}

func noulRequest() Request {
	return Request{
		State: "test",
		Questions: map[string]Question{
			"q": {Type: "noul", Instructions: "Is this a test?"},
		},
	}
}

func TestClientValidatesTypedAnswers(t *testing.T) {
	choiceQuestion := Question{Type: "choice", Instructions: "Select one.", Criteria: map[string]any{"yes": nil, "no": nil}}
	tests := []struct {
		name     string
		question Question
		answer   string
		wantErr  bool
	}{
		{"noul zero", noulRequest().Questions["q"], `{"type":"noul","noul":0}`, false},
		{"noul missing", noulRequest().Questions["q"], `{"type":"noul"}`, true},
		{"noul null", noulRequest().Questions["q"], `{"type":"noul","noul":null}`, true},
		{"noul range", noulRequest().Questions["q"], `{"type":"noul","noul":1.1}`, true},
		{"noul negative", noulRequest().Questions["q"], `{"type":"noul","noul":-0.1}`, true},
		{"noul string", noulRequest().Questions["q"], `{"type":"noul","noul":"secret"}`, true},
		{"noul wrong type", noulRequest().Questions["q"], `{"type":"choice","choice":"yes"}`, true},
		{"choice zero confidence", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0,"probabilities":{"yes":0.5,"no":0.5}}`, false},
		{"choice missing confidence", choiceQuestion, `{"type":"choice","choice":"yes","probabilities":{"yes":0.5,"no":0.5}}`, true},
		{"choice missing value", choiceQuestion, `{"type":"choice","confidence":0,"probabilities":{"yes":0.5,"no":0.5}}`, true},
		{"choice unknown value", choiceQuestion, `{"type":"choice","choice":"maybe","confidence":0,"probabilities":{"yes":0.5,"no":0.5}}`, true},
		{"choice missing probabilities", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0.5}`, true},
		{"choice null probability", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0.5,"probabilities":{"yes":1,"no":null}}`, true},
		{"choice unknown probability", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0.5,"probabilities":{"yes":0.8,"maybe":0.2}}`, true},
		{"choice non-unit probability total", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0.5,"probabilities":{"yes":0.8,"no":0.3}}`, false},
		{"choice probability range", choiceQuestion, `{"type":"choice","choice":"yes","confidence":0.5,"probabilities":{"yes":1.1,"no":0}}`, true},
		{"choice confidence range", choiceQuestion, `{"type":"choice","choice":"yes","confidence":2,"probabilities":{"yes":0.8,"no":0.2}}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(writer, `{"model":"jev-1.13.0","answers":{"q":%s},"usage":{"input_tokens":1,"output_tokens":1}}`, test.answer)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "secret", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Evaluate(context.Background(), Request{State: "test", Questions: map[string]Question{"q": test.question}})
			if (err != nil) != test.wantErr {
				t.Fatalf("got error %v, want error %v", err, test.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked response content: %v", err)
			}
			if !test.wantErr && result.Answers["q"].Type != test.question.Type {
				t.Fatalf("unexpected result: %#v", result)
			}
		})
	}
}

func TestClientRejectsMalformedOrOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":   `{"model":"secret"`,
		"no model":       `{"answers":{"q":{"type":"noul","noul":0}}}`,
		"no answers":     `{"model":"jev-1.13.0","answers":{}}`,
		"wrong ID":       `{"model":"jev-1.13.0","answers":{"other":{"type":"noul","noul":0}}}`,
		"extra ID":       `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0},"other":{"type":"noul","noul":0}}}`,
		"null answer":    `{"model":"jev-1.13.0","answers":{"q":null}}`,
		"negative usage": `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0}},"usage":{"input_tokens":-1}}`,
		"no usage":       `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0}}}`,
		"partial usage":  `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0}},"usage":{"input_tokens":1}}`,
		"trailing JSON":  `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0}}}{"secret":true}`,
		"oversized":      `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0}},"extra":"` + strings.Repeat("x", maxResponseBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "secret", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Evaluate(context.Background(), noulRequest())
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientPreservesDecodedUsageWhenTypedValidationFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul"}},"usage":{"input_tokens":12,"output_tokens":3}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Evaluate(context.Background(), noulRequest())
	if err == nil || response.Model != "jev-1.13.0" || response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 3 {
		t.Fatalf("typed validation lost provider usage: response=%#v err=%v", response, err)
	}
}

func TestClientCancellationAndTimeout(t *testing.T) {
	for _, cancelBeforeSend := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelBeforeSend), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				select {
				case <-request.Context().Done():
				case <-time.After(250 * time.Millisecond):
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "secret", 30*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantErr := context.DeadlineExceeded
			if cancelBeforeSend {
				cancel()
				wantErr = context.Canceled
			}
			_, err = client.Evaluate(ctx, noulRequest())
			if !errors.Is(err, wantErr) || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("unexpected cancellation error: %v", err)
			}
			if requests.Load() > 1 {
				t.Fatalf("client retried request: %d", requests.Load())
			}
		})
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), noulRequest())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTemporaryRedirect || redirected.Load() {
		t.Fatalf("unexpected redirect behavior: %v, followed %v", err, redirected.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"seconds", http.Header{"Retry-After": {"3"}}, 3 * time.Second},
		{"milliseconds first", http.Header{"Retry-After-Ms": {"125"}, "Retry-After": {"3"}}, 125 * time.Millisecond},
		{"date", http.Header{"Retry-After": {now.Add(time.Minute).Format(http.TimeFormat)}}, time.Minute},
		{"negative", http.Header{"Retry-After": {"-1"}}, 0},
		{"past", http.Header{"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)}}, 0},
		{"invalid", http.Header{"Retry-After": {"secret"}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseRetryAfter(test.header, now); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewClientRejectsCredentialsInURL(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:secret@example.com", "https://example.com?token=secret", "https://example.com#secret",
		"http://example.com", "http://192.0.2.1", "not a URL",
	} {
		_, err := NewClient(baseURL, "secret", time.Second)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
