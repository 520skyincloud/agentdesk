package executor

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

// Opt-in provider smoke test; synthetic conversations only, no PMS writes or
// customer messages. Normal package tests never make network requests.
func TestJevLiveIntentRouting(t *testing.T) {
	if os.Getenv("AGENT_DESK_JEV_LIVE_TEST") != "1" {
		t.Skip("set AGENT_DESK_JEV_LIVE_TEST=1 for real JEV smoke tests")
	}
	if strings.TrimSpace(os.Getenv(jevAPIKeyEnv)) == "" {
		t.Fatal("JEV live test requires a server-side API key")
	}
	t.Setenv(intentDetectProviderEnv, "typesafe_jev")
	pair := func(customer, service string) adapter.HistoryBuildResult {
		return adapter.HistoryBuildResult{RawItems: []models.Message{
			{ID: 1, SenderType: enums.IMSenderTypeCustomer, Content: customer},
			{ID: 2, SenderType: enums.IMSenderTypeAI, Content: service},
		}}
	}
	cases := []struct {
		name    string
		input   string
		history adapter.HistoryBuildResult
		routes  []string
		texts   []string
		human   bool
	}{
		{name: "parking", input: "酒店有没有停车场？", routes: []string{"parking"}},
		{name: "order_missing_phone", input: "我想查订单", routes: []string{"order_query"}},
		{name: "phone_followup", input: "13900000000", history: pair("帮我查订单", "请提供预订手机号。"), routes: []string{"order_query"}},
		{name: "phone_correction", input: "不是这个号码，是13900000001", history: pair("帮我查13900000000的订单", "这个手机号暂未查到订单。"), routes: []string{"order_query"}},
		{name: "new_topic", input: "酒店有停车场吗", history: pair("查我的订单", "请提供手机号。"), routes: []string{"parking"}},
		{name: "upgrade_member", input: "能不能升房，会员可以免费吗", routes: []string{"room_upgrade", "upgrade_eligibility"}, texts: []string{"能不能升房，", "会员可以免费吗"}},
		{name: "explicit_handoff", input: "帮我转人工", routes: []string{"explicit_handoff"}, human: true},
		{name: "no_handoff", input: "不要转人工，帮我查订单", routes: []string{"order_query"}},
		{name: "checkin_resource", input: "把入住小程序发我", routes: []string{"provide_mini_program"}},
		{name: "weather", input: "长沙今天天气怎么样", routes: []string{"weather_query"}},
		{name: "six_questions", input: "酒店有停车场吗？早餐几点？WiFi密码是什么？发票怎么开？帮我查订单。把酒店定位发我。", routes: []string{"parking", "breakfast", "network_wifi", "invoice", "order_query", "provide_location"}, texts: []string{"酒店有停车场吗？", "早餐几点？", "WiFi密码是什么？", "发票怎么开？", "帮我查订单。", "把酒店定位发我。"}},
		{name: "period_before_order_slot", input: "帮我查订单。手机号13900000000，还有停车场吗", routes: []string{"order_query", "parking"}, texts: []string{"帮我查订单。手机号13900000000，", "还有停车场吗"}},
		{name: "unpunctuated", input: "酒店有停车场吗早餐几点WiFi密码是什么", routes: []string{"parking", "breakfast", "network_wifi"}, texts: []string{"酒店有停车场吗", "早餐几点", "WiFi密码是什么"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := RunInput{UserMessage: models.Message{ID: 3, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: tc.input}}
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), runtimeIntentDetectTimeout)
			defer cancel()
			intent, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(ctx, req, tc.history, nil)
			if err != nil {
				t.Fatal(err)
			}
			intent = normalizeModelIntentTrace(intent, req, tc.history, nil)
			var routes []string
			for _, task := range intent.IntentTasks {
				routes = append(routes, task.SubIntent)
				t.Logf("task %s/%s [%s/%s] text=%q refs=%v", task.Intent, task.SubIntent, task.RelationToPrevious, task.ResolutionState, task.Text, task.SourceRefs)
			}
			t.Logf("elapsed=%s tasks=%d human=%v model=%s", time.Since(started).Round(time.Millisecond), len(routes), intent.NeedsHumanRoute, intent.Reason)
			if intent.NeedsHumanRoute != tc.human {
				t.Errorf("human=%v, want %v", intent.NeedsHumanRoute, tc.human)
			}
			pos := 0
			for _, route := range routes {
				if pos < len(tc.routes) && route == tc.routes[pos] {
					pos++
				}
			}
			if pos != len(tc.routes) {
				t.Errorf("routes=%v; expected ordered coverage=%v", routes, tc.routes)
			}
			if tc.name != "no_handoff" && len(routes) != len(tc.routes) {
				t.Errorf("task count=%d, want exactly %d", len(routes), len(tc.routes))
			}
			if tc.name == "no_handoff" && len(routes) > 2 {
				t.Errorf("denial and order lookup must not be fragmented: %v", routes)
			}
			if len(tc.texts) > 0 {
				var texts []string
				for _, task := range intent.IntentTasks {
					texts = append(texts, task.Text)
				}
				if !reflect.DeepEqual(texts, tc.texts) {
					t.Errorf("task texts=%q, want exact original spans=%q", texts, tc.texts)
				}
			}
		})
	}
}
