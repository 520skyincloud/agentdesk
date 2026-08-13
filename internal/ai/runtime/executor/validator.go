package executor

import (
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

type ReplyValidationInput struct {
	Output       contracts.ReplyOutputV2
	Plan         contracts.ReplyPlanV2
	Evidence     contracts.EvidenceBundleV1
	ActionLedger contracts.ActionLedgerV1
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
	if issues := validateReplyActionReferences(input); len(issues) > 0 {
		result.Checks.ActionReferences = "failed"
		result.Errors = append(result.Errors, issues...)
		result.Status = "rejected"
	}
	if v.full {
		if issues := validateReplySafety(input); len(issues) > 0 {
			result.Checks.Safety = "failed"
			result.Errors = append(result.Errors, issues...)
			result.Status = "rejected"
		}
		if issues := validateReplyCommitInvariants(input); len(issues) > 0 {
			result.Checks.CommitInvariants = "failed"
			result.Errors = append(result.Errors, issues...)
			result.Status = "rejected"
		}
	}
	return result
}

func normalizeReplyParts(parts []contracts.ReplyPartV2, plan *contracts.ReplyPlanV2) []contracts.ReplyPartV2 {
	ret := make([]contracts.ReplyPartV2, 0, len(parts))
	for _, part := range parts {
		part.Content = strings.TrimSpace(part.Content)
		part.TaskKeys = uniqueTrimmedStrings(part.TaskKeys)
		part.EvidenceRefs = uniqueTrimmedStrings(part.EvidenceRefs)
		part.ActionRefs = uniqueTrimmedStrings(part.ActionRefs)
		ret = append(ret, part)
	}
	sort.SliceStable(ret, func(i, j int) bool {
		return minimumTaskSequence(ret[i].TaskKeys, plan) < minimumTaskSequence(ret[j].TaskKeys, plan)
	})
	return ret
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
