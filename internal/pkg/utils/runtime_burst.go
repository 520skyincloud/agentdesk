package utils

import (
	"regexp"
	"strings"
)

const RuntimeCustomerBurstEnvelopeMarker = "[[AGENTDESK_CUSTOMER_BURST_V1]]"

const runtimeCustomerBurstDisplayHeading = "本轮客户连续消息（按时间顺序）："

var runtimeCustomerBurstItemPattern = regexp.MustCompile(`^\s*(?:\d+\s*[.．、]\s*)?\[(?:消息|文字|图片|语音|文件|定位|小程序|表情|视频|动画表情)(?:\d+)?\]\s*`)

func BuildRuntimeCustomerBurstEnvelope(parts []string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return RuntimeCustomerBurstEnvelopeMarker + "\n" +
		"客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题；如果前面是图片、语音、文件，后面的短句通常是在追问它：\n" +
		strings.Join(cleaned, "\n")
}

func IsRuntimeCustomerBurstEnvelope(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.HasPrefix(content, RuntimeCustomerBurstEnvelopeMarker) || strings.HasPrefix(content, runtimeCustomerBurstDisplayHeading) {
		return true
	}
	firstLine := content
	if index := strings.IndexByte(content, '\n'); index >= 0 {
		firstLine = content[:index]
	}
	return strings.Contains(firstLine, "客人刚才连续发了几条消息")
}

func RuntimeCustomerBurstItems(content string) []string {
	content = strings.TrimSpace(content)
	if !IsRuntimeCustomerBurstEnvelope(content) {
		return nil
	}
	lines := strings.Split(content, "\n")
	items := make([]string, 0, len(lines))
	current := ""
	seenBoundary := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == RuntimeCustomerBurstEnvelopeMarker || line == runtimeCustomerBurstDisplayHeading {
			continue
		}
		if !seenBoundary && strings.Contains(line, "客人刚才连续发了几条消息") {
			continue
		}
		if runtimeCustomerBurstItemPattern.MatchString(line) {
			if current != "" {
				items = append(items, current)
			}
			current = line
			seenBoundary = true
			continue
		}
		if current != "" {
			current += "\n" + line
		}
	}
	if current != "" {
		items = append(items, current)
	}
	if len(items) > 0 {
		return items
	}

	// Historical envelopes did not always carry numbered item markers.
	legacy := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == RuntimeCustomerBurstEnvelopeMarker || line == runtimeCustomerBurstDisplayHeading || strings.Contains(line, "客人刚才连续发了几条消息") {
			continue
		}
		legacy = append(legacy, line)
	}
	return legacy
}

func RuntimeCustomerBurstDisplayText(content string) string {
	items := RuntimeCustomerBurstItems(content)
	if len(items) == 0 {
		return strings.TrimSpace(content)
	}
	return runtimeCustomerBurstDisplayHeading + "\n" + strings.Join(items, "\n")
}

func RuntimeCustomerBurstItemText(item string) string {
	item = strings.TrimSpace(item)
	if item == "" {
		return ""
	}
	return strings.TrimSpace(runtimeCustomerBurstItemPattern.ReplaceAllString(item, ""))
}
