package pms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestSearchOrdersFiltersOwnershipDeduplicatesAndPreservesLongIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("keyword") != "13800000000" ||
			r.URL.Query().Get("hotelId") != "hotel-1" || r.URL.Query().Get("tenantId") != "tenant-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if strings.Contains(r.URL.Path, "/receptOrder/") {
			_, _ = w.Write([]byte(`{"code":200,"data":{"total":3,"rows":[
				{"hotelId":"hotel-1","receptOrderId":9007199254740993,"reserveOrderId":11,"reservePhoneList":["13800000000"],"roomName":"星旗","orderStatus":"0015003","internalRemark":"secret"},
				{"hotelId":"hotel-2","receptOrderId":22,"reserveOrderId":12,"reservePhone":"13800000000"},
				{"hotelId":"hotel-1","receptOrderId":33,"reserveOrderId":13,"reservePhone":"13900000000"}
			]}}`))
		} else {
			_, _ = w.Write([]byte(`{"code":200,"data":{"total":2,"rows":[
				{"hotelId":"hotel-1","reserveOrderId":11,"reservePhone":"13800000000"},
				{"hotelId":"hotel-1","reserveOrderId":14,"reservePhone":"13800000000","roomName":"云漫"}
			]}}`))
		}
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1",
		Headers: map[string]string{"tenant-id": "tenant-1"}})
	result, err := client.SearchOrders(context.Background(), "13800000000")
	if err != nil {
		t.Fatal(err)
	}
	rows := result.Data.(map[string]any)["rows"].([]any)
	if len(rows) != 2 || firstString(rows[0].(map[string]any), "receptOrderId") != "9007199254740993" {
		t.Fatalf("lost orders or precision: %#v", rows)
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{"13800000000", "13900000000", "internalRemark", "secret", "hotel-2"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private field leaked: %s", secret)
		}
	}
}

func TestSearchOrdersEmptyAndIncompletePages(t *testing.T) {
	for _, total := range []string{"0", "1"} {
		t.Run(total, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":200,"data":{"total":` + total + `,"rows":[]}}`))
			}))
			defer server.Close()
			client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
			result, err := client.SearchOrders(context.Background(), "13800000000")
			if total == "1" {
				if err == nil {
					t.Fatal("incomplete page must not claim no orders")
				}
			} else if err != nil || len(result.Data.(map[string]any)["rows"].([]any)) != 0 {
				t.Fatalf("empty search failed: %#v %v", result, err)
			}
		})
	}
}

func TestMemberProgramUsesRealCatalogIDsWithoutPhone(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("phone") != "" {
			t.Error("public benefits must only issue GET without personal identity")
		}
		if strings.Contains(r.URL.Path, "/grade/page") {
			if r.URL.Query().Get("tenantId") != "tenant-1" || r.URL.Query().Get("hotelId") != "" {
				t.Error("catalog tenant binding missing or hotel leaked")
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"total":5,"rows":[
				{"gradeId":"ordinary","status":0},{"gradeId":"silver","status":0},
				{"gradeId":"gold","status":0},{"gradeId":"diamond","status":0},
				{"gradeId":"disabled","status":1}]}}`))
			return
		}
		id := r.URL.Query().Get("gradeId")
		calls[id]++
		_, _ = w.Write([]byte(`{"code":200,"data":{"tenantId":1,"gradeCode":"code","ascOrder":1,"status":0,"statusName":"启用","validityText":"永久","gradeId":"` + id + `","gradeName":"` + id +
			`","gradeAvailable":true,"benefits":[{"benefitType":"discount","benefitId":"discount","benefitName":"折扣","ascOrder":1,"label":"8.5折"}],"upgradeRuleSummary":"12房夜且2000成长值"}}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL,
		Headers: map[string]string{"tenant-id": "tenant-1"}})
	result, err := client.MemberProgram(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	grades := result.Data.(map[string]any)["grades"].([]any)
	if len(grades) != 4 || calls["diamond"] != 1 || calls["disabled"] != 0 {
		t.Fatalf("incomplete catalog: %#v %#v", grades, calls)
	}
}

func TestRoomPricesBoundTenantAndMergeWithoutInventingQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/admin-api/hpms/changePrice/query" ||
			q.Get("tenantId") != "tenant-1" || q.Get("hotelId") != "hotel-1" ||
			q.Get("roomId") != "type-1" || q.Get("beginTime") != "2026-09-28" {
			t.Errorf("wrong board request: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":[{"roomId":"type-1","price":null,"bookings":{"2026-09-28":{"price":218},"2026-09-29":{"price":null}}}]}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1",
		Headers: map[string]string{"tenant-id": "tenant-1"}})
	result, err := client.Query(context.Background(), "room_prices", map[string]string{
		"roomTypeId": "type-1", "startDate": "2026-09-28", "endDate": "2026-09-30",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalDay := map[string]any{"available": 2}
	inventory := []any{map[string]any{"roomId": "type-1", "bookings": map[string]any{
		"2026-09-28": originalDay, "2026-09-29": map[string]any{"available": 3},
	}}}
	merged := MergeRoomPrices(inventory, result.Data).([]any)[0].(map[string]any)
	days := merged["bookings"].(map[string]any)
	if firstString(days["2026-09-28"].(map[string]any), "price") != "218" ||
		days["2026-09-29"].(map[string]any)["price"] != nil || originalDay["price"] != nil ||
		merged["currency"] != nil || merged["priceBasis"] != nil {
		t.Fatalf("merged price corrupted facts: %#v", merged)
	}
	if _, err := client.Query(context.Background(), "inventory", map[string]string{
		"hotelId": "other", "beginTime": "2026-09-28", "endTime": "2026-09-29",
	}); err == nil {
		t.Fatal("cross-hotel query allowed")
	}
}
