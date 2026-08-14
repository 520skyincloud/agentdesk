package services

import (
	"strings"
	"time"

	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// 契约 22.16：失败分类与阶段恢复决策。技术失败绝不进入 handoff_pending，
// 恢复策略函数必须把技术失败到人工的转换视为非法状态迁移并记录
// handoff_technical_failure_blocked。

// AIReplyFailureClass 是 Job 级失败分类。
type AIReplyFailureClass string

const (
	FailureProtocol  AIReplyFailureClass = "protocol"
	FailureContent   AIReplyFailureClass = "content"
	FailureNetwork   AIReplyFailureClass = "network"
	FailureKnowledge AIReplyFailureClass = "knowledge"
	FailureDatabase  AIReplyFailureClass = "database"
	FailureScope     AIReplyFailureClass = "scope"
	FailureBusiness  AIReplyFailureClass = "business"
	FailureSafety    AIReplyFailureClass = "safety"
)

// ExecutionCheckpoint 是恢复决策输入的摘要（不包含模型正文）。
type ExecutionCheckpoint struct {
	TurnVersion            int
	StageAttemptCount      int
	MaxStageAttempts       int
	TaskKeys               []string
	CommittedTaskKeys      []string
	HasAnySuccess          bool
	IsLatestTurnVersion    bool
	CheckpointFingerprint  string
	PlanFingerprint        string
	MediaAnalysisTerminal  bool
	CapabilityHandoffRoute bool
}

// RecoveryDecision 是 DecideRecovery 的输出。
type RecoveryDecision struct {
	FailureClass      AIReplyFailureClass
	Action            string // retry_stage/retry_job/terminal_notice/terminal_failed/handoff
	ResumeStage       string
	BlockedTransition string // 非空表示拦截了一次非法迁移
	ReasonCode        string
}

// DecideRecovery 按契约 22.16 分类恢复动作。禁止技术 Failure -> handoff。
func DecideRecovery(failureClass AIReplyFailureClass, state ExecutionCheckpoint) RecoveryDecision {
	maxAttempts := state.MaxStageAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if state.CapabilityHandoffRoute && failureClass == FailureBusiness {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "handoff", ResumeStage: "handoff",
			ReasonCode: "capability_route_business_handoff",
		}
	}
	// 技术/协议/网络/数据库失败：短重试或确定性终态提示，绝不转人工。
	if failureClass == FailureProtocol || failureClass == FailureNetwork ||
		failureClass == FailureDatabase || failureClass == FailureScope {
		if state.StageAttemptCount < maxAttempts {
			return RecoveryDecision{
				FailureClass: failureClass, Action: "retry_stage",
				ReasonCode: "stage_retry_within_budget",
			}
		}
		return terminalNoticeDecision(failureClass, state)
	}
	if failureClass == FailureContent || failureClass == FailureKnowledge {
		if state.StageAttemptCount < maxAttempts {
			return RecoveryDecision{
				FailureClass: failureClass, Action: "retry_stage",
				ReasonCode: "content_retry_within_budget",
			}
		}
		return terminalNoticeDecision(failureClass, state)
	}
	if failureClass == FailureSafety {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "terminal_failed",
			ReasonCode: "safety_failure_terminal",
		}
	}
	// 防御性兜底：任何未知/业务失败要求人工时都必须被拦截并记录。
	if failureClass == FailureBusiness {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "terminal_notice",
			ReasonCode: "business_failure_without_capability_route",
		}
	}
	terminal := terminalNoticeDecision(failureClass, state)
	terminal.BlockedTransition = "handoff_technical_failure_blocked"
	return terminal
}

func terminalNoticeDecision(failureClass AIReplyFailureClass, state ExecutionCheckpoint) RecoveryDecision {
	if state.MediaAnalysisTerminal {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "terminal_notice",
			ReasonCode: "media_analysis_terminal_resend_request",
		}
	}
	if state.HasAnySuccess {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "terminal_notice",
			ReasonCode: "partial_success_technical_notice",
		}
	}
	if state.IsLatestTurnVersion {
		return RecoveryDecision{
			FailureClass: failureClass, Action: "terminal_notice",
			ReasonCode: "technical_failure_notified",
		}
	}
	return RecoveryDecision{
		FailureClass: failureClass, Action: "terminal_failed",
		ReasonCode: "technical_failure_superseded",
	}
}

// NormalizeAIReplyFailureClass 把任意错误分类字符串归一为合法分类。
func NormalizeAIReplyFailureClass(value string) AIReplyFailureClass {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "protocol":
		return FailureProtocol
	case "content":
		return FailureContent
	case "network", "timeout":
		return FailureNetwork
	case "knowledge":
		return FailureKnowledge
	case "database", "db":
		return FailureDatabase
	case "scope":
		return FailureScope
	case "business":
		return FailureBusiness
	case "safety":
		return FailureSafety
	default:
		return FailureProtocol
	}
}

// AdvanceJobStageDB 推进 Job 恢复阶段（阶段推进归零 StageAttemptCount）。
func AdvanceJobStageDB(jobID int64, from, to, checkpoint string) (bool, error) {
	return repositories.AIReplyJobRepository.CASAdvanceStage(
		sqls.DB(), jobID, from, to, checkpoint, time.Now(),
	)
}
