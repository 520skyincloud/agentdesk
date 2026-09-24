package main

import "testing"

func TestRequiredOutcomeCombinesAllConstraints(t *testing.T) {
	expected := outcomeRequirement{
		TextContainsAll: []string{"沐阳", "1501"}, TextContainsAny: []string{"有房", "可选"},
		ResourceTypesAll: []string{"pillow_product"},
	}
	for _, text := range []string{"沐阳可选", "沐阳1501", "沐阳1501可选"} {
		if requiredOutcomeSatisfied(record{ReplyText: text}, expected) {
			t.Fatalf("partial text or missing resource passed: %s", text)
		}
	}
	rec := record{ReplyText: "沐阳1501可选", CommitMessages: []commitRecord{{ResourceType: "pillow_product", Status: "sent"}}}
	if !requiredOutcomeSatisfied(rec, expected) {
		t.Fatal("all constraints should pass")
	}
}

func TestMissingPriceIsSafeResponseButNotResolvedBusiness(t *testing.T) {
	sc := findEvalScenario(t, "UX01")
	turnScenario := scenarioFromTurn(sc, 4, sc.Turns[4])
	rec := record{Status: "completed", LatencyMs: 1000, ReplyText: "沐阳目前有房，当前订单金额376.00元，实时房价没有显示，算不出准确差价。"}
	rec.Score, rec.Issues = scoreRecord(turnScenario, rec)
	result := assessJourneyAcceptance(turnScenario, rec)
	if !result.ResponseChecks || result.Business != "blocked_or_incomplete" || result.automatedChecksPassed() {
		t.Fatalf("safe missing-price reply was treated as completed: %#v", result)
	}
}

func TestLatencyAndDeliveryCannotBeHiddenByScore(t *testing.T) {
	sc := scenario{LatencyWarningMs: 12000}
	rec := record{Status: "completed", ReplyText: "咖啡在洗衣房。", LatencyMs: 18000}
	rec.Score, rec.Issues = scoreRecord(sc, rec)
	result := assessJourneyAcceptance(sc, rec)
	if !result.ResponseChecks || result.LatencyWithinBudget || result.automatedChecksPassed() ||
		result.Delivery != "not_verified_channel_zero" || result.Business != "not_verified" {
		t.Fatalf("unsupported acceptance claim: %#v", result)
	}
}

func TestCompletedStatusDoesNotHideEmptyReply(t *testing.T) {
	rec := record{Status: "completed", LatencyMs: 1000}
	rec.Score, rec.Issues = scoreRecord(scenario{}, rec)
	if assessJourneyAcceptance(scenario{}, rec).ResponseChecks {
		t.Fatal("empty completed runtime passed")
	}
}
