package executor

import (
	"encoding/json"
	"strings"
)

// normalizeStructuredModelObject removes only known transport wrappers around
// one JSON object. It does not search arbitrary prose for braces, so business
// text cannot be silently promoted into a valid protocol response.
func normalizeStructuredModelObject(raw string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if strings.HasPrefix(trimmed, "{") {
		return trimmed, false
	}
	if strings.HasPrefix(trimmed, "```json") && strings.HasSuffix(trimmed, "```") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "```json"), "```"))
		if strings.HasPrefix(inner, "{") {
			return inner, true
		}
	}
	if strings.HasPrefix(trimmed, "```") && strings.HasSuffix(trimmed, "```") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "```"), "```"))
		if strings.HasPrefix(inner, "{") {
			return inner, true
		}
	}
	if strings.HasPrefix(trimmed, "<think>") {
		if end := strings.Index(trimmed, "</think>"); end >= 0 {
			inner := strings.TrimSpace(trimmed[end+len("</think>"):])
			if strings.HasPrefix(inner, "{") {
				return inner, true
			}
		}
	}
	if strings.HasPrefix(trimmed, `"`) {
		var decoded string
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			decoded = strings.TrimSpace(decoded)
			if strings.HasPrefix(decoded, "{") {
				return decoded, true
			}
		}
	}
	return trimmed, false
}
