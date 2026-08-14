package executor

import (
	"testing"

	"agent-desk/internal/models"
)

func TestGateEnabledDefaultsOn(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{TenantID: 1, StoreID: 2, StoreStaffBindingID: 3}}
	for _, gate := range []replyGate{gateFactSourceBoundary, gateResourceEligibility, gateEvidenceQuality, gatePromptLayer} {
		if !gateEnabled(gate, req) {
			t.Fatalf("gate %s must default to enabled", gate)
		}
	}
}

func TestGateGlobalOff(t *testing.T) {
	t.Setenv("AI_REPLY_FACT_SOURCE_BOUNDARY_V2", "off")
	req := RunInput{Conversation: models.Conversation{TenantID: 1}}
	if gateEnabled(gateFactSourceBoundary, req) {
		t.Fatal("off must disable the gate globally")
	}
}

func TestGateTenantExclusion(t *testing.T) {
	t.Setenv("AI_REPLY_EVIDENCE_QUALITY_GATE_V1_EXCLUDE_TENANT_IDS", "7,9")
	excluded := RunInput{Conversation: models.Conversation{TenantID: 7}}
	included := RunInput{Conversation: models.Conversation{TenantID: 8}}
	if gateEnabled(gateEvidenceQuality, excluded) {
		t.Fatal("tenant 7 must be excluded from the gate")
	}
	if !gateEnabled(gateEvidenceQuality, included) {
		t.Fatal("tenant 8 must remain gated")
	}
}
