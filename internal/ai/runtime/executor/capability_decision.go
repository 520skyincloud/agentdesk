package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// CapabilityDecisionV1 按「多模态契约 12.4」实现服务端能力路由：
// 模型只给 requestMode；系统按发布配置决定 answer / tool / 澄清 / 业务人工 / 不支持。
// 关键不变量：技术失败、知识无命中、协议失败绝不能把 route 改成 business_handoff。
type CapabilityDecisionV1 struct {
	TaskKey           string
	PolicyFingerprint string
	RequestMode       string
	CapabilityCode    string
	Route             string // knowledge_answer/direct_answer/tool_execute/clarify_required_fields/confirm_action/business_handoff/reject_unsupported/social_reply/no_reply
	KnowledgePolicy   string // required/optional/forbidden
	ExecutionMode     string // none/tool/human
	RequiredFields    []string
	MissingFields     []string
	Confirmation      string // none/required
	ResponseMode      string // text/text_and_action/clarification/handoff_confirmation/skip
	ReasonCode        string
}

// CapabilityPolicy 是一个 QuestionUnit 对应的已发布能力（来自 ReplyIntentConfig 派生）。
type CapabilityPolicy struct {
	IntentCode       string
	NeedsKnowledge   bool
	NeedsHumanRoute  bool
	HumanRoutePolicy string // managed_mode 等；空表示发布配置不允许人工
	ToolCodes        []string
	RequiredFields   []string
	// CollectedFields 是当前 Turn 已收集的字段（房号等）；缺字段时立即澄清。
	CollectedFields map[string]string
}

// DeriveCapabilityDecision 对一个 QuestionUnit 输出确定性路由。
// 不调用模型、不按关键词覆盖 Intent；按 requestMode + 发布能力分流。
func DeriveCapabilityDecision(unit QuestionUnit, policy CapabilityPolicy) (CapabilityDecisionV1, error) {
	fingerprint := capabilityPolicyFingerprint(unit, policy)
	decision := CapabilityDecisionV1{
		TaskKey:           "",
		PolicyFingerprint: fingerprint,
		RequestMode:       unit.RequestMode,
		CapabilityCode:    policy.IntentCode + "/" + unit.SubIntent,
	}
	switch unit.RequestMode {
	case "answer", "clarify_previous", "correct_previous", "social":
		// 信息询问：没有 Tool 也必须正常回答或说明资料未写明，不得转人工。
		if unit.RequestMode == "social" {
			decision.Route = "social_reply"
			decision.KnowledgePolicy = "forbidden"
			decision.ExecutionMode = "none"
			decision.ResponseMode = "text"
			decision.ReasonCode = "social_reply"
			return decision, nil
		}
		decision.KnowledgePolicy = "required"
		if policy.NeedsKnowledge {
			decision.Route = "knowledge_answer"
			decision.ResponseMode = "text"
			decision.ReasonCode = "information_answer_via_knowledge"
		} else {
			decision.Route = "direct_answer"
			decision.ResponseMode = "text"
			decision.ReasonCode = "information_answer_direct"
		}
		decision.ExecutionMode = "none"
		return decision, nil

	case "request_action":
		// 办理请求：必须落到 Tool/澄清/确认/业务人工/明确不支持之一，禁止无输出 pending。
		missing := missingCapabilityFields(policy.RequiredFields, policy.CollectedFields)
		decision.RequiredFields = policy.RequiredFields
		decision.MissingFields = missing
		if len(policy.ToolCodes) > 0 {
			if len(missing) > 0 {
				decision.Route = "clarify_required_fields"
				decision.KnowledgePolicy = "forbidden"
				decision.ExecutionMode = "tool"
				decision.ResponseMode = "clarification"
				decision.ReasonCode = "capability_required_fields_missing"
				return decision, nil
			}
			decision.Route = "tool_execute"
			decision.KnowledgePolicy = "optional"
			decision.ExecutionMode = "tool"
			decision.Confirmation = "required"
			decision.ResponseMode = "text_and_action"
			decision.ReasonCode = "capability_tool_execute"
			return decision, nil
		}
		// 无 Tool：只有发布能力明确允许人工才可进入业务人工（与 human_complaint_risk 的投诉人工不同源）。
		if policy.NeedsHumanRoute && strings.TrimSpace(policy.HumanRoutePolicy) != "" {
			decision.Route = "business_handoff"
			decision.KnowledgePolicy = "forbidden"
			decision.ExecutionMode = "human"
			decision.Confirmation = "required"
			decision.ResponseMode = "handoff_confirmation"
			decision.ReasonCode = "capability_business_handoff_eligible"
			return decision, nil
		}
		decision.Route = "reject_unsupported"
		decision.KnowledgePolicy = "optional"
		decision.ExecutionMode = "none"
		decision.ResponseMode = "text"
		decision.ReasonCode = "capability_unsupported"
		return decision, nil

	case "confirm_previous":
		decision.Route = "confirm_action"
		decision.KnowledgePolicy = "forbidden"
		decision.ExecutionMode = "none"
		decision.Confirmation = "confirmed"
		decision.ResponseMode = "text"
		decision.ReasonCode = "confirm_previous_action"
		return decision, nil

	case "cancel_previous":
		decision.Route = "no_reply"
		decision.KnowledgePolicy = "forbidden"
		decision.ExecutionMode = "none"
		decision.ResponseMode = "skip"
		decision.ReasonCode = "cancel_previous_handled_by_route"
		return decision, nil

	default:
		return CapabilityDecisionV1{}, fmt.Errorf("unsupported request mode %q", unit.RequestMode)
	}
}

// capabilityPolicyFingerprint 只包含稳定策略输入，不含客户正文或模型输出。
func capabilityPolicyFingerprint(unit QuestionUnit, policy CapabilityPolicy) string {
	parts := []string{
		policy.IntentCode,
		unit.SubIntent,
		unit.RequestMode,
		strings.Join(policy.ToolCodes, ","),
		strings.Join(policy.RequiredFields, ","),
		policy.HumanRoutePolicy,
		fmt.Sprintf("%v", policy.NeedsKnowledge),
		fmt.Sprintf("%v", policy.NeedsHumanRoute),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func missingCapabilityFields(required []string, collected map[string]string) []string {
	missing := make([]string, 0, len(required))
	for _, field := range required {
		if _, ok := collected[strings.TrimSpace(field)]; !ok {
			missing = append(missing, field)
		}
	}
	return missing
}
