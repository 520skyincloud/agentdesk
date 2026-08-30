package executor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"
	"agent-desk/internal/pkg/utils"
)

func detectRuntimeIntentForPipeline(
	ctx context.Context,
	req RunInput,
	history adapter.HistoryBuildResult,
	detector runtimeIntentModelDetector,
) (callbacks.IntentTraceData, callbacks.IntentPromptTraceData, bool, bool) {
	snapshot, ok := replyruntime.ManualResumeSnapshotFromContext(ctx)
	if !ok {
		intent, prompt, configured := detectRuntimeIntentWithModel(ctx, req, history, detector)
		return intent, prompt, configured, false
	}
	if snapshot.ContractMode != replyruntime.ManualResumeContractV2 && snapshot.ContractMode != replyruntime.ManualResumeContractLegacy {
		intent, prompt, configured := detectRuntimeIntentWithModel(ctx, req, history, detector)
		return intent, prompt, configured, false
	}

	frozenTasks := manualResumeFrozenIntentTasks(snapshot.FrozenTasks, snapshot.ContractMode)
	if len(frozenTasks) != len(snapshot.FrozenTasks) {
		intent, prompt, configured := detectRuntimeIntentWithModel(ctx, req, history, detector)
		return intent, prompt, configured, false
	}
	newIntent := callbacks.IntentTraceData{}
	newIntentContractV2 := true
	if len(snapshot.NewSources) > 0 {
		newReq := req
		newReq.UserMessage = manualResumeIntentMessage(req.UserMessage, snapshot.NewSources)
		var configured bool
		newIntent, _, configured = detectRuntimeIntentWithModel(ctx, newReq, history, detector)
		if !configured {
			return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false, true
		}
		newIntent.IntentTasks = remapManualResumeNewTaskSourceRefs(newIntent.IntentTasks, snapshot.NewSources)
		newIntentContractV2 = newIntent.SemanticContractExpected && newIntent.SourceRefsValidated
	}

	combinedTasks, merged := mergeManualResumeIntentTasks(frozenTasks, newIntent.IntentTasks)
	if !merged {
		combinedTasks = mergeManualResumeIntentTasksWithClarification(frozenTasks, newIntent.IntentTasks)
	}
	sort.SliceStable(combinedTasks, func(left, right int) bool {
		return manualResumeTaskPrimarySourceIndex(combinedTasks[left]) < manualResumeTaskPrimarySourceIndex(combinedTasks[right])
	})
	v2Contract := snapshot.ContractMode == replyruntime.ManualResumeContractV2 && newIntentContractV2
	intent := callbacks.IntentTraceData{
		IntentConfidence:         1,
		ShouldReply:              true,
		IntentTasks:              combinedTasks,
		SemanticContractExpected: v2Contract,
		SourceRefsValidated:      v2Contract && snapshot.SourcesValidated,
		Reason:                   fmt.Sprintf("manual resume reused frozen task plan from run log %d", snapshot.RunLogID),
	}
	intent = normalizeModelOwnedIntentTaskActions(intent)
	intent = deriveModelIntentFromTasks(intent)
	intent.DetectedIntent = intent.PrimaryIntent
	intent.MatchedIntentCode = intent.PrimaryIntent
	if strings.TrimSpace(newIntent.Reason) != "" {
		intent.Reason = appendIntentReason(intent.Reason, "new manual-wait source intent: "+newIntent.Reason)
	}
	return intent, promptForModelDetectedIntent(intent, loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))), true, true
}

func manualResumeFrozenIntentTasks(plans []replyruntime.ManualResumeTaskPlan, contractMode string) []callbacks.IntentTaskTraceData {
	ret := make([]callbacks.IntentTaskTraceData, 0, len(plans))
	for _, plan := range plans {
		intent := canonicalIntentCode(plan.Intent)
		if intent == "" {
			continue
		}
		text := strings.TrimSpace(plan.OriginalText)
		if text == "" {
			text = strings.TrimSpace(plan.Text)
		}
		resolvedText := strings.TrimSpace(plan.ResolvedText)
		if resolvedText == "" {
			resolvedText = strings.TrimSpace(plan.Text)
		}
		if resolvedText == "" {
			resolvedText = text
		}
		task := callbacks.IntentTaskTraceData{
			Intent:             intent,
			SubIntent:          strings.TrimSpace(plan.SubIntent),
			Objective:          semanticGateNormalizeObjective(plan.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(plan.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(plan.ResolutionState),
			Entities:           manualResumeIntentEntities(plan.Entities),
			Text:               text,
			ResolvedText:       resolvedText,
			SourceRefs:         normalizeRuntimeIntentSourceRefs(plan.SourceRefs),
			NeedsKnowledge:     plan.NeedsKnowledge,
			NeedsResource:      plan.NeedsResource,
			NeedsTool:          plan.NeedsTool,
			NeedsHumanRoute:    plan.NeedsHumanRoute,
			ResourceAction:     strings.TrimSpace(plan.ResourceAction),
		}
		if contractMode == replyruntime.ManualResumeContractLegacy {
			switch intent {
			case "hotel_info", "service_request":
				task.NeedsKnowledge = true
			case "hotel_variable":
				task.NeedsResource = true
			case "human_complaint_risk":
				task.NeedsHumanRoute = true
			}
		}
		if task.ResourceAction != "" {
			task.NeedsResource = true
		}
		ret = append(ret, task)
	}
	return ret
}

func manualResumeIntentEntities(items []replyruntime.ManualResumeEntity) []callbacks.IntentEntityTraceData {
	ret := make([]callbacks.IntentEntityTraceData, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		entityType := strings.TrimSpace(item.Type)
		if text == "" && entityType == "" {
			continue
		}
		ret = append(ret, callbacks.IntentEntityTraceData{Text: text, Type: entityType})
	}
	return ret
}

type manualResumeIntentMergeEntry struct {
	task   callbacks.IntentTaskTraceData
	frozen bool
}

func mergeManualResumeIntentTasks(frozenTasks []callbacks.IntentTaskTraceData, newTasks []callbacks.IntentTaskTraceData) ([]callbacks.IntentTaskTraceData, bool) {
	return mergeManualResumeIntentTasksWithMode(frozenTasks, newTasks, false)
}

func mergeManualResumeIntentTasksWithClarification(frozenTasks []callbacks.IntentTaskTraceData, newTasks []callbacks.IntentTaskTraceData) []callbacks.IntentTaskTraceData {
	ret, _ := mergeManualResumeIntentTasksWithMode(frozenTasks, newTasks, true)
	return ret
}

func mergeManualResumeIntentTasksWithMode(frozenTasks []callbacks.IntentTaskTraceData, newTasks []callbacks.IntentTaskTraceData, clarifyUnmatched bool) ([]callbacks.IntentTaskTraceData, bool) {
	entries := make([]manualResumeIntentMergeEntry, 0, len(frozenTasks)+len(newTasks))
	for _, task := range frozenTasks {
		entries = append(entries, manualResumeIntentMergeEntry{task: cloneManualResumeIntentTask(task), frozen: true})
	}
	for _, incoming := range newTasks {
		incoming = cloneManualResumeIntentTask(incoming)
		if index := manualResumeEquivalentTaskIndex(entries, incoming); index == -2 {
			return nil, false
		} else if index >= 0 {
			incoming.SourceRefs = mergeReplyTaskSourceRefs(entries[index].task.SourceRefs, incoming.SourceRefs)
			entries[index].task = incoming
			continue
		}

		relation := semanticGateNormalizeRelation(incoming.RelationToPrevious)
		switch relation {
		case "clarification_answer", "modify_previous", "correction", "cancel_previous", "answer_rejected":
			index, matched := manualResumeUniqueFrozenTaskIndex(entries, incoming)
			if !matched {
				if !clarifyUnmatched {
					return nil, false
				}
				entries = manualResumeDropFrozenIntentEntries(entries)
				incoming = semanticGateClarificationTask(incoming)
				incoming.RelationToPrevious = relation
				incoming.ResolutionState = runtimeIntentResolutionAmbiguous
				entries = append(entries, manualResumeIntentMergeEntry{task: incoming})
				continue
			}
			previousRefs := append([]string(nil), entries[index].task.SourceRefs...)
			if relation == "cancel_previous" {
				entries = append(entries[:index], entries[index+1:]...)
				incoming.SourceRefs = mergeReplyTaskSourceRefs(incoming.SourceRefs, previousRefs)
				entries = append(entries, manualResumeIntentMergeEntry{task: incoming})
				continue
			}
			incoming.SourceRefs = mergeReplyTaskSourceRefs(incoming.SourceRefs, previousRefs)
			entries[index].task = incoming
		default:
			entries = append(entries, manualResumeIntentMergeEntry{task: incoming})
		}
	}

	ret := make([]callbacks.IntentTaskTraceData, 0, len(entries))
	for _, entry := range entries {
		ret = append(ret, cloneManualResumeIntentTask(entry.task))
	}
	return ret, true
}

func manualResumeDropFrozenIntentEntries(entries []manualResumeIntentMergeEntry) []manualResumeIntentMergeEntry {
	ret := make([]manualResumeIntentMergeEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.frozen {
			ret = append(ret, entry)
		}
	}
	return ret
}

func cloneManualResumeIntentTask(task callbacks.IntentTaskTraceData) callbacks.IntentTaskTraceData {
	ret := task
	ret.Entities = append([]callbacks.IntentEntityTraceData(nil), task.Entities...)
	ret.SourceRefs = append([]string(nil), task.SourceRefs...)
	return ret
}

func manualResumeEquivalentTaskIndex(entries []manualResumeIntentMergeEntry, task callbacks.IntentTaskTraceData) int {
	matchedIndex := -1
	for index, entry := range entries {
		if manualResumeIntentTasksEquivalent(entry.task, task) {
			if matchedIndex >= 0 {
				return -2
			}
			matchedIndex = index
		}
	}
	return matchedIndex
}

func manualResumeIntentTasksEquivalent(left callbacks.IntentTaskTraceData, right callbacks.IntentTaskTraceData) bool {
	return canonicalIntentCode(left.Intent) == canonicalIntentCode(right.Intent) &&
		strings.EqualFold(strings.TrimSpace(left.SubIntent), strings.TrimSpace(right.SubIntent)) &&
		semanticGateNormalizeObjective(left.Objective) == semanticGateNormalizeObjective(right.Objective) &&
		normalizeRuntimeIntentProtocolAtomicText(left.Text) == normalizeRuntimeIntentProtocolAtomicText(right.Text) &&
		normalizeRuntimeIntentProtocolAtomicText(left.ResolvedText) == normalizeRuntimeIntentProtocolAtomicText(right.ResolvedText) &&
		manualResumeIntentEntitiesEqual(left.Entities, right.Entities) &&
		left.NeedsKnowledge == right.NeedsKnowledge &&
		left.NeedsResource == right.NeedsResource &&
		left.NeedsTool == right.NeedsTool &&
		left.NeedsHumanRoute == right.NeedsHumanRoute &&
		strings.TrimSpace(left.ResourceAction) == strings.TrimSpace(right.ResourceAction)
}

func manualResumeIntentEntitiesEqual(left []callbacks.IntentEntityTraceData, right []callbacks.IntentEntityTraceData) bool {
	if len(left) != len(right) {
		return false
	}
	remaining := make(map[string]int, len(left))
	for _, item := range left {
		remaining[manualResumeIntentEntityKey(item)]++
	}
	for _, item := range right {
		key := manualResumeIntentEntityKey(item)
		if remaining[key] == 0 {
			return false
		}
		remaining[key]--
	}
	return true
}

func manualResumeIntentEntityKey(item callbacks.IntentEntityTraceData) string {
	return strings.ToLower(strings.TrimSpace(item.Type)) + ":" + normalizeRuntimeKnowledgeQuery(item.Text)
}

func manualResumeUniqueFrozenTaskIndex(entries []manualResumeIntentMergeEntry, incoming callbacks.IntentTaskTraceData) (int, bool) {
	frozenIndexes := make([]int, 0, len(entries))
	for index, entry := range entries {
		if entry.frozen {
			frozenIndexes = append(frozenIndexes, index)
		}
	}
	if len(frozenIndexes) == 1 {
		return frozenIndexes[0], true
	}

	bestIndex := -1
	bestScore := 0
	bestCount := 0
	for _, index := range frozenIndexes {
		score, compatible := manualResumeFrozenTaskMatchScore(entries[index].task, incoming)
		if !compatible || score <= 0 {
			continue
		}
		switch {
		case score > bestScore:
			bestIndex = index
			bestScore = score
			bestCount = 1
		case score == bestScore:
			bestCount++
		}
	}
	return bestIndex, bestIndex >= 0 && bestCount == 1
}

func manualResumeFrozenTaskMatchScore(frozen callbacks.IntentTaskTraceData, incoming callbacks.IntentTaskTraceData) (int, bool) {
	frozenIntent := canonicalIntentCode(frozen.Intent)
	incomingIntent := canonicalIntentCode(incoming.Intent)
	if incomingIntent != "" && incomingIntent != "interaction" && frozenIntent != incomingIntent {
		return 0, false
	}
	if left, right := strings.TrimSpace(frozen.ResourceAction), strings.TrimSpace(incoming.ResourceAction); left != "" && right != "" && left != right {
		return 0, false
	}

	score := 0
	if incomingIntent != "" && frozenIntent == incomingIntent {
		score += 4
	}
	if subIntent := strings.TrimSpace(incoming.SubIntent); subIntent != "" && strings.EqualFold(strings.TrimSpace(frozen.SubIntent), subIntent) {
		score += 4
	}
	if objective := semanticGateNormalizeObjective(incoming.Objective); objective != "" && objective == semanticGateNormalizeObjective(frozen.Objective) {
		score += 2
	}
	if normalizeRuntimeIntentProtocolAtomicText(incoming.ResolvedText) != "" &&
		normalizeRuntimeIntentProtocolAtomicText(incoming.ResolvedText) == normalizeRuntimeIntentProtocolAtomicText(frozen.ResolvedText) {
		score += 8
	}
	if manualResumeIntentEntitiesOverlap(frozen.Entities, incoming.Entities) {
		score += 6
	}
	if manualResumeSourceRefsOverlap(frozen.SourceRefs, incoming.SourceRefs) {
		score += 8
	}
	if action := strings.TrimSpace(incoming.ResourceAction); action != "" && action == strings.TrimSpace(frozen.ResourceAction) {
		score += 6
	}
	return score, true
}

func manualResumeIntentEntitiesOverlap(left []callbacks.IntentEntityTraceData, right []callbacks.IntentEntityTraceData) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		seen[manualResumeIntentEntityKey(item)] = struct{}{}
	}
	for _, item := range right {
		if _, ok := seen[manualResumeIntentEntityKey(item)]; ok {
			return true
		}
	}
	return false
}

func manualResumeSourceRefsOverlap(left []string, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, ref := range left {
		if ref = strings.ToUpper(strings.TrimSpace(ref)); ref != "" {
			seen[ref] = struct{}{}
		}
	}
	for _, ref := range right {
		if _, ok := seen[strings.ToUpper(strings.TrimSpace(ref))]; ok {
			return true
		}
	}
	return false
}

func manualResumeIntentMessage(base models.Message, sources []replyruntime.ManualResumeSource) models.Message {
	parts := make([]string, 0, len(sources))
	for index, source := range sources {
		text := strings.TrimSpace(source.Text)
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. [%s%d] %s", index+1, manualResumeIntentSourceLabel(source.MessageType), source.MessageID, text))
	}
	ret := base
	ret.MessageType = enums.IMMessageTypeText
	ret.Payload = ""
	ret.Content = utils.BuildRuntimeCustomerBurstEnvelope(parts)
	return ret
}

func manualResumeIntentSourceLabel(messageType string) string {
	switch enums.IMMessageType(strings.TrimSpace(messageType)) {
	case enums.IMMessageTypeImage:
		return "图片"
	case enums.IMMessageTypeVoice:
		return "语音"
	case enums.IMMessageTypeAttachment:
		return "文件"
	case enums.IMMessageTypeLocation:
		return "定位"
	case enums.IMMessageTypeMiniProgram:
		return "小程序"
	case enums.IMMessageTypeGIF:
		return "表情"
	default:
		return "文字"
	}
}

func remapManualResumeNewTaskSourceRefs(tasks []callbacks.IntentTaskTraceData, sources []replyruntime.ManualResumeSource) []callbacks.IntentTaskTraceData {
	ret := append([]callbacks.IntentTaskTraceData(nil), tasks...)
	for taskIndex := range ret {
		ret[taskIndex].SourceRefs = append([]string(nil), ret[taskIndex].SourceRefs...)
		for refIndex, ref := range ret[taskIndex].SourceRefs {
			sourceIndex := runtimeIntentSourceRefIndex(ref)
			if sourceIndex < 0 || sourceIndex >= len(sources) {
				continue
			}
			if mapped := strings.ToUpper(strings.TrimSpace(sources[sourceIndex].Ref)); mapped != "" {
				ret[taskIndex].SourceRefs[refIndex] = mapped
			}
		}
	}
	return ret
}

func manualResumeTaskPrimarySourceIndex(task callbacks.IntentTaskTraceData) int {
	if len(task.SourceRefs) == 0 {
		return int(^uint(0) >> 1)
	}
	index := runtimeIntentSourceRefIndex(task.SourceRefs[0])
	if index < 0 {
		return int(^uint(0) >> 1)
	}
	return index
}

func restoreManualResumeFrozenTaskIDs(ctx context.Context, plan callbacks.ReplyPlanTraceData) callbacks.ReplyPlanTraceData {
	snapshot, ok := replyruntime.ManualResumeSnapshotFromContext(ctx)
	if !ok || len(snapshot.FrozenTasks) == 0 {
		return plan
	}
	matchedFrozen := make([]bool, len(plan.TaskPlans))
	usedIDs := make(map[string]struct{}, len(snapshot.FrozenTasks))
	for _, frozen := range snapshot.FrozenTasks {
		matchedIndex := -1
		metadataMatch := false
		for index := range plan.TaskPlans {
			if matchedFrozen[index] || !manualResumeTaskPlanMatchesFrozen(plan.TaskPlans[index], frozen) {
				continue
			}
			matchedIndex = index
			metadataMatch = true
			break
		}
		if matchedIndex < 0 {
			matchedIndex = manualResumeExpandedSourcePlanIndex(plan.TaskPlans, matchedFrozen, frozen)
			metadataMatch = matchedIndex >= 0
		}
		if matchedIndex < 0 {
			matchedIndex = manualResumeReplacementPlanIndex(plan.TaskPlans, matchedFrozen, frozen)
		}
		if matchedIndex < 0 {
			continue
		}
		matchedFrozen[matchedIndex] = true
		if taskID := strings.TrimSpace(frozen.TaskID); taskID != "" {
			if _, exists := usedIDs[taskID]; !exists {
				plan.TaskPlans[matchedIndex].TaskID = taskID
				usedIDs[taskID] = struct{}{}
			}
		}
		if metadataMatch {
			restoreManualResumeFrozenTaskMetadata(&plan.TaskPlans[matchedIndex], frozen, snapshot.ContractMode)
		}
	}
	taskIndex := 0
	contextIndex := 0
	for index := range plan.TaskPlans {
		if matchedFrozen[index] && strings.TrimSpace(plan.TaskPlans[index].TaskID) != "" {
			continue
		}
		prefix := "task-"
		if plan.TaskPlans[index].OutputKind == "context_only" {
			prefix = "context-"
		}
		for {
			if prefix == "context-" {
				contextIndex++
				plan.TaskPlans[index].TaskID = fmt.Sprintf("%s%d", prefix, contextIndex)
			} else {
				taskIndex++
				plan.TaskPlans[index].TaskID = fmt.Sprintf("%s%d", prefix, taskIndex)
			}
			if _, exists := usedIDs[plan.TaskPlans[index].TaskID]; !exists {
				usedIDs[plan.TaskPlans[index].TaskID] = struct{}{}
				break
			}
		}
	}
	return plan
}

func manualResumeTaskPlanMatchesFrozen(plan callbacks.ReplyTaskPlanTraceData, frozen replyruntime.ManualResumeTaskPlan) bool {
	if canonicalIntentCode(plan.Intent) != canonicalIntentCode(frozen.Intent) {
		return false
	}
	if strings.TrimSpace(plan.SubIntent) != strings.TrimSpace(frozen.SubIntent) ||
		semanticGateNormalizeObjective(plan.Objective) != semanticGateNormalizeObjective(frozen.Objective) ||
		semanticGateNormalizeRelation(plan.RelationToPrevious) != semanticGateNormalizeRelation(frozen.RelationToPrevious) ||
		semanticGateNormalizeResolution(plan.ResolutionState) != semanticGateNormalizeResolution(frozen.ResolutionState) ||
		normalizeRuntimeIntentProtocolAtomicText(plan.OriginalText) != normalizeRuntimeIntentProtocolAtomicText(firstNonEmptyManualResumeText(frozen.OriginalText, frozen.Text)) ||
		normalizeRuntimeIntentProtocolAtomicText(plan.ResolvedText) != normalizeRuntimeIntentProtocolAtomicText(firstNonEmptyManualResumeText(frozen.ResolvedText, frozen.Text)) ||
		strings.TrimSpace(plan.ResourceAction) != strings.TrimSpace(frozen.ResourceAction) ||
		!manualResumeReplyPlanEntitiesEqual(plan.Entities, frozen.Entities) ||
		!manualResumeNormalizedSourceRefsEqual(plan.SourceRefs, frozen.SourceRefs) {
		return false
	}
	if frozen.NeedsKnowledge || frozen.NeedsResource || frozen.NeedsTool || frozen.NeedsHumanRoute {
		return plan.NeedsKnowledge == frozen.NeedsKnowledge &&
			plan.NeedsResource == frozen.NeedsResource &&
			plan.NeedsTool == frozen.NeedsTool &&
			plan.NeedsHumanRoute == frozen.NeedsHumanRoute
	}
	return true
}

func restoreManualResumeFrozenTaskMetadata(plan *callbacks.ReplyTaskPlanTraceData, frozen replyruntime.ManualResumeTaskPlan, contractMode string) {
	if plan == nil {
		return
	}
	if len(frozen.Entities) > 0 {
		plan.Entities = manualResumeIntentEntities(frozen.Entities)
	}
	if contractMode == replyruntime.ManualResumeContractV2 || frozen.NeedsKnowledge || frozen.NeedsResource || frozen.NeedsTool || frozen.NeedsHumanRoute {
		plan.NeedsKnowledge = frozen.NeedsKnowledge
		plan.NeedsResource = frozen.NeedsResource
		plan.NeedsTool = frozen.NeedsTool
		plan.NeedsHumanRoute = frozen.NeedsHumanRoute
	}
	if value := strings.TrimSpace(frozen.OutputKind); value != "" {
		plan.OutputKind = value
		plan.ReplyRequired = frozen.ReplyRequired
	}
	if value := strings.TrimSpace(frozen.Output); value != "" {
		plan.Output = value
	}
	if value := strings.TrimSpace(frozen.ResourceAction); value != "" {
		plan.ResourceAction = value
	}
	plan.MissingAspects = append([]string(nil), frozen.MissingAspects...)
}

func manualResumeExpandedSourcePlanIndex(plans []callbacks.ReplyTaskPlanTraceData, used []bool, frozen replyruntime.ManualResumeTaskPlan) int {
	matchedIndex := -1
	for index, plan := range plans {
		if index < len(used) && used[index] {
			continue
		}
		if !manualResumeTaskPlanMatchesFrozenIgnoringSources(plan, frozen) || !manualResumeSourceRefsContain(plan.SourceRefs, frozen.SourceRefs) {
			continue
		}
		if matchedIndex >= 0 {
			return -1
		}
		matchedIndex = index
	}
	return matchedIndex
}

func manualResumeTaskPlanMatchesFrozenIgnoringSources(plan callbacks.ReplyTaskPlanTraceData, frozen replyruntime.ManualResumeTaskPlan) bool {
	copyPlan := plan
	copyPlan.SourceRefs = append([]string(nil), frozen.SourceRefs...)
	copyPlan.RelationToPrevious = frozen.RelationToPrevious
	copyPlan.ResolutionState = frozen.ResolutionState
	return manualResumeTaskPlanMatchesFrozen(copyPlan, frozen)
}

func manualResumeSourceRefsContain(all []string, subset []string) bool {
	seen := make(map[string]struct{}, len(all))
	for _, ref := range normalizeRuntimeIntentSourceRefs(all) {
		seen[ref] = struct{}{}
	}
	for _, ref := range normalizeRuntimeIntentSourceRefs(subset) {
		if _, ok := seen[ref]; !ok {
			return false
		}
	}
	return len(subset) > 0
}

func manualResumeReplacementPlanIndex(plans []callbacks.ReplyTaskPlanTraceData, used []bool, frozen replyruntime.ManualResumeTaskPlan) int {
	bestIndex := -1
	bestScore := 0
	bestCount := 0
	for index, plan := range plans {
		if index < len(used) && used[index] {
			continue
		}
		relation := semanticGateNormalizeRelation(plan.RelationToPrevious)
		switch relation {
		case "clarification_answer", "modify_previous", "correction", "answer_rejected":
		default:
			continue
		}
		score := 0
		if manualResumeSourceRefsOverlap(plan.SourceRefs, frozen.SourceRefs) {
			score += 8
		}
		if canonicalIntentCode(plan.Intent) == canonicalIntentCode(frozen.Intent) {
			score += 4
		}
		if strings.EqualFold(strings.TrimSpace(plan.SubIntent), strings.TrimSpace(frozen.SubIntent)) {
			score += 4
		}
		if semanticGateNormalizeObjective(plan.Objective) == semanticGateNormalizeObjective(frozen.Objective) {
			score += 2
		}
		if manualResumeReplyPlanEntitiesOverlap(plan.Entities, frozen.Entities) {
			score += 6
		}
		if score <= 0 {
			continue
		}
		switch {
		case score > bestScore:
			bestIndex = index
			bestScore = score
			bestCount = 1
		case score == bestScore:
			bestCount++
		}
	}
	if bestCount != 1 {
		return -1
	}
	return bestIndex
}

func manualResumeReplyPlanEntitiesEqual(left []callbacks.IntentEntityTraceData, right []replyruntime.ManualResumeEntity) bool {
	return manualResumeIntentEntitiesEqual(left, manualResumeIntentEntities(right))
}

func manualResumeReplyPlanEntitiesOverlap(left []callbacks.IntentEntityTraceData, right []replyruntime.ManualResumeEntity) bool {
	return manualResumeIntentEntitiesOverlap(left, manualResumeIntentEntities(right))
}

func manualResumeNormalizedSourceRefsEqual(left []string, right []string) bool {
	left = normalizeRuntimeIntentSourceRefs(left)
	right = normalizeRuntimeIntentSourceRefs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func firstNonEmptyManualResumeText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
