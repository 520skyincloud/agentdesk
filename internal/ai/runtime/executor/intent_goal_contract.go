package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

const (
	runtimeSubjectPublicPolicy       = "public_policy"
	runtimeSubjectPublicMembership   = "public_membership"
	runtimeSubjectPersonalMembership = "personal_membership"
	runtimeSubjectCurrentOrder       = "current_order"
	runtimeSubjectHistoricalOrder    = "historical_order"
	runtimeSubjectCurrentStay        = "current_stay"
	runtimeSubjectRoomCandidates     = "room_candidates"
)

func jevTaskSubjectScope(route string) string {
	switch strings.TrimSpace(route) {
	case "member_program":
		return runtimeSubjectPublicMembership
	case "member_info", "member_benefits":
		return runtimeSubjectPersonalMembership
	case "order_history":
		return runtimeSubjectHistoricalOrder
	case "order_query", "order_detail", "order_status":
		return runtimeSubjectCurrentOrder
	case "room_change", "room_upgrade", "room_assignment", "upgrade_eligibility",
		"price_difference", "renewal", "late_checkout", "order_price_dispute":
		return runtimeSubjectCurrentStay
	case "room_status", "room_inventory":
		return runtimeSubjectRoomCandidates
	default:
		return ""
	}
}

// The typed objective carries the requested fields. The broad objective remains
// compatible with the existing runtime; downstream code need not parse wording.
func jevRequestedAspects(objective string) (string, []string) {
	switch strings.TrimSpace(objective) {
	case "checkout_time", "checkin_time", "stay_dates":
		return "time", []string{objective}
	case "member_level_names", "member_level":
		return "identity", []string{objective}
	case "member_benefits", "member_upgrade_conditions", "member_retention_conditions":
		return "policy", []string{objective}
	case "member_validity":
		return "time", []string{objective}
	case "order_amount", "price_difference":
		return "price", []string{objective}
	case "room_type":
		return "identity", []string{objective}
	case "room_number":
		return "location", []string{objective}
	case "availability_and_price":
		return "compound_information", []string{"availability", "price"}
	case "room_change_assessment":
		return "compound_information", []string{"availability", "price_difference", "member_benefits"}
	case "", "unknown":
		return objective, nil
	default:
		return objective, []string{objective}
	}
}

func copyRuntimeIntentGoalToReply(task callbacks.IntentTaskTraceData, plan *callbacks.ReplyTaskPlanTraceData) {
	plan.SubjectScope = task.SubjectScope
	plan.RequestedAspects = append([]string(nil), task.RequestedAspects...)
	plan.SelectionSource = task.SelectionSource
	plan.SelectionRef = task.SelectionRef
}

func runtimeIntentScopeIsMembership(scope string) bool {
	return scope == runtimeSubjectPersonalMembership || scope == runtimeSubjectPublicMembership
}

func runtimeIntentContextEntitiesForScope(entities []callbacks.IntentEntityTraceData, scope string) []callbacks.IntentEntityTraceData {
	ret := make([]callbacks.IntentEntityTraceData, 0, len(entities))
	for _, entity := range entities {
		if scope == runtimeSubjectPublicMembership {
			continue
		}
		if scope == runtimeSubjectPersonalMembership {
			switch entity.Type {
			case runtimeIntentEntityOrderLocator, runtimeIntentEntityTargetRoomType:
				continue
			}
		}
		ret = append(ret, entity)
	}
	return ret
}
