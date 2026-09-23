package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/services"
)

func TestPhoneArgForActionDoesNotUseKeywordForPhoneLookup(t *testing.T) {
	for _, action := range []string{"reserve_order_by_phone", "recept_order_by_phone", "member_info_by_phone", "member_benefits_by_phone"} {
		if got := phoneArgForAction(action, "", "ORDER-001"); got != "" {
			t.Fatalf("%s must not use keyword as phone, got %q", action, got)
		}
		if got := phoneArgForAction(action, "13800000000", "ORDER-001"); got != "13800000000" {
			t.Fatalf("%s must keep phone, got %q", action, got)
		}
	}
}

func TestPMSQueryToolOrderLookupAcceptsCustomerNoWithoutPhone(t *testing.T) {
	for _, tc := range []struct {
		action string
		path   string
	}{
		{action: "reserve_order_by_phone", path: "/admin-api/hpms/orderManage/reserveOrder/detailByPhone"},
		{action: "recept_order_by_phone", path: "/admin-api/hpms/orderManage/receptOrder/detailByPhone"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tc.path {
					t.Fatalf("unexpected PMS request: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("phone") != "" || r.URL.Query().Get("customerNo") != "MEMBER-001" {
					t.Fatalf("customer number contract was not preserved: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"code":0,"data":{"customerNo":"MEMBER-001"}}`))
			}))
			defer server.Close()
			usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

			got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"`+tc.action+`","customerNo":"MEMBER-001"}`)
			if err != nil || !strings.Contains(got, `"status":"ok"`) || !strings.Contains(got, `"customerNo":"MEMBER-001"`) {
				t.Fatalf("customerNo order lookup failed: %s %v", got, err)
			}
		})
	}
}

func TestPMSQueryToolMemberLookupStillRequiresPhoneWithCustomerNo(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1"})
	for _, action := range []string{"member_info_by_phone", "member_benefits_by_phone"} {
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"`+action+`","customerNo":"MEMBER-001"}`)
		if err != nil || !strings.Contains(got, `"status":"unavailable"`) || !strings.Contains(got, "有效手机号") {
			t.Fatalf("%s must not accept customerNo instead of phone: %s %v", action, got, err)
		}
	}
}

func TestPMSQueryToolSchemaExposesOnlyReadOnlyActions(t *testing.T) {
	info, err := NewPMSQueryTool().Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	action, ok := schema.Properties.Get("action")
	if !ok || len(action.Enum) != 11 {
		t.Fatalf("unexpected read-only action schema: %#v", action)
	}
	for _, value := range action.Enum {
		if name, ok := value.(string); !ok || !isPMSReadOnlyAction(name) {
			t.Fatalf("schema exposed an unsupported action: %#v", value)
		}
	}
	for _, name := range []string{"phone", "roomTypeId", "metrics", "beginTime", "endTime", "excludeReserveOrderId", "excludeReceptOrderId"} {
		if _, ok := schema.Properties.Get(name); !ok {
			t.Fatalf("member query parameter missing: %s", name)
		}
	}
	for _, name := range []string{"renewPayload", "hotelId", "tenantId", "baseUrl", "authorization", "gradeId", "gradeCode"} {
		if _, ok := schema.Properties.Get(name); ok {
			t.Fatalf("runtime must not expose protected parameter: %s", name)
		}
	}
	for _, instruction := range []string{"member_benefits_by_phone", "工具内部先查会员", "不需要模型提供等级 ID", "gradeAvailable=false", "冻结/挂失", "不代表已升房"} {
		if !strings.Contains(info.Desc, instruction) {
			t.Errorf("member usage boundary missing: %s", instruction)
		}
	}
}

func TestPMSQueryToolPassesReadOnlyInventoryFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin-api/hpms/changeInventory/query" {
			t.Fatalf("unexpected PMS request: %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		for key, want := range map[string]string{
			"beginTime": "2026-09-21",
			"endTime":   "2026-09-23",
			"roomId":    "ROOM-TYPE-1",
		} {
			if got := query.Get(key); got != want {
				t.Errorf("unexpected %s: got %q want %q", key, got, want)
			}
		}
		if query.Get("startDate") != "" || query.Get("endDate") != "" {
			t.Errorf("tool date aliases must be normalized before forwarding: %s", r.URL.RawQuery)
		}
		if query.Get("roomTypeId") != "" || query.Get("metrics") != "" {
			t.Errorf("undocumented inventory parameters must not be forwarded: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"inventory","beginTime":"2026-09-21","endTime":"2026-09-23","roomTypeId":"ROOM-TYPE-1","metrics":"sold,sellable"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"status":"ok"`) {
		t.Fatalf("inventory query failed: %s", got)
	}
}

func TestPMSQueryToolRejectsInventoryWithoutCompleteValidDates(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", HotelID: "hotel-1"})
	for _, payload := range []string{
		`{"action":"inventory","beginTime":"2026-09-23"}`,
		`{"action":"inventory","beginTime":"2026-09-23","endTime":"not-a-date"}`,
	} {
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), payload)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"status":"unavailable"`) || !strings.Contains(got, "入住和离店日期") {
			t.Fatalf("invalid inventory dates must be blocked before HTTP: %s", got)
		}
	}
}

func TestPMSQueryToolStayRoomAvailabilityUsesRealRoomOrders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin-api/hpms/roomDetails/realTimeRoomStatus/select" {
			t.Fatalf("unexpected PMS request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"date":"2026-09-23","list":[{"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
			{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","controlStatus":"0074001","reserveOrderInfoList":[]},
			{"homeId":"H-1502","homeName":"1502","homeStatus":"0017002","controlStatus":"0074001","reserveOrderInfoList":[{"reserveOrderId":"OTHER","checkInTime":"2026-09-24 14:00:00","checkOutTime":"2026-09-26 12:00:00"}]},
			{"homeId":"H-1503","homeName":"1503","homeStatus":"0017004","controlStatus":"0074001","checkInOrderInfoList":[{"reserveOrderId":"CURRENT","receptOrderId":"REC-CURRENT","checkInTime":"2026-09-22 14:00:00","checkOutTime":"2026-09-25 12:00:00"}]}
			]}]}}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{
		"action":"stay_room_availability","roomTypeId":"ROOM-2",
		"beginTime":"2026-09-23","endTime":"2026-09-25",
		"excludeReserveOrderId":"CURRENT","excludeReceptOrderId":"REC-CURRENT"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"status":"ok"`, `"candidateCount":2`, `"homeName":"1501"`, `"homeName":"1503"`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("specific-room availability missing %s: %s", expected, got)
		}
	}
	if strings.Contains(got, `"homeName":"1502"`) || strings.Contains(got, "OTHER") || strings.Contains(got, "REC-CURRENT") {
		t.Fatalf("conflicting or internal order data leaked: %s", got)
	}
}

func TestPMSQueryToolStayRoomAvailabilityKeepsCoverageBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"date":"2026-09-23","list":[{"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
			{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","controlStatus":"0074001"}
			]}]}}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{
		"action":"stay_room_availability","roomTypeId":"ROOM-2",
		"beginTime":"2026-10-22","endTime":"2026-10-25"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"status":"ok"`) || !strings.Contains(got, `"coverageComplete":false`) || !strings.Contains(got, `"status":"partial"`) {
		t.Fatalf("future coverage was overstated: %s", got)
	}
}

func TestPMSQueryToolRenewCandidatesUsesBoundedPaginationAndRealFilter(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RawQuery
		_, _ = w.Write([]byte(`{"code":0,"data":{"total":0,"rows":[]}}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1"})

	for _, payload := range []string{
		`{"action":"renew_candidates","currentReceptOrderId":"RECEPT-1"}`,
		`{"action":"renew_candidates","reservePhone":"138-0013-8000","pageNum":3,"pageSize":500}`,
	} {
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), payload)
		if err != nil || !strings.Contains(got, `"status":"ok"`) {
			t.Fatalf("renew candidate query failed: %s %v", got, err)
		}
	}
	first := <-requests
	second := <-requests
	if !strings.Contains(first, "pageNum=1") || !strings.Contains(first, "pageSize=20") || !strings.Contains(first, "currentReceptOrderId=RECEPT-1") {
		t.Fatalf("default pagination or current order filter missing: %s", first)
	}
	if !strings.Contains(second, "pageNum=3") || !strings.Contains(second, "pageSize=100") || !strings.Contains(second, "reservePhone=13800138000") {
		t.Fatalf("bounded pagination or reserve phone filter missing: %s", second)
	}
}

func TestPMSQueryToolRenewCandidatesRejectsMissingFilterTwice(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", HotelID: "hotel-1"})
	for _, payload := range []string{
		`{"action":"renew_candidates"}`,
		`{"action":"renew_candidates","reservePhone":"not-a-phone"}`,
	} {
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), payload)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"status":"unavailable"`) || !strings.Contains(got, "真实预订筛选条件") {
			t.Fatalf("missing renew candidate filter must be blocked: %s", got)
		}
	}
}

func TestPMSQueryToolPriceDifferenceUsesOnlyReadQueries(t *testing.T) {
	var writeRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeRequests.Add(1)
			t.Fatalf("price assessment must never write to PMS: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/admin-api/hpms/orderManage/receptOrder/detail":
			if r.URL.Query().Get("receptOrderId") != "90071992547409931" {
				t.Fatalf("unexpected order id: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{
				"receptOrderId":"90071992547409931",
				"checkInTime":"2026-09-21T14:00:00+08:00",
				"checkOutTime":"2026-09-23T12:00:00+08:00",
				"reserveProductList":[{"roomName":"标准大床房","productDetailPriceList":[
					{"date":"2026-09-21","consumeAmount":"100.10","consumeAmountType":"ROOM_FEE","currency":"CNY"},
					{"date":"2026-09-22","consumeAmount":"120.20","consumeAmountType":"ROOM_FEE","currency":"CNY"}
				]}]}}`))
		case "/admin-api/hpms/changeInventory/query":
			query := r.URL.Query()
			if query.Get("beginTime") != "2026-09-21" || query.Get("endTime") != "2026-09-23" ||
				query.Get("roomId") != "ROOM-TYPE-2" || query.Get("roomTypeId") != "" || query.Get("metrics") != "" {
				t.Fatalf("unexpected inventory query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":[{
				"productId":"ROOM-TYPE-2",
				"bookings":{
					"2026-09-21":{"available":2,"price":"130.30","consumeAmountType":"ROOM_FEE","currency":"CNY"},
					"2026-09-22":{"available":1,"price":"140.40","consumeAmountType":"ROOM_FEE","currency":"CNY"}
				}
			}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "hotel-1", AllowWrite: false})

	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{
		"action":"price_difference",
		"receptOrderId":"90071992547409931",
		"roomTypeId":"ROOM-TYPE-2"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"status":"ok"`, `"status":"exact"`, `"difference":"50.40"`, `"availability":"available"`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("price assessment missing %s: %s", expected, got)
		}
	}
	if writeRequests.Load() != 0 {
		t.Fatalf("unexpected PMS write requests: %d", writeRequests.Load())
	}
}

func TestPMSQueryToolPriceDifferenceRequiresRealIdentifiers(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", AllowWrite: false})
	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"price_difference","roomTypeId":"guess-room-type"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"status":"unavailable"`) || !strings.Contains(got, "真实订单 ID") {
		t.Fatalf("missing order id must not trigger speculative queries: %s", got)
	}
}

func TestPMSQueryToolRejectsWritesEvenWhenWriteConfigIsEnabled(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, AllowWrite: true})

	for _, action := range []string{"renew", " renew ", "assign_room", "change_room", "late_checkout", "batch_update_price"} {
		payload, _ := json.Marshal(map[string]any{"action": action, "renewPayload": map[string]any{"receptOrderId": 123}})
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), string(payload))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"status":"unsupported"`) || strings.Contains(got, "confirmation_required") {
			t.Fatalf("write operation must be rejected before creating a draft: %s", got)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("read-only runtime performed %d write requests", got)
	}
}

func TestPMSQueryToolBenefitsByPhoneUsesReturnedGradeIDInOneCall(t *testing.T) {
	const gradeID = "90071992547409931"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("tenant-id") != "2" {
			t.Errorf("expected contextual tenant and GET: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Has("tenantId") || r.URL.Query().Has("hotelId") {
			t.Error("member queries must not receive hotel/tenant query parameters")
		}
		switch r.URL.Path {
		case "/admin-api/member/open/members/info/by-phone":
			if r.URL.Query().Get("phone") != "13800138000" {
				t.Error("member lookup must use the explicit phone")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"tenantId":2,"memberGuestId":"MEMBER-001","memberNo":"MG-001","maskedPhone":"138****8000","gradeId":"` + gradeID + `","gradeName":"金卡","gradeAvailable":true,"status":0,"statusName":"启用"}}`))
		case "/admin-api/member/open/members/benefits/by-grade":
			if r.URL.Query().Get("gradeId") != gradeID || r.URL.Query().Has("phone") {
				t.Errorf("benefits must use the returned grade ID: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"tenantId":2,"gradeId":"` + gradeID + `","gradeCode":"` + gradeID + `","gradeName":"金卡","ascOrder":1,"status":0,"statusName":"启用","gradeAvailable":true,"validityText":"长期有效","benefits":[{"benefitType":"MEMBER_PRICE","benefitId":"BENEFIT-001","benefitName":"会员价","label":"会员价：8.5折","contentValue":"8.5","ascOrder":1}]}}`))
		default:
			t.Errorf("unexpected member endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{
		Enabled: true, BaseURL: server.URL, HotelID: "2", Headers: map[string]string{"tenant-id": "2"},
	})

	tool := NewPMSQueryTool()
	infoJSON, err := tool.InvokableRun(context.Background(), `{"action":"member_benefits_by_phone","phone":"13800138000","gradeId":"wrong","gradeCode":"金卡"}`)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Status string `json:"status"`
		Data   struct {
			Member struct {
				GradeID string `json:"gradeId"`
			} `json:"member"`
			Grade struct {
				GradeID string `json:"gradeId"`
			} `json:"grade"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
		t.Fatal(err)
	}
	if info.Status != "ok" || info.Data.Member.GradeID != gradeID || info.Data.Grade.GradeID != gradeID ||
		strings.Contains(infoJSON, "13800138000") || !strings.Contains(infoJSON, "8.5折") {
		t.Fatalf("unexpected normalized member result: %s", infoJSON)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected exactly two reads, got %d", requests.Load())
	}
}

func TestPMSQueryToolRejectsModelSuppliedDirectGradeLookup(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1"})
	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"member_benefits_by_grade","gradeId":"猜测等级"}`)
	if err != nil || !strings.Contains(got, `"status":"unsupported"`) {
		t.Fatalf("direct grade lookup must not be exposed to model calls: %s %v", got, err)
	}
}

func TestPMSQueryToolBenefitsKeepMemberFactsWhenGradeIsMissingOrFails(t *testing.T) {
	for _, tc := range []struct {
		name, gradeID string
		wantRequests  int32
	}{
		{name: "missing grade", wantRequests: 1},
		{name: "grade query failure", gradeID: "GRADE-1", wantRequests: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/admin-api/member/open/members/info/by-phone" {
					_, _ = w.Write([]byte(`{"code":200,"data":{"tenantId":"2","memberGuestId":"MEMBER-001","memberNo":"MG-001","gradeId":"` + tc.gradeID + `","gradeAvailable":false,"status":2,"statusName":"冻结"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"code":512,"msg":"private-data 13800138000"}`))
			}))
			defer server.Close()
			usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL})
			got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"member_benefits_by_phone","phone":"13800138000"}`)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{`"status":"partial"`, `"member":`, `"grade":null`, `"gradeAvailable":false`, `"statusName":"冻结"`, `"status":2`} {
				if !strings.Contains(got, expected) {
					t.Fatalf("partial member facts missing %s: %s", expected, got)
				}
			}
			if strings.Contains(got, "private-data") || strings.Contains(got, "13800138000") {
				t.Fatalf("private error leaked: %s", got)
			}
			if requests.Load() != tc.wantRequests {
				t.Fatalf("unexpected repeated or speculative reads: %d", requests.Load())
			}
		})
	}
}

func TestPMSQueryToolBenefitsStopsWhenMemberLookupFails(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/admin-api/member/open/members/info/by-phone" {
			t.Errorf("must not query a grade without member facts: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":563,"msg":"private-member 13800138000"}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL})
	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"member_benefits_by_phone","phone":"13800138000"}`)
	if err != nil || !strings.Contains(got, `"status":"unavailable"`) || requests.Load() != 1 {
		t.Fatalf("member failure must stop the lookup: %s %v reads=%d", got, err, requests.Load())
	}
	if strings.Contains(got, "private-member") || strings.Contains(got, "13800138000") {
		t.Fatalf("member error leaked sensitive data: %s", got)
	}
}

func TestPMSQueryToolBenefitsShareCallerDeadlineWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/admin-api/member/open/members/info/by-phone" {
			_, _ = w.Write([]byte(`{"code":200,"data":{"tenantId":"2","memberGuestId":"MEMBER-001","memberNo":"MG-001","gradeId":"GRADE-1","gradeAvailable":true}}`))
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, err := NewPMSQueryTool().InvokableRun(ctx, `{"action":"member_benefits_by_phone","phone":"13800138000"}`)
	if err != nil || !strings.Contains(got, `"status":"partial"`) || !strings.Contains(got, `"grade":null`) {
		t.Fatalf("timed-out grade lookup must retain known member facts: %s %v", got, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected two bounded reads without retry, got %d", requests.Load())
	}
}

func TestPMSQueryToolPreservesUnavailableGradeAndFrozenStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"tenantId":2,"memberGuestId":"MEMBER-001","memberNo":"MG-001","gradeId":"GRADE-1","gradeAvailable":false,"status":2,"statusName":"冻结"}}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL})
	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"member_info_by_phone","phone":"13800138000"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"gradeAvailable":false`, `"statusName":"冻结"`, `"status":2`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("member restriction was lost: %s", got)
		}
	}
}

func TestPMSQueryToolDoesNotTreatKeywordAsMemberPhone(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1"})
	for _, action := range []string{"member_info_by_phone", "member_benefits_by_phone"} {
		got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"`+action+`","keyword":"13800138000"}`)
		if err != nil || !strings.Contains(got, `"status":"unavailable"`) || !strings.Contains(got, "手机号") {
			t.Fatalf("missing explicit phone must not invoke a member lookup: %s %v", got, err)
		}
	}
}

func TestPMSQueryToolRoomStatusDoesNotExposeGuestRoster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"date":"2026-09-15","list":[{"roomId":"90071992547409931","roomName":"大床房","homeCount":1,"homeCardList":[{"homeId":"1001","homeName":"101","homeStatusName":"空净","reserveName":"不应返回的住客","phone":"13800138000","receptOrderId":"private-order","guestList":[{"name":"住客乙","idCard":"private-id-card"}]}]}]}}`))
	}))
	defer server.Close()
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: server.URL, HotelID: "2"})
	got, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"room_status"}`)
	if err != nil || !strings.Contains(got, `"status":"ok"`) {
		t.Fatalf("room status query failed: %s %v", got, err)
	}
	for _, expected := range []string{"90071992547409931", "大床房", `"homeName":"101"`, "空净"} {
		if !strings.Contains(got, expected) {
			t.Errorf("customer room fact missing: %s in %s", expected, got)
		}
	}
	for _, forbidden := range []string{"不应返回的住客", "13800138000", "private-order", "guestList", "住客乙", "private-id-card"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("room status leaked guest data: %s", forbidden)
		}
	}
}

func TestPMSQueryToolParseErrorsDoNotEchoSensitiveInput(t *testing.T) {
	_, err := NewPMSQueryTool().InvokableRun(context.Background(), `{"action":"member_info_by_phone","phone":13800138000}`)
	if err == nil || strings.Contains(err.Error(), "13800138000") {
		t.Fatalf("invalid input must return a redacted parse error: %v", err)
	}
}

func TestWxWorkRuntimeAllowlistActuallyMountsReadOnlyPMSTool(t *testing.T) {
	usePMSQueryToolConfig(t, config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", AllowWrite: true})
	agent := services.WxWorkProtocolInstanceService.BuildRuntimeAIAgent(&models.WxWorkProtocolInstance{})
	var allowed []string
	if err := json.Unmarshal([]byte(agent.AllowedGraphTools), &allowed); err != nil {
		t.Fatal(err)
	}
	toolset, err := registry.NewRegistry(NewWeatherTool(), NewPMSQueryTool()).Resolve(registry.Context{
		AIAgent: agent, AllowedToolCodes: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(toolset.StaticTools) != 2 || toolset.StaticToolCodes["pms_query"] != toolx.BuiltinPMSQuery.Code {
		t.Fatalf("PMS was removed by runtime allowlist: %#v", toolset.StaticToolCodes)
	}
	for _, runtimeTool := range toolset.StaticTools {
		if queryTool, ok := runtimeTool.(*PMSQueryTool); ok {
			got, err := queryTool.InvokableRun(context.Background(), `{"action":"renew","renewPayload":{"receptOrderId":123}}`)
			if err != nil || !strings.Contains(got, `"status":"unsupported"`) {
				t.Fatalf("mounted runtime PMS tool must remain read-only: %s %v", got, err)
			}
		}
	}
}

func usePMSQueryToolConfig(t *testing.T, cfg config.PMSConfig) {
	t.Helper()
	previous := config.CurrentOrNil()
	t.Cleanup(func() { config.SetCurrent(previous) })
	config.SetCurrent(&config.Config{PMS: cfg})
}

func TestPhoneArgForActionKeepsKeywordFallbackForOtherQueries(t *testing.T) {
	if got := phoneArgForAction("room_status", "", "13800000000"); got != "13800000000" {
		t.Fatalf("expected keyword fallback for non-phone action, got %q", got)
	}
}

func TestPhoneArgForActionNormalizesSupportedCustomerFormats(t *testing.T) {
	for _, input := range []string{"+86 138 0013 8000", "138-0013-8000"} {
		if got := phoneArgForAction("member_info_by_phone", input, ""); got != "13800138000" {
			t.Fatalf("phone %q was not normalized: %q", input, got)
		}
	}
}
