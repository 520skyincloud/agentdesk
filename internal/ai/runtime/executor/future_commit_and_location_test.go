package executor

import (
	"testing"
)

func TestFutureCommitPhraseDetectsPromises(t *testing.T) {
	cases := map[string]bool{
		"我帮你查查有没有优惠":    true,
		"我去查一下，等下给你信儿":  true,
		"我帮你记下了":        true,
		"我这边先确认下再回复你":   true,
		"有停车场，地下车库有充电桩": false,
		"退房后在小程序里申请发票":  false,
	}
	for content, want := range cases {
		if got := futureCommitPhrase(content); got != want {
			t.Fatalf("futureCommitPhrase(%q) = %v, want %v", content, got, want)
		}
	}
}

func TestLooksLikeNonHotelLocation(t *testing.T) {
	cases := map[string]bool{
		"菜市场定位呢": true,
		"银行定位发我": true,
		"菜市场的定位": true,
		"酒店定位发我": false,
		"你们店在哪":  false,
		"停车场怎么走": false,
		"定位":     false,
	}
	for text, want := range cases {
		if got := looksLikeNonHotelLocation(text); got != want {
			t.Fatalf("looksLikeNonHotelLocation(%q) = %v, want %v", text, got, want)
		}
	}
}
