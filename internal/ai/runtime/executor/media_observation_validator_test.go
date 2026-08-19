package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestReplyValidatorRejectsEvasiveReadyMediaAnswer(t *testing.T) {
	input := mediaObservationValidationInput("看你发的图，我这边只能看到图片内容，没法猜具体是啥。你说的是图里的东西吗？")
	result := NewReplyValidator().Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "media_observation_not_used") {
		t.Fatalf("evasive media answer must be repaired, got %#v", result)
	}
}

func TestReplyValidatorAcceptsConcreteLowConfidenceMediaAnswer(t *testing.T) {
	input := mediaObservationValidationInput("看起来像一个像素风的浅棕黄色圆形食物，有点像鸡蛋或面包，不过图比较抽象，我不敢百分百确定。")
	result := NewReplyValidator().Validate(input)
	if result.Status != "passed" {
		t.Fatalf("concrete low-confidence media answer must pass, got %#v", result)
	}
}

func TestReplyValidatorRejectsLongMediaAcknowledgementWithoutObservation(t *testing.T) {
	for _, content := range []string{
		"收到图片了，我来看看。",
		"这张图片的信息比较复杂，我还需要再研究一下，这个问题现在不好说。",
	} {
		result := NewReplyValidator().Validate(mediaObservationValidationInput(content))
		if result.Status != "repairable_protocol_error" || !validationHasCode(result, "media_observation_not_used") {
			t.Fatalf("media acknowledgement without an observation must be repaired, content=%q result=%#v", content, result)
		}
	}
}

func TestReplyValidatorAcceptsNaturalConcreteMediaDescription(t *testing.T) {
	result := NewReplyValidator().Validate(mediaObservationValidationInput("图中是一个浅棕黄色的圆形图标，大概是鸡蛋。"))
	if result.Status != "passed" {
		t.Fatalf("natural concrete media description must pass, got %#v", result)
	}
}

func mediaObservationValidationInput(content string) ReplyValidationInput {
	return ReplyValidationInput{
		Output: contracts.ReplyOutputV2{
			SchemaVersion: contracts.ReplyOutputV2SchemaVersion,
			Parts:         []contracts.ReplyPartV2{{TaskKeys: []string{"task-media"}, Content: content}},
		},
		Plan: contracts.ReplyPlanV2{
			SchemaVersion:  contracts.ReplyPlanV2SchemaVersion,
			TurnVersion:    1,
			ShouldGenerate: true,
			Tasks: []contracts.ReplyPlanTaskV2{{
				TaskKey: "task-media", Sequence: 1, Intent: "interaction", SubIntent: "media_context_follow_up",
				Objective: "这啥你知道不", OutputMode: "text",
				Knowledge:    contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed", ReasonCode: "knowledge_not_needed"},
				EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{"must_use_media_observation"},
			}},
			GlobalConstraints: contracts.ReplyPlanGlobalConstraints{
				MaxReplyParts: 3, MaxQuestionsPerPart: 4, ForbiddenClaims: []string{},
			},
		},
		Evidence: contracts.EvidenceBundleV1{
			SchemaVersion: contracts.EvidenceBundleV1SchemaVersion, ScopeFingerprint: "scope",
			RetrievalStatus: "not_needed", Items: []contracts.EvidenceItemV1{}, Resources: []contracts.EvidenceResourceV1{},
		},
		ActionLedger: contracts.ActionLedgerV1{
			SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{},
		},
		Gates: DefaultReplyValidationGates(),
	}
}
