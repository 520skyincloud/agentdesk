package builders

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/services"
)

func TestBuildStoreModelCredentialNeverExposesSecretMaterial(t *testing.T) {
	secret := "sk-builder-secret"
	fingerprint := securex.Fingerprint(secret)
	now := time.Now()
	data := &services.StoreModelCredentialData{
		Store: models.Store{ID: 9, TenantID: 7, StoreCode: "store-9", Name: "测试门店"},
		Credential: &models.StoreModelCredential{
			TenantID: 7, StoreID: 9,
			EncryptedKey: "ciphertext-active", KeyNonce: "nonce-active", KeyFingerprint: fingerprint,
			MasterKeyID: "master-key-id", CredentialRevision: 2, Status: enums.StoreCredentialStatusActive,
			CandidateEncryptedKey: "ciphertext-candidate", CandidateKeyNonce: "nonce-candidate",
			CandidateKeyFingerprint: fingerprint, CandidateMasterKeyID: "candidate-master-key-id",
			CandidateRevision: 3, CandidateStatus: enums.StoreCredentialStatusPendingApproval,
			CandidateApprovalStatus: enums.CredentialApprovalStatusPending, CandidateRequestedAt: &now,
		},
		Policy: &models.StoreCredentialPolicy{AllowCredentialSelfService: true, RequireSupervisorApproval: true, Status: enums.StatusOk},
		Assignment: &models.StoreModelProfileAssignment{
			TemplateID: 11, TemplateRevision: 4, PendingTemplateID: 12, PendingTemplateRevision: 5,
		},
		ActiveTemplate:  &models.ModelProfileTemplate{ID: 11, Name: "当前方案", GatewayBaseURL: "https://private-gateway.example.com/v1"},
		PendingTemplate: &models.ModelProfileTemplate{ID: 12, Name: "候选方案", GatewayBaseURL: "https://candidate-private.example.com/v1"},
		ActiveSlots:     []models.ModelProfileSlot{{ModelName: "reply-model"}},
		PendingSlots:    []models.ModelProfileSlot{{ModelName: "next-model"}},
		CanSelfService:  true,
	}

	raw, err := json.Marshal(BuildStoreModelCredential(data))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, required := range []string{"************", fingerprint[len(fingerprint)-6:], "reply-model", "next-model"} {
		if !strings.Contains(body, required) {
			t.Fatalf("safe response missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{
		secret, fingerprint, "ciphertext-active", "ciphertext-candidate", "nonce-active", "nonce-candidate",
		"master-key-id", "candidate-master-key-id", "private-gateway.example.com", "candidate-private.example.com",
		`"apiKey"`, `"encryptedKey"`, `"keyNonce"`, `"keyFingerprint"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("safe response leaked %q: %s", forbidden, body)
		}
	}
}

func TestBuildStoreModelCredentialAuditOnlyReturnsFingerprintSuffix(t *testing.T) {
	fingerprint := securex.Fingerprint("sk-audit-secret")
	items := []models.StoreModelCredentialAuditLog{{
		ID: 1, TenantID: 7, StoreID: 9, Action: enums.CredentialAuditActionActivate,
		Result: enums.CredentialAuditResultSuccess, FingerprintLast6: fingerprint[len(fingerprint)-6:],
		OperatorName: "manager", CreatedAt: time.Now(),
	}}
	raw, err := json.Marshal(BuildStoreModelCredentialAuditList(items))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, fingerprint) || !strings.Contains(body, fingerprint[len(fingerprint)-6:]) {
		t.Fatalf("unsafe audit response: %s", body)
	}
}
