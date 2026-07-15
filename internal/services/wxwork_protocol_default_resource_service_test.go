package services

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"github.com/mlogclub/simple/sqls"
)

func TestWxWorkDefaultResourceServiceTaskDoesNotEatRuntimeQuestions(t *testing.T) {
	runtimeCases := []string{
		"你们酒店有几双拖鞋",
		"你们酒店有停车场没",
		"停车场要钱吗",
		"有充电枪吗",
		"拖鞋在哪里拿",
	}
	for _, text := range runtimeCases {
		if wantsServiceTask(text) {
			t.Fatalf("expected reply runtime to handle %q instead of default service task", text)
		}
	}

	serviceCases := []string{
		"能不能送两瓶水到房间",
		"给我补一双拖鞋",
		"房间没纸巾了要纸巾",
		"空调坏了",
		"帮我拿行李",
	}
	for _, text := range serviceCases {
		if !wantsServiceTask(text) {
			t.Fatalf("expected service task for %q", text)
		}
	}
}

func TestWxWorkDefaultResourcePendingServiceTaskDoesNotHijackNewTopic(t *testing.T) {
	draft := serviceTaskDraft{Kind: "送拖鞋", RawText: "给我补一双拖鞋"}
	newTopics := []string{"你们酒店有停车场没", "停车场要钱吗", "有充电枪吗", "你这不是胡闹呢"}
	for _, text := range newTopics {
		if isLikelyServiceTaskContinuation(text, draft) {
			t.Fatalf("expected new topic not to continue pending task for %q", text)
		}
	}
	if !isLikelyServiceTaskContinuation("再补一双拖鞋", draft) {
		t.Fatal("expected same service item continuation")
	}
}

func TestAppendMiniProgramQueryKeepsExistingParams(t *testing.T) {
	got := appendMiniProgramQuery("pages/order/index?scene=abc", map[string]string{
		"storeId":   "123",
		"storeCode": "HFNQ",
		"storeName": "丽斯未来酒店（合肥南七店）",
	})
	if !strings.HasPrefix(got, "pages/order/index?") {
		t.Fatalf("unexpected path prefix: %s", got)
	}
	for _, want := range []string{"scene=abc", "storeId=123", "storeCode=HFNQ", "storeName="} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %s", want, got)
		}
	}
}

func TestInjectMiniProgramStoreParamsUsesInstanceStoreInfo(t *testing.T) {
	sqls.SetDB(nil)
	body := map[string]any{"page_path": "pages/index/index", "title": "安心宿"}
	injectMiniProgramStoreParams(body, &models.WxWorkProtocolInstance{StoreID: 88})
	pagePath := body["page_path"].(string)
	if !strings.Contains(pagePath, "storeId=88") {
		t.Fatalf("expected store params in page_path, got %s", pagePath)
	}
}

func TestInjectMiniProgramStoreParamsPrefersConfiguredScene(t *testing.T) {
	body := map[string]any{
		"page_path":   "pages/home/home.html",
		"store_scene": "hotel=hf-nanqi&channel=desk",
		"store_query_params": map[string]any{
			"hotelId": "HFNQ001",
		},
	}
	injectMiniProgramStoreParams(body, &models.WxWorkProtocolInstance{StoreID: 88})
	pagePath := body["page_path"].(string)
	if !strings.Contains(pagePath, "scene=hotel%3Dhf-nanqi%26channel%3Ddesk") {
		t.Fatalf("expected configured scene in page_path, got %s", pagePath)
	}
	if !strings.Contains(pagePath, "hotelId=HFNQ001") {
		t.Fatalf("expected configured store query params in page_path, got %s", pagePath)
	}
	if strings.Contains(pagePath, "storeId=88") {
		t.Fatalf("did not expect fallback storeId when configured params exist, got %s", pagePath)
	}
}

func TestDeleteMiniProgramInternalStoreKeys(t *testing.T) {
	body := map[string]any{"store_scene": "abc", "storeQueryParams": map[string]any{"hotelId": "1"}, "page_path": "pages/home/home.html"}
	deleteMiniProgramInternalStoreKeys(body)
	if _, ok := body["store_scene"]; ok {
		t.Fatal("expected store_scene to be stripped before protocol send")
	}
	if _, ok := body["storeQueryParams"]; ok {
		t.Fatal("expected storeQueryParams to be stripped before protocol send")
	}
}

func TestBuildDefaultLocationMessageUsesStructuredPayload(t *testing.T) {
	content, payload, err := WxWorkProtocolDefaultResourceService.BuildDefaultLocationMessage(&models.WxWorkProtocolInstance{
		StoreLongitude:      "117.263908",
		StoreLatitude:       "31.824097",
		StoreNavigationName: "丽斯未来酒店",
		StoreAddress:        "九珑湾停车场入口",
	})
	if err != nil {
		t.Fatalf("build location payload: %v", err)
	}
	if content != "丽斯未来酒店" {
		t.Fatalf("unexpected content: %q", content)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("payload is not json: %v", err)
	}
	if body["title"] != "丽斯未来酒店" {
		t.Fatalf("unexpected title: %#v", body["title"])
	}
	if body["address"] != "九珑湾停车场入口" {
		t.Fatalf("unexpected address: %#v", body["address"])
	}
	if body["longitude"].(float64) != 117.263908 {
		t.Fatalf("unexpected longitude: %#v", body["longitude"])
	}
	if body["latitude"].(float64) != 31.824097 {
		t.Fatalf("unexpected latitude: %#v", body["latitude"])
	}
}

func TestBuildDefaultPhoneMessageUsesExplicitStoreContactPhone(t *testing.T) {
	content, payload, err := WxWorkProtocolDefaultResourceService.BuildDefaultPhoneMessage(&models.WxWorkProtocolInstance{
		StoreContactPhone: " 0551-88886666 ",
		Remark:            "备注里不要再猜电话 19900001111",
		StoreAddress:      "地址里也可能有 400-000-0000",
	})
	if err != nil {
		t.Fatalf("build phone message: %v", err)
	}
	if content != "酒店电话：0551-88886666" {
		t.Fatalf("unexpected phone content: %q", content)
	}
	if payload != "" {
		t.Fatalf("expected empty phone payload, got %q", payload)
	}
}

func TestBuildDefaultPhoneMessageRequiresExplicitStoreContactPhone(t *testing.T) {
	_, _, err := WxWorkProtocolDefaultResourceService.BuildDefaultPhoneMessage(&models.WxWorkProtocolInstance{
		Remark:       "备注里有 19900001111 也不能猜",
		StoreAddress: "地址里有 400-000-0000 也不能猜",
	})
	if err == nil || !strings.Contains(err.Error(), "未配置联系电话") {
		t.Fatalf("expected missing explicit phone error, got %v", err)
	}
}

func TestBuildDefaultMiniProgramMessageStripsProtocolEchoFields(t *testing.T) {
	sqls.SetDB(nil)
	content, payload, err := WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(&models.WxWorkProtocolInstance{
		StoreID: 88,
		DefaultMiniProgramPayload: `{
			"title":"e秒安心住",
			"appname":"自由家安心宿",
			"page_path":"pages/order/index",
			"conversation_id":"S:old",
			"protocol_msg_id":"old_msg",
			"send_result":"ok",
			"store_query_params":{"hotelId":"HFNQ001"}
		}`,
	})
	if err != nil {
		t.Fatalf("build mini program payload: %v", err)
	}
	if content != "e秒安心住" {
		t.Fatalf("unexpected content: %q", content)
	}
	if strings.Contains(payload, "conversation_id") || strings.Contains(payload, "protocol_msg_id") || strings.Contains(payload, "send_result") {
		t.Fatalf("expected protocol echo fields stripped, got %s", payload)
	}
	if strings.Contains(payload, "store_query_params") {
		t.Fatalf("expected internal store params stripped, got %s", payload)
	}
	if !strings.Contains(payload, "hotelId=HFNQ001") {
		t.Fatalf("expected configured hotelId in page path, got %s", payload)
	}
}
