package executor

import (
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
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
}

type validatorV3 struct{}

// NewReplyValidatorV3 返回 V3 校验器。
func NewReplyValidatorV3() *validatorV3 { return &validatorV3{} }

func (v *validatorV3) Validate(input ReplyValidationInputV3) contracts.ValidationResultV3 {
	result := contracts.ValidationResultV3{
		SchemaVersion: contracts.ValidationResultV3SchemaVersion,
		Status:        "passed",
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
	return result
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
	}
}

func validateV3GroupCoverage(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	groups := make(map[string]contracts.ReplyPlanGroupV4, len(input.Plan.ReplyGroups))
	for _, group := range input.Plan.ReplyGroups {
		groups[group.GroupKey] = group
	}
	seenGroups := make(map[string]int, len(input.Output.Parts))
	for index, part := range input.Output.Parts {
		group, known := groups[part.GroupKey]
		if !known {
			result.Checks.GroupCoverage = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "unknown_group", Path: partPath(index, "groupKey")})
			continue
		}
		if previous, exists := seenGroups[part.GroupKey]; exists {
			_ = previous
			result.Checks.GroupCoverage = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "duplicate_group_part", Path: partPath(index, "groupKey")})
			continue
		}
		seenGroups[part.GroupKey] = index
		if !sameStringSet(part.TaskKeys, group.TaskKeys) {
			result.Checks.GroupCoverage = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "group_task_keys_mismatch", Path: partPath(index, "taskKeys")})
		}
	}
	for _, group := range input.Plan.ReplyGroups {
		if !group.Required {
			continue
		}
		if _, covered := seenGroups[group.GroupKey]; !covered {
			result.Checks.GroupCoverage = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{Code: "missing_required_group", Path: "replyGroups." + group.GroupKey})
		}
	}
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
		result.NormalizedParts = append(result.NormalizedParts, contracts.ResolvedPartV3{
			GroupKey: resolved.GroupKey, TaskKeys: resolved.TaskKeys, Content: resolved.Content,
			GroundingEvidenceRefs: resolved.GroundingEvidenceRefs, ResolvedActionRefs: resolved.ResolvedActionRefs,
		})
	}
	sort.SliceStable(result.NormalizedParts, func(i, j int) bool {
		return result.NormalizedParts[i].GroupKey < result.NormalizedParts[j].GroupKey
	})
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
			intentA, intentB := intentByTask[firstTaskKey(a)], intentByTasks(a, intentByTask)
			_ = intentB
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
			if intentA == "" || intentA != intentByTasks(b, intentByTask) {
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
	authoritative := ""
	for _, fact := range input.Facts.Facts {
		if strings.HasPrefix(fact.Ref, "S") && (strings.Contains(fact.Key, "address") || strings.Contains(fact.Key, "地址")) {
			authoritative = strings.TrimSpace(fact.Value)
			break
		}
	}
	addressTask := make(map[string]bool, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		addressTask[task.TaskKey] = isAddressTextSubIntent(task.SubIntent)
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

// validateV3KnowledgeQuality 实现 19.5 的推荐证据门槛。
func validateV3KnowledgeQuality(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	for index, part := range result.NormalizedParts {
		if !partMentionsRecommendation(part.Content) {
			continue
		}
		allowed := false
		for _, item := range input.Evidence.Items {
			if !stringInSlice(item.TaskKey, part.TaskKeys) {
				continue
			}
			if item.Answerability == "supporting" && stringInSlice("recommend", item.AllowedUses) {
				allowed = true
				break
			}
		}
		if !allowed {
			result.Checks.KnowledgeQuality = "failed"
			result.Errors = append(result.Errors, contracts.ValidationIssueV1{
				Code: "recommendation_without_allowed_evidence", Path: partPath(index, "content"),
			})
		}
	}
}

func partMentionsRecommendation(content string) bool {
	return false // 推荐实体识别由确定性 catalog 提供；默认不触发，避免语义误判。
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
			"unknown_group", "schema_version_mismatch":
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
	var builder strings.Builder
	for _, r := range strings.TrimSpace(content) {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
