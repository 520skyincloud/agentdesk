package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 契约 22.12：最终 AnswerGroup 由服务端在知识、能力和 Action 状态都已确定后构造，
// 模型不得自行生成。确定性规则：
//  1. handoff、resource_only 和真实 Tool Action 不与普通知识文本混组。
//  2. 知识 Task 只有 evidence fingerprint 相同（或权威 FactRef 集合相同且
//     answerability/outputMode 一致）才可合并。
//  3. no_context 只有 intent/subIntent、缺失字段和澄清目标相同才可合并。
//  4. 每组最多 4 个 Task；单 Job 可领取 6 个 Task，一次 Generate 最多前 3 个 ready group。
//  5. AnswerGroupKey 使用 TurnID、排序后 TaskKey、输出模式、Capability route、
//     Evidence/Action fingerprint 的 canonical JSON 计算，不使用模型生成文本。

const (
	answerGroupMaxTasks       = 4
	answerGroupMaxReadyGroups = 3
)

// AnswerGroup 是最终分组结果（reply_plan.v4 / reply_output.v3 使用）。
type AnswerGroup struct {
	GroupKey   string
	TaskKeys   []string
	Sequence   int
	OutputMode string
}

// TaskRuntimeView 是参与分组的 Task 运行时视图（由 task ledger 聚合）。
type TaskRuntimeView struct {
	TurnID    int64
	TaskKey   string
	Sequence  int
	Intent    string
	SubIntent string
}

// TaskEvidenceResultView 是分组所需的证据摘要。
type TaskEvidenceResultView struct {
	Status             string
	Fingerprint        string
	AuthoritativeFacts []string
	Answerability      string
}

// AnswerGroupSignature 是分组的确定性签名。
type AnswerGroupSignature struct {
	OutputMode          string
	CapabilityRoute     string
	KnowledgeStatus     string
	EvidenceFingerprint string
	ActionFingerprint   string
	ClarificationSet    string
	IntentScope         string
}

// CapabilityDecisionView 是分组所需的能力决定摘要（CapabilityDecisionV1 的投影）。
type CapabilityDecisionView struct {
	TaskKey           string
	Route             string
	MissingFieldSet   string
	ClarificationGoal string
}

// ActionLedgerView 提供按 TaskKey 的 action fingerprint。
type ActionLedgerView interface {
	ActionFingerprintForTask(taskKey string) string
}

type mapActionLedgerView map[string]string

func (m mapActionLedgerView) ActionFingerprintForTask(taskKey string) string { return m[taskKey] }

// MapActionLedgerView 从 map 构造 ActionLedgerView。
func MapActionLedgerView(items map[string]string) ActionLedgerView { return mapActionLedgerView(items) }

// FinalOutputMode 计算 Task 的最终输出模式：能力路由 × Action 状态。
func FinalOutputMode(route string, hasRealAction bool) string {
	switch route {
	case "business_handoff":
		return "handoff"
	case "tool_execute", "confirm_action":
		if hasRealAction {
			return "resource_only"
		}
		return "text"
	default:
		// knowledge_answer / clarify_required_fields / reject_unsupported /
		// social_reply / no_reply / direct_answer 均输出文本。
		return "text"
	}
}

// BuildFinalAnswerGroups 按契约 22.12 构造确定性分组（append-or-start）。
func BuildFinalAnswerGroups(
	turnID int64,
	tasks []TaskRuntimeView,
	decisions map[string]CapabilityDecisionView,
	evidence map[string]TaskEvidenceResultView,
	actions ActionLedgerView,
) []AnswerGroup {
	ordered := append([]TaskRuntimeView(nil), tasks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	groups := make([]AnswerGroup, 0, len(ordered))
	signatures := make([]AnswerGroupSignature, 0, len(ordered))
	for _, task := range ordered {
		decision := decisions[task.TaskKey]
		ev := evidence[task.TaskKey]
		actionFP := ""
		if actions != nil {
			actionFP = actions.ActionFingerprintForTask(task.TaskKey)
		}
		signature := AnswerGroupSignature{
			OutputMode:          FinalOutputMode(decision.Route, actionFP != ""),
			CapabilityRoute:     decision.Route,
			KnowledgeStatus:     ev.Status,
			EvidenceFingerprint: ev.Fingerprint,
			ActionFingerprint:   actionFP,
			ClarificationSet:    clarificationSignature(decision),
			IntentScope:         strings.TrimSpace(task.Intent) + "\x1f" + strings.TrimSpace(task.SubIntent),
		}
		groups, signatures = appendOrStartDeterministicGroup(groups, signatures, turnID, task, signature, evidence)
	}
	return groups
}

func appendOrStartDeterministicGroup(
	groups []AnswerGroup,
	signatures []AnswerGroupSignature,
	turnID int64,
	task TaskRuntimeView,
	signature AnswerGroupSignature,
	evidence map[string]TaskEvidenceResultView,
) ([]AnswerGroup, []AnswerGroupSignature) {
	// 规则 1：handoff / resource_only（真实 Tool Action）单独成组。
	if signature.OutputMode == "handoff" || signature.OutputMode == "resource_only" {
		key := AnswerGroupKey(turnID, []string{task.TaskKey}, signature)
		groups = append(groups, AnswerGroup{GroupKey: key, TaskKeys: []string{task.TaskKey}, Sequence: len(groups) + 1, OutputMode: signature.OutputMode})
		return groups, append(signatures, signature)
	}
	for index := range groups {
		if len(groups[index].TaskKeys) >= answerGroupMaxTasks {
			continue
		}
		if !sameGroupSignature(signatures[index], signature) {
			continue
		}
		if !knowledgeMergeAllowed(groups[index], task, signature, evidence) {
			continue
		}
		groups[index].TaskKeys = append(groups[index].TaskKeys, task.TaskKey)
		groups[index].GroupKey = AnswerGroupKey(turnID, groups[index].TaskKeys, signature)
		return groups, signatures
	}
	key := AnswerGroupKey(turnID, []string{task.TaskKey}, signature)
	groups = append(groups, AnswerGroup{GroupKey: key, TaskKeys: []string{task.TaskKey}, Sequence: len(groups) + 1, OutputMode: signature.OutputMode})
	return groups, append(signatures, signature)
}

func sameGroupSignature(a, b AnswerGroupSignature) bool {
	return a.OutputMode == b.OutputMode && a.CapabilityRoute == b.CapabilityRoute &&
		a.EvidenceFingerprint == b.EvidenceFingerprint && a.ActionFingerprint == b.ActionFingerprint &&
		a.ClarificationSet == b.ClarificationSet
}

// knowledgeMergeAllowed 实现规则 2 与规则 3 的合并资格（签名相等已保证
// fingerprint / 澄清集一致，这里补 intent/subIntent 与权威 FactRef 校验）。
func knowledgeMergeAllowed(group AnswerGroup, task TaskRuntimeView, signature AnswerGroupSignature, evidence map[string]TaskEvidenceResultView) bool {
	// 规则 3：no_context/unavailable/blocked 只有 intent/subIntent 一致才可合并，
	// 不能把不同未知问题用一句“资料没有”吞掉。
	if signature.KnowledgeStatus == "no_context" || signature.KnowledgeStatus == "unavailable" || signature.KnowledgeStatus == "blocked" {
		for _, key := range group.TaskKeys {
			if evidence[key].Status != signature.KnowledgeStatus {
				return false
			}
		}
		return true
	}
	// 规则 2：知识 Task 在签名 fingerprint 为空时按权威 FactRef 集合 +
	// answerability 一致合并；fingerprint 非空时由签名相等保证。
	if signature.CapabilityRoute == "knowledge_answer" && signature.EvidenceFingerprint == "" {
		ev := evidence[task.TaskKey]
		for _, key := range group.TaskKeys {
			other := evidence[key]
			if strings.Join(other.AuthoritativeFacts, ",") != strings.Join(ev.AuthoritativeFacts, ",") ||
				other.Answerability != ev.Answerability {
				return false
			}
		}
	}
	return true
}

func clarificationSignature(decision CapabilityDecisionView) string {
	return strings.Join([]string{
		strings.TrimSpace(decision.MissingFieldSet),
		strings.TrimSpace(decision.ClarificationGoal),
	}, "\x1f")
}

// AnswerGroupKey 使用 TurnID、排序后 TaskKey、输出模式、Capability route、
// Evidence/Action fingerprint 的 canonical JSON 计算（规则 6）。
func AnswerGroupKey(turnID int64, taskKeys []string, signature AnswerGroupSignature) string {
	sorted := append([]string(nil), taskKeys...)
	sort.Strings(sorted)
	canonical, err := json.Marshal(map[string]any{
		"turnID":              fmt.Sprintf("%d", turnID),
		"taskKeys":            sorted,
		"outputMode":          signature.OutputMode,
		"capabilityRoute":     signature.CapabilityRoute,
		"evidenceFingerprint": signature.EvidenceFingerprint,
		"actionFingerprint":   signature.ActionFingerprint,
	})
	if err != nil {
		canonical = []byte(strings.Join(sorted, ","))
	}
	sum := sha256.Sum256(canonical)
	return "grp_" + hex.EncodeToString(sum[:16])
}

// SelectReadyGroups 单次 Generate 最多选择前 3 个 ready group（规则 5）。
func SelectReadyGroups(groups []AnswerGroup) []AnswerGroup {
	selected := make([]AnswerGroup, 0, answerGroupMaxReadyGroups)
	for _, group := range groups {
		if len(selected) >= answerGroupMaxReadyGroups {
			break
		}
		selected = append(selected, group)
	}
	return selected
}
