package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// 契约 15.3：ResolveReplyPart 消除模型漏回显 Evidence/Action 引用造成的
// missing_task_evidence 技术失败。GroundingEvidenceRefs 表示服务端为该组
// 提供并允许使用的支撑集合，不声称模型逐字使用了每一条 Evidence。

// ResolvedReplyPart 是服务端解析后的输出分段。
type ResolvedReplyPart struct {
	GroupKey              string
	TaskKeys              []string
	Content               string
	GroundingEvidenceRefs []string
	ResolvedActionRefs    []string
	ValidationCodes       []string
}

// ResolveReplyPart 用 Plan 中该组的 Task 引用解析证据与动作引用。
func ResolveReplyPart(plan contracts.ReplyPlanV4, part contracts.ReplyPartV3) ResolvedReplyPart {
	tasks := tasksForGroup(plan, part.GroupKey)
	resolved := ResolvedReplyPart{
		GroupKey:              strings.TrimSpace(part.GroupKey),
		TaskKeys:              uniqueStrings(part.TaskKeys),
		Content:               strings.TrimSpace(part.Content),
		GroundingEvidenceRefs: unionTaskEvidenceRefs(tasks),
		ResolvedActionRefs:    unionTaskActionRefs(tasks),
	}
	// 模型回显的 taskKeys 必须是组内成员；多余引用直接丢弃。
	groupKeys := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		groupKeys[task.TaskKey] = struct{}{}
	}
	filtered := resolved.TaskKeys[:0]
	for _, key := range resolved.TaskKeys {
		if _, ok := groupKeys[key]; ok {
			filtered = append(filtered, key)
		}
	}
	resolved.TaskKeys = filtered
	if len(resolved.TaskKeys) == 0 {
		for _, task := range tasks {
			resolved.TaskKeys = append(resolved.TaskKeys, task.TaskKey)
		}
	}
	return resolved
}

func tasksForGroup(plan contracts.ReplyPlanV4, groupKey string) []contracts.ReplyPlanTaskV4 {
	members := map[string]struct{}{}
	for _, group := range plan.ReplyGroups {
		if group.GroupKey == groupKey {
			for _, key := range group.TaskKeys {
				members[key] = struct{}{}
			}
		}
	}
	if len(members) == 0 {
		return nil
	}
	tasks := make([]contracts.ReplyPlanTaskV4, 0, len(members))
	for _, task := range plan.Tasks {
		if _, ok := members[task.TaskKey]; ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func unionTaskEvidenceRefs(tasks []contracts.ReplyPlanTaskV4) []string {
	refs := make([]string, 0, len(tasks)*4)
	for _, task := range tasks {
		refs = append(refs, task.EvidenceRefs...)
		refs = append(refs, task.RequiredFactRefs...)
	}
	return uniqueStrings(refs)
}

func unionTaskActionRefs(tasks []contracts.ReplyPlanTaskV4) []string {
	refs := make([]string, 0, len(tasks)*2)
	for _, task := range tasks {
		refs = append(refs, task.ActionRefs...)
	}
	return uniqueStrings(refs)
}

func uniqueStrings(items []string) []string {
	ret := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
	}
	return ret
}
