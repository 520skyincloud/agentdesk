package utils

import "testing"

func TestIsExplicitHumanHandoffRequestAuthorization(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{text: "", want: false},
		{text: "人工", want: true},
		{text: "人工客服", want: true},
		{text: "帮我转人工", want: true},
		{text: "可以帮我转接人工吗", want: true},
		{text: "能不能找人工", want: true},
		{text: "联系前台同事", want: true},
		{text: "不要机器人，帮我转人工", want: true},
		{text: "不需要送毛巾，帮我找同事", want: true},
		{text: "不要人工智能，找真人", want: true},
		{text: "不要转人工，先回答我", want: false},
		{text: "不转人工", want: false},
		{text: "能不能不转人工", want: false},
		{text: "可以不转人工吗", want: false},
		{text: "先不找人工", want: false},
		{text: "不要人工客服和真人客服", want: false},
		{text: "不要人工客服或者真人客服", want: false},
		{text: "不用转接人工", want: false},
		{text: "别帮我转人工", want: false},
		{text: "不要自动转人工", want: false},
		{text: "不要动不动就转人工", want: false},
		{text: "我没让你转人工", want: false},
		{text: "我没有说要转人工", want: false},
		{text: "我不是要找人工", want: false},
		{text: "能不能不要转人工", want: false},
		{text: "取消转人工", want: false},
		{text: "撤销人工介入", want: false},
		{text: "帮我转人工，算了", want: false},
		{text: "找同事，还是不用了", want: false},
		{text: "不要转人工，还是帮我找同事", want: true},
		{text: "转人工，然后取消转人工", want: false},
		{text: "不要转人工但帮我找同事", want: true},
		{text: "不要人工客服但帮我找真人", want: true},
		{text: "帮我转人工但不要找同事", want: false},
		{text: "为什么又转人工了", want: false},
		{text: "你怎么直接转人工", want: false},
		{text: "转人工是什么意思", want: false},
		{text: "你说的人工客服是什么", want: false},
		{text: "“转人工”是什么意思", want: false},
		{text: "'帮我转人工'是客户之前说的", want: false},
		{text: "他说「转人工」，但我不需要", want: false},
		{text: "解释“转人工”，然后帮我找真人", want: true},
		{text: "找人工智能相关的资料", want: false},
		{text: "人工智能能解决吗", want: false},
		{text: "房间空调坏了，帮我处理一下", want: false},
		{text: "这个订单查不到，怎么回事", want: false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := IsExplicitHumanHandoffRequest(tc.text); got != tc.want {
				t.Fatalf("IsExplicitHumanHandoffRequest(%q)=%v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestIsExplicitHumanHandoffRequestBurstUsesLatestAuthorization(t *testing.T) {
	for _, tc := range []struct {
		messages []string
		want     bool
	}{
		{messages: []string{"1. [消息101] 帮我转人工", "2. [消息102] 不要转人工了"}, want: false},
		{messages: []string{"1. [消息101] 不要转人工", "2. [消息102] 还是帮我找同事吧"}, want: true},
	} {
		if got := IsExplicitHumanHandoffRequest(BuildRuntimeCustomerBurstEnvelope(tc.messages)); got != tc.want {
			t.Fatalf("merged callback authorization=%v, want %v for %#v", got, tc.want, tc.messages)
		}
	}
}
