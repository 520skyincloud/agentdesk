package pms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestMemberQueryUsesDocumentedPathsAndIsolatesQueryScope(t *testing.T) {
	tests := []struct {
		name   string
		action string
		path   string
		args   map[string]string
		want   url.Values
	}{
		{
			name: "phone", action: "member_info_by_phone", path: memberInfoByPhonePath,
			args: map[string]string{
				"phone": " 13800138000 ", "hotelId": "untrusted-hotel", "tenantId": "untrusted-tenant",
				"gradeId": "ignored", "customerNo": "ignored",
			},
			want: url.Values{"phone": {"13800138000"}},
		},
		{
			name: "grade id", action: "member_benefits_by_grade", path: memberBenefitsByGradePath,
			args: map[string]string{
				"gradeId": "GRADE-T2-GOLD", "phone": "13800138000",
				"hotelId": "untrusted-hotel", "tenantId": "untrusted-tenant",
			},
			want: url.Values{"gradeId": {"GRADE-T2-GOLD"}},
		},
		{
			name: "grade code", action: "member_benefits_by_grade", path: memberBenefitsByGradePath,
			args: map[string]string{"gradeCode": "GRADE-T2-GOLD"},
			want: url.Values{"gradeCode": {"GRADE-T2-GOLD"}},
		},
		{
			name: "grade id precedence belongs to PMS", action: "member_benefits_by_grade", path: memberBenefitsByGradePath,
			args: map[string]string{"gradeId": "GRADE-T2-GOLD", "gradeCode": "OLD-CODE"},
			want: url.Values{"gradeId": {"GRADE-T2-GOLD"}, "gradeCode": {"OLD-CODE"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tt.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if !reflect.DeepEqual(r.URL.Query(), tt.want) {
					t.Errorf("unexpected query parameters: %#v", r.URL.Query())
				}
				if r.Header.Get("tenant-id") != "2" || r.Header.Get("Authorization") != "Bearer test" {
					t.Error("server-side authentication and tenant context must be preserved")
				}
				data := validMemberInfoData
				if tt.action == "member_benefits_by_grade" {
					data = validMemberGradeData
				}
				_, _ = fmt.Fprintf(w, `{"code":200,"data":%s}`, data)
			}))
			defer server.Close()
			client := NewClient(config.PMSConfig{
				Enabled: true, BaseURL: server.URL, HotelID: "configured-hotel",
				Authorization: "Bearer test", Headers: map[string]string{"tenant-id": "2"},
			})
			if _, err := client.Query(context.Background(), tt.action, tt.args); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemberQueryRejectsInvalidParametersBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
	tests := []struct {
		action string
		args   map[string]string
	}{
		{action: "member_info_by_phone"},
		{action: "member_info_by_phone", args: map[string]string{"phone": " "}},
		{action: "member_info_by_phone", args: map[string]string{"phone": "12800138000"}},
		{action: "member_info_by_phone", args: map[string]string{"phone": "1380013800"}},
		{action: "member_info_by_phone", args: map[string]string{"phone": "+8613800138000"}},
		{action: "member_info_by_phone", args: map[string]string{"phone": "1380013800x"}},
		{action: "member_benefits_by_grade"},
		{action: "member_benefits_by_grade", args: map[string]string{"gradeId": " ", "gradeCode": "\t"}},
	}
	for index, tt := range tests {
		t.Run(fmt.Sprintf("%s/%d", tt.action, index), func(t *testing.T) {
			if _, err := client.Query(context.Background(), tt.action, tt.args); err == nil {
				t.Fatal("invalid parameters must be rejected")
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid parameters sent %d requests", got)
	}
}

func TestMemberInfoPreservesDocumentedFactsAndDropsUnknownFields(t *testing.T) {
	data := memberQueryFixture(t, "member_info_by_phone", `{
		"code":0,"data":{
			"tenantId":9007199254740993,"memberGuestId":"9007199254740995","memberNo":"MG-001",
			"name":"张三","maskedPhone":"138****8000","status":2,"statusName":"冻结",
			"gradeId":null,"gradeName":null,"gradeAvailable":false,"validStartTime":null,
			"validEndTime":null,"memberCreatedTime":"2026-09-14T10:00:00",
			"joinChannelCode":"MANUAL_ENTRY","joinChannelName":"人工录入",
			"phone":"13800138000","cardNumber":"private-card","address":"private-address",
			"remark":"private-note","token":"private-token","unexpected":{"secret":"private-secret"}
		}
	}`)
	if len(data) != 15 {
		t.Fatalf("expected only documented member fields, got %d: %#v", len(data), data)
	}
	if got := data["tenantId"].(json.Number).String(); got != "9007199254740993" {
		t.Fatalf("tenant id precision lost: %s", got)
	}
	if data["memberGuestId"] != "9007199254740995" || data["statusName"] != "冻结" || data["gradeAvailable"] != false {
		t.Fatalf("member facts were altered: %#v", data)
	}
	if value, ok := data["validEndTime"]; !ok || value != nil {
		t.Fatalf("nullable validity must be preserved: %#v", data)
	}
	assertMemberQueryHasNoPrivateData(t, data)
}

func TestMemberBenefitsPreserveAmountRulesAndNestedWhitelist(t *testing.T) {
	data := memberQueryFixture(t, "member_benefits_by_grade", `{
		"success":true,"data":{
			"tenantId":9007199254740993,"gradeId":"GRADE-T2-GOLD","gradeCode":"GRADE-T2-GOLD",
			"gradeName":"金卡","ascOrder":2,"status":0,"statusName":"启用","gradeAvailable":true,
			"validityText":"12个月","paidUpgradeEnabled":1,"paidUpgradePricingMode":0,
			"paidUpgradeAmount":12345678901234567890.123456789,"downgradeRule":0,
			"downgradeRuleName":"下降一级","upgradeRuleSummary":"等级成长积分 >= 1000",
			"keepGradeRuleSummary":"等级成长积分 >= 800",
			"gradeRule":{"gradeId":"GRADE-T2-GOLD","autoUpgradeEnabled":1,"keepGradeRuleType":2,
				"downgradeRule":0,"cardNumber":"private-card","unknownRule":"private-rule"},
			"benefits":[{"benefitType":"MEMBER_PRICE","benefitId":"MBI-001","benefitName":"会员价",
				"label":"会员价：8.5折","contentMode":"ROOM_PRICE","contentValue":"8.5","contentText":null,
				"benefitDescription":"金卡会员享受会员价。","ascOrder":1,"token":"private-token",
				"extra":{"phone":"13800138000"}}],
			"internalNotes":"private-note"
		}
	}`)
	if got := data["paidUpgradeAmount"].(json.Number).String(); got != "12345678901234567890.123456789" {
		t.Fatalf("amount precision lost: %s", got)
	}
	rule := data["gradeRule"].(map[string]any)
	if len(rule) != 4 || rule["gradeId"] != "GRADE-T2-GOLD" {
		t.Fatalf("unexpected grade rules: %#v", rule)
	}
	benefits := data["benefits"].([]any)
	if len(benefits) != 1 || len(benefits[0].(map[string]any)) != 9 {
		t.Fatalf("unexpected benefits: %#v", benefits)
	}
	benefit := benefits[0].(map[string]any)
	if value, ok := benefit["contentText"]; !ok || value != nil {
		t.Fatalf("nullable content must be preserved: %#v", benefit)
	}
	assertMemberQueryHasNoPrivateData(t, data)
}

func TestMemberQueryPreservesDisabledGradeAndNullFields(t *testing.T) {
	data := memberQueryFixture(t, "member_benefits_by_grade", `{
		"code":0,"data":{"tenantId":2,"gradeId":"GRADE-DISABLED","gradeCode":"GRADE-DISABLED",
			"gradeName":"停用卡","ascOrder":1,"validityText":"永久","status":1,"statusName":"停用",
			"gradeAvailable":false,"paidUpgradeAmount":null,"gradeRule":null,
			"upgradeRuleSummary":null,"keepGradeRuleSummary":null,"benefits":[]}
	}`)
	if data["gradeAvailable"] != false || data["statusName"] != "停用" {
		t.Fatalf("disabled grade facts must remain unavailable: %#v", data)
	}
	if data["gradeRule"] != nil || data["paidUpgradeAmount"] != nil || len(data["benefits"].([]any)) != 0 {
		t.Fatalf("null and empty facts were fabricated: %#v", data)
	}
}

func TestMemberQueryRejectsMalformedKnownObjectsWithoutLeaking(t *testing.T) {
	for _, payload := range []string{
		`{"code":0,"data":[]}`,
		`{"code":0,"data":{"gradeRule":"private-rule"}}`,
		`{"code":0,"data":{"benefits":["private-benefit"]}}`,
		`{"code":0,"data":{"benefits":{"token":"private-token"}}}`,
		`{"code":0}`,
		`private-token 13800138000`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(payload))
		}))
		client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
		_, err := client.Query(context.Background(), "member_benefits_by_grade", map[string]string{"gradeId": "GRADE-001"})
		server.Close()
		if err == nil {
			t.Fatalf("invalid member response accepted: %s", payload)
		}
		assertMemberQueryErrorIsSafe(t, err)
	}
}

func TestMemberQueryRejectsNullDataWithoutFabricatingFacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
	_, err := client.Query(context.Background(), "member_info_by_phone", map[string]string{"phone": "13800138000"})
	if err == nil {
		t.Fatal("null member data must not be reported as a successful lookup")
	}
	assertMemberQueryErrorIsSafe(t, err)
}

func TestMemberQueryBusinessErrorsDoNotEchoPrivateData(t *testing.T) {
	for _, code := range []int{561, 640, 563, 512, 401, 999} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"code":%d,"msg":"13800138000 private-token https://private.example/path"}`, code)
			}))
			defer server.Close()
			client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
			_, err := client.Query(context.Background(), "member_info_by_phone", map[string]string{"phone": "13800138000"})
			if err == nil {
				t.Fatal("PMS business failure must not become success")
			}
			assertMemberQueryErrorIsSafe(t, err)
		})
	}
}

func TestMemberQueryRejectsSuccessFalseWithoutEchoingMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"13800138000 private-token https://private.example/path"}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
	_, err := client.Query(context.Background(), "member_info_by_phone", map[string]string{"phone": "13800138000"})
	if err == nil {
		t.Fatal("explicit failure must not become success")
	}
	assertMemberQueryErrorIsSafe(t, err)
}

func TestPMSQueryTransportErrorsDoNotEchoPrivateRequestURL(t *testing.T) {
	for _, action := range []string{"member_info_by_phone", "member_benefits_by_grade", "recept_order_by_phone", "room_status", "inventory"} {
		t.Run(action, func(t *testing.T) {
			client := NewClient(config.PMSConfig{
				Enabled: true, BaseURL: "https://private.example", HotelID: "hotel-1",
				Authorization: "Bearer private-token",
			})
			client.httpClient.Transport = memberQueryRoundTripper(func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("request %s failed with 13800138000 private-token", r.URL.String())
			})
			args := map[string]string{
				"phone": "13800138000", "gradeId": "GRADE-T2-GOLD", "keyword": "13800138000",
			}
			if action == "inventory" {
				args["beginTime"] = "2026-09-22"
				args["endTime"] = "2026-09-23"
			}
			_, err := client.Query(context.Background(), action, args)
			if err == nil {
				t.Fatal("transport failure must not become success")
			}
			assertMemberQueryErrorIsSafe(t, err)
		})
	}
}

func TestInventoryQueryUsesOnlyConfiguredTenant(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		args       map[string]string
		wantTenant string
		wantError  bool
	}{
		{name: "default", headers: map[string]string{"tenant-id": "2"}, wantTenant: "2"},
		{name: "case insensitive header", headers: map[string]string{"Tenant-Id": "2"}, wantTenant: "2"},
		{name: "matching input", headers: map[string]string{"tenant-id": "2"}, args: map[string]string{"tenantId": "2"}, wantTenant: "2"},
		{name: "conflicting input", headers: map[string]string{"tenant-id": "2"}, args: map[string]string{"tenantId": "9"}, wantError: true},
		{name: "unbound input", args: map[string]string{"tenantId": "9"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != inventoryPath || r.URL.Query().Get("tenantId") != tt.wantTenant {
					t.Errorf("unexpected inventory scope: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
			}))
			defer server.Close()
			client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, Headers: tt.headers})
			args := map[string]string{"beginTime": "2026-09-22", "endTime": "2026-09-23"}
			for key, value := range tt.args {
				args[key] = value
			}
			_, err := client.Query(context.Background(), "inventory", args)
			if (err != nil) != tt.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantError && requests.Load() != 0 {
				t.Fatal("a conflicting tenant must not send an HTTP request")
			}
		})
	}
}

const validMemberInfoData = `{"tenantId":2,"memberGuestId":"MG-001","memberNo":"MG-001","gradeAvailable":true}`
const validMemberGradeData = `{"tenantId":2,"gradeId":"GRADE-001","gradeCode":"GRADE-001","gradeName":"银卡","ascOrder":1,"status":0,"statusName":"启用","gradeAvailable":true,"validityText":"永久","benefits":[]}`

func TestMemberQueriesAcceptExactStringTenantIDFromLiveAPI(t *testing.T) {
	for _, tc := range []struct{ action, data string }{
		{"member_info_by_phone", validMemberInfoData},
		{"member_benefits_by_grade", strings.Replace(validMemberGradeData, `"benefits":[]`,
			`"benefits":[],"upgradeRuleSummary":"累计入住房夜 >= 1 或 等级成长积分 >= 5","keepGradeRuleSummary":null`, 1)},
	} {
		t.Run(tc.action, func(t *testing.T) {
			payload := strings.Replace(tc.data, `"tenantId":2`, `"tenantId":"9007199254740993"`, 1)
			data := memberQueryFixture(t, tc.action, `{"code":200,"data":`+payload+`}`)
			if data["tenantId"] != "9007199254740993" {
				t.Fatalf("string tenant ID precision or type changed: %#v", data["tenantId"])
			}
			if tc.action == "member_benefits_by_grade" {
				if data["keepGradeRuleSummary"] != nil || len(data["benefits"].([]any)) != 0 {
					t.Fatalf("empty live grade facts were altered: %#v", data)
				}
			}
		})
	}
}

func TestMemberQueryRejectsMalformedEnvelopeAndRequiredFields(t *testing.T) {
	tests := []struct {
		name, action, payload string
	}{
		{"missing status", "member_info_by_phone", `{"data":` + validMemberInfoData + `}`},
		{"object status", "member_info_by_phone", `{"code":{},"data":` + validMemberInfoData + `}`},
		{"string success", "member_info_by_phone", `{"success":"true","data":` + validMemberInfoData + `}`},
		{"empty info", "member_info_by_phone", `{"code":200,"data":{}}`},
		{"missing member id", "member_info_by_phone", `{"code":0,"data":{"tenantId":2,"memberNo":"MG-001","gradeAvailable":true}}`},
		{"numeric member id", "member_info_by_phone", `{"code":0,"data":{"tenantId":2,"memberGuestId":123,"memberNo":"MG-001","gradeAvailable":true}}`},
		{"arbitrary tenant id", "member_info_by_phone", `{"code":0,"data":` + strings.Replace(validMemberInfoData, `"tenantId":2`, `"tenantId":"tenant-other"`, 1) + `}`},
		{"fractional tenant id", "member_info_by_phone", `{"code":0,"data":` + strings.Replace(validMemberInfoData, `"tenantId":2`, `"tenantId":2.5`, 1) + `}`},
		{"scientific tenant id", "member_info_by_phone", `{"code":0,"data":` + strings.Replace(validMemberInfoData, `"tenantId":2`, `"tenantId":"2e3"`, 1) + `}`},
		{"string availability", "member_info_by_phone", `{"code":0,"data":{"tenantId":2,"memberGuestId":"MG-001","memberNo":"MG-001","gradeAvailable":"true"}}`},
		{"unmasked phone", "member_info_by_phone", `{"code":0,"data":{"tenantId":2,"memberGuestId":"MG-001","memberNo":"MG-001","gradeAvailable":true,"maskedPhone":"13800138000"}}`},
		{"missing benefits", "member_benefits_by_grade", `{"code":0,"data":` + strings.Replace(validMemberGradeData, `,"benefits":[]`, "", 1) + `}`},
		{"invalid grade rule", "member_benefits_by_grade", `{"code":0,"data":` + strings.Replace(validMemberGradeData, `"benefits":[]`, `"benefits":[],"gradeRule":"private-rule"`, 1) + `}`},
		{"missing benefit fields", "member_benefits_by_grade", `{"code":0,"data":` + strings.Replace(validMemberGradeData, `"benefits":[]`, `"benefits":[{"label":"优惠"}]`, 1) + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.payload))
			}))
			defer server.Close()
			client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL})
			_, err := client.Query(context.Background(), tt.action, map[string]string{"phone": "13800138000", "gradeId": "GRADE-001"})
			if err == nil {
				t.Fatal("invalid response must be rejected")
			}
			assertMemberQueryErrorIsSafe(t, err)
		})
	}
}

func memberQueryFixture(t *testing.T, action, payload string) map[string]any {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	result, err := client.Query(context.Background(), action, map[string]string{
		"phone": "13800138000", "gradeId": "GRADE-T2-GOLD",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected member data object, got %#v", result.Data)
	}
	return data
}

func assertMemberQueryHasNoPrivateData(t *testing.T, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-", "13800138000"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("private data leaked into query result: %s", raw)
		}
	}
}

func assertMemberQueryErrorIsSafe(t *testing.T, err error) {
	t.Helper()
	for _, forbidden := range []string{"13800138000", "private-", "https://", "http://", "Authorization"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("private data leaked into query error: %v", err)
		}
	}
}

type memberQueryRoundTripper func(*http.Request) (*http.Response, error)

func (f memberQueryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
