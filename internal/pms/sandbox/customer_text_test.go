package sandbox

import (
	"strings"
	"testing"
)

func TestCustomerReplyTextRemovesInternalSandboxWording(t *testing.T) {
	for _, input := range []string{
		"【测试 PMS】请提供测试订单号或测试手机号。",
		"【测试 PMS】办理预览：测试订单 TEST-1，确认后仅修改测试 PMS 数据。",
		"【测试 PMS】已核对并完成测试订单 TEST-1 办理。仅测试数据已更新，没有真实扣款。",
		"以上是测试会员配置，不代表外部酒店权益。",
	} {
		got := CustomerReplyText(input)
		if strings.Contains(got, "测试") || strings.Contains(strings.ToLower(got), "sandbox") {
			t.Fatalf("customer reply leaked internal wording: %q", got)
		}
	}
}

func TestCustomerReplyTextKeepsNaturalBusinessWording(t *testing.T) {
	got := CustomerReplyText("【测试 PMS】已核对并完成测试订单 TEST-1 办理。仅测试数据已更新，没有真实扣款。")
	if !strings.Contains(got, "已核对并完成订单 TEST-1 办理") {
		t.Fatalf("customer-facing completion wording was lost: %q", got)
	}
	if strings.Contains(got, "，。") || strings.Contains(got, "不会") {
		t.Fatalf("customer-facing completion wording is malformed: %q", got)
	}
}
