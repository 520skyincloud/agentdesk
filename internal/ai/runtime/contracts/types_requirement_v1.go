package contracts

// 多模态契约 10.8/10.10/3.2.4：AnswerRequirement 是 Task 内必须逐项完成的
// 答案义务；Task 终态由 Requirement 集合派生，禁止 Task delivered 而
// Required Requirement 仍无 Outcome。

// AnswerRequirementSetV1 持久在 AIReplyTurnTask.AnswerRequirementsJSON：
// 只保存 key/kind/来源 span 引用/required/顺序，不保存模型答案。
type AnswerRequirementSetV1 struct {
	SchemaVersion string                     `json:"schemaVersion"`
	TaskKey       string                     `json:"taskKey"`
	Requirements  []AnswerRequirementItemV1  `json:"requirements"`
}

// AnswerRequirementItemV1 单条答案义务。
type AnswerRequirementItemV1 struct {
	Key          string `json:"key"` // R1..Rn，服务端分配
	Kind         string `json:"kind"`
	SourceMsgID  int64  `json:"sourceMessageId"`
	SpanStart    int    `json:"spanStart"`
	SpanEnd      int    `json:"spanEnd"`
	Required     bool   `json:"required"`
	Sequence     int    `json:"sequence"`
}

// RequirementStateSetV1 持久在 RequirementStateJSON：outcome/queryKey/
// EvidenceRef/错误码，不保存知识正文。
type RequirementStateSetV1 struct {
	SchemaVersion string                      `json:"schemaVersion"`
	States        []RequirementStateItemV1    `json:"states"`
}

// RequirementStateItemV1 单条义务结果（10.10 状态机）。
type RequirementStateItemV1 struct {
	Key            string `json:"key"`
	Outcome        string `json:"outcome"` // answered/no_hit/failed_retryable/failed_terminal/clarify/covered/superseded/handoff/skipped_policy
	QueryKey       string `json:"queryKey"`
	EvidenceRef    string `json:"evidenceRef"`
	ErrorCode      string `json:"errorCode"`
}

// RequirementOutcomeTerminal 判定 10.10 终态集合。
func RequirementOutcomeTerminal(outcome string) bool {
	switch outcome {
	case "answered", "no_hit", "failed_terminal", "clarify", "covered", "superseded", "handoff", "skipped_policy":
		return true
	}
	return false
}

// RequirementStateSetV1SchemaVersion 是持久集合的版本标记。
const RequirementStateSetV1SchemaVersion = "requirement_state_set.v1"

// AnswerRequirementSetV1SchemaVersion 同上。
const AnswerRequirementSetV1SchemaVersion = "answer_requirement_set.v1"

// ResolvedTurnCoverageV1（契约 10.7/3.3）：持久记录每个来源问题由哪个
// Task 处理或覆盖，与 Task 创建同事务写入 Job。
type ResolvedTurnCoverageV1 struct {
	SchemaVersion string                       `json:"schemaVersion"`
	TurnID        int64                        `json:"turnId"`
	TurnVersion   int                          `json:"turnVersion"`
	Items         []ResolvedCoverageItemV1     `json:"items"`
}

// ResolvedCoverageItemV1 单个来源问题的归宿。
type ResolvedCoverageItemV1 struct {
	MessageID       int64  `json:"messageId"`
	CanonicalHash   string `json:"canonicalHash"`
	TaskID          int64  `json:"taskId"`
	TaskKey         string `json:"taskKey"`
	Status          string `json:"status"` // covered/ignored
	CoveredByTaskID int64  `json:"coveredByTaskId"`
	ReasonCode      string `json:"reasonCode"`
}

// ResolvedTurnCoverageV1SchemaVersion 同上。
const ResolvedTurnCoverageV1SchemaVersion = "resolved_turn_coverage.v1"
