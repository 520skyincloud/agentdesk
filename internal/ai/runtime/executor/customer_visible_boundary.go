package executor

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

const customerVisiblePartMaxRunes = 4000

// applyCustomerVisibleBoundary is the only text boundary before Commit. It
// cleans the same payload that Commit will persist instead of only cleaning a
// separately joined ReplyText string.
func applyCustomerVisibleBoundary(summary *RunResult, collector *callbacks.RuntimeTraceCollector) error {
	if summary == nil || collector == nil {
		return fmt.Errorf("customer visible boundary requires runtime state")
	}
	intent := collector.Data.Pipeline.Intent
	changed := false
	dropped := 0

	if len(summary.ReplyParts) > 0 {
		parts := make([]contracts.ReplyPartV2, 0, len(summary.ReplyParts))
		for _, part := range summary.ReplyParts {
			original := strings.TrimSpace(part.Content)
			part.Content, _, _ = cleanGeneratedReplyText(original, intent)
			part.TaskKeys = uniqueTrimmedStrings(part.TaskKeys)
			part.EvidenceRefs = uniqueTrimmedStrings(part.EvidenceRefs)
			part.ActionRefs = uniqueTrimmedStrings(part.ActionRefs)
			if part.Content != original {
				changed = true
			}
			if !validCustomerVisiblePart(part) {
				dropped++
				changed = true
				continue
			}
			parts = append(parts, part)
		}
		summary.ReplyParts = parts
		summary.ReplyText = joinValidatedReplyParts(parts)
		if dropped > 0 && summary.ReplyPlanV2 != nil && summary.EvidenceBundle != nil && summary.ActionLedgerV2 != nil {
			output := contracts.ReplyOutputV2{
				SchemaVersion: contracts.ReplyOutputV2SchemaVersion,
				Parts:         append([]contracts.ReplyPartV2(nil), parts...),
			}
			preserveRuntimeValidReplyParts(summary, output, summary.RunRequest, false)
			cause := &runtimeTaskValidationFailure{Reason: "customer visible boundary removed an invalid task answer"}
			if !applySafeRuntimeDegraded(summary, collector, summary.RunRequest, cause) {
				return fmt.Errorf("customer visible boundary could not settle every reply task")
			}
		}
		if summary.ReplyText != joinValidatedReplyParts(summary.ReplyParts) {
			return fmt.Errorf("customer visible reply parts do not match reply text")
		}
	} else {
		original := strings.TrimSpace(summary.ReplyText)
		cleaned, _, _ := cleanGeneratedReplyText(original, intent)
		if cleaned != original {
			changed = true
		}
		if !validCustomerVisibleText(cleaned) {
			cleaned = ""
			dropped++
			changed = true
		}
		summary.ReplyText = cleaned
	}

	collector.Data.Output.ReplyText = summary.ReplyText
	if changed {
		reason := "customer visible boundary normalized committed reply content"
		if dropped > 0 {
			reason = fmt.Sprintf("customer visible boundary dropped %d invalid reply part(s)", dropped)
		}
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(collector.Data.Pipeline.Validate.Reason, reason)
	}
	return nil
}

func validCustomerVisiblePart(part contracts.ReplyPartV2) bool {
	return len(part.TaskKeys) > 0 && validCustomerVisibleText(part.Content)
}

func validCustomerVisibleText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > customerVisiblePartMaxRunes {
		return false
	}
	if strings.Contains(text, "<<NEXT_MESSAGE>>") {
		return false
	}
	lower := strings.ToLower(text)
	for _, internalTerm := range []string{"taskkey", "evidenceref", "actionref", "reply_plan", "intent_tasks", "内部标签", "模型提示词"} {
		if strings.Contains(lower, strings.ToLower(internalTerm)) {
			return false
		}
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}
