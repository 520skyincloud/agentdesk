package contracts

import "time"

// 多模态契约 20：handoff_decision.v2 / handoff_pending_action.v2。
// 技术失败与知识缺口 mode=none 只进 Trace/指标，禁止伪造 PendingAction。

const (
	HandoffDecisionV2SchemaVersion      = SchemaHandoffDecisionV2
	HandoffPendingActionV2SchemaVersion = SchemaHandoffPendingActionV2
	HandoffOriginBusiness               = "business_handoff"
	HandoffOriginSafety                 = "safety_handoff"
	HandoffModeNone                     = "none"
	HandoffModeConfirm                  = "confirm"
	HandoffModeDispatch                 = "dispatch"
)

// HandoffDecisionV2 是人工兜底决策（内部对象，不直接持久化）。
type HandoffDecisionV2 struct {
	SchemaVersion       string    `json:"schemaVersion"`
	TenantID            int64     `json:"tenantId"`
	StoreID             int64     `json:"storeId"`
	StoreStaffBindingID int64     `json:"storeStaffBindingId"`
	ProtocolInstanceID  int64     `json:"protocolInstanceId"`
	ConversationID      int64     `json:"conversationId"`
	SessionNo           int       `json:"sessionNo"`
	TurnID              int64     `json:"turnId"`
	TurnVersion         int       `json:"turnVersion"`
	TaskKeys            []string  `json:"taskKeys"`
	OriginType          string    `json:"originType"`
	Mode                string    `json:"mode"`
	ReasonCode          string    `json:"reasonCode"`
	FailureClass        string    `json:"failureClass"`
	BlockedTransition   string    `json:"blockedTransition,omitempty"`
	DecidedAt           time.Time `json:"decidedAt"`
}

// Pending 判定是否产生持久 PendingAction。
func (d HandoffDecisionV2) Pending() bool {
	return d.Mode == HandoffModeConfirm || d.Mode == HandoffModeDispatch
}

// HandoffPendingActionV2 是写入 ConversationRouteState.PendingActionPayload
// 的持久负载；必须绑定完整 TaskSet 与会话 scope。
type HandoffPendingActionV2 struct {
	SchemaVersion       string    `json:"schemaVersion"`
	HandoffToken        string    `json:"handoffToken"`
	TenantID            int64     `json:"tenantId"`
	StoreID             int64     `json:"storeId"`
	StoreStaffBindingID int64     `json:"storeStaffBindingId"`
	ProtocolInstanceID  int64     `json:"protocolInstanceId"`
	ConversationID      int64     `json:"conversationId"`
	SessionNo           int       `json:"sessionNo"`
	TurnID              int64     `json:"turnId"`
	TurnVersion         int       `json:"turnVersion"`
	TaskKeys            []string  `json:"taskKeys"`
	OriginMessageID     int64     `json:"originMessageId"`
	RequestID           string    `json:"requestId"`
	OriginType          string    `json:"originType"`
	Mode                string    `json:"mode"`
	ReasonCode          string    `json:"reasonCode"`
	CreatedAt           time.Time `json:"createdAt"`
	ExpiresAt           time.Time `json:"expiresAt"`
}

// Valid 做契约 20.1 的本地校验：expiresAt > createdAt、scope 完整、
// originType/mode 只允许持久化组合。
func (a HandoffPendingActionV2) Valid() bool {
	if a.ExpiresAt.IsZero() || a.CreatedAt.IsZero() || !a.ExpiresAt.After(a.CreatedAt) {
		return false
	}
	if len(a.HandoffToken) < 16 || len(a.HandoffToken) > 128 {
		return false
	}
	if len(a.TaskKeys) == 0 || len(a.TaskKeys) > 12 {
		return false
	}
	if a.OriginType != HandoffOriginBusiness && a.OriginType != HandoffOriginSafety {
		return false
	}
	if a.Mode != HandoffModeConfirm && a.Mode != HandoffModeDispatch {
		return false
	}
	return a.TenantID > 0 && a.StoreID > 0 && a.StoreStaffBindingID > 0 &&
		a.ProtocolInstanceID > 0 && a.ConversationID > 0 && a.SessionNo > 0 &&
		a.TurnID > 0 && a.TurnVersion > 0 && a.OriginMessageID > 0 && a.RequestID != ""
}
