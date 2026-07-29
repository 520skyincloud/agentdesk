package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/repositories"
)

const agentDeskAuthorizationCallbackURL = "https://weibao.omnireva.com/api/wecom/provider/authorization/callback"

func TestWeComProviderSetSessionInfoRequestContract(t *testing.T) {
	for _, authType := range []int{1, 0} {
		t.Run("auth_type_"+strconv.Itoa(authType), func(t *testing.T) {
			fixture := setupArrivalLinkTestFixture(t)
			ticketCiphertext, ticketNonce, err := fixture.security.Encrypt("suite_ticket", "suite-ticket-value")
			if err != nil {
				t.Fatal(err)
			}
			if err := repositories.ArrivalRepository.UpdateSuiteCredential(
				fixture.db,
				fixture.authorization.SuiteCredentialID,
				map[string]any{
					"suite_ticket_ciphertext": ticketCiphertext,
					"suite_ticket_nonce":      ticketNonce,
				},
			); err != nil {
				t.Fatal(err)
			}

			var sessionBody map[string]any
			var suiteTokenCalls, preAuthCalls, sessionCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/cgi-bin/service/get_suite_token":
					suiteTokenCalls++
					_, _ = w.Write([]byte(`{"errcode":0,"suite_access_token":"suite-token","expires_in":7200}`))
				case "/cgi-bin/service/get_pre_auth_code":
					preAuthCalls++
					if req.Method != http.MethodGet || req.URL.Query().Get("suite_access_token") != "suite-token" {
						t.Errorf("unexpected pre-auth request: %s %s", req.Method, req.URL.String())
					}
					_, _ = w.Write([]byte(`{"errcode":0,"pre_auth_code":"pre-auth-code"}`))
				case "/cgi-bin/service/set_session_info":
					sessionCalls++
					if req.Method != http.MethodPost || req.URL.Query().Get("suite_access_token") != "suite-token" {
						t.Errorf("unexpected session request: %s %s", req.Method, req.URL.String())
					}
					decoder := json.NewDecoder(req.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&sessionBody); err != nil {
						t.Errorf("decode set_session_info body: %v", err)
					}
					_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
				default:
					http.NotFound(w, req)
				}
			}))
			defer server.Close()

			cfg := config.Current()
			cfg.Arrival.WeComAPIBaseURL = server.URL
			cfg.Arrival.WeComAuthType = authType
			cfg.Arrival.PublicBaseURL = "https://weibao.omnireva.com"
			config.SetCurrent(&cfg)

			authorizationURL, preAuthCode, err := newWeComProviderService().BeginAuthorization("State123")
			if err != nil {
				t.Fatalf("BeginAuthorization() error=%v", err)
			}
			if preAuthCode != "pre-auth-code" || suiteTokenCalls != 1 || preAuthCalls != 1 || sessionCalls != 1 {
				t.Fatalf(
					"unexpected result preAuth=%q calls=%d/%d/%d",
					preAuthCode,
					suiteTokenCalls,
					preAuthCalls,
					sessionCalls,
				)
			}
			parsedURL, err := url.Parse(authorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsedURL.Scheme != "https" ||
				parsedURL.Host != "open.work.weixin.qq.com" ||
				parsedURL.Path != "/3rdapp/install" ||
				parsedURL.Fragment != "" ||
				parsedURL.User != nil {
				t.Fatalf("unexpected authorization endpoint: %s", authorizationURL)
			}
			if parsedURL.Query().Get("state") != "State123" ||
				parsedURL.Query().Get("pre_auth_code") != "pre-auth-code" {
				t.Fatalf("unexpected authorization URL: %s", authorizationURL)
			}
			redirectURI := parsedURL.Query().Get("redirect_uri")
			if redirectURI != agentDeskAuthorizationCallbackURL {
				t.Fatalf("redirect_uri=%q want %q", redirectURI, agentDeskAuthorizationCallbackURL)
			}
			encodedOnce := url.QueryEscape(agentDeskAuthorizationCallbackURL)
			encodedTwice := url.QueryEscape(encodedOnce)
			if !strings.Contains(parsedURL.RawQuery, "redirect_uri="+encodedOnce) ||
				strings.Contains(parsedURL.RawQuery, "redirect_uri="+encodedTwice) {
				t.Fatalf("redirect_uri must be URL encoded exactly once: %s", parsedURL.RawQuery)
			}
			if len(sessionBody) != 2 || sessionBody["pre_auth_code"] != "pre-auth-code" {
				t.Fatalf("unexpected set_session_info top-level body: %#v", sessionBody)
			}
			sessionInfo, ok := sessionBody["session_info"].(map[string]any)
			if !ok || len(sessionInfo) != 1 {
				t.Fatalf("unexpected session_info: %#v", sessionBody["session_info"])
			}
			value, ok := sessionInfo["auth_type"].(json.Number)
			if !ok || value.String() != strconv.Itoa(authType) {
				t.Fatalf("auth_type=%#v want JSON integer %d", sessionInfo["auth_type"], authType)
			}
		})
	}
}

func TestWeComProviderRejectsInvalidAuthorizationStateBeforeRequest(t *testing.T) {
	setupArrivalLinkTestFixture(t)
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestCount++
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	cfg.Arrival.WeComAuthType = 1
	config.SetCurrent(&cfg)

	provider := newWeComProviderService()
	for _, state := range []string{
		"",
		" leading",
		"trailing ",
		"contains-dash",
		"contains_underbar",
		"contains.dot",
		"contains+plus",
		"contains/slash",
		"contains=equals",
		"中文",
		"A\nB",
		strings.Repeat("A", 129),
	} {
		if _, _, err := provider.BeginAuthorization(state); err == nil {
			t.Fatalf("invalid state accepted: %q", state)
		}
	}
	if requestCount != 0 {
		t.Fatalf("invalid state triggered %d provider requests", requestCount)
	}

	cfg.Arrival.WeComAuthType = 2
	config.SetCurrent(&cfg)
	if _, _, err := provider.BeginAuthorization("State123"); err == nil {
		t.Fatal("invalid auth type accepted")
	}
	if requestCount != 0 {
		t.Fatalf("invalid auth type triggered %d provider requests", requestCount)
	}
}

func TestRandomWeComAuthorizationStateContract(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 32; i++ {
		state, err := randomWeComAuthorizationState()
		if err != nil {
			t.Fatal(err)
		}
		if len(state) != 64 || !weComAuthorizationStatePattern.MatchString(state) {
			t.Fatalf("generated invalid state %q", state)
		}
		for _, char := range state {
			if !((char >= 'A' && char <= 'Z') ||
				(char >= 'a' && char <= 'z') ||
				(char >= '0' && char <= '9')) {
				t.Fatalf("generated state contains non-ASCII-alphanumeric character %q", char)
			}
		}
		if strings.ContainsAny(state, "-_.+/=") {
			t.Fatalf("generated state contains forbidden character: %q", state)
		}
		if _, exists := seen[state]; exists {
			t.Fatal("generated duplicate authorization state")
		}
		seen[state] = struct{}{}
	}
}
