package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/strictjson"
)

func applyRuntimeStructuredReplyOutput(raw string, summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput) error {
	if summary != nil && summary.UseRuntimeV3Generate {
		return applyRuntimeReplyOutputV3(raw, summary, collector, req)
	}
	return applyRuntimeReplyOutputV2(raw, summary, collector, req)
}

func applyRuntimeReplyOutputV3(raw string, summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput) error {
	if summary == nil || summary.ReplyPlanV4 == nil || summary.EvidenceBundleV2 == nil || summary.ActionLedgerV2 == nil {
		return fmt.Errorf("reply_output.v3 validation context is incomplete")
	}
	parsed, err := parseRuntimeReplyOutputV3(raw)
	if err != nil {
		if collector != nil {
			collector.Data.Pipeline.Validate.Status = "failed"
			collector.Data.Pipeline.Validate.Reason = replyProtocolErrorReason(err)
		}
		if runtimeProtocolRepairAllowed(err) {
			return &replyOutputProtocolError{
				Contract: contracts.ReplyOutputV3SchemaVersion, RawResponse: raw,
				Reason: replyProtocolErrorReason(err), Cause: err,
			}
		}
		return err
	}
	facts := contracts.RuntimeContextSnapshotV2{
		SchemaVersion: contracts.SchemaRuntimeContextSnapshotV2,
		Facts:         append([]contracts.RuntimeContextFactV2(nil), summary.AuthoritativeFacts...),
	}
	validation := NewReplyValidatorV3().Validate(ReplyValidationInputV3{
		Output: parsed, Plan: *summary.ReplyPlanV4, Evidence: *summary.EvidenceBundleV2,
		Observations: append([]contracts.ObservationV1(nil), summary.Observations...),
		Facts:        facts, ActionLedger: *summary.ActionLedgerV2, Req: req,
	})
	summary.ValidationResultV3 = &validation
	if collector != nil {
		collector.Data.Pipeline.Validate.Status = validation.Status
		collector.Data.Pipeline.Validate.Reason = validationResultReasonV3(validation)
	}
	switch validation.Status {
	case "passed", "warning":
		summary.ResolvedReplyPartsV3 = append([]contracts.ResolvedPartV3(nil), validation.NormalizedParts...)
		summary.ReplyText = joinResolvedReplyPartsV3(validation.NormalizedParts)
		return nil
	case "repairable_protocol_error", "retryable_content_error":
		return &replyOutputProtocolError{
			Contract: contracts.ReplyOutputV3SchemaVersion, RawResponse: raw,
			Reason: validationResultReasonV3(validation),
		}
	default:
		return fmt.Errorf("reply_output.v3 rejected: %s", validationResultReasonV3(validation))
	}
}

func parseRuntimeReplyOutputV3(raw string) (contracts.ReplyOutputV3, error) {
	normalized, _ := normalizeStructuredModelObject(raw)
	parsed, err := strictjson.DecodeObject[contracts.ReplyOutputV3]([]byte(normalized), strictjson.DecodeOptions{
		MaxBytes: 64 * 1024,
		Schema:   contracts.MustSchema(contracts.SchemaReplyOutputV3),
	})
	if err != nil {
		return contracts.ReplyOutputV3{}, err
	}
	if parsed.SchemaVersion != contracts.ReplyOutputV3SchemaVersion {
		return contracts.ReplyOutputV3{}, fmt.Errorf("reply_output.v3 schema version mismatch")
	}
	return parsed, nil
}

func validationResultReasonV3(result contracts.ValidationResultV3) string {
	if len(result.Errors) == 0 {
		return strings.TrimSpace(result.Status)
	}
	codes := make([]string, 0, len(result.Errors))
	for _, issue := range result.Errors {
		if code := strings.TrimSpace(issue.Code); code != "" {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return strings.TrimSpace(result.Status)
	}
	return strings.Join(uniqueTrimmedStrings(codes), ",")
}

func joinResolvedReplyPartsV3(parts []contracts.ResolvedPartV3) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if content := strings.TrimSpace(part.Content); content != "" {
			texts = append(texts, content)
		}
	}
	// ReplyText is only a human-readable aggregate for logs and compatibility.
	// Message boundaries are carried by ResolvedReplyPartsV3, never by a token
	// that can leak into customer-visible text.
	return strings.Join(texts, "\n\n")
}

func runtimeReplyContractName(summary *RunResult) string {
	if summary != nil && summary.UseRuntimeV3Generate {
		return contracts.ReplyOutputV3SchemaVersion
	}
	return contracts.ReplyOutputV2SchemaVersion
}

func runtimeReplyProtocolRepairReason(summary *RunResult) string {
	if summary != nil && summary.UseRuntimeV3Generate {
		return "reply_output_v3_protocol_repair"
	}
	return "reply_output_v2_protocol_repair"
}
