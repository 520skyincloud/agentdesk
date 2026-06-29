package services

import "testing"

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
	if !wantsCheckInMiniProgram("我想办入住") {
		t.Fatal("expected check-in specific tip intent")
	}
	if wantsCheckInMiniProgram("小程序发我一下") {
		t.Fatal("expected plain mini program request not to be check-in specific")
	}
}
