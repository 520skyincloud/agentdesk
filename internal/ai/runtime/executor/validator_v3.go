package executor

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/contracts"

	"golang.org/x/text/unicode/norm"
)

// 契约 19：ValidatorV3。Evidence Ref 不再由模型回显校验（19.6），由
// resolveServerOwnedReferences 用服务端并集替代，消除 missing_task_evidence
// 技术失败。Validator 只分类输出和建议恢复阶段，不决定是否转人工。

// ReplyValidationInputV3 是 ValidatorV3 输入。
type ReplyValidationInputV3 struct {
	Output       contracts.ReplyOutputV3
	Plan         contracts.ReplyPlanV4
	Evidence     contracts.EvidenceBundleV2
	Observations []contracts.ObservationV1
	Facts        contracts.RuntimeContextSnapshotV2
	ActionLedger contracts.ActionLedgerV1
	Req          RunInput
}

type validatorV3 struct{}

// NewReplyValidatorV3 返回 V3 校验器。
func NewReplyValidatorV3() *validatorV3 { return &validatorV3{} }

func (v *validatorV3) Validate(input ReplyValidationInputV3) contracts.ValidationResultV3 {
	result := contracts.ValidationResultV3{
		SchemaVersion:   contracts.ValidationResultV3SchemaVersion,
		Status:          "passed",
		NormalizedParts: []contracts.ResolvedPartV3{},
		Checks: contracts.ValidationChecksV3{
			Schema: "passed", GroupCoverage: "passed", TaskCoverage: "passed",
			ServerResolvedRefs: "passed", DuplicateContent: "passed", FactSource: "passed",
			KnowledgeQuality: "passed", ActionClaims: "passed", Safety: "passed", CommitInvariants: "passed",
		},
		Errors: []contracts.ValidationIssueV1{}, Warnings: []contracts.ValidationIssueV1{},
	}
	validateV3Schema(input, &result)
	validateV3GroupCoverage(input, &result)
	validateV3TaskCoverage(input, &result)
	resolveV3ServerOwnedReferences(input, &result)
	validateV3DuplicateContent(input, &result)
	validateV3FactSource(input, &result)
	validateV3KnowledgeQuality(input, &result)
	validateV3ActionClaims(input, &result)
	validateV3Safety(input, &result)
	validateV3CommitInvariants(input, &result)
	classifyV3Recovery(&result)
	normalizeValidationResultV3(&result)
	if _, err := contracts.MarshalValidationResultV3(result); err != nil {
		return validationResultV3ContractFailure(err)
	}
	return result
}

func normalizeValidationResultV3(result *contracts.ValidationResultV3) {
	if result == nil {
		return
	}
	if result.NormalizedParts == nil {
		result.NormalizedParts = []contracts.ResolvedPartV3{}
	}
	if result.Errors == nil {
		result.Errors = []contracts.ValidationIssueV1{}
	}
	if result.Warnings == nil {
		result.Warnings = []contracts.ValidationIssueV1{}
	}
	for index := range result.NormalizedParts {
		part := &result.NormalizedParts[index]
		if part.TaskKeys == nil {
			part.TaskKeys = []string{}
		}
		if part.GroundingEvidenceRefs == nil {
			part.GroundingEvidenceRefs = []string{}
		}
		if part.ResolvedActionRefs == nil {
			part.ResolvedActionRefs = []string{}
		}
	}
}

func validationResultV3ContractFailure(err error) contracts.ValidationResultV3 {
	message := fmt.Sprintf("validation_result.v3 contract failure: %v", err)
	if len(message) > 500 {
		message = message[:500]
	}
	return contracts.ValidationResultV3{
		SchemaVersion:   contracts.ValidationResultV3SchemaVersion,
		Status:          "rejected",
		NormalizedParts: []contracts.ResolvedPartV3{},
		Checks: contracts.ValidationChecksV3{
			Schema: "failed", GroupCoverage: "failed", TaskCoverage: "failed",
			ServerResolvedRefs: "failed", DuplicateContent: "failed", FactSource: "failed",
			KnowledgeQuality: "failed", ActionClaims: "failed", Safety: "failed", CommitInvariants: "failed",
		},
		Errors: []contracts.ValidationIssueV1{{
			Code: "internal_validation_contract_invalid", Path: "$", Message: message,
		}},
		Warnings:      []contracts.ValidationIssueV1{},
		RecoveryStage: "none",
	}
}

func validateV3Schema(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	if input.Output.SchemaVersion != contracts.ReplyOutputV3SchemaVersion {
		result.Checks.Schema = "failed"
		result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "schema_version_mismatch", Path: "schemaVersion"})
		return
	}
	if len(input.Output.Parts) == 0 {
		result.Checks.Schema = "failed"
		result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "empty_output_parts", Path: "parts"})
		return
	}
	if len(input.Output.Parts) > 3 {
		result.Checks.Schema = "failed"
		result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "too_many_parts", Path: "parts"})
	}
	for index, part := range input.Output.Parts {
		if strings.TrimSpace(part.GroupKey) == "" {
			result.Checks.Schema = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "missing_group_key", Path: partPath(index, "groupKey")})
		}
		if strings.TrimSpace(part.Content) == "" {
			result.Checks.Schema = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "empty_content", Path: partPath(index, "content")})
		}
		if len(part.Content) > 2000 {
			result.Checks.Schema = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "content_too_long", Path: partPath(index, "content")})
		}
		if containsV3InternalControlMarker(part.Content) {
			result.Checks.Schema = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "internal_control_marker", Path: partPath(index, "content")})
		}
	}
}

func containsV3InternalControlMarker(content string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(content))
	for _, marker := range []string{"<<NEXT_MESSAGE>>", "<NEXT_MESSAGE>", "[[NEXT_MESSAGE]]", "[SYSTEM_POLICY]", "[RUNTIME_CONTRACT]"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func validateV3GroupCoverage(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	groups := make(map[string]contracts.ReplyPlanGroupV4, len(input.Plan.ReplyGroups))
	for _, group := range input.Plan.ReplyGroups {
		groups[group.GroupKey] = group
	}
	// 契约 12.1：模型漏写/误写 groupKey 不触发整链失败（服务端可推导）。
	// 权威分组由服务端 BuildFinalAnswerGroups 决定；模型回显仅用于组序审计。
	// 按 taskKeys 反查真实组：若 taskKeys 覆盖了组内全部 Task，视为该组已覆盖。
	taskKeyToGroup := make(map[string]string, len(input.Plan.Tasks))
	for _, group := range input.Plan.ReplyGroups {
		for _, taskKey := range group.TaskKeys {
			taskKeyToGroup[taskKey] = group.GroupKey
		}
	}
	seenGroups := make(map[string]int, len(input.Output.Parts))
	for index, part := range input.Output.Parts {
		// 先按模型回显的 groupKey 查；未命中时按 taskKeys 反查真实组。
		groupKey := part.GroupKey
		if _, known := groups[groupKey]; !known {
			mapped := deriveGroupKeyFromTaskKeys(part.TaskKeys, taskKeyToGroup)
			if mapped != "" {
				groupKey = mapped
			} else {
				// 无法映射到任何已知组：只告警不拒绝（可能是模型自由分段）。
				result.Warnings = append(result.Warnings, contracts.ValidationIssueV1{Code: "unknown_group_derived", Path: partPath(index, "groupKey")})
				continue
			}
		}
		if previous, exists := seenGroups[groupKey]; exists {
			_ = previous
			result.Warnings = append(result.Warnings, contracts.ValidationIssueV1{Code: "duplicate_group_part", Path: partPath(index, "groupKey")})
			continue
		}
		seenGroups[groupKey] = index
		group := groups[groupKey]
		if !sameStringSet(part.TaskKeys, group.TaskKeys) {
			// taskKeys 集合不完全一致：告警（分组由服务端权威决定，模型可能少列）。
			result.Warnings = append(result.Warnings, contracts.ValidationIssueV1{Code: "group_task_keys_derived", Path: partPath(index, "taskKeys")})
		}
	}
	for _, group := range input.Plan.ReplyGroups {
		if !group.Required {
			continue
		}
		if _, covered := seenGroups[group.GroupKey]; !covered {
			// 有 required 组未覆盖：若组内 taskKey 出现在任何 part 中则视为已覆盖
			coveredByTask := false
			for _, taskKey := range group.TaskKeys {
				for _, part := range input.Output.Parts {
					if sameStringSet([]string{taskKey}, part.TaskKeys) || containsRune(part.TaskKeys, taskKey) {
						coveredByTask = true
						break
					}
				}
				if coveredByTask {
					break
				}
			}
			if !coveredByTask {
				result.Checks.GroupCoverage = "failed"
				result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "missing_required_group", Path: "replyGroups." + group.GroupKey})
			}
		}
	}
}

// deriveGroupKeyFromTaskKeys 按 taskKeys 反查所属组。
func deriveGroupKeyFromTaskKeys(taskKeys []string, taskKeyToGroup map[string]string) string {
	for _, taskKey := range taskKeys {
		if groupKey, ok := taskKeyToGroup[taskKey]; ok {
			return groupKey
		}
	}
	return ""
}

func containsRune(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func validateV3TaskCoverage(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	covered := map[string]struct{}{}
	for _, part := range input.Output.Parts {
		for _, key := range part.TaskKeys {
			covered[key] = struct{}{}
		}
	}
	for _, task := range input.Plan.Tasks {
		if task.OutputMode == "skip" {
			continue
		}
		if _, ok := covered[task.TaskKey]; !ok {
			result.Checks.TaskCoverage = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "uncovered_task", Path: "tasks." + task.TaskKey})
		}
	}
}

// resolveV3ServerOwnedReferences 实现 19.6：引用由服务端解析。
func resolveV3ServerOwnedReferences(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	for _, part := range input.Output.Parts {
		resolved := ResolveReplyPart(input.Plan, part)
		if fallback, reason, ok := deterministicKnowledgeFallback(input.Plan, resolved.TaskKeys); ok {
			resolved.Content = fallback
			result.Warnings = append(result.Warnings, contracts.ValidationIssueV1{
				Code: reason, Path: "replyGroups." + resolved.GroupKey,
			})
		} else if grounded, ok := deterministicGroundedKnowledgeContent(input.Plan, input.Evidence, resolved.TaskKeys); ok {
			resolved.Content = grounded
		}
		result.NormalizedParts = append(result.NormalizedParts, contracts.ResolvedPartV3{
			GroupKey: resolved.GroupKey, TaskKeys: resolved.TaskKeys, Content: resolved.Content,
			GroundingEvidenceRefs: resolved.GroundingEvidenceRefs, ResolvedActionRefs: resolved.ResolvedActionRefs,
		})
	}
	sequenceByGroup := make(map[string]int, len(input.Plan.ReplyGroups))
	for _, group := range input.Plan.ReplyGroups {
		sequenceByGroup[group.GroupKey] = group.Sequence
	}
	sort.SliceStable(result.NormalizedParts, func(i, j int) bool {
		left, right := sequenceByGroup[result.NormalizedParts[i].GroupKey], sequenceByGroup[result.NormalizedParts[j].GroupKey]
		if left == right {
			return result.NormalizedParts[i].GroupKey < result.NormalizedParts[j].GroupKey
		}
		return left < right
	})
}

func deterministicKnowledgeFallback(plan contracts.ReplyPlanV4, taskKeys []string) (content string, reason string, ok bool) {
	if len(taskKeys) == 0 {
		return "", "", false
	}
	statuses := make([]string, 0, len(taskKeys))
	claimTypes := make([]string, 0, len(taskKeys))
	for _, key := range taskKeys {
		matched := false
		for _, task := range plan.Tasks {
			if task.TaskKey != key {
				continue
			}
			matched = true
			if task.Knowledge.Policy != "required" {
				return "", "", false
			}
			status := strings.TrimSpace(task.Knowledge.Status)
			switch status {
			case "no_context", "unavailable", "unanswerable":
				statuses = append(statuses, status)
				claimTypes = append(claimTypes, strings.TrimSpace(task.ClaimType))
			default:
				return "", "", false
			}
			break
		}
		if !matched {
			return "", "", false
		}
	}
	if stringInSlice("unavailable", statuses) {
		return "这项信息暂时查询失败，请稍后再试。", "server_fallback_knowledge_unavailable", true
	}
	if stringInSlice("unanswerable", statuses) {
		return "当前资料不足，我暂时无法确认这项信息。", "server_fallback_knowledge_unanswerable", true
	}
	if stringInSlice("recommendation", claimTypes) {
		return "当前资料没有写明可推荐的具体地点，我暂时不能可靠推荐。", "server_fallback_knowledge_no_context", true
	}
	return "当前资料没有写明这项信息，我暂时不能确认。", "server_fallback_knowledge_no_context", true
}

// deterministicGroundedKnowledgeContent is the final content boundary for all
// knowledge-required facts, procedures, policies and recommendations. Model
// prose is never allowed to add a second factual source: the customer-visible
// answer is projected only from exact, supporting, answer_text evidence already
// bound to the selected Task by the server.
func deterministicGroundedKnowledgeContent(plan contracts.ReplyPlanV4, evidence contracts.EvidenceBundleV2, taskKeys []string) (string, bool) {
	if len(taskKeys) == 0 {
		return "", false
	}
	taskByKey := make(map[string]contracts.ReplyPlanTaskV4, len(plan.Tasks))
	for _, task := range plan.Tasks {
		taskByKey[task.TaskKey] = task
	}
	ordered := make([]contracts.ReplyPlanTaskV4, 0, len(taskKeys))
	for _, key := range taskKeys {
		task, ok := taskByKey[key]
		if !ok || task.Knowledge.Policy != "required" || task.Knowledge.Status != "has_context" {
			return "", false
		}
		ordered = append(ordered, task)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Sequence == ordered[j].Sequence {
			return ordered[i].TaskKey < ordered[j].TaskKey
		}
		return ordered[i].Sequence < ordered[j].Sequence
	})
	evidenceByRef := make(map[string]contracts.EvidenceItemV2, len(evidence.Items))
	for _, item := range evidence.Items {
		evidenceByRef[item.Ref] = item
	}
	answers := make([]string, 0, len(ordered))
	seenAnswer := make(map[string]struct{}, len(ordered))
	for _, task := range ordered {
		// RequiredFactRefs are authoritative anchors, not a replacement for the
		// task's ordinary knowledge evidence. A delivery-address question can
		// legitimately need both store.address (S*) and a delivery policy (K*).
		// Keep the protected facts first, then append the remaining task-bound
		// evidence without duplicates so the final deterministic projection does
		// not silently drop part of a multi-requirement answer.
		refs := appendUniqueStrings(
			append([]string(nil), task.RequiredFactRefs...),
			task.EvidenceRefs...,
		)
		answer := groundedEvidenceAnswerForTask(task, refs, evidenceByRef)
		if answer == "" {
			return "", false
		}
		normalized := normalizeReplyContent(answer)
		if _, exists := seenAnswer[normalized]; exists {
			continue
		}
		seenAnswer[normalized] = struct{}{}
		answers = append(answers, answer)
	}
	if len(answers) == 0 {
		return "", false
	}
	return boundedGroundedAnswer(strings.Join(answers, "\n"), 1600), true
}

func groundedEvidenceAnswerForTask(task contracts.ReplyPlanTaskV4, refs []string, evidenceByRef map[string]contracts.EvidenceItemV2) string {
	parts := make([]string, 0, 2)
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		item, ok := evidenceByRef[ref]
		if !ok || item.Answerability != "supporting" || item.TopicMatch != "exact" ||
			!stringInSlice("answer_text", item.AllowedUses) || !stringInSlice(task.TaskKey, item.TaskKeys) {
			continue
		}
		content := sanitizeGroundedEvidenceContent(item.Content)
		if content == "" {
			continue
		}
		normalized := normalizeReplyContent(content)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		parts = append(parts, boundedGroundedAnswer(content, 800))
		if len(parts) >= 2 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	answer := strings.Join(parts, "\n")
	if isAddressTextSubIntent(task.SubIntent) && !strings.Contains(answer, "地址") {
		answer = "酒店地址是：" + answer
	}
	return answer
}

func sanitizeGroundedEvidenceContent(content string) string {
	content = strings.TrimSpace(content)
	for _, marker := range []string{"<<NEXT_MESSAGE>>", "<NEXT_MESSAGE>", "[[NEXT_MESSAGE]]", "[SYSTEM_POLICY]", "[RUNTIME_CONTRACT]"} {
		content = strings.ReplaceAll(content, marker, "")
	}
	if index := strings.Index(content, "答案："); index >= 0 && index+len("答案：") < len(content) {
		content = strings.TrimSpace(content[index+len("答案："):])
	}
	return strings.TrimSpace(content)
}

func boundedGroundedAnswer(content string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(content))
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return string(runes)
	}
	cut := maxRunes
	for index := maxRunes - 1; index >= maxRunes/2; index-- {
		switch runes[index] {
		case '。', '！', '？', '.', '!', '?', '\n':
			cut = index + 1
			index = -1
		}
	}
	return strings.TrimSpace(string(runes[:cut]))
}

// validateV3DuplicateContent 实现 19.3 的两层重复检测。
func validateV3DuplicateContent(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	parts := result.NormalizedParts
	intentByTask := map[string]string{}
	for _, task := range input.Plan.Tasks {
		intentByTask[task.TaskKey] = task.Intent + "/" + task.SubIntent
	}
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			a, b := parts[i], parts[j]
			sameGroup := a.GroupKey == b.GroupKey
			normA, normB := normalizeReplyContent(a.Content), normalizeReplyContent(b.Content)
			exact := normA != "" && normA == normB
			if !exact {
				continue
			}
			if sameGroup {
				result.Checks.DuplicateContent = "failed"
				result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "duplicate_content_same_group", Path: a.GroupKey})
				continue
			}
			result.Checks.DuplicateContent = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "retryable_content_error", Path: b.GroupKey})
		}
	}
	// 第二层：高度重复只写 warning，不吞 group。
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			a, b := parts[i], parts[j]
			if a.GroupKey == b.GroupKey {
				continue
			}
			normA, normB := normalizeReplyContent(a.Content), normalizeReplyContent(b.Content)
			if normA == "" || normB == "" {
				continue
			}
			intentA, intentB := intentByTasks(a, intentByTask), intentByTasks(b, intentByTask)
			if intentA == "" || intentA != intentB {
				continue
			}
			if bigramJaccard(normA, normB) >= 0.85 || containmentWithLengthGap(normA, normB) {
				result.Warnings = append(result.Warnings, contracts.ValidationIssueV1{Code: "high_similarity_content", Path: b.GroupKey})
			}
		}
	}
}

// bigramJaccard 计算中文 rune bigram Jaccard 相似度。
func bigramJaccard(a, b string) float64 {
	runesA, runesB := []rune(a), []rune(b)
	if len(runesA) < 2 || len(runesB) < 2 {
		if a == b {
			return 1
		}
		return 0
	}
	setA := bigramSet(runesA)
	setB := bigramSet(runesB)
	intersection := 0
	for gram := range setA {
		if _, ok := setB[gram]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func bigramSet(runes []rune) map[string]struct{} {
	set := make(map[string]struct{}, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		set[string(runes[i:i+2])] = struct{}{}
	}
	return set
}

// containmentWithLengthGap：一个规范化内容完全包含另一个且长度差 < 30%。
func containmentWithLengthGap(a, b string) bool {
	la, lb := len([]rune(a)), len([]rune(b))
	if la == 0 || lb == 0 {
		return false
	}
	longer, shorter := la, lb
	if lb > la {
		longer, shorter = lb, la
	}
	if float64(longer-shorter)/float64(longer) >= 0.30 {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// validateV3FactSource 延续 19.4 的门店身份事实边界：地址断言必须与 S* 权威
// 快照一致；权威缺失时禁止具体地址断言。
func validateV3FactSource(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	for _, issue := range validateV3StoreIdentityAssertions(input) {
		result.Checks.FactSource = "failed"
		result.Errors = append(result.Errors, issue)
	}
	authoritative := ""
	addressFactRefs := make(map[string]struct{})
	for _, fact := range input.Facts.Facts {
		if strings.HasPrefix(fact.Ref, "S") && (strings.Contains(fact.Key, "address") || strings.Contains(fact.Key, "地址")) {
			addressFactRefs[fact.Ref] = struct{}{}
			if authoritative == "" {
				authoritative = strings.TrimSpace(fact.Value)
			}
		}
	}
	addressTask := make(map[string]bool, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		addressTask[task.TaskKey] = isAddressTextSubIntent(task.SubIntent) || refsContainAny(task.RequiredFactRefs, addressFactRefs)
	}
	for index, part := range result.NormalizedParts {
		content := strings.TrimSpace(part.Content)
		if content == "" || !assertedAddressAssertion(content) {
			continue
		}
		isAddressPart := false
		for _, key := range part.TaskKeys {
			if addressTask[key] {
				isAddressPart = true
				break
			}
		}
		if !isAddressPart {
			continue
		}
		if authoritative == "" || !addressMatchesAuthoritative(content, authoritative) {
			result.Checks.FactSource = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{
				Code: "protected_fact_source_violation", Path: partPath(index, "content"),
			})
		}
	}
}

func validateV3StoreIdentityAssertions(input ReplyValidationInputV3) []contracts.ValidationIssueV1 {
	return validateV3StoreIdentityAssertionsAgainstAuthoritative(input, authoritativeStoreNames(input.Req))
}

// validateV3StoreIdentityAssertionsAgainstAuthoritative keeps the protected
// store-identity rule deterministic and independently testable. The caller is
// responsible for supplying names loaded from the authoritative Store record;
// customer text, OCR, ASR, history and knowledge evidence are never accepted as
// additions to this allow-list.
func validateV3StoreIdentityAssertionsAgainstAuthoritative(
	input ReplyValidationInputV3,
	authoritative []string,
) []contracts.ValidationIssueV1 {
	if len(authoritative) == 0 {
		return nil
	}
	protectedFactRefs := make(map[string]struct{})
	for _, fact := range input.Facts.Facts {
		key := strings.ToLower(strings.TrimSpace(fact.Key))
		if strings.HasPrefix(fact.Ref, "S") &&
			(strings.Contains(key, "address") || strings.Contains(key, "store.name") ||
				strings.Contains(key, "brand") || strings.Contains(key, "navigation") ||
				strings.Contains(key, "地址") || strings.Contains(key, "门店名称")) {
			protectedFactRefs[fact.Ref] = struct{}{}
		}
	}
	planByTask := make(map[string]contracts.ReplyPlanTaskV4, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	parts := make([]contracts.ReplyPartV3, 0, len(input.Output.Parts))
	for _, part := range input.Output.Parts {
		protectedScope := explicitStoreIdentityAssertion(part.Content)
		for _, taskKey := range part.TaskKeys {
			task, ok := planByTask[taskKey]
			if !ok {
				continue
			}
			if isAddressTextSubIntent(task.SubIntent) || refsContainAny(task.RequiredFactRefs, protectedFactRefs) {
				protectedScope = true
				break
			}
		}
		if protectedScope {
			parts = append(parts, part)
		}
	}
	return validateStoreNamesAgainstAuthoritative(parts, authoritative)
}

func explicitStoreIdentityAssertion(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	for _, marker := range []string{
		"地址填", "外卖填", "配送填", "填这个", "填那", "填", "店名", "门店名",
		"酒店叫", "公寓叫", "我们是", "本店是", "就是", "住的是", "名称是",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func refsContainAny(refs []string, allowed map[string]struct{}) bool {
	for _, ref := range refs {
		if _, ok := allowed[ref]; ok {
			return true
		}
	}
	return false
}

func validateStoreNamesAgainstAuthoritative(parts []contracts.ReplyPartV3, authoritative []string) []contracts.ValidationIssueV1 {
	if len(authoritative) == 0 {
		return nil
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for index, part := range parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		for _, name := range extractAssertedPlaceNames(content) {
			if placeNameAuthorized(name, authoritative) {
				continue
			}
			issues = append(issues, contracts.ValidationIssueV1{
				Code: "protected_fact_source_violation", Path: partPath(index, "content"),
				Message: "reply asserts an unauthorized store place name",
			})
			break
		}
	}
	return issues
}

// validateV3KnowledgeQuality 实现 19.5 的推荐证据门槛。
func validateV3KnowledgeQuality(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	for index, part := range result.NormalizedParts {
		recommendationTasks := recommendationTaskKeys(part.TaskKeys, input.Plan)
		if len(recommendationTasks) == 0 {
			continue
		}
		allowedEvidence := make([]contracts.EvidenceItemV2, 0)
		for _, item := range input.Evidence.Items {
			if !stringSlicesIntersect(item.TaskKeys, recommendationTasks) {
				continue
			}
			if item.ClaimType == "recommendation" && item.TopicMatch == "exact" &&
				item.Answerability == "supporting" && stringInSlice("recommend", item.AllowedUses) {
				allowedEvidence = append(allowedEvidence, item)
			}
		}
		if len(allowedEvidence) == 0 {
			if !recommendationOutputHasClaims(part.Content) {
				continue
			}
			result.Checks.KnowledgeQuality = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{
				Code: "recommendation_without_allowed_evidence", Path: partPath(index, "content"),
			})
			continue
		}
		if unsupported := unsupportedRecommendationSegments(part.Content, allowedEvidence); len(unsupported) > 0 {
			result.Checks.KnowledgeQuality = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{
				Code: "recommendation_entity_unsupported", Path: partPath(index, "content"),
				Message: strings.Join(unsupported, "、"),
			})
		}
	}
}

func stringSlicesIntersect(a, b []string) bool {
	for _, value := range a {
		if stringInSlice(value, b) {
			return true
		}
	}
	return false
}

func recommendationTaskKeys(taskKeys []string, plan contracts.ReplyPlanV4) []string {
	ret := make([]string, 0, len(taskKeys))
	for _, task := range plan.Tasks {
		if task.ClaimType == "recommendation" && stringInSlice(task.TaskKey, taskKeys) {
			ret = append(ret, task.TaskKey)
		}
	}
	return ret
}

func recommendationOutputHasClaims(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	for _, clause := range strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '。', '！', '!', '？', '?', '；', ';', '\n', '\r':
			return true
		default:
			return false
		}
	}) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		for _, marker := range []string{"可以去", "可以看看", "可以逛", "推荐去", "推荐你", "建议去", "值得去", "不妨去", "比如", "例如"} {
			if strings.Contains(clause, marker) {
				return true
			}
		}
		if recommendationUncertaintyClause(clause) {
			continue
		}
		if strings.Contains(clause, "推荐") || strings.Contains(clause, "、") {
			return true
		}
	}
	return false
}

func recommendationUncertaintyClause(content string) bool {
	for _, marker := range []string{
		"没有写明", "未写明", "没有相关", "暂无", "暂时没有", "没有可靠",
		"无法确认", "不能确定", "不清楚", "资料中没有", "资料里没有", "知识库没有",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func unsupportedRecommendationSegments(content string, evidence []contracts.EvidenceItemV2) []string {
	sourceParts := make([]string, 0, len(evidence)*2)
	for _, item := range evidence {
		sourceParts = append(sourceParts, item.Title, item.Content)
	}
	source := normalizeReplyContent(strings.Join(sourceParts, " "))
	if source == "" {
		return []string{"empty_recommendation_evidence"}
	}
	unsupported := make([]string, 0)
	for _, segment := range splitRecommendationSegments(content) {
		candidate := trimRecommendationFraming(segment)
		if candidate == "" || recommendationConnectorOnly(candidate) {
			continue
		}
		normalized := normalizeReplyContent(candidate)
		if len([]rune(normalized)) < 2 || strings.Contains(source, normalized) {
			continue
		}
		common := longestCommonRuneSubstring(normalized, source)
		if common >= 4 && float64(common)/float64(len([]rune(normalized))) >= 0.65 {
			continue
		}
		unsupported = append(unsupported, candidate)
	}
	return uniqueStringsPreserveOrder(unsupported)
}

func splitRecommendationSegments(content string) []string {
	return strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '、', '，', ',', '。', '；', ';', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func trimRecommendationFraming(value string) string {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{
		"附近可以去", "附近可以看看", "附近可以逛逛", "周边可以去", "周边可以看看",
		"想玩可以去", "想吃可以去", "可以去", "可以看看", "可以逛逛", "推荐去", "推荐",
		"比如", "例如", "还有", "包括",
	} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimSpace(value)
}

func recommendationConnectorOnly(value string) bool {
	value = normalizeReplyContent(value)
	if value == "" {
		return true
	}
	for _, prefix := range []string{"这几个", "这些", "上面这些", "都可以", "都不错", "都挺", "按你的喜好", "当前资料", "知识库", "暂时没有", "暂未", "没有写明"} {
		if strings.HasPrefix(value, normalizeReplyContent(prefix)) {
			return true
		}
	}
	return false
}

func longestCommonRuneSubstring(left, right string) int {
	a, b := []rune(left), []rune(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	previous := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] != b[j-1] {
				continue
			}
			current[j] = previous[j-1] + 1
			if current[j] > best {
				best = current[j]
			}
		}
		previous = current
	}
	return best
}

func uniqueStringsPreserveOrder(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	ret := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

// validateV3ActionClaims：未解析到 Action 引用的组不得声称已办理。
func validateV3ActionClaims(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	for index, part := range result.NormalizedParts {
		if len(part.ResolvedActionRefs) == 0 && claimsCompletedAction(part.Content) {
			result.Checks.ActionClaims = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "uncommitted_action_claim", Path: partPath(index, "content")})
		}
	}
}

func claimsCompletedAction(content string) bool {
	for _, marker := range []string{"已为您办好", "已经办好", "已办理完成", "已经帮你", "已为您安排"} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func validateV3Safety(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	internalIdentifiers := make([]string, 0, len(input.Plan.Tasks)+len(input.Evidence.Items)+len(input.ActionLedger.Actions))
	for _, task := range input.Plan.Tasks {
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(task.TaskKey))
	}
	for _, evidence := range input.Evidence.Items {
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(evidence.Ref))
	}
	for _, action := range input.ActionLedger.Actions {
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(action.ActionKey))
	}
	for index, part := range result.NormalizedParts {
		content := strings.TrimSpace(part.Content)
		lower := strings.ToLower(content)
		for _, term := range []string{"taskkey", "evidenceref", "actionref", "reply_plan", "intent_tasks", "内部标签", "模型提示词"} {
			if strings.Contains(lower, term) {
				result.Checks.Safety = "failed"
				result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "internal_term_exposed", Path: partPath(index, "content")})
				break
			}
		}
		for _, identifier := range internalIdentifiers {
			if identifier != "" && strings.Contains(content, identifier) {
				result.Checks.Safety = "failed"
				result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "internal_identifier_exposed", Path: partPath(index, "content")})
				break
			}
		}
	}
}

func validateV3CommitInvariants(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	seen := map[string]struct{}{}
	for index, part := range result.NormalizedParts {
		key := strings.TrimSpace(part.GroupKey)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			result.Checks.CommitInvariants = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "commit_duplicate_group", Path: partPath(index, "groupKey")})
		}
		seen[key] = struct{}{}
	}
}

// classifyV3Recovery 只分类输出和建议恢复阶段，不决定转人工。
func classifyV3Recovery(result *contracts.ValidationResultV3) {
	if len(result.Errors) == 0 {
		if len(result.Warnings) > 0 && result.Status == "passed" {
			result.Status = "warning"
		}
		result.RecoveryStage = "none"
		return
	}
	for _, issue := range result.Errors {
		if issue.Code == "retryable_content_error" {
			result.Status = "retryable_content_error"
			result.RecoveryStage = "generate"
			return
		}
	}
	for _, issue := range result.Errors {
		switch issue.Code {
		case "missing_group_key", "empty_content", "empty_output_parts", "too_many_parts",
			"duplicate_group_part", "group_task_keys_mismatch", "missing_required_group",
			"unknown_group", "schema_version_mismatch", "internal_control_marker":
			result.Status = "repairable_protocol_error"
			result.RecoveryStage = "generate"
			return
		}
	}
	result.Status = "rejected"
	result.RecoveryStage = "none"
}

func partPath(index int, field string) string {
	return "parts[" + itoa(index) + "]." + field
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func sameStringSet(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	setA := make(map[string]struct{}, len(a))
	for _, item := range a {
		setA[item] = struct{}{}
	}
	for _, item := range b {
		if _, ok := setA[item]; !ok {
			return false
		}
		delete(setA, item)
	}
	return len(setA) == 0
}

func firstTaskKey(part contracts.ResolvedPartV3) string {
	if len(part.TaskKeys) == 0 {
		return ""
	}
	return part.TaskKeys[0]
}

func intentByTasks(part contracts.ResolvedPartV3, intentByTask map[string]string) string {
	intents := make([]string, 0, len(part.TaskKeys))
	for _, key := range part.TaskKeys {
		if intent := intentByTask[key]; intent != "" {
			intents = append(intents, intent)
		}
	}
	sort.Strings(intents)
	return strings.Join(intents, "|")
}

func stringInSlice(value string, items []string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

// normalizeReplyContent 折叠空白与标点后比较。
func normalizeReplyContent(content string) string {
	content = strings.ToLower(norm.NFKC.String(strings.TrimSpace(content)))
	var builder strings.Builder
	for _, r := range content {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
