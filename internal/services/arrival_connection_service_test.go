package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

type arrivalConnectionCompletionFixture struct {
	arrivalLinkTestFixture
	state      string
	invitation *models.StoreArrivalInvitation
	attempt    *models.WeComAuthorizationAttempt
	instance   *models.WxWorkProtocolInstance
}

func TestArrivalCompleteConnectionConfirmsCrossNamespaceMapping(t *testing.T) {
	tests := []struct {
		name           string
		employeeUserID string
	}{
		{name: "different ids", employeeUserID: "protocol-profile-user"},
		{name: "same ids", employeeUserID: "official-contact-member"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupArrivalConnectionCompletionFixture(
				t,
				[]string{"official-contact-member"},
				tt.employeeUserID,
			)
			originalEmployeeUserID := fixture.instance.EmployeeUserID
			memberToken := fixture.selectionToken(t, "official-contact-member", fixture.attempt.ID, fixture.attempt.ExpiresAt)

			result, err := ArrivalConnectionService.CompleteConnection(request.CompleteArrivalConnectionRequest{
				AuthorizationState:       fixture.state,
				ContactMemberToken:       memberToken,
				WxWorkProtocolInstanceID: fixture.instance.ID,
			})
			if err != nil {
				t.Fatalf("CompleteConnection() error=%v", err)
			}
			if result.ConnectionStatus != string(enums.ArrivalConnectionStatusActive) ||
				!result.AuthorizationOK ||
				!result.MemberOK ||
				!result.InstanceOK {
				t.Fatalf("unexpected verification result: %#v", result)
			}

			connection := repositories.ArrivalRepository.GetConnection(
				fixture.db,
				fixture.connection.ID,
				fixture.tenantID,
			)
			if connection == nil ||
				connection.ConnectionStatus != enums.ArrivalConnectionStatusActive ||
				connection.WxWorkProtocolInstanceID != fixture.instance.ID ||
				connection.ContactMemberCiphertext == "" ||
				connection.ContactMemberCiphertext == "official-contact-member" ||
				connection.ContactMemberFingerprint != fixture.security.Fingerprint("contact_member", "official-contact-member") {
				t.Fatalf("cross-namespace mapping was not persisted independently: %#v", connection)
			}
			storedMember, err := fixture.security.Decrypt(
				"contact_member",
				connection.ContactMemberCiphertext,
				connection.ContactMemberNonce,
			)
			if err != nil || storedMember != "official-contact-member" {
				t.Fatalf("stored official member=%q error=%v", storedMember, err)
			}
			storedInstance := repositories.WxWorkProtocolInstanceRepository.Get(fixture.db, fixture.instance.ID)
			if storedInstance == nil || storedInstance.EmployeeUserID != originalEmployeeUserID {
				t.Fatal("completion modified WxWorkProtocolInstance.EmployeeUserID")
			}

			invitation := repositories.ArrivalRepository.FindInvitationByHash(fixture.db, fixture.invitation.TokenHash)
			attempt := repositories.ArrivalRepository.GetAuthorizationAttempt(
				fixture.db,
				fixture.attempt.ID,
				fixture.tenantID,
			)
			if invitation == nil || invitation.Status != enums.StatusDisabled || invitation.UsedAt == nil {
				t.Fatalf("invitation was not consumed: %#v", invitation)
			}
			if attempt == nil || attempt.Status != enums.StatusDisabled {
				t.Fatalf("authorization attempt was not consumed: %#v", attempt)
			}

			var audit models.ArrivalAuditLog
			if err := fixture.db.Where("action = ?", "connection.complete").Order("id DESC").Take(&audit).Error; err != nil {
				t.Fatalf("read completion audit: %v", err)
			}
			detail := map[string]any{}
			if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
				t.Fatalf("decode audit detail: %v", err)
			}
			if detail["mappingMode"] != "operator_confirmed_cross_namespace" {
				t.Fatalf("mapping mode=%#v", detail["mappingMode"])
			}
			responseJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"official-contact-member",
				originalEmployeeUserID,
				fixture.instance.Guid,
				fixture.instance.StoreRoomConversationID,
				"test-corp-access-token",
				"test-permanent-code",
			} {
				if forbidden != "" &&
					(strings.Contains(audit.DetailJSON, forbidden) || strings.Contains(string(responseJSON), forbidden)) {
					t.Fatalf("completion leaked protected identifier %q", forbidden)
				}
			}
		})
	}
}

func TestArrivalCompleteConnectionRejectsInvalidMemberInstanceAndSelection(t *testing.T) {
	t.Run("member no longer official", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"still-official-member"},
			"protocol-profile-user",
		)
		token := fixture.selectionToken(t, "removed-official-member", fixture.attempt.ID, fixture.attempt.ExpiresAt)
		assertArrivalCompletionFails(t, fixture, token)
	})

	t.Run("instance belongs to another tenant", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"official-contact-member"},
			"protocol-profile-user",
		)
		if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
			Where("id = ?", fixture.instance.ID).
			Update("tenant_id", fixture.tenantID+1).Error; err != nil {
			t.Fatal(err)
		}
		assertArrivalCompletionFails(t, fixture, fixture.validSelectionToken(t))
	})

	t.Run("instance belongs to another store", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"official-contact-member"},
			"protocol-profile-user",
		)
		otherStore := &models.Store{
			TenantID:    fixture.tenantID,
			StoreCode:   "arrival-other-store",
			Name:        "其他门店",
			Status:      enums.StatusOk,
			AuditFields: arrivalSystemAuditFields(time.Now()),
		}
		if err := fixture.db.Create(otherStore).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
			Where("id = ?", fixture.instance.ID).
			Update("store_id", otherStore.ID).Error; err != nil {
			t.Fatal(err)
		}
		assertArrivalCompletionFails(t, fixture, fixture.validSelectionToken(t))
	})

	for _, status := range []enums.Status{enums.StatusDisabled, enums.StatusDeleted} {
		t.Run("instance unavailable "+fmt.Sprint(status), func(t *testing.T) {
			fixture := setupArrivalConnectionCompletionFixture(
				t,
				[]string{"official-contact-member"},
				"protocol-profile-user",
			)
			if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
				Where("id = ?", fixture.instance.ID).
				Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			assertArrivalCompletionFails(t, fixture, fixture.validSelectionToken(t))
		})
	}

	t.Run("forged selection token", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"official-contact-member"},
			"protocol-profile-user",
		)
		token := fixture.validSelectionToken(t)
		last := byte('A')
		if token[len(token)-1] == last {
			last = 'B'
		}
		assertArrivalCompletionFails(t, fixture, token[:len(token)-1]+string(last))
	})

	t.Run("expired selection token", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"official-contact-member"},
			"protocol-profile-user",
		)
		token := fixture.selectionToken(t, "official-contact-member", fixture.attempt.ID, time.Now().Add(-time.Minute))
		assertArrivalCompletionFails(t, fixture, token)
	})

	t.Run("selection token belongs to another attempt", func(t *testing.T) {
		fixture := setupArrivalConnectionCompletionFixture(
			t,
			[]string{"official-contact-member"},
			"protocol-profile-user",
		)
		token := fixture.selectionToken(
			t,
			"official-contact-member",
			fixture.attempt.ID+1,
			fixture.attempt.ExpiresAt,
		)
		assertArrivalCompletionFails(t, fixture, token)
	})
}

func TestArrivalCompleteConnectionRollsBackAllWritesWhenAuditFails(t *testing.T) {
	fixture := setupArrivalConnectionCompletionFixture(
		t,
		[]string{"official-contact-member"},
		"protocol-profile-user",
	)
	auditTable := fixture.db.NamingStrategy.TableName("ArrivalAuditLog")
	trigger := fmt.Sprintf(
		"CREATE TRIGGER fail_connection_complete_audit BEFORE INSERT ON `%s` "+
			"WHEN NEW.action = 'connection.complete' BEGIN SELECT RAISE(ABORT, 'forced audit failure'); END",
		auditTable,
	)
	if err := fixture.db.Exec(trigger).Error; err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}

	_, err := ArrivalConnectionService.CompleteConnection(request.CompleteArrivalConnectionRequest{
		AuthorizationState:       fixture.state,
		ContactMemberToken:       fixture.validSelectionToken(t),
		WxWorkProtocolInstanceID: fixture.instance.ID,
	})
	if err == nil {
		t.Fatal("audit failure unexpectedly committed connection completion")
	}
	connection := repositories.ArrivalRepository.GetConnection(
		fixture.db,
		fixture.connection.ID,
		fixture.tenantID,
	)
	invitation := repositories.ArrivalRepository.FindInvitationByHash(fixture.db, fixture.invitation.TokenHash)
	attempt := repositories.ArrivalRepository.GetAuthorizationAttempt(
		fixture.db,
		fixture.attempt.ID,
		fixture.tenantID,
	)
	if connection == nil ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusPendingBinding ||
		connection.ContactMemberCiphertext != "" ||
		connection.WxWorkProtocolInstanceID != 0 {
		t.Fatalf("connection update escaped transaction rollback: %#v", connection)
	}
	if invitation == nil || invitation.Status != enums.StatusOk || invitation.UsedAt != nil {
		t.Fatalf("invitation update escaped transaction rollback: %#v", invitation)
	}
	if attempt == nil || attempt.Status != enums.StatusOk {
		t.Fatalf("attempt update escaped transaction rollback: %#v", attempt)
	}
}

func TestArrivalProviderOptionsDoNotAutoMatchCrossNamespaceIDs(t *testing.T) {
	fixture := setupArrivalConnectionCompletionFixture(
		t,
		[]string{"official-contact-member"},
		"official-contact-member",
	)
	draft := &models.WxWorkProtocolInstance{
		TenantID:            fixture.tenantID,
		Guid:                "arrival-unverified-replacement",
		ChannelID:           fixture.instance.ChannelID,
		EmployeeName:        "未完成验证的替换草稿",
		StoreID:             fixture.store.ID,
		StoreStaffBindingID: fixture.instance.StoreStaffBindingID,
		ReplacesInstanceID:  fixture.instance.ID,
		HealthStatus:        "online",
		Status:              enums.StatusOk,
	}
	if err := fixture.db.Create(draft).Error; err != nil {
		t.Fatalf("create unverified replacement draft: %v", err)
	}
	options, err := ArrivalConnectionService.ProviderOptions(fixture.state)
	if err != nil {
		t.Fatalf("ProviderOptions() error=%v", err)
	}
	if len(options.Members) != 1 || options.Members[0].Label != "可用客户联系成员 1" {
		t.Fatalf("official member label inferred from protocol profile: %#v", options.Members)
	}
	if len(options.Instances) != 1 || options.Instances[0].ID != fixture.instance.ID || options.Instances[0].Name != "黄奇峰" {
		t.Fatalf("provider options included an unverified replacement draft: %#v", options.Instances)
	}
}

func setupArrivalConnectionCompletionFixture(
	t *testing.T,
	officialMembers []string,
	employeeUserID string,
) arrivalConnectionCompletionFixture {
	t.Helper()
	base := setupArrivalLinkTestFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := repositories.ArrivalRepository.UpdateConnection(
		base.db,
		base.connection.ID,
		base.tenantID,
		map[string]any{
			"connection_status":            enums.ArrivalConnectionStatusPendingBinding,
			"contact_member_ciphertext":    "",
			"contact_member_nonce":         "",
			"contact_member_fingerprint":   "",
			"wx_work_protocol_instance_id": 0,
			"last_verified_at":             nil,
		},
	); err != nil {
		t.Fatalf("prepare pending connection: %v", err)
	}

	invitation := &models.StoreArrivalInvitation{
		TenantID:    base.tenantID,
		StoreID:     base.store.ID,
		TokenHash:   base.security.Fingerprint("arrival_invitation", "completion-invitation"),
		ExpiresAt:   now.Add(time.Hour),
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := base.db.Create(invitation).Error; err != nil {
		t.Fatalf("create completion invitation: %v", err)
	}
	state := "CompletionState123"
	completedAt := now
	attempt := &models.WeComAuthorizationAttempt{
		TenantID:              base.tenantID,
		StoreID:               base.store.ID,
		InvitationID:          invitation.ID,
		StateHash:             base.security.Fingerprint("authorization_state", state),
		TenantAuthorizationID: base.authorization.ID,
		ExpiresAt:             now.Add(time.Hour),
		CompletedAt:           &completedAt,
		Status:                enums.StatusOk,
		AuditFields:           arrivalSystemAuditFields(now),
	}
	if err := base.db.Create(attempt).Error; err != nil {
		t.Fatalf("create completion attempt: %v", err)
	}
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(
		base.db,
		base.instance.ID,
		base.tenantID,
		map[string]any{
			"guid":                       "completion-instance-guid",
			"employee_user_id":           employeeUserID,
			"employee_name":              "黄奇峰",
			"store_room_conversation_id": "S:completion-room-conversation",
			"health_status":              "online",
			"status":                     enums.StatusOk,
			"updated_at":                 now,
		},
	); err != nil {
		t.Fatalf("update completion protocol instance: %v", err)
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(base.db, base.instance.ID, base.tenantID)
	if instance == nil {
		t.Fatal("completion protocol instance missing")
	}

	accessTokenCiphertext, accessTokenNonce, err := base.security.Encrypt(
		"corp_access_token",
		"test-corp-access-token",
	)
	if err != nil {
		t.Fatal(err)
	}
	accessTokenExpiresAt := now.Add(time.Hour)
	if err := repositories.ArrivalRepository.UpdateTenantAuthorization(
		base.db,
		base.authorization.ID,
		base.tenantID,
		map[string]any{
			"permanent_code_ciphertext":    "test-permanent-code",
			"corp_access_token_ciphertext": accessTokenCiphertext,
			"corp_access_token_nonce":      accessTokenNonce,
			"corp_access_token_expires_at": accessTokenExpiresAt,
		},
	); err != nil {
		t.Fatalf("prepare authorization token: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet ||
			req.URL.Path != "/cgi-bin/externalcontact/get_follow_user_list" ||
			req.URL.Query().Get("access_token") != "test-corp-access-token" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errcode":     0,
			"errmsg":      "ok",
			"follow_user": officialMembers,
		})
	}))
	t.Cleanup(server.Close)
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)

	return arrivalConnectionCompletionFixture{
		arrivalLinkTestFixture: base,
		state:                  state,
		invitation:             invitation,
		attempt:                attempt,
		instance:               instance,
	}
}

func (f arrivalConnectionCompletionFixture) selectionToken(
	t *testing.T,
	memberUserID string,
	attemptID int64,
	expiresAt time.Time,
) string {
	t.Helper()
	token, err := f.security.SelectionToken("contact_member", attemptID, memberUserID, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f arrivalConnectionCompletionFixture) validSelectionToken(t *testing.T) string {
	t.Helper()
	return f.selectionToken(t, "official-contact-member", f.attempt.ID, f.attempt.ExpiresAt)
}

func assertArrivalCompletionFails(
	t *testing.T,
	fixture arrivalConnectionCompletionFixture,
	selectionToken string,
) {
	t.Helper()
	_, err := ArrivalConnectionService.CompleteConnection(request.CompleteArrivalConnectionRequest{
		AuthorizationState:       fixture.state,
		ContactMemberToken:       selectionToken,
		WxWorkProtocolInstanceID: fixture.instance.ID,
	})
	if err == nil {
		t.Fatal("invalid connection completion unexpectedly succeeded")
	}
	connection := repositories.ArrivalRepository.GetConnection(
		fixture.db,
		fixture.connection.ID,
		fixture.tenantID,
	)
	if connection == nil || connection.ConnectionStatus != enums.ArrivalConnectionStatusPendingBinding {
		t.Fatalf("failed completion mutated connection status: %#v", connection)
	}
}
