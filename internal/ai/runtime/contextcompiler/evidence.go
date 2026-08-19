package contextcompiler

import (
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func requiredEvidenceTaskKeys(plan *contracts.ReplyPlanV2) []string {
	if plan == nil {
		return nil
	}
	ret := make([]string, 0)
	for _, task := range plan.Tasks {
		if task.Knowledge.Policy == "required" && task.Knowledge.Status == "has_context" {
			ret = appendUnique(ret, task.TaskKey)
		}
	}
	return ret
}

func selectRequiredEvidence(bundle *contracts.EvidenceBundleV1, taskKeys []string) ([]contracts.EvidenceItemV1, bool) {
	if len(taskKeys) == 0 {
		return nil, true
	}
	if bundle == nil {
		return nil, false
	}
	candidates := append([]contracts.EvidenceItemV1(nil), bundle.Items...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].SourceType != candidates[j].SourceType {
			return evidenceSourceRank(candidates[i].SourceType) > evidenceSourceRank(candidates[j].SourceType)
		}
		if candidates[i].Answerability != candidates[j].Answerability {
			return evidenceAnswerabilityRank(candidates[i].Answerability) > evidenceAnswerabilityRank(candidates[j].Answerability)
		}
		return candidates[i].Score > candidates[j].Score
	})
	selected := make([]contracts.EvidenceItemV1, 0)
	covered := make(map[string]bool, len(taskKeys))
	for _, item := range candidates {
		if item.Answerability == "not_supporting" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		use := false
		for _, taskKey := range taskKeys {
			if !covered[taskKey] && containsString(item.TaskKeys, taskKey) {
				covered[taskKey] = true
				use = true
			}
		}
		if use {
			selected = append(selected, item)
		}
	}
	for _, taskKey := range taskKeys {
		if !covered[taskKey] {
			return selected, false
		}
	}
	return selected, true
}

func optionalEvidenceItems(bundle *contracts.EvidenceBundleV1, selected []contracts.EvidenceItemV1) []contracts.EvidenceItemV1 {
	if bundle == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		seen[item.Ref] = struct{}{}
	}
	ret := make([]contracts.EvidenceItemV1, 0, len(bundle.Items))
	for _, item := range bundle.Items {
		if _, exists := seen[item.Ref]; exists || item.Answerability == "not_supporting" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		ret = append(ret, item)
	}
	sort.SliceStable(ret, func(i, j int) bool {
		if ret[i].SourceType != ret[j].SourceType {
			return evidenceSourceRank(ret[i].SourceType) > evidenceSourceRank(ret[j].SourceType)
		}
		if ret[i].Answerability != ret[j].Answerability {
			return evidenceAnswerabilityRank(ret[i].Answerability) > evidenceAnswerabilityRank(ret[j].Answerability)
		}
		return ret[i].Score > ret[j].Score
	})
	return ret
}

func projectEvidence(bundle *contracts.EvidenceBundleV1, items []contracts.EvidenceItemV1) contracts.EvidenceBundleV1 {
	projectedItems := append([]contracts.EvidenceItemV1(nil), items...)
	for index := range projectedItems {
		projectedItems[index].Content = cleanEvidenceContentForModel(projectedItems[index].Content)
	}
	projected := contracts.EvidenceBundleV1{
		SchemaVersion:   contracts.EvidenceBundleV1SchemaVersion,
		RetrievalStatus: "not_needed", Items: projectedItems, Resources: []contracts.EvidenceResourceV1{},
	}
	if bundle == nil {
		return projected
	}
	projected.ScopeFingerprint = bundle.ScopeFingerprint
	projected.RetrievalStatus = bundle.RetrievalStatus
	refs := make(map[string]struct{})
	for _, item := range items {
		for _, ref := range item.ResourceRefs {
			refs[ref] = struct{}{}
		}
	}
	for _, resource := range bundle.Resources {
		if _, ok := refs[resource.Ref]; ok {
			projected.Resources = append(projected.Resources, resource)
		}
	}
	return projected
}

func truncateEvidenceContent(item contracts.EvidenceItemV1, maxRunes int) contracts.EvidenceItemV1 {
	if maxRunes < 1 {
		item.Content = ""
		return item
	}
	runes := []rune(strings.TrimSpace(item.Content))
	if len(runes) <= maxRunes {
		return item
	}
	cut := maxRunes
	for i := maxRunes - 1; i >= maxRunes/2; i-- {
		switch runes[i] {
		case '。', '！', '？', '.', '!', '?', '\n':
			cut = i + 1
			i = -1
		}
	}
	item.Content = strings.TrimSpace(string(runes[:cut]))
	return item
}

func evidenceAnswerabilityRank(value string) int {
	switch value {
	case "supporting":
		return 2
	case "partial":
		return 1
	default:
		return 0
	}
}

func evidenceSourceRank(value string) int {
	switch value {
	case "store_fact":
		return 3
	case "tool_result":
		return 2
	case "fastgpt":
		return 1
	default:
		return 0
	}
}

func cleanEvidenceContentForModel(content string) string {
	content = strings.TrimSpace(strings.NewReplacer("\r", "\n", "\t", " ").Replace(content))
	if index := strings.LastIndex(content, "答案："); index >= 0 {
		return strings.TrimSpace(content[index+len("答案："):])
	}
	if index := strings.LastIndex(content, "答案:"); index >= 0 {
		return strings.TrimSpace(content[index+len("答案:"):])
	}
	return content
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
