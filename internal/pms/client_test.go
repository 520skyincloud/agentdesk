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
		if r.URL.Query().Get("customerNo") != "MEMBER-001" {
			t.Fatalf("legacy memberId must map to customerNo: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"receptOrderId":9007199254740993}}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	result, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
		"phone": "13800000000", "memberId": "MEMBER-001",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["receptOrderId"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("long id precision was lost: %#v", result.Data)
	}
}

func TestClientCurrentOrderLookupSupportsCustomerNoAndCombinedFilters(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		path           string
		args           map[string]string
		wantPhone      string
		wantCustomerNo string
	}{
		{
			name:           "recept customer number",
			action:         "recept_order_by_phone",
			path:           receptOrderByPhonePath,
			args:           map[string]string{"customerNo": "MEMBER-001"},
			wantCustomerNo: "MEMBER-001",
		},
		{
			name:           "reserve combined",
			action:         "reserve_order_by_phone",
			path:           reserveOrderByPhonePath,
			args:           map[string]string{"phone": "13800000000", "customerNo": "MEMBER-001"},
			wantPhone:      "13800000000",
			wantCustomerNo: "MEMBER-001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("hotelId") != "hotel-1" ||
					q.Get("phone") != tt.wantPhone ||
					q.Get("customerNo") != tt.wantCustomerNo {
					t.Fatalf("unexpected query: %s", r.URL.RawQuery)
				}
				if q.Get("memberId") != "" {
					t.Fatalf("legacy memberId must not be forwarded: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"code":0,"data":{"orderId":101}}`))
			}))
			defer server.Close()

			client := NewClient(config.PMSConfig{
				Enabled: true,
				BaseURL: server.URL,
				HotelID: "hotel-1",
			})
			if _, err := client.Query(context.Background(), tt.action, tt.args); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientCurrentOrderLookupPrefersCustomerNoOverLegacyMemberID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("customerNo"); got != "MEMBER-NEW" {
			t.Fatalf("customerNo must take precedence, got %q", got)
		}
		if r.URL.Query().Get("memberId") != "" {
			t.Fatal("legacy memberId must not be forwarded")
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	if _, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
		"customerNo": "MEMBER-NEW",
		"memberId":   "MEMBER-OLD",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClientCurrentOrderLookupRequiresHotelAndIdentifier(t *testing.T) {
	t.Run("hotel id", func(t *testing.T) {
		client := NewClient(config.PMSConfig{Enabled: true, BaseURL: "http://example.com"})
		_, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
			"phone": "13800000000",
		})
		if err == nil || !strings.Contains(err.Error(), "酒店ID不能为空") {
			t.Fatalf("expected hotel id validation error, got %v", err)
		}
	})

	t.Run("phone or customer number", func(t *testing.T) {
		client := NewClient(config.PMSConfig{
			Enabled: true,
			BaseURL: "http://example.com",
			HotelID: "hotel-1",
		})
		_, err := client.Query(context.Background(), "reserve_order_by_phone", nil)
		if err == nil || !strings.Contains(err.Error(), "手机号码和会员编号/协议公司编号必须要有一个") {
			t.Fatalf("expected identifier validation error, got %v", err)
		}
	})
}

func TestClientQueryRejectsSuccessFalseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"检测到多条匹配订单"}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	_, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
		"phone": "13800000000",
	})
	if err == nil || !strings.Contains(err.Error(), "检测到多条匹配订单") {
		t.Fatalf("expected PMS business error, got %v", err)
	}
}

func TestClientQueryAcceptsRespInfoSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"receptOrderId":101}}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	result, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{
		"phone": "13800000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != "hpms" {
		t.Fatalf("unexpected source: %#v", result)
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
		if q.Get("metrics") != "" {
			t.Fatalf("undocumented metrics query must not be forwarded: %s", r.URL.RawQuery)
		}
		if q.Get("roomId") != "ROOM-2" || q.Get("roomTypeId") != "" {
			t.Fatalf("tool room type alias was not mapped to documented roomId: %s", r.URL.RawQuery)
		}
		if q.Get("startDate") != "" || q.Get("endDate") != "" {
			t.Fatalf("legacy date names must not be forwarded: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
	}))
	defer server.Close()

	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	if _, err := client.Query(context.Background(), "inventory", map[string]string{
		"startDate":  "2026-09-14",
		"endDate":    "2026-09-15",
		"roomTypeId": "ROOM-2",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClientInventoryRequiresCompleteValidDatesBeforeHTTP(t *testing.T) {
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", HotelID: "hotel-1"})
	for _, args := range []map[string]string{
		{"beginTime": "2026-09-23"},
		{"beginTime": "2026-09-23", "endTime": "2026-09-xx"},
	} {
		_, err := client.Query(context.Background(), "inventory", args)
		if err == nil || !strings.Contains(err.Error(), "入住和离店日期") {
			t.Fatalf("invalid inventory range must be rejected, args=%#v err=%v", args, err)
		}
	}
}

func TestClientDetailQueriesRequireTheirRealIdentifiers(t *testing.T) {
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", HotelID: "hotel-1"})
	for _, tc := range []struct {
		action string
		want   string
	}{
		{action: "reserve_order_detail", want: "预订单 ID"},
		{action: "recept_order_detail", want: "接待单 ID"},
	} {
		_, err := client.Query(context.Background(), tc.action, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s must require its identifier: %v", tc.action, err)
		}
	}
}

func TestClientRenewCandidatesDefaultsPaginationAndRequiresFilter(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RawQuery
		_, _ = w.Write([]byte(`{"code":0,"data":{"total":0,"rows":[]}}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

	for _, args := range []map[string]string{
		{"currentReceptOrderId": "RECEPT-1"},
		{"reserveOrderNo": "RESERVE-2", "pageNum": "2", "pageSize": "30"},
	} {
		if _, err := client.Query(context.Background(), "renew_candidates", args); err != nil {
			t.Fatal(err)
		}
	}
	first := <-requests
	second := <-requests
	if !strings.Contains(first, "pageNum=1") || !strings.Contains(first, "pageSize=20") {
		t.Fatalf("default renew pagination missing: %s", first)
	}
	if !strings.Contains(second, "pageNum=2") || !strings.Contains(second, "pageSize=30") {
		t.Fatalf("explicit renew pagination missing: %s", second)
	}

	for _, args := range []map[string]string{nil, {"pageNum": "1", "pageSize": "20"}} {
		_, err := client.Query(context.Background(), "renew_candidates", args)
		if err == nil || !strings.Contains(err.Error(), "真实预订筛选条件") {
			t.Fatalf("renew candidate filter must be required: %#v err=%v", args, err)
		}
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
