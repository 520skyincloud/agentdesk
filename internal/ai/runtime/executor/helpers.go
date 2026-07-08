package executor

import "strings"

func appendIfMissing(items []string, values ...string) []string {
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		exists := false
		for _, existing := range items {
			if strings.TrimSpace(existing) == item {
				exists = true
				break
			}
		}
		if !exists {
			items = append(items, item)
		}
	}
	return items
}

func preview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}
