package contracts

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/pkg/strictjson"
)

const (
	maxAnswerRequirementSetBytes = 16 * 1024
	maxRequirementStateSetBytes  = 16 * 1024
	maxResolvedCoverageBytes     = 128 * 1024
	maxValidationResultV3Bytes   = 128 * 1024
)

func DecodeAnswerRequirementSetV1(raw []byte) (AnswerRequirementSetV1, error) {
	decoded, err := strictjson.DecodeObject[AnswerRequirementSetV1](raw, strictjson.DecodeOptions{
		MaxBytes: maxAnswerRequirementSetBytes,
		Schema:   MustSchema(SchemaAnswerRequirementSetV1),
	})
	if err != nil {
		return AnswerRequirementSetV1{}, err
	}
	seenKeys := make(map[string]struct{}, len(decoded.Requirements))
	seenSequences := make(map[int]struct{}, len(decoded.Requirements))
	for index, item := range decoded.Requirements {
		if item.SpanEnd < item.SpanStart {
			return AnswerRequirementSetV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/requirements/%d/spanEnd", index), "spanEnd precedes spanStart",
			)
		}
		if _, exists := seenKeys[item.Key]; exists {
			return AnswerRequirementSetV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/requirements/%d/key", index), "requirement key is duplicated",
			)
		}
		if _, exists := seenSequences[item.Sequence]; exists {
			return AnswerRequirementSetV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/requirements/%d/sequence", index), "requirement sequence is duplicated",
			)
		}
		seenKeys[item.Key] = struct{}{}
		seenSequences[item.Sequence] = struct{}{}
	}
	return decoded, nil
}

func MarshalAnswerRequirementSetV1(value AnswerRequirementSetV1) ([]byte, error) {
	return marshalStrictRuntimeContract(value, DecodeAnswerRequirementSetV1)
}

func ValidateAnswerRequirementBindingV1(
	value AnswerRequirementSetV1,
	taskKey string,
	sourceMessageID int64,
	spanStart, spanEnd int,
) error {
	if strings.TrimSpace(value.TaskKey) != strings.TrimSpace(taskKey) {
		return runtimeContractInvariant("$/taskKey", "taskKey does not match the authoritative task")
	}
	for index, requirement := range value.Requirements {
		if requirement.SourceMsgID != sourceMessageID {
			return runtimeContractInvariant(
				fmt.Sprintf("$/requirements/%d/sourceMessageId", index),
				"sourceMessageId does not match the authoritative task",
			)
		}
		if requirement.SpanStart != spanStart || requirement.SpanEnd != spanEnd {
			return runtimeContractInvariant(
				fmt.Sprintf("$/requirements/%d", index),
				"source span does not match the authoritative task",
			)
		}
	}
	return nil
}

func DecodeRequirementStateSetV1(raw []byte) (RequirementStateSetV1, error) {
	decoded, err := strictjson.DecodeObject[RequirementStateSetV1](raw, strictjson.DecodeOptions{
		MaxBytes: maxRequirementStateSetBytes,
		Schema:   MustSchema(SchemaRequirementStateSetV1),
	})
	if err != nil {
		return RequirementStateSetV1{}, err
	}
	seen := make(map[string]struct{}, len(decoded.States))
	for index, item := range decoded.States {
		if _, exists := seen[item.Key]; exists {
			return RequirementStateSetV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/states/%d/key", index), "requirement state key is duplicated",
			)
		}
		seen[item.Key] = struct{}{}
	}
	return decoded, nil
}

func MarshalRequirementStateSetV1(value RequirementStateSetV1) ([]byte, error) {
	if value.States == nil {
		value.States = []RequirementStateItemV1{}
	}
	return marshalStrictRuntimeContract(value, DecodeRequirementStateSetV1)
}

func DecodeResolvedTurnCoverageV1(raw []byte) (ResolvedTurnCoverageV1, error) {
	decoded, err := strictjson.DecodeObject[ResolvedTurnCoverageV1](raw, strictjson.DecodeOptions{
		MaxBytes: maxResolvedCoverageBytes,
		Schema:   MustSchema(SchemaResolvedTurnCoverageV1),
	})
	if err != nil {
		return ResolvedTurnCoverageV1{}, err
	}
	seen := make(map[string]struct{}, len(decoded.Items))
	for index, item := range decoded.Items {
		if (item.TaskID > 0) != (strings.TrimSpace(item.TaskKey) != "") {
			return ResolvedTurnCoverageV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/items/%d", index), "taskId and taskKey must be present together",
			)
		}
		identity := fmt.Sprintf("%d/%d/%s", item.MessageID, item.TaskID, strings.TrimSpace(item.TaskKey))
		if _, exists := seen[identity]; exists {
			return ResolvedTurnCoverageV1{}, runtimeContractInvariant(
				fmt.Sprintf("$/items/%d", index), "coverage identity is duplicated",
			)
		}
		seen[identity] = struct{}{}
	}
	return decoded, nil
}

func MarshalResolvedTurnCoverageV1(value ResolvedTurnCoverageV1) ([]byte, error) {
	if value.Items == nil {
		value.Items = []ResolvedCoverageItemV1{}
	}
	return marshalStrictRuntimeContract(value, DecodeResolvedTurnCoverageV1)
}

func DecodeValidationResultV3(raw []byte) (ValidationResultV3, error) {
	return strictjson.DecodeObject[ValidationResultV3](raw, strictjson.DecodeOptions{
		MaxBytes: maxValidationResultV3Bytes,
		Schema:   MustSchema(SchemaValidationResultV3),
	})
}

func MarshalValidationResultV3(value ValidationResultV3) ([]byte, error) {
	if value.NormalizedParts == nil {
		value.NormalizedParts = []ResolvedPartV3{}
	}
	if value.Errors == nil {
		value.Errors = []ValidationIssueV1{}
	}
	if value.Warnings == nil {
		value.Warnings = []ValidationIssueV1{}
	}
	for index := range value.NormalizedParts {
		part := &value.NormalizedParts[index]
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
	return marshalStrictRuntimeContract(value, DecodeValidationResultV3)
}

func marshalStrictRuntimeContract[T any](value T, decode func([]byte) (T, error)) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if _, err := decode(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func runtimeContractInvariant(path, message string) error {
	return &strictjson.ProtocolError{
		Code: strictjson.ErrorJSONBusinessInvariant, Path: path, Message: message,
	}
}
