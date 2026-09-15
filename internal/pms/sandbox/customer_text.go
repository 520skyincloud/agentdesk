package sandbox

import "strings"

// CustomerReplyText removes sandbox-only labels from text that is sent to a
// customer. Provider and dataset details remain available in internal traces.
func CustomerReplyText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, replacement := range []struct {
		old string
		new string
	}{
		{"【测试 PMS】已核对并完成测试订单", "已核对并完成订单"},
		{"确认后仅修改测试 PMS 数据", "确认后会更新订单"},
		{"仅修改测试 PMS 数据", "更新订单"},
		{"以上是测试会员配置，不代表外部酒店权益。", "以上为当前会员权益信息。"},
		{"以上是测试会员配置，不代表外部酒店权益", "以上为当前会员权益信息"},
		{"仅测试数据已更新，没有真实扣款。", ""},
		{"仅测试数据已更新，没有真实扣款", ""},
		{"仅测试数据已更新，", ""},
		{"不会真实扣款。", ""},
		{"不会真实扣款", ""},
		{"仅为测试配置", ""},
		{"测试配置：", ""},
		{"测试配置", ""},
		{"【测试 PMS】", ""},
		{"【测试PMS】", ""},
		{"测试 PMS", ""},
		{"测试PMS", ""},
		{"测试订单", "订单"},
		{"当前测试订单", "当前订单"},
		{"测试订单号", "订单号"},
		{"测试手机号", "手机号"},
		{"测试会员", "会员"},
		{"测试商品资源", "商品资源"},
		{"测试商品", "商品"},
		{"测试房型", "房型"},
		{"测试净房", "净房"},
		{"测试房间", "房间"},
		{"测试门店", "门店"},
		{"测试办理", "办理"},
		{"测试方案", "方案"},
		{"测试数据", "数据"},
		{"不代表外部酒店权益。", ""},
		{"不代表已实际付款。", ""},
		{"测试", ""},
	} {
		text = strings.ReplaceAll(text, replacement.old, replacement.new)
	}
	text = strings.ReplaceAll(text, "  ", " ")
	text = strings.ReplaceAll(text, "在 后台", "在后台")
	text = strings.ReplaceAll(text, "到 后台", "到后台")
	text = strings.ReplaceAll(text, "，。", "。")
	text = strings.ReplaceAll(text, "；。", "。")
	text = strings.ReplaceAll(text, "；；", "；")
	text = strings.ReplaceAll(text, "。。", "。")
	text = strings.TrimSpace(strings.Trim(text, "；; "))
	return text
}
