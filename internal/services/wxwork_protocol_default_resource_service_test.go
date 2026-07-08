package services

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"github.com/mlogclub/simple/sqls"
)

func TestWxWorkDefaultResourceLocationIntentBuckets(t *testing.T) {
	directCases := []string{
		"发个定位给我",
		"门店位置呢大哥",
		"酒店定位发我一个",
		"你倒是把定位发啊",
		"你们酒店在哪里",
		"怎么去你们酒店",
		"到店路线怎么走",
	}
	for _, text := range directCases {
		if !wantsDirectStoreLocation(text) {
			t.Fatalf("expected direct location intent for %q", text)
		}
	}

	weakCases := []string{
		"离我多远",
		"大概路线呢",
	}
	for _, text := range weakCases {
		if wantsDirectStoreLocation(text) {
			t.Fatalf("expected weak location intent not direct for %q", text)
		}
		if !wantsLocationDiscussion(text) {
			t.Fatalf("expected weak location discussion for %q", text)
		}
	}
}

func TestWxWorkDefaultResourceConfirmationIntent(t *testing.T) {
	confirmations := []string{"可以", "发啊", "好", "嗯", "对的", "OK"}
	for _, text := range confirmations {
		if !isPositiveConfirmation(text) {
			t.Fatalf("expected confirmation for %q", text)
		}
	}

	notConfirmations := []string{"可以办理入住吗", "好的那怎么去", "发票怎么开", "可以帮我送水吗"}
	for _, text := range notConfirmations {
		if isPositiveConfirmation(text) {
			t.Fatalf("expected non-confirmation for %q", text)
		}
	}
}

func TestWxWorkDefaultResourceMiniProgramIntent(t *testing.T) {
	if !wantsDefaultMiniProgram("怎么办入住呢") {
		t.Fatal("expected check-in to request default mini program")
	}
	if !wantsDefaultMiniProgram("我想办入住") {
		t.Fatal("expected check-in to request default mini program")
	}
	if !wantsDefaultMiniProgram("小程序发我一下") {
		t.Fatal("expected plain mini program request to request default mini program")
	}
}

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
