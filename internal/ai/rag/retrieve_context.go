package rag

import (
	"context"
	"fmt"
	"strings"
)

func (s *retrieve) SelectContextResults(results []RetrieveResult, maxTokens int) []RetrieveResult {
	if len(results) == 0 {
		return nil
	}

	normalizedResults := normalizeContextResults(results)
	selected := make([]RetrieveResult, 0, len(normalizedResults))
	totalTokens := 0
	documentUsage := make(map[string]int)

	for _, item := range normalizedResults {
		usageKey := buildContextUsageKey(item)
		if documentUsage[usageKey] >= 2 {
			continue
		}
		chunkText := buildContextChunkText(item)
		estimatedTokens := len(chunkText) / 2
		if totalTokens+estimatedTokens > maxTokens {
			break
		}
		selected = append(selected, item)
		totalTokens += estimatedTokens
		documentUsage[usageKey]++
	}
	return selected
}

func (s *retrieve) BuildContext(_ context.Context, results []RetrieveResult, maxTokens int) string {
	if len(results) == 0 {
		return ""
	}

	normalizedResults := s.SelectContextResults(results, maxTokens)
	var builder strings.Builder
	for _, r := range normalizedResults {
		builder.WriteString(buildContextChunkText(r))
	}

	return builder.String()
}

func normalizeContextResults(results []RetrieveResult) []RetrieveResult {
	if len(results) == 0 {
		return nil
	}
	return dedupeSectionResults(results)
}

func dedupeSectionResults(results []RetrieveResult) []RetrieveResult {
	seen := make(map[string]struct{})
	deduped := make([]RetrieveResult, 0, len(results))
	for _, item := range results {
		key := buildSectionKey(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func buildSectionKey(item RetrieveResult) string {
	return "source:" + strings.TrimSpace(item.SourceRecordID)
}

func buildContextUsageKey(item RetrieveResult) string {
	return "source:" + strings.TrimSpace(item.SourceRecordID)
}

func buildContextChunkText(item RetrieveResult) string {
	title := strings.TrimSpace(item.DocumentTitle)
	if title == "" {
		title = strings.TrimSpace(item.Title)
	}
	if title == "" {
		title = fmt.Sprintf("FastGPT知识条目#%s", strings.TrimSpace(item.SourceRecordID))
	}
	if item.SectionPath != "" {
		return fmt.Sprintf("【文档：%s｜章节：%s】\n%s\n\n", title, item.SectionPath, item.Content)
	}
	if item.Title != "" {
		return fmt.Sprintf("【文档：%s｜标题：%s】\n%s\n\n", title, item.Title, item.Content)
	}
	return fmt.Sprintf("【文档：%s】\n%s\n\n", title, item.Content)
}
