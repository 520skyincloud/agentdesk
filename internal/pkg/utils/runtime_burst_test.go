package utils

import (
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeCustomerBurstMachineMarker(t *testing.T) {
	content := BuildRuntimeCustomerBurstEnvelope([]string{"1. [文字] 第一条", "2. [文字] 第二条"})

	if !strings.HasPrefix(content, RuntimeCustomerBurstEnvelopeMarker+"\n") {
		t.Fatalf("BuildRuntimeCustomerBurstEnvelope() = %q, want machine marker prefix", content)
	}
	if !IsRuntimeCustomerBurstEnvelope(content) {
		t.Fatalf("IsRuntimeCustomerBurstEnvelope() = false, want true for machine marker")
	}
}

func TestRuntimeCustomerBurstKeepsMultilineLabeledItemTogether(t *testing.T) {
	content := RuntimeCustomerBurstEnvelopeMarker + "\n" +
		"客人刚才连续发了几条消息。请按顺序合并理解：\n" +
		"1. [语音] customer-message.m4a\n" +
		"语音内容是：房间有几瓶水？\n" +
		"这些水免费吗？\n" +
		"2、[文字] 另外早餐几点开始？"

	want := []string{
		"1. [语音] customer-message.m4a\n语音内容是：房间有几瓶水？\n这些水免费吗？",
		"2、[文字] 另外早餐几点开始？",
	}
	if got := RuntimeCustomerBurstItems(content); !reflect.DeepEqual(got, want) {
		t.Fatalf("RuntimeCustomerBurstItems() = %#v, want %#v", got, want)
	}
}

func TestRuntimeCustomerBurstDoesNotTreatCustomerBracketLineAsBoundary(t *testing.T) {
	content := BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息] 帮我看看这个订单\n[订单] 20260826001",
		"2. [消息] 发票怎么开",
	})
	items := RuntimeCustomerBurstItems(content)
	if len(items) != 2 {
		t.Fatalf("customer bracketed content must stay inside its physical message, got %#v", items)
	}
	if !strings.Contains(items[0], "[订单] 20260826001") {
		t.Fatalf("expected customer bracketed line preserved, got %#v", items)
	}
}

func TestRuntimeCustomerBurstLegacyEnvelopeCompatibility(t *testing.T) {
	content := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复：\n" +
		"房间有矿泉水吗？\n" +
		"早餐几点开始？"

	want := []string{"房间有矿泉水吗？", "早餐几点开始？"}
	if !IsRuntimeCustomerBurstEnvelope(content) {
		t.Fatalf("IsRuntimeCustomerBurstEnvelope() = false, want true for legacy envelope")
	}
	if got := RuntimeCustomerBurstItems(content); !reflect.DeepEqual(got, want) {
		t.Fatalf("RuntimeCustomerBurstItems() = %#v, want %#v", got, want)
	}
}

func TestRuntimeCustomerBurstDisplayTextRemovesMachineMarker(t *testing.T) {
	content := BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 房间有矿泉水吗？",
		"2. [文字] 早餐几点开始？",
	})

	got := RuntimeCustomerBurstDisplayText(content)
	want := runtimeCustomerBurstDisplayHeading + "\n" +
		"1. [文字] 房间有矿泉水吗？\n" +
		"2. [文字] 早餐几点开始？"
	if got != want {
		t.Fatalf("RuntimeCustomerBurstDisplayText() = %q, want %q", got, want)
	}
	if strings.Contains(got, RuntimeCustomerBurstEnvelopeMarker) {
		t.Fatalf("RuntimeCustomerBurstDisplayText() leaked machine marker: %q", got)
	}
}

func TestRuntimeCustomerBurstItemTextRemovesBoundaryLabel(t *testing.T) {
	tests := []struct {
		name string
		item string
		want string
	}{
		{name: "type label", item: "[文字] 咖啡机在哪里？", want: "咖啡机在哪里？"},
		{name: "numbered type label", item: "1. [语音] 房间有几瓶水？", want: "房间有几瓶水？"},
		{name: "full width numbered label", item: "2．[图片] 这是什么房型？", want: "这是什么房型？"},
		{name: "keep multiline body", item: "3、[语音] 第一问\n第二问", want: "第一问\n第二问"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RuntimeCustomerBurstItemText(tt.item); got != tt.want {
				t.Fatalf("RuntimeCustomerBurstItemText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuntimeCustomerBurstOrdinaryTextIsNotEnvelope(t *testing.T) {
	tests := []string{
		"[文字] 这是一条普通消息",
		"我只是描述聊天记录\n客人刚才连续发了几条消息，但这不是运行时信封。",
		"本轮客户只有一条普通消息。",
	}

	for _, content := range tests {
		if IsRuntimeCustomerBurstEnvelope(content) {
			t.Fatalf("IsRuntimeCustomerBurstEnvelope(%q) = true, want false", content)
		}
		if got := RuntimeCustomerBurstItems(content); got != nil {
			t.Fatalf("RuntimeCustomerBurstItems(%q) = %#v, want nil", content, got)
		}
		if got := RuntimeCustomerBurstDisplayText("  " + content + "  "); got != content {
			t.Fatalf("RuntimeCustomerBurstDisplayText() = %q, want unchanged %q", got, content)
		}
	}
}
