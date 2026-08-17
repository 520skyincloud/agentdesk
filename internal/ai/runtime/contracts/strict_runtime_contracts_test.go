package contracts

import (
	"testing"

	"agent-desk/internal/pkg/strictjson"
)

func TestStrictRuntimeContractsRoundTrip(t *testing.T) {
	requirements := AnswerRequirementSetV1{
		SchemaVersion: AnswerRequirementSetV1SchemaVersion,
		TaskKey:       "turn_task_1",
		Requirements: []AnswerRequirementItemV1{{
			Key: "R1", Kind: "existence", SourceMsgID: 42,
			SpanStart: 0, SpanEnd: 5, Required: true, Sequence: 1,
		}},
	}
	raw, err := MarshalAnswerRequirementSetV1(requirements)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAnswerRequirementSetV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAnswerRequirementBindingV1(decoded, "turn_task_1", 42, 0, 5); err != nil {
		t.Fatal(err)
	}

	states := RequirementStateSetV1{
		SchemaVersion: RequirementStateSetV1SchemaVersion,
		States: []RequirementStateItemV1{{
			Key: "R1", Outcome: "answered", EvidenceRef: "message:100",
		}},
	}
	if _, err := MarshalRequirementStateSetV1(states); err != nil {
		t.Fatal(err)
	}

	coverage := ResolvedTurnCoverageV1{
		SchemaVersion: ResolvedTurnCoverageV1SchemaVersion,
		TurnID:        7, TurnVersion: 2,
		Items: []ResolvedCoverageItemV1{{
			MessageID: 42, TaskID: 9, TaskKey: "turn_task_1", Status: "scheduled",
		}},
	}
	if _, err := MarshalResolvedTurnCoverageV1(coverage); err != nil {
		t.Fatal(err)
	}
}

func TestStrictRuntimeContractsRejectInvalidPersistedJSON(t *testing.T) {
	tests := []struct {
		name string
		call func() error
		code string
	}{
		{
			name: "wrong answer requirement version",
			call: func() error {
				_, err := DecodeAnswerRequirementSetV1([]byte(`{"schemaVersion":"answer_requirements.v1","taskKey":"turn_task_1","requirements":[]}`))
				return err
			},
			code: strictjson.ErrorJSONSchemaInvalid,
		},
		{
			name: "duplicate requirement key",
			call: func() error {
				_, err := DecodeAnswerRequirementSetV1([]byte(`{"schemaVersion":"answer_requirement_set.v1","taskKey":"turn_task_1","requirements":[{"key":"R1","kind":"a","sourceMessageId":1,"spanStart":0,"spanEnd":1,"required":true,"sequence":1},{"key":"R1","kind":"b","sourceMessageId":1,"spanStart":1,"spanEnd":2,"required":true,"sequence":2}]}`))
				return err
			},
			code: strictjson.ErrorJSONBusinessInvariant,
		},
		{
			name: "null requirement states",
			call: func() error {
				_, err := DecodeRequirementStateSetV1([]byte(`{"schemaVersion":"requirement_state_set.v1","states":null}`))
				return err
			},
			code: strictjson.ErrorJSONSchemaInvalid,
		},
		{
			name: "coverage task identity half present",
			call: func() error {
				_, err := DecodeResolvedTurnCoverageV1([]byte(`{"schemaVersion":"resolved_turn_coverage.v1","turnId":1,"turnVersion":1,"items":[{"messageId":1,"canonicalHash":"","taskId":2,"taskKey":"","status":"scheduled","coveredByTaskId":0,"reasonCode":""}]}`))
				return err
			},
			code: strictjson.ErrorJSONBusinessInvariant,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if code, ok := strictjson.CodeOf(err); !ok || code != tt.code {
				t.Fatalf("error=%v code=%q want=%q", err, code, tt.code)
			}
		})
	}
}
