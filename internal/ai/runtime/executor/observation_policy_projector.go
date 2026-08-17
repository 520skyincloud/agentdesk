package executor

import (
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/observationpolicy"
)

// ObservationPolicyProjector 按「多模态契约 7.2 最低权限表」把 Provider 候选投影为
// 最终 Observation。Provider（Vision/ASR/文件解析）只输出候选分类，不携带使用权限；
// 权限由本纯函数按 mediaType/observationType/contentRole 决定。模型即使把截图里的
// 历史对话识别为“升房请求”，也没有资格把它升级为门店事实、资源动作或人工动作。

// MediaObservationCandidate 是 Provider 输出的单条候选（不含权限）。
type MediaObservationCandidate = contracts.MediaAnalysisCandidateItemV1

// MediaSource 描述观察的媒体来源。
type MediaSource = observationpolicy.Source

// ObservationUse 授权的使用用途（封闭枚举）。
const (
	obsUseDescribeMedia    = observationpolicy.UseDescribeMedia
	obsUseResolveReference = observationpolicy.UseResolveReference
	obsUseBuildQuery       = observationpolicy.UseBuildQuery
	obsUseQuoteDocument    = observationpolicy.UseQuoteDocument
)

// forbiddenAll 是默认禁止用途全集（门店身份/地址/电话/政策/资源资格/人工决定）。
func forbiddenAll() []string {
	return observationpolicy.ForbiddenFactUses()
}

// ProjectObservation 按最低权限表投影：contentRole 决定 allowedUses，
// forbiddenUses 永远包含全部门店事实与动作决定。
func ProjectObservation(source MediaSource, candidate MediaObservationCandidate) (allowed []string, forbidden []string) {
	return observationpolicy.Project(source, candidate)
}

// ObservationSupportsUse 判定一条 Observation 是否被授权用于某用途。
func ObservationSupportsUse(allowedUses []string, use string) bool {
	return observationpolicy.SupportsUse(allowedUses, use)
}
