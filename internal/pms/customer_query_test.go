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

func TestCustomerQueryDataProjectsRoomStatusWithoutGuestRoster(t *testing.T) {
	source := customerQueryFixture(t, `{"date":"2026-09-15","list":[{"roomId":9007199254740993,
		"roomName":"大床房","homeCount":1,"homeCardList":[{"homeId":9007199254740995,
			"homeName":"101","roomName":"大床房","floorName":"1楼","homeStatus":"CLEAN",
			"controlStatus":"AVAILABLE","masterCustomerName":"private-guest",
			"reserveOrderInfoList":[{"reserveOrderId":123,"phone":"13800138000"}],
			"checkInOrderInfoList":[{"name":"private-guest","reservePhone":"13800138000"}],
			"receptCustomerList":[{"name":"private-guest","cardNumber":"private-card"}]}]}]}`)
	projected, err := CustomerQueryData("room_status", source)
	if err != nil {
		t.Fatal(err)
	}
	assertMemberQueryHasNoPrivateData(t, projected)
	raw, _ := json.Marshal(projected)
	for _, wanted := range []string{`9007199254740993`, `9007199254740995`, `"homeName":"101"`, `"floorName":"1楼"`} {
		if !strings.Contains(string(raw), wanted) {
			t.Fatalf("missing room fact %s: %s", wanted, raw)
		}
	}
	for _, forbidden := range []string{"reserveOrderInfoList", "checkInOrderInfoList", "receptCustomerList", "masterCustomerName"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("guest roster field %s leaked: %s", forbidden, raw)
		}
	}
	original, _ := json.Marshal(source)
	if !strings.Contains(string(original), "13800138000") {
		t.Fatal("customer projection must not modify internal data")
	}
}

func TestCustomerQueryDataPreservesOrderAmountsAndInternalLookupData(t *testing.T) {
	payload := `{"receptOrderId":9007199254740993,"reserveOrderId":9007199254740995,
		"roomName":"大床房","homeName":"101","roomFee":12345678901234567890.123456789,
		"waitPayAmount":null,"reservePhone":"13800138000","reserveRemark":"private-note",
		"receptCustomerList":[{"name":"private-name","phone":"13800138000","cardNumber":"private-card"}],
		"reserveProductList":[{"roomName":"大床房","homePrice":null,
			"productDetailPriceList":[{"date":"2026-09-15","consumeAmount":199.9900,"currency":"CNY","privateNote":"private-note"}]}],
		"receptOrderList":[{"receptOrderId":9007199254740997,"homeName":"101","receptCustomerList":[{"phone":"13800138000"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":` + payload + `}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	result, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{"phone": "13800138000"})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := CustomerQueryData(result.Action, result.Data)
	if err != nil {
		t.Fatal(err)
	}
	assertMemberQueryHasNoPrivateData(t, projected)
	raw, _ := json.Marshal(projected)
	for _, wanted := range []string{`9007199254740993`, `9007199254740995`, `9007199254740997`,
		`12345678901234567890.123456789`, `"waitPayAmount":null`, `"homePrice":null`, `199.9900`} {
		if !strings.Contains(string(raw), wanted) {
			t.Fatalf("missing order fact %s: %s", wanted, raw)
		}
	}
	internal := result.Data.(map[string]any)
	if internal["reservePhone"] != "13800138000" || internal["reserveRemark"] != "private-note" {
		t.Fatal("internal operation data must stay available")
	}
}

func TestCustomerQueryDataPreservesReadableOrderStatusNames(t *testing.T) {
	source := customerQueryFixture(t, `{
		"reserveOrderId":9007199254740995,
		"reserveStatus":"0008005",
		"reserveStatusName":"已预订",
		"receptOrderList":[{
			"receptOrderId":9007199254740997,
			"orderStatus":"0015001",
			"orderStatusName":"已入住"
		}]
	}`)
	projected, err := CustomerQueryData("reserve_order_by_phone", source)
	if err != nil {
		t.Fatal(err)
	}
	root := projected.(map[string]any)
	if root["reserveStatusName"] != "已预订" {
		t.Fatalf("readable reserve status was dropped: %#v", root)
	}
	rows := root["receptOrderList"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["orderStatusName"] != "已入住" {
		t.Fatalf("readable reception status was dropped: %#v", rows)
	}
}

func TestCustomerQueryDataPreservesInventoryNullAndDateFacts(t *testing.T) {
	source := customerQueryFixture(t, `[{"productId":9007199254740993,"productName":"大床房","price":null,
		"roomCount":5,"bookings":{"2026-09-15":{"sold":2,"available":3,"occupied":2,"maintenance":0,"oversold":false,
		"guestName":"private-name"},"private-phone-13800138000":{"available":1}}}]`)
	projected, err := CustomerQueryData("inventory", source)
	if err != nil {
		t.Fatal(err)
	}
	assertMemberQueryHasNoPrivateData(t, projected)
	raw, _ := json.Marshal(projected)
	for _, wanted := range []string{`9007199254740993`, `"price":null`, `"2026-09-15"`, `"available":3`} {
		if !strings.Contains(string(raw), wanted) {
			t.Fatalf("missing inventory fact %s: %s", wanted, raw)
		}
	}
}

func TestCustomerQueryDataRejectsMalformedNestedFields(t *testing.T) {
	for _, tc := range []struct{ action, payload string }{
		{"room_status", `{"list":{"private":"data"}}`},
		{"reserve_order_detail", `{"receptOrderList":["private-data"]}`},
		{"inventory", `{"bookings":{}}`},
		{"renew_candidates", `{"rows":[{"allowedHomeHandleTypes":["KEEP_CURRENT_HOME",123]}]}`},
	} {
		_, err := CustomerQueryData(tc.action, customerQueryFixture(t, tc.payload))
		if err == nil {
			t.Fatalf("invalid customer response accepted: %s", tc.payload)
		}
		assertMemberQueryErrorIsSafe(t, err)
	}
}

func TestExistingPMSBusinessErrorsDoNotEchoPrivateData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":500,"msg":"private-error 13800138000 https://private.example/?token=private-token"}`))
	}))
	defer server.Close()
	client := NewClient(config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})
	_, err := client.Query(context.Background(), "recept_order_by_phone", map[string]string{"phone": "13800138000"})
	if err == nil {
		t.Fatal("business error must not become success")
	}
	assertMemberQueryErrorIsSafe(t, err)
}

func customerQueryFixture(t *testing.T, payload string) any {
	t.Helper()
	var result any
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
