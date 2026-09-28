package executor

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/jev"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func addJevGoalSlotQuestions(spans []jevIntentSpan, questions map[string]jev.Question, contexts map[string]jevIntentContext) {
	hasRoomContext := false
	for _, candidate := range contexts {
		hasRoomContext = hasRoomContext || runtimePMSTaskRetainsTargetRoomType(candidate.SubIntent) ||
			runtimeIntentEntityValue(candidate.Entities, runtimeIntentEntityTargetRoomType) != ""
	}
	for spanIndex, span := range spans {
		questions[span.Ref+"_scope"] = jev.Question{
			Type: "choice",
			Instructions: map[string]any{
				"taskRef": span.Ref, "currentText": span.Text,
				"question": "Select the customer's business object and time scope independently of the route. 上次/最近一次/上一回 means latest_history, INCLUDING when correcting the phone. A named-room follow-up such as 那就看看沐阳吧，有房吗 after an ongoing room-change request is inherit_context: preserve its stay dates and goal even when the prior answer failed. Choose room_candidates only for an independent room search, not to discard an active stay. An explicit switch to this/current reservation is current_order.",
			},
			Criteria: map[string]any{
				"inherit_context":                "Continue the business object and temporal scope of the explicitly selected prior context.",
				"latest_history":                 "The most recent historical stay for the supplied/corrected identity, selected by actual stay time.",
				runtimeSubjectHistoricalOrder:    "Historical stays without a unique latest/date/order selection.",
				runtimeSubjectCurrentOrder:       "This/current/active reservation or order.",
				runtimeSubjectCurrentStay:        "An ongoing stay adjustment, room change, upgrade, renewal or related assessment.",
				runtimeSubjectRoomCandidates:     "An independent room/inventory search, not a continuation of an existing stay decision.",
				runtimeSubjectPersonalMembership: "This customer's own membership identity or eligibility.",
				runtimeSubjectPublicMembership:   "Public hotel membership tiers or named-tier rules, not this customer's own record.",
				runtimeSubjectPublicPolicy:       "General hotel policy or knowledge.",
				"none":                           "No business object applies.",
			},
		}
		targets := map[string]any{
			"none":    "No target applies, or the customer rejects the previous named selection. Supplying a phone/date or asking a price for the same goal retains its target using the prior reference, not none.",
			"current": "This task explicitly names a room/room type the customer wants to inspect or select. A pronoun such as 这个/那个 alone is NOT a name.",
		}
		for ref, candidate := range contexts {
			if candidate.DateValue != "" || candidate.Text == "" || ref == span.Ref {
				continue
			}
			laterCurrentTask := false
			for _, later := range spans[spanIndex:] {
				laterCurrentTask = laterCurrentTask || later.Ref == ref
			}
			if laterCurrentTask {
				continue
			}
			targets[ref] = "Use the room target from this explicitly selected prior customer context: " + candidate.Text
		}
		if hasRoomContext {
			questions[span.Ref+"_target_ref"] = jev.Question{
				Type: "choice",
				Instructions: map[string]any{
					"taskRef": span.Ref, "currentText": span.Text,
					"question": "Where is the named room/room-type target for THIS task? Resolve 换成这个需要补多少/那间/这个房型 to the prior named selection; never treat the rest of a price question as a room name. 那就看看沐阳吧 names 沐阳 in current text even before the hotel recommends anything. No PMS IDs may be invented.",
				},
				Criteria: targets,
			}
		}
		seenDates := map[string]bool{}
		for index, mention := range runtimePMSDateMentions(span.Text, time.Now().In(runtimeHotelLocation())) {
			if seenDates[mention.date] {
				continue
			}
			seenDates[mention.date] = true
			key := fmt.Sprintf("%s_date_%d_role", span.Ref, index+1)
			contexts[key] = jevIntentContext{DateValue: mention.date}
			questions[key] = jev.Question{
				Type: "choice",
				Instructions: map[string]any{
					"taskRef": span.Ref, "currentText": span.Text, "date": mention.date,
					"question": "What role does this date play in the customer's requested stay interval? 住到/离开/退房 on a date is an end date in ALL scenarios, including room changes, not a new arrival date. A corrected or rejected old date is ignored. Do not turn mentioned dates into room availability facts.",
				},
				Criteria: map[string]any{
					"start":   "The requested arrival/start of occupancy or effective room change.",
					"end":     "The requested departure/end of occupancy.",
					"ignored": "A rejected/old/example/non-stay date, or not a uniquely understood stay interval value.",
				},
			}
		}
	}
}

func runtimeHotelLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}

func applyJevTaskGoalSlots(task *callbacks.IntentTaskTraceData, span jevIntentSpan, response jev.Response, contexts map[string]jevIntentContext) error {
	scope := response.Answers[span.Ref+"_scope"].Choice
	switch scope {
	case "latest_history":
		task.SubjectScope = runtimeSubjectHistoricalOrder
		setRuntimeIntentEntity(&task.Entities, runtimeIntentEntityHistoryChoice, "latest")
	case runtimeSubjectHistoricalOrder, runtimeSubjectCurrentOrder, runtimeSubjectCurrentStay,
		runtimeSubjectRoomCandidates, runtimeSubjectPersonalMembership, runtimeSubjectPublicMembership, runtimeSubjectPublicPolicy:
		task.SubjectScope = scope
	case "inherit_context", "none":
	default:
		return fmt.Errorf("jev missing/invalid customer subject scope")
	}
	if task.SubjectScope == runtimeSubjectHistoricalOrder && isPMSRuntimeSubIntent(task.SubIntent) &&
		!isMemberRuntimeSubIntent(task.SubIntent) {
		task.SubIntent = "order_query"
	}
	if task.SubjectScope != runtimeSubjectHistoricalOrder || scope == runtimeSubjectHistoricalOrder {
		retained := make([]callbacks.IntentEntityTraceData, 0, len(task.Entities))
		for _, entity := range task.Entities {
			if entity.Type != runtimeIntentEntityHistoryChoice {
				retained = append(retained, entity)
			}
		}
		task.Entities = retained
	}
	target := response.Answers[span.Ref+"_target_ref"].Choice
	if target == "" {
		target = "none"
		if task.DialogueAct == "selection" && runtimePMSTaskRetainsTargetRoomType(task.SubIntent) &&
			runtimePMSRoomKeyword(callbacks.ReplyTaskPlanTraceData{OriginalText: span.Text}) == "" {
			target = "current"
		}
	}
	switch target {
	case "none":
		if response.Answers[span.Ref+"_target_ref"].Choice != "" {
			retained := make([]callbacks.IntentEntityTraceData, 0, len(task.Entities))
			for _, entity := range task.Entities {
				if entity.Type != runtimeIntentEntityTargetRoomType {
					retained = append(retained, entity)
				}
			}
			task.Entities = retained
			task.SelectionSource, task.SelectionRef = "", ""
		}
	case "current":
		setRuntimeIntentEntity(&task.Entities, runtimeIntentEntityTargetRoomType, span.Text)
		task.SelectionSource, task.SelectionRef = "customer", span.SourceRef
	default:
		previous, valid := contexts[target]
		if !valid || previous.DateValue != "" {
			return fmt.Errorf("jev missing/invalid room target reference")
		}
		room := firstNonEmptyReplyTaskText(
			runtimeIntentEntityValue(previous.Entities, runtimeIntentEntityTargetRoomType), previous.Text,
		)
		setRuntimeIntentEntity(&task.Entities, runtimeIntentEntityTargetRoomType, room)
		task.SelectionSource, task.SelectionRef = firstNonEmptyReplyTaskText(previous.SelectionSource, "customer"), target
	}
	dateValues := map[string][]string{}
	for key, candidate := range contexts {
		if candidate.DateValue == "" || !strings.HasPrefix(key, span.Ref+"_date_") {
			continue
		}
		role := response.Answers[key].Choice
		switch role {
		case "start":
			dateValues[runtimeIntentEntityStayStartDate] = appendIfMissing(dateValues[runtimeIntentEntityStayStartDate], candidate.DateValue)
		case "end":
			dateValues[runtimeIntentEntityStayEndDate] = appendIfMissing(dateValues[runtimeIntentEntityStayEndDate], candidate.DateValue)
		case "ignored":
		default:
			return fmt.Errorf("jev missing/invalid stay date role")
		}
	}
	for _, role := range []string{runtimeIntentEntityStayStartDate, runtimeIntentEntityStayEndDate} {
		values := dateValues[role]
		if len(values) > 1 {
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			*task = semanticGateClarificationTask(*task)
			return nil
		}
		if len(values) == 1 {
			setRuntimeIntentEntity(&task.Entities, role, values[0])
		}
	}
	return nil
}
