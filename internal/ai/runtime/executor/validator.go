package executor

import (
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

type ReplyValidationInput struct {
	Output contracts.ReplyOutputV2
	// Req 供受保护事实校验解析权威门店身份；零值时跳过名称门禁。
	Req          RunInput
	Plan         contracts.ReplyPlanV2
	Evidence     contracts.EvidenceBundleV1
	ActionLedger contracts.ActionLedgerV1
	// ServerValidatedTaskBindings is set only after every logical reply part has
	// already been validated separately and the server merely packs those parts
	// to the outbound message limit. The packed content must keep its TaskKeys
	// without re-inferring ownership from customer-visible wording.
	ServerValidatedTaskBindings bool
	// Gates 是 P9 灰度开关快照（构造时按 RunInput 计算）；零值默认全开，
	// 保证纯函数测试与未接线的调用方保持门禁开启的安全默认。
	Gates ReplyValidationGates
}

// ReplyValidationGates 是校验期门禁开关快照。
type ReplyValidationGates struct {
	FactSourceBoundary bool
	UnsupportedDomain  bool
}

// DefaultReplyValidationGates 返回全开的默认门禁（安全默认）。
func DefaultReplyValidationGates() ReplyValidationGates {
	return ReplyValidationGates{FactSourceBoundary: true, UnsupportedDomain: true}
}

type ReplyValidator interface {
	Validate(input ReplyValidationInput) contracts.ValidationResultV1
}

type deterministicReplyValidator struct {
	full bool
}

func NewReplyValidator() ReplyValidator {
	return deterministicReplyValidator{full: true}
}

func NewReplyValidatorForMode(mode string) ReplyValidator {
	return deterministicReplyValidator{full: strings.TrimSpace(mode) == runtimeValidatorV2}
}

func (v deterministicReplyValidator) Validate(input ReplyValidationInput) contracts.ValidationResultV1 {
	result := contracts.ValidationResultV1{
		SchemaVersion: contracts.ValidationResultV1SchemaVersion,
		Status:        "passed", NormalizedParts: normalizeReplyParts(input.Output.Parts, &input.Plan),
		Checks: contracts.ValidationChecksV1{
			Schema: "passed", TaskCoverage: "passed", EvidenceReferences: "passed",
			FactGrounding: "passed", ActionReferences: "passed", Safety: "passed", CommitInvariants: "passed",
		},
		Errors: []contracts.ValidationIssueV1{}, Warnings: []contracts.ValidationIssueV1{},
	}
	input.Output.Parts = result.NormalizedParts
	gates := input.Gates
	if !gates.FactSourceBoundary && !gates.UnsupportedDomain {
		gates = DefaultReplyValidationGates() // 零值视为未接线，安全默认全开
	}
	coverageErrors, repairable := validateReplyTaskCoverage(input)
	if len(coverageErrors) > 0 {
		result.Checks.TaskCoverage = "failed"
		result.Errors = append(result.Errors, coverageErrors...)
		if repairable {
			result.Status = "repairable_protocol_error"
		} else {
			result.Status = "rejected"
		}
	}
	if issues := validateReplyEvidenceReferences(input); len(issues) > 0 {
		result.Checks.EvidenceReferences = "failed"
		result.Errors = append(result.Errors, issues...)
		result.Status = "rejected"
	}
	if issues := validateReplyFactGrounding(input); len(issues) > 0 {
		result.Checks.FactGrounding = "failed"
		result.Errors = append(result.Errors, issues...)
		if result.Status != "rejected" {
			result.Status = "repairable_protocol_error"
		}
	}
	if issues := validateNoHitKnownScopeClarification(input); len(issues) > 0 {
		result.Checks.FactGrounding = "failed"
		result.Errors = append(result.Errors, issues...)
		if result.Status != "rejected" {
			result.Status = "repairable_protocol_error"
		}
	}
	if issues := validateReplyMediaObservationUse(input); len(issues) > 0 {
		result.Checks.FactGrounding = "failed"
		result.Errors = append(result.Errors, issues...)
		if result.Status != "rejected" {
			result.Status = "repairable_protocol_error"
		}
	}
	// 领域硬约束（房态/会员）：系统无数据源，任何断言都是编造，一票否决，不修复。
	if gates.UnsupportedDomain {
		if issues := validateReplyUnsupportedDomain(input); len(issues) > 0 {
			result.Checks.FactGrounding = "failed"
			result.Errors = append(result.Errors, issues...)
			result.Status = "rejected"
		}
	}
	// FactSourceBoundary Phase1（文档 15.2）：地址类任务的地址断言必须与权威门店地址一致，
	// 客户 OCR/历史里的地址（壹间公寓）一律 rejected。业务事实错误不可协议修复。
	if gates.FactSourceBoundary {
		if issues := validateReplyFactSourceBoundary(input); len(issues) > 0 {
			result.Checks.FactGrounding = "failed"
			result.Errors = append(result.Errors, issues...)
			if replyEvidenceHasAuthoritativeStoreFact(input.Evidence) && result.Status != "rejected" {
				result.Status = "repairable_protocol_error"
			} else {
				result.Status = "rejected"
			}
		}
	}
	if issues := validateReplyActionReferences(input); len(issues) > 0 {
		result.Checks.ActionReferences = "failed"
		result.Errors = append(result.Errors, issues...)
		result.Status = "rejected"
	}
	if v.full {
		if issues := validateReplyFutureCommitClaims(input); len(issues) > 0 {
			result.Checks.Safety = "failed"
			result.Errors = append(result.Errors, issues...)
			result.Status = "rejected"
		}
		if issues := validateReplySafety(input); len(issues) > 0 {
			result.Checks.Safety = "failed"
			result.Errors = append(result.Errors, issues...)
			result.Status = "rejected"
		}
		if issues := validateReplyCommitInvariants(input); len(issues) > 0 {
			result.Checks.CommitInvariants = "failed"
			result.Errors = append(result.Errors, issues...)
			if result.Status != "rejected" && repairableReplyCommitInvariantIssues(issues) {
				result.Status = "repairable_protocol_error"
			} else {
				result.Status = "rejected"
			}
		}
	}
	return result
}

func validateNoHitKnownScopeClarification(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	if input.Req.Conversation.StoreID <= 0 {
		return nil
	}
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for partIndex, part := range input.Output.Parts {
		compact := compactReplyText(part.Content)
		if !containsAny(compact, []string{"哪家店", "哪个店", "哪家酒店", "哪个酒店", "哪个门店", "哪一个门店", "订的是哪家", "预订的是哪家"}) {
			continue
		}
		for _, taskKey := range part.TaskKeys {
			task, ok := planByTask[taskKey]
			if !ok || (task.Knowledge.Status != "no_context" && task.Knowledge.Status != "unanswerable" && task.Knowledge.Status != "unavailable") {
				continue
			}
			issues = append(issues, validationIssue(
				"known_scope_reasked",
				fmt.Sprintf("$.parts[%d].content", partIndex),
				"reply asks for store scope that is already fixed by the conversation",
			))
			break
		}
	}
	return issues
}

func replyEvidenceHasAuthoritativeStoreFact(evidence contracts.EvidenceBundleV1) bool {
	for _, item := range evidence.Items {
		if item.SourceType == "store_fact" && strings.TrimSpace(item.Content) != "" {
			return true
		}
	}
	return false
}

func normalizeReplyParts(parts []contracts.ReplyPartV2, plan *contracts.ReplyPlanV2) []contracts.ReplyPartV2 {
	ret := make([]contracts.ReplyPartV2, 0, len(parts))
	for _, part := range parts {
		part.Content = strings.TrimSpace(part.Content)
		part.TaskKeys = uniqueTrimmedStrings(part.TaskKeys)
		part.EvidenceRefs = uniqueTrimmedStrings(part.EvidenceRefs)
		part.ActionRefs = uniqueTrimmedStrings(part.ActionRefs)
		// 契约 12.1/13.2 deterministic_autofix：Evidence/Action 引用由服务端
		// 按计划派生。模型漏回显不得触发 rejected 与整链重试（生产
		// missing_task_evidence 根因）；模型多回显的未知引用仍在
		// evidence_reference_validator 中拒绝。
		if isRuntimeTaskFailureNotice(part.Content) {
			part.EvidenceRefs = nil
			part.ActionRefs = nil
		} else {
			part.EvidenceRefs = unionStringSets(part.EvidenceRefs, planEvidenceRefsForTasks(plan, part.TaskKeys))
			part.ActionRefs = unionStringSets(part.ActionRefs, planActionRefsForTasks(plan, part.TaskKeys))
		}
		ret = append(ret, part)
	}
	sort.SliceStable(ret, func(i, j int) bool {
		return minimumTaskSequence(ret[i].TaskKeys, plan) < minimumTaskSequence(ret[j].TaskKeys, plan)
	})
	return ret
}

func planEvidenceRefsForTasks(plan *contracts.ReplyPlanV2, taskKeys []string) []string {
	if plan == nil || len(taskKeys) == 0 {
		return nil
	}
	refs := make([]string, 0, len(taskKeys)*4)
	for _, task := range plan.Tasks {
		if !taskKeyCovered(taskKeys, task.TaskKey) {
			continue
		}
		refs = append(refs, task.EvidenceRefs...)
	}
	return refs
}

func planActionRefsForTasks(plan *contracts.ReplyPlanV2, taskKeys []string) []string {
	if plan == nil || len(taskKeys) == 0 {
		return nil
	}
	refs := make([]string, 0, len(taskKeys)*2)
	for _, task := range plan.Tasks {
		if !taskKeyCovered(taskKeys, task.TaskKey) {
			continue
		}
		refs = append(refs, task.ActionRefs...)
	}
	return refs
}

func unionStringSets(first, second []string) []string {
	merged := uniqueTrimmedStrings(first)
	seen := make(map[string]struct{}, len(merged))
	for _, item := range merged {
		seen[item] = struct{}{}
	}
	for _, item := range second {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func taskKeyCovered(taskKeys []string, value string) bool {
	for _, item := range taskKeys {
		if item == value {
			return true
		}
	}
	return false
}

func uniqueTrimmedStrings(items []string) []string {
	ret := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
	}
	return ret
}

func validationIssue(code, path, message string) contracts.ValidationIssueV1 {
	return contracts.ValidationIssueV1{Code: code, Path: path, Message: message}
}
