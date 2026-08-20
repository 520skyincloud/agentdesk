package executor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
)

// 契约 20.2：确定性 Handoff 政策。模型分类器只能识别客户对既有
// PendingAction 的确认或取消，不能决定技术失败是否转人工。

// HandoffTaskView 是决策所需的 Task 摘要。
type HandoffTaskView struct {
	TaskKey              string
	TaskKeys             []string
	TurnID               int64
	TurnVersion          int
	TenantID             int64
	StoreID              int64
	StoreStaffBindingID  int64
	ProtocolInstanceID   int64
	ConversationID       int64
	SessionNo            int
	SafetyCritical       bool
	ExplicitHumanRequest bool
}

// HandoffFailureClass 是决策输入的失败分类（technical/knowledge_gap/none…）。
type HandoffFailureClass string

const (
	HandoffFailureNone         HandoffFailureClass = ""
	HandoffFailureTechnical    HandoffFailureClass = "technical"
	HandoffFailureKnowledgeGap HandoffFailureClass = "knowledge_gap"
)

// DecideHandoff 输出 HandoffDecisionV2。
func DecideHandoff(task HandoffTaskView, capability CapabilityDecisionV1, failureClass HandoffFailureClass) contracts.HandoffDecisionV2 {
	taskKeys := uniqueTrimmedStrings(task.TaskKeys)
	if len(taskKeys) == 0 && strings.TrimSpace(task.TaskKey) != "" {
		taskKeys = []string{strings.TrimSpace(task.TaskKey)}
	}
	decision := contracts.HandoffDecisionV2{
		SchemaVersion: contracts.HandoffDecisionV2SchemaVersion,
		TenantID:      task.TenantID, StoreID: task.StoreID,
		StoreStaffBindingID: task.StoreStaffBindingID, ProtocolInstanceID: task.ProtocolInstanceID,
		ConversationID: task.ConversationID, SessionNo: task.SessionNo,
		TurnID: task.TurnID, TurnVersion: task.TurnVersion,
		TaskKeys:  taskKeys,
		DecidedAt: time.Now().UTC(),
	}
	switch {
	case task.SafetyCritical:
		decision.OriginType = contracts.HandoffOriginSafety
		decision.Mode = contracts.HandoffModeConfirm
		decision.ReasonCode = "safety_critical_confirmation"
	case failureClass == HandoffFailureTechnical:
		decision.Mode = contracts.HandoffModeNone
		decision.FailureClass = string(failureClass)
		decision.ReasonCode = "technical_failure"
		decision.BlockedTransition = "handoff_technical_failure_blocked"
	case failureClass == HandoffFailureKnowledgeGap:
		decision.Mode = contracts.HandoffModeNone
		decision.FailureClass = string(failureClass)
		decision.ReasonCode = "knowledge_gap"
	case task.ExplicitHumanRequest:
		decision.OriginType = contracts.HandoffOriginBusiness
		decision.Mode = contracts.HandoffModeConfirm
		decision.ReasonCode = "customer_explicit_human_request"
	case capability.Route == "business_handoff" && capability.ExecutionMode == "human":
		decision.OriginType = contracts.HandoffOriginBusiness
		decision.Mode = contracts.HandoffModeConfirm
		decision.ReasonCode = "capability_route_business_handoff"
	default:
		decision.Mode = contracts.HandoffModeNone
		decision.ReasonCode = "not_eligible"
	}
	return decision
}

// BuildHandoffPendingAction 从已确认 decision 构造持久 PendingAction。
func BuildHandoffPendingAction(decision contracts.HandoffDecisionV2, originMessageID int64, requestID string, ttl time.Duration) (contracts.HandoffPendingActionV2, error) {
	if !decision.Pending() {
		return contracts.HandoffPendingActionV2{}, fmt.Errorf("handoff decision mode %s cannot persist a pending action", decision.Mode)
	}
	now := time.Now().UTC()
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	token, err := newHandoffToken()
	if err != nil {
		return contracts.HandoffPendingActionV2{}, err
	}
	action := contracts.HandoffPendingActionV2{
		SchemaVersion: contracts.HandoffPendingActionV2SchemaVersion,
		HandoffToken:  token,
		TenantID:      decision.TenantID, StoreID: decision.StoreID,
		StoreStaffBindingID: decision.StoreStaffBindingID, ProtocolInstanceID: decision.ProtocolInstanceID,
		ConversationID: decision.ConversationID, SessionNo: decision.SessionNo,
		TurnID: decision.TurnID, TurnVersion: decision.TurnVersion,
		TaskKeys: decision.TaskKeys, OriginMessageID: originMessageID, RequestID: requestID,
		OriginType: decision.OriginType, Mode: decision.Mode, ReasonCode: decision.ReasonCode,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if !action.Valid() {
		return contracts.HandoffPendingActionV2{}, fmt.Errorf("handoff pending action scope is incomplete")
	}
	return action, nil
}

func newHandoffToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "handoff-" + hex.EncodeToString(raw), nil
}
