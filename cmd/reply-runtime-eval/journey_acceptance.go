package main

import "strings"

// These are evidence dimensions, not a second model judge or a runtime gate.
type journeyAcceptance struct {
	ResponseChecks      bool     `json:"responseChecks"`
	Business            string   `json:"business"`
	MissingBusiness     []string `json:"missingBusiness,omitempty"`
	LatencyWithinBudget bool     `json:"latencyWithinBudget"`
	Naturalness         string   `json:"naturalness"`
	Delivery            string   `json:"delivery"`
}

func assessJourneyAcceptance(sc scenario, rec record) journeyAcceptance {
	ret := journeyAcceptance{
		ResponseChecks: rec.Status == "completed" && rec.ErrorMessage == "",
		Business:       "not_verified", Naturalness: "requires_review",
		Delivery: "not_verified_channel_zero",
	}
	for _, issue := range rec.Issues {
		if !strings.HasPrefix(issue, "latency over ") {
			ret.ResponseChecks = false
		}
	}
	budget := sc.LatencyWarningMs
	if budget <= 0 {
		budget = 8000
	}
	ret.LatencyWithinBudget = rec.LatencyMs > 0 && rec.LatencyMs <= budget
	if len(sc.BusinessOutcomes) > 0 {
		ret.Business = "evidence_matched_requires_review"
		for _, expected := range sc.BusinessOutcomes {
			if !requiredOutcomeSatisfied(rec, expected) {
				ret.MissingBusiness = append(ret.MissingBusiness, expected.Label)
			}
		}
		if len(ret.MissingBusiness) > 0 {
			ret.Business = "blocked_or_incomplete"
		}
	}
	return ret
}

func (a journeyAcceptance) automatedChecksPassed() bool {
	return a.ResponseChecks && a.LatencyWithinBudget && a.Business != "blocked_or_incomplete"
}
