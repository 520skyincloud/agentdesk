package services

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func TestStoreConversationDifferentBindingsStaySeparate(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	store := createContinuityTestStore(t, db, 101, "separate-bindings")
	channel := createContinuityTestChannel(t, db, 101, "separate-bindings")
	bindingA := createWxWorkProtocolTestBinding(t, db, store, "separate-a")
	bindingB := createWxWorkProtocolTestBinding(t, db, store, "separate-b")
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := continuityExternalUser("same-customer")

	conversationA, _, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *aiAgent, StoreConversationScope{
		StoreID: store.ID, StoreStaffBindingID: bindingA.ID,
	})
	if err != nil {
		t.Fatalf("create first binding conversation: %v", err)
	}
	conversationB, _, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *aiAgent, StoreConversationScope{
		StoreID: store.ID, StoreStaffBindingID: bindingB.ID,
	})
	if err != nil {
		t.Fatalf("create second binding conversation: %v", err)
	}
	if conversationA.ID == conversationB.ID {
		t.Fatal("different store staff bindings must not share one conversation")
	}
	if conversationA.CustomerID != conversationB.CustomerID || conversationA.StoreID != conversationB.StoreID {
		t.Fatalf("expected same customer and store scope, got A=%+v B=%+v", conversationA, conversationB)
	}
	if conversationA.ThreadKey == nil || conversationB.ThreadKey == nil || *conversationA.ThreadKey == *conversationB.ThreadKey {
		t.Fatalf("different bindings require distinct thread keys, A=%v B=%v", conversationA.ThreadKey, conversationB.ThreadKey)
	}
}

func TestStoreConversationSameBindingInstanceReplacementStartsNewSession(t *testing.T) {
	db, store, channel, binding, first, external, conversation := setupStoreConversationContinuityFixture(t, "instance-replacement")
	now := time.Now()
	firstSessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, first, now)
	if err != nil {
		t.Fatalf("prepare first instance: %v", err)
	}
	if firstSessionNo != 1 {
		t.Fatalf("first sessionNo=%d, want 1", firstSessionNo)
	}
	firstMessage, err := MessageService.SendCustomerMessageInSession(conversation.ID, "continuity-first", enums.IMMessageTypeText, "第一段", "", external, firstSessionNo)
	if err != nil {
		t.Fatalf("persist first session message: %v", err)
	}

	replacement := createContinuityTestInstance(t, db, store, channel, binding, "replacement", "online")
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", first.ID).Updates(map[string]any{
		"replaced_by_instance_id": replacement.ID,
		"replaced_at":             now,
		"status":                  enums.StatusDisabled,
	}).Error; err != nil {
		t.Fatalf("retire first instance: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", replacement.ID).Update("replaces_instance_id", first.ID).Error; err != nil {
		t.Fatalf("link replacement instance: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", replacement.ID).Update("remote_setup_submitted_at", now).Error; err != nil {
		t.Fatalf("activate replacement instance: %v", err)
	}
	replacement.ReplacesInstanceID = first.ID
	replacement.RemoteSetupSubmittedAt = &now

	reused, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *createWelcomeTestAIAgent(t, db, ""), StoreConversationScope{
		StoreID: store.ID, StoreStaffBindingID: binding.ID,
	})
	if err != nil {
		t.Fatalf("resolve replacement conversation: %v", err)
	}
	if created || reused.ID != conversation.ID {
		t.Fatalf("same binding replacement must reuse conversation %d, got %+v created=%v", conversation.ID, reused, created)
	}
	secondSessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, replacement, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("prepare replacement instance: %v", err)
	}
	if secondSessionNo != 2 {
		t.Fatalf("replacement sessionNo=%d, want 2", secondSessionNo)
	}
	secondMessage, err := MessageService.SendCustomerMessageInSession(conversation.ID, "continuity-second", enums.IMMessageTypeText, "第二段", "", external, secondSessionNo)
	if err != nil {
		t.Fatalf("persist replacement session message: %v", err)
	}
	if firstMessage.SessionNo != 1 || secondMessage.SessionNo != 2 {
		t.Fatalf("messages lost fixed session attribution: first=%d second=%d", firstMessage.SessionNo, secondMessage.SessionNo)
	}
	sessions := ConversationChannelSessionService.ListInTenant(conversation.ID, conversation.TenantID)
	if len(sessions) != 2 || sessions[0].EndedAt == nil || sessions[1].WxWorkInstanceID != replacement.ID {
		t.Fatalf("unexpected channel sessions: %+v", sessions)
	}
}

func TestStoreConversationPendingReplacementCannotTakeCurrentRoute(t *testing.T) {
	db, store, channel, binding, current, _, conversation := setupStoreConversationContinuityFixture(t, "pending-replacement")
	now := time.Now()
	if sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, current, now); err != nil || sessionNo != 1 {
		t.Fatalf("prepare current session=%d err=%v", sessionNo, err)
	}

	draft := createContinuityTestInstance(t, db, store, channel, binding, "pending-replacement-draft", "online")
	draft.ReplacesInstanceID = current.ID
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", draft.ID).Update("replaces_instance_id", current.ID).Error; err != nil {
		t.Fatalf("mark replacement draft: %v", err)
	}
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, draft, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "未完成替换验证") {
		t.Fatalf("pending replacement must not take current route, err=%v", err)
	}
	beforeConversationCount := countConversationHistoryTestRows(t, db, &models.Conversation{})
	beforeMappingCount := countConversationHistoryTestRows(t, db, &models.WxWorkKFConversation{})
	if err := WxWorkProtocolService.handleChatMessage(draft, request.WxProtocolChatMsg{
		Seq: "draft-1", ID: "draft-message-1", Sender: "draft-customer", Receiver: draft.EmployeeUserID,
		RoomID: "0", MsgType: wxProtocolMsgText, ContentType: wxProtocolMsgText, Content: "草稿不应接管会话",
	}, `{}`); err == nil || !strings.Contains(err.Error(), "未完成替换验证") {
		t.Fatalf("pending replacement message must fail before conversation mutation, err=%v", err)
	}
	if got := countConversationHistoryTestRows(t, db, &models.Conversation{}); got != beforeConversationCount {
		t.Fatalf("pending replacement message changed conversation count: before=%d after=%d", beforeConversationCount, got)
	}
	if got := countConversationHistoryTestRows(t, db, &models.WxWorkKFConversation{}); got != beforeMappingCount {
		t.Fatalf("pending replacement message changed mapping count: before=%d after=%d", beforeMappingCount, got)
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route == nil || route.WxWorkInstanceID != current.ID || route.SessionNo != 1 {
		t.Fatalf("pending replacement changed current route: %+v", route)
	}
}

func TestStoreConversationPendingReplacementCannotProvideAIRuntime(t *testing.T) {
	db, store, channel, binding, current, _, conversation := setupStoreConversationContinuityFixture(t, "pending-replacement-ai")
	draft := createContinuityTestInstance(t, db, store, channel, binding, "pending-replacement-ai-draft", "online")
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", draft.ID).Updates(map[string]any{
		"replaces_instance_id": current.ID,
		"ai_reply_enabled":     true,
	}).Error; err != nil {
		t.Fatalf("mark replacement draft: %v", err)
	}
	if err := db.Model(&models.ConversationRouteState{}).Where("conversation_id = ?", conversation.ID).
		Update("wx_work_instance_id", draft.ID).Error; err != nil {
		t.Fatalf("point route at replacement draft: %v", err)
	}

	if _, ok := WxWorkProtocolInstanceService.BuildRuntimeAIAgentForConversation(conversation.ID); ok {
		t.Fatal("replacement draft must not provide an AI runtime")
	}
	if got := repositories.WxWorkProtocolInstanceRepository.GetActivatedCurrentInTenant(db, draft.ID, store.TenantID); got != nil {
		t.Fatalf("replacement draft resolved as activated current instance: %+v", got)
	}
}

func TestStoreConversationRepeatedInactivePrepareDoesNotDoubleIncrement(t *testing.T) {
	db, _, _, _, instance, _, conversation := setupStoreConversationContinuityFixture(t, "inactivity")
	if initial, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now()); err != nil || initial != 1 {
		t.Fatalf("prepare initial session=%d err=%v, want 1", initial, err)
	}
	oldActiveAt := time.Now().Add(-defaultConversationSessionGap - time.Hour)
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("last_active_at", oldActiveAt).Error; err != nil {
		t.Fatalf("age conversation: %v", err)
	}
	if err := db.Model(&models.ConversationChannelSession{}).Where("conversation_id = ? AND session_no = ?", conversation.ID, 1).Update("started_at", oldActiveAt).Error; err != nil {
		t.Fatalf("age initial channel session: %v", err)
	}
	conversation.LastActiveAt = oldActiveAt
	if first, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now()); err != nil || first != 2 {
		t.Fatalf("first inactivity prepare session=%d err=%v, want 2", first, err)
	}
	if second, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now().Add(time.Second)); err != nil || second != 2 {
		t.Fatalf("second inactivity prepare session=%d err=%v, want stable 2", second, err)
	}
	if sessions := ConversationChannelSessionService.ListInTenant(conversation.ID, conversation.TenantID); len(sessions) != 2 {
		t.Fatalf("repeated callback created extra sessions: %+v", sessions)
	}
}

func TestStoreConversationOutboundRequiresActiveStoreAndBinding(t *testing.T) {
	db, store, _, binding, instance, external, conversation := setupStoreConversationContinuityFixture(t, "outbound-scope")
	externalID := strings.TrimPrefix(external.ExternalID, "wxwork_protocol:")
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now()); err != nil {
		t.Fatalf("prepare current instance route: %v", err)
	}
	if err := WxWorkProtocolService.upsertConversationMapping(instance, conversation.ID, request.WxProtocolChatMsg{}, externalID, `{}`); err != nil {
		t.Fatalf("create protocol mapping: %v", err)
	}
	if ready, status := WxWorkProtocolService.ConversationReplyReadiness(conversation); !ready || status != wxWorkReplyStatusReady {
		t.Fatalf("baseline outbound readiness=(%v,%q)", ready, status)
	}

	if err := db.Model(&models.Store{}).Where("id = ?", store.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable Store: %v", err)
	}
	if ready, status := WxWorkProtocolService.ConversationReplyReadiness(conversation); ready || status != wxWorkReplyStatusUnavailable {
		t.Fatalf("disabled Store readiness=(%v,%q), want unavailable", ready, status)
	}
	if err := db.Model(&models.Store{}).Where("id = ?", store.ID).Update("status", enums.StatusOk).Error; err != nil {
		t.Fatalf("restore Store: %v", err)
	}

	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", binding.ID).
		Updates(map[string]any{"status": enums.StatusDisabled, "active_user_id": nil}).Error; err != nil {
		t.Fatalf("disable Store binding: %v", err)
	}
	if ready, status := WxWorkProtocolService.ConversationReplyReadiness(conversation); ready || status != wxWorkReplyStatusUnavailable {
		t.Fatalf("disabled binding readiness=(%v,%q), want unavailable", ready, status)
	}
}

func TestStoreConversationManualInheritance(t *testing.T) {
	db, store, channel, sourceBinding, sourceInstance, external, conversation := setupStoreConversationContinuityFixture(t, "manual-inheritance")
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source session: %v", err)
	}
	sourceThreadKey := *conversation.ThreadKey
	sourceMessage := &models.Message{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "source-history", SeqNo: 1,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(sourceMessage).Error; err != nil {
		t.Fatalf("create source message: %v", err)
	}
	sourceExternalID := strings.TrimPrefix(external.ExternalID, "wxwork_protocol:")
	sourceMapping := &models.WxWorkKFConversation{
		TenantID: store.TenantID, ConversationID: conversation.ID, ChannelID: channel.ID,
		OpenKfID: "wx_protocol:" + sourceInstance.Guid + ":single", ExternalUserID: sourceExternalID,
		SessionStatus: string(enums.WxWorkKFSessionStatusActive), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(sourceMapping).Error; err != nil {
		t.Fatalf("create source protocol mapping: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "manual-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "manual-target", "online")
	operator := continuityTenantAdmin(store.TenantID)

	updated, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID:            conversation.ID,
		TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID:    targetInstance.ID,
		Reason:                    "原员工离职，由主管确认交接",
	}, operator, "continuity-manual-inheritance")
	if err != nil {
		t.Fatalf("inherit conversation: %v", err)
	}
	if updated == nil || updated.ID == conversation.ID || updated.CustomerID != conversation.CustomerID || updated.StoreID != store.ID || updated.StoreStaffBindingID != targetBinding.ID {
		t.Fatalf("unexpected inherited conversation: %+v", updated)
	}
	wantThreadKey := buildStoreConversationThreadKey(store.TenantID, store.ID, conversation.CustomerID, targetBinding.ID)
	if updated.ThreadKey == nil || *updated.ThreadKey != wantThreadKey {
		t.Fatalf("thread key=%v, want %q", updated.ThreadKey, wantThreadKey)
	}
	sourceAfter := ConversationService.Get(conversation.ID)
	if sourceAfter == nil || sourceAfter.Status != enums.IMConversationStatusClosed || sourceAfter.StoreStaffBindingID != sourceBinding.ID ||
		sourceAfter.ThreadKey == nil || *sourceAfter.ThreadKey != sourceThreadKey || sourceAfter.ChannelID != channel.ID {
		t.Fatalf("source conversation must remain immutable physical history: %+v", sourceAfter)
	}
	var sourceMessageAfter models.Message
	if err := db.First(&sourceMessageAfter, sourceMessage.ID).Error; err != nil || sourceMessageAfter.ConversationID != conversation.ID {
		t.Fatalf("source message ownership changed: item=%+v err=%v", sourceMessageAfter, err)
	}
	route := ConversationRouteService.GetByConversationIDInTenant(updated.ID, store.TenantID)
	if route == nil || route.StoreStaffBindingID != targetBinding.ID || route.WxWorkInstanceID != targetInstance.ID || route.SessionNo != 1 {
		t.Fatalf("unexpected inherited route: %+v", route)
	}
	sourceSessions := ConversationChannelSessionService.ListInTenant(conversation.ID, store.TenantID)
	targetSessions := ConversationChannelSessionService.ListInTenant(updated.ID, store.TenantID)
	if len(sourceSessions) != 1 || sourceSessions[0].EndedAt == nil || len(targetSessions) != 1 ||
		targetSessions[0].StartReason != conversationSessionReasonManualInheritance || targetSessions[0].SessionNo != 1 {
		t.Fatalf("unexpected inherited sessions: source=%+v target=%+v", sourceSessions, targetSessions)
	}
	var link models.ConversationContinuityLink
	if err := db.Where("tenant_id = ? AND predecessor_conversation_id = ?", store.TenantID, conversation.ID).Take(&link).Error; err != nil || link.SuccessorConversationID != updated.ID {
		t.Fatalf("source-to-successor link missing: item=%+v err=%v", link, err)
	}
	if err := db.First(sourceMapping, sourceMapping.ID).Error; err != nil || sourceMapping.Status != enums.StatusDisabled {
		t.Fatalf("source protocol mapping must be disabled after inheritance: item=%+v err=%v", sourceMapping, err)
	}
	if ready, status := WxWorkProtocolService.ConversationReplyReadiness(updated); ready || status != wxWorkReplyStatusWaitingTargetMessage {
		t.Fatalf("inherited conversation reply readiness=(%v,%q), want waiting target message", ready, status)
	}
	if err := WxWorkProtocolService.RequireConversationOutboundRoute(db, updated); err == nil || !strings.Contains(err.Error(), "目标企微员工号收到") {
		t.Fatalf("expected outbound to remain blocked before target callback, got %v", err)
	}
	if err := WxWorkProtocolService.upsertConversationMapping(targetInstance, updated.ID, request.WxProtocolChatMsg{}, sourceExternalID, `{}`); err != nil {
		t.Fatalf("activate target protocol mapping: %v", err)
	}
	if ready, status := WxWorkProtocolService.ConversationReplyReadiness(updated); !ready || status != wxWorkReplyStatusReady {
		t.Fatalf("target callback should make reply ready, got (%v,%q)", ready, status)
	}
	var event models.ConversationEventLog
	if err := db.Where("conversation_id = ? AND request_id = ?", conversation.ID, "continuity-manual-inheritance:predecessor").Take(&event).Error; err != nil {
		t.Fatalf("find inheritance event: %v", err)
	}
	if !strings.Contains(event.Payload, `"mappingMode":"operator_confirmed_cross_namespace"`) {
		t.Fatalf("inheritance mapping mode missing: %s", event.Payload)
	}
	for _, secret := range []string{sourceInstance.Guid, targetInstance.Guid, sourceInstance.EmployeeUserID, targetInstance.EmployeeUserID, "conversation_id"} {
		if secret != "" && (strings.Contains(event.Payload, secret) || strings.Contains(event.Content, secret)) {
			t.Fatalf("inheritance audit leaked protocol identifier %q", secret)
		}
	}
	if sourceBinding.ID == targetBinding.ID {
		t.Fatal("fixture must use different bindings")
	}
}

func TestStoreConversationInheritanceMovesCurrentArrivalBindingWithoutRewritingTicket(t *testing.T) {
	db, store, channel, sourceBinding, sourceInstance, external, sourceConversation := setupStoreConversationContinuityFixture(t, "arrival-inheritance")
	if _, err := ConversationChannelSessionService.PrepareInbound(sourceConversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source session: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "arrival-inheritance-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "arrival-inheritance-target", "online")
	now := time.Now()
	identity := &models.MiniProgramIdentity{
		TenantID: store.TenantID, AppID: "arrival-inheritance-app",
		OpenIDCiphertext: "test-ciphertext", OpenIDNonce: "test-nonce", OpenIDFingerprint: "arrival-inheritance-open",
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(identity).Error; err != nil {
		t.Fatalf("create mini-program identity: %v", err)
	}
	connection := &models.StoreArrivalConnection{
		TenantID: store.TenantID, StoreID: store.ID, StoreStaffBindingID: sourceBinding.ID,
		StoreScene: "arrival-inheritance-scene", ContactProviderMode: enums.ArrivalContactProviderModeStaticPluginTicket,
		StaticContactPlugID: "arrival-inheritance-plug", WxWorkProtocolInstanceID: sourceInstance.ID,
		ConnectionStatus: enums.ArrivalConnectionStatusActive, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(connection).Error; err != nil {
		t.Fatalf("create arrival connection: %v", err)
	}
	ticket := &models.ArrivalBindingTicket{
		TenantID: store.TenantID, StoreID: store.ID, StoreStaffBindingID: sourceBinding.ID,
		WxWorkProtocolInstanceID: sourceInstance.ID, CustomerID: sourceConversation.CustomerID,
		ConversationID: sourceConversation.ID, TicketHash: "arrival-inheritance-ticket",
		TokenEntropyHash: "arrival-inheritance-entropy", TicketStatus: enums.ArrivalBindingTicketStatusConsumed,
		ExpiresAt: now.Add(time.Hour), ConsumedAt: &now, ConsumedMiniProgramIdentityID: identity.ID,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(ticket).Error; err != nil {
		t.Fatalf("create arrival ticket: %v", err)
	}
	arrivalBinding := &models.ArrivalStoreBinding{
		TenantID: store.TenantID, StoreID: store.ID, StoreStaffBindingID: sourceBinding.ID,
		MiniProgramIdentityID: identity.ID, WxWorkProtocolInstanceID: sourceInstance.ID,
		CustomerID: sourceConversation.CustomerID, ConversationID: sourceConversation.ID,
		ProtocolConversationCiphertext: "stale-protocol-ciphertext", ProtocolConversationNonce: "stale-protocol-nonce",
		ProtocolConversationFingerprint: "stale-protocol-fingerprint", BindingProofType: enums.ArrivalBindingProofTypeCardTicket,
		BindingTicketID: ticket.ID, BindingStatus: enums.ArrivalBindingStatusBound, EvidenceHash: "arrival-inheritance-evidence",
		ProtocolMappedAt: &now, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(arrivalBinding).Error; err != nil {
		t.Fatalf("create arrival binding: %v", err)
	}

	successor, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID: sourceConversation.ID, TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID: targetInstance.ID, Reason: "门店员工离职，主管确认交接",
	}, continuityTenantAdmin(store.TenantID), "arrival-inheritance")
	if err != nil {
		t.Fatalf("inherit arrival conversation: %v", err)
	}
	currentBinding := repositories.ArrivalRepository.GetBinding(db, arrivalBinding.ID, store.TenantID)
	if currentBinding == nil || currentBinding.StoreStaffBindingID != targetBinding.ID ||
		currentBinding.WxWorkProtocolInstanceID != targetInstance.ID || currentBinding.ConversationID != successor.ID {
		t.Fatalf("arrival binding did not move to current successor: %+v", currentBinding)
	}
	if currentBinding.ProtocolConversationCiphertext != "" || currentBinding.ProtocolConversationNonce != "" ||
		currentBinding.ProtocolConversationFingerprint != "" || currentBinding.ProtocolMappedAt != nil {
		t.Fatalf("stale protocol mapping survived inheritance: %+v", currentBinding)
	}
	currentTicket := repositories.ArrivalRepository.GetBindingTicket(db, ticket.ID, store.TenantID)
	if currentTicket == nil || currentTicket.StoreStaffBindingID != sourceBinding.ID ||
		currentTicket.WxWorkProtocolInstanceID != sourceInstance.ID || currentTicket.ConversationID != sourceConversation.ID {
		t.Fatalf("immutable ticket evidence was rewritten: %+v", currentTicket)
	}
	if !arrivalBindingMatchesTicketDB(db, currentBinding, currentTicket) {
		t.Fatal("current arrival binding no longer traces to immutable ticket evidence")
	}
	if status := ArrivalLinkService.bindingStatusForCardTicket(store.TenantID, store.ID, currentBinding); status != enums.ArrivalBindingStatusLegacyUnmapped {
		t.Fatalf("arrival status before target callback=%q, want legacy_unmapped", status)
	}
	externalID := strings.TrimPrefix(external.ExternalID, "wxwork_protocol:")
	if err := WxWorkProtocolService.upsertConversationMapping(targetInstance, successor.ID, request.WxProtocolChatMsg{}, externalID, `{}`); err != nil {
		t.Fatalf("activate target protocol mapping: %v", err)
	}
	if status := ArrivalLinkService.bindingStatusForCardTicket(store.TenantID, store.ID, currentBinding); status != enums.ArrivalBindingStatusBound {
		t.Fatalf("arrival status after target callback=%q, want bound", status)
	}
	var audit models.ArrivalAuditLog
	if err := db.Where("entity_type = ? AND entity_id = ? AND action = ?", "arrival_store_binding", arrivalBinding.ID, "conversation_inheritance").Take(&audit).Error; err != nil {
		t.Fatalf("find arrival inheritance audit: %v", err)
	}
	for _, secret := range []string{sourceInstance.Guid, targetInstance.Guid, sourceInstance.EmployeeUserID, targetInstance.EmployeeUserID, external.ExternalID, "stale-protocol"} {
		if secret != "" && strings.Contains(audit.DetailJSON, secret) {
			t.Fatalf("arrival inheritance audit leaked protocol identifier %q", secret)
		}
	}
}

func TestStoreConversationClosedManualInheritanceReusesPendingSessionOnFirstInbound(t *testing.T) {
	db, store, channel, _, sourceInstance, _, conversation := setupStoreConversationContinuityFixture(t, "closed-manual-inheritance")
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source session: %v", err)
	}
	closedAt := time.Now()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"status": enums.IMConversationStatusClosed, "closed_at": closedAt, "updated_at": closedAt,
	}).Error; err != nil {
		t.Fatalf("close conversation: %v", err)
	}
	if err := db.Model(&models.ConversationRouteState{}).Where("conversation_id = ?", conversation.ID).Updates(map[string]any{
		"route_status": enums.ConversationRouteStatusClosed, "route_target": "closed", "updated_at": closedAt,
	}).Error; err != nil {
		t.Fatalf("close route: %v", err)
	}
	conversation.Status = enums.IMConversationStatusClosed
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "closed-manual-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "closed-manual-target", "online")
	successor, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID: conversation.ID, TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID: targetInstance.ID, Reason: "关闭会话离职交接",
	}, continuityTenantAdmin(store.TenantID), "closed-manual-inheritance")
	if err != nil {
		t.Fatalf("inherit closed conversation: %v", err)
	}
	if successor == nil || successor.ID == conversation.ID {
		t.Fatalf("manual inheritance must create a successor: %+v", successor)
	}

	sessionNo, err := ConversationChannelSessionService.PrepareInbound(successor.ID, targetInstance, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("prepare first target inbound: %v", err)
	}
	if sessionNo != 1 {
		t.Fatalf("first target inbound session=%d, want pending manual session 1", sessionNo)
	}
	if sessions := ConversationChannelSessionService.ListInTenant(successor.ID, store.TenantID); len(sessions) != 1 || sessions[0].StartReason != conversationSessionReasonManualInheritance {
		t.Fatalf("first target inbound must not create a duplicate session: %+v", sessions)
	}
	if sourceAfter := ConversationService.Get(conversation.ID); sourceAfter == nil || sourceAfter.Status != enums.IMConversationStatusClosed {
		t.Fatalf("source conversation must remain closed: %+v", sourceAfter)
	}
	if reopened := ConversationService.Get(successor.ID); reopened == nil || reopened.Status == enums.IMConversationStatusClosed || reopened.ClosedAt != nil {
		t.Fatalf("target inbound did not reopen inherited conversation: %+v", reopened)
	}
}

func TestStoreConversationManualInheritanceRejectsInvalidTargets(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal)
		wantError string
	}{
		{
			name: "other store",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, _ *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				otherStore := createContinuityTestStore(t, db, store.TenantID, "invalid-other-store")
				otherChannel := createContinuityTestChannel(t, db, store.TenantID, "invalid-other-store")
				binding := createWxWorkProtocolTestBinding(t, db, otherStore, "invalid-other-store")
				return binding, createContinuityTestInstance(t, db, otherStore, otherChannel, binding, "invalid-other-store", "online"), continuityTenantAdmin(store.TenantID)
			},
			wantError: "不属于当前门店",
		},
		{
			name: "offline instance",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				binding := createWxWorkProtocolTestBinding(t, db, store, "invalid-offline")
				return binding, createContinuityTestInstance(t, db, store, channel, binding, "invalid-offline", "offline"), continuityTenantAdmin(store.TenantID)
			},
			wantError: "不在线",
		},
		{
			name: "replaced instance",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				binding := createWxWorkProtocolTestBinding(t, db, store, "invalid-replaced")
				instance := createContinuityTestInstance(t, db, store, channel, binding, "invalid-replaced", "online")
				instance.ReplacedByInstanceID = 999
				if err := db.Model(instance).Update("replaced_by_instance_id", instance.ReplacedByInstanceID).Error; err != nil {
					t.Fatalf("mark instance replaced: %v", err)
				}
				return binding, instance, continuityTenantAdmin(store.TenantID)
			},
			wantError: "已被替换",
		},
		{
			name: "disabled binding",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				binding := createWxWorkProtocolTestBinding(t, db, store, "invalid-disabled-binding")
				if err := db.Model(binding).Updates(map[string]any{"status": enums.StatusDisabled, "active_user_id": nil}).Error; err != nil {
					t.Fatalf("disable target binding: %v", err)
				}
				binding.Status = enums.StatusDisabled
				return binding, createContinuityTestInstance(t, db, store, channel, binding, "invalid-disabled-binding", "online"), continuityTenantAdmin(store.TenantID)
			},
			wantError: "已停用",
		},
		{
			name: "unauthorized scope",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				binding := createWxWorkProtocolTestBinding(t, db, store, "invalid-scope")
				instance := createContinuityTestInstance(t, db, store, channel, binding, "invalid-scope", "online")
				return binding, instance, &dto.AuthPrincipal{
					UserID: 99001, TenantID: store.TenantID, ActiveTenantID: store.TenantID,
					Roles: []string{constants.RoleCodeCsTeamLeader}, Permissions: []string{constants.PermissionConversationInherit.Code},
				}
			},
			wantError: "无权限安排该会话继承",
		},
		{
			name: "other tenant",
			prepare: func(t *testing.T, db *gorm.DB, _ *models.Store, _ *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				otherStore := createContinuityTestStore(t, db, 202, "invalid-other-tenant")
				otherChannel := createContinuityTestChannel(t, db, 202, "invalid-other-tenant")
				binding := createWxWorkProtocolTestBinding(t, db, otherStore, "invalid-other-tenant")
				return binding, createContinuityTestInstance(t, db, otherStore, otherChannel, binding, "invalid-other-tenant", "online"), continuityTenantAdmin(101)
			},
			wantError: "超出当前数据范围",
		},
		{
			name: "deleted instance",
			prepare: func(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel) (*models.StoreStaffBinding, *models.WxWorkProtocolInstance, *dto.AuthPrincipal) {
				binding := createWxWorkProtocolTestBinding(t, db, store, "invalid-deleted-instance")
				instance := createContinuityTestInstance(t, db, store, channel, binding, "invalid-deleted-instance", "online")
				if err := db.Model(instance).Update("status", enums.StatusDeleted).Error; err != nil {
					t.Fatalf("delete target instance: %v", err)
				}
				instance.Status = enums.StatusDeleted
				return binding, instance, continuityTenantAdmin(store.TenantID)
			},
			wantError: "已停用",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, store, channel, _, sourceInstance, _, conversation := setupStoreConversationContinuityFixture(t, "invalid-target-"+strings.ReplaceAll(tt.name, " ", "-"))
			if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, sourceInstance, time.Now()); err != nil {
				t.Fatalf("prepare source session: %v", err)
			}
			binding, instance, operator := tt.prepare(t, db, store, channel)
			_, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
				ConversationID: conversation.ID, TargetStoreStaffBindingID: binding.ID,
				TargetWxWorkInstanceID: instance.ID, Reason: "测试非法目标",
			}, operator, "invalid-target")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestStoreConversationBatchInheritancePreviewAndExecute(t *testing.T) {
	db, store, channel, sourceBinding, sourceInstance, _, first := setupStoreConversationContinuityFixture(t, "batch-inheritance")
	if _, err := ConversationChannelSessionService.PrepareInbound(first.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare first conversation: %v", err)
	}
	second, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(
		continuityExternalUser("batch-second"), channel.ID, *createWelcomeTestAIAgent(t, db, ""),
		StoreConversationScope{StoreID: store.ID, StoreStaffBindingID: sourceBinding.ID},
	)
	if err != nil || !created {
		t.Fatalf("create second conversation: item=%+v created=%v err=%v", second, created, err)
	}
	if _, err := ConversationChannelSessionService.PrepareInbound(second.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare second conversation: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "batch-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "batch-target", "online")
	operator := continuityTenantAdmin(store.TenantID)
	previewReq := request.PreviewStoreConversationInheritanceRequest{
		SourceStoreStaffBindingID: sourceBinding.ID,
		TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID:    targetInstance.ID,
	}
	preview, err := ConversationService.PreviewStoreConversationInheritance(previewReq, operator)
	if err != nil {
		t.Fatalf("preview batch inheritance: %v", err)
	}
	if preview.EligibleCount != 2 || preview.ConflictCount != 0 || len(preview.Items) != 2 || len(preview.PreviewVersion) != sha256.Size*2 {
		t.Fatalf("unexpected batch preview: %+v", preview)
	}
	result, err := ConversationService.BatchInheritStoreConversations(request.BatchInheritStoreConversationsRequest{
		PreviewStoreConversationInheritanceRequest: previewReq,
		ConversationIDs: []int64{second.ID, first.ID}, PreviewVersion: preview.PreviewVersion,
		Reason: "原员工离职，批量交接",
	}, operator, "batch-inheritance")
	if err != nil {
		t.Fatalf("execute batch inheritance: %v", err)
	}
	if result.InheritedCount != 2 || result.CreatedCount != 2 || result.LinkedCount != 0 || len(result.ConversationIDs) != 2 {
		t.Fatalf("unexpected batch result: %+v", result)
	}
	for _, conversationID := range []int64{first.ID, second.ID} {
		source := ConversationService.Get(conversationID)
		if source == nil || source.StoreStaffBindingID != sourceBinding.ID || source.Status != enums.IMConversationStatusClosed {
			t.Fatalf("source conversation %d was mutated instead of closed: %+v", conversationID, source)
		}
		var link models.ConversationContinuityLink
		if err := db.Where("tenant_id = ? AND predecessor_conversation_id = ?", store.TenantID, conversationID).Take(&link).Error; err != nil {
			t.Fatalf("load continuity link for %d: %v", conversationID, err)
		}
		target := ConversationService.Get(link.SuccessorConversationID)
		if target == nil || target.StoreStaffBindingID != targetBinding.ID || target.CustomerID != source.CustomerID || target.ThreadKey == nil {
			t.Fatalf("successor conversation for %d invalid: %+v", conversationID, target)
		}
		sessions := ConversationChannelSessionService.ListInTenant(target.ID, store.TenantID)
		if len(sessions) != 1 || sessions[0].SessionNo != 1 || sessions[0].StartReason != conversationSessionReasonManualInheritance {
			t.Fatalf("successor session for %d invalid: %+v", conversationID, sessions)
		}
	}
}

func TestStoreConversationBatchInheritanceRejectsStalePreviewAtomically(t *testing.T) {
	db, store, channel, sourceBinding, sourceInstance, _, conversation := setupStoreConversationContinuityFixture(t, "batch-stale")
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source conversation: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "batch-stale-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "batch-stale-target", "online")
	operator := continuityTenantAdmin(store.TenantID)
	previewReq := request.PreviewStoreConversationInheritanceRequest{
		SourceStoreStaffBindingID: sourceBinding.ID,
		TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID:    targetInstance.ID,
	}
	preview, err := ConversationService.PreviewStoreConversationInheritance(previewReq, operator)
	if err != nil {
		t.Fatalf("preview stale batch: %v", err)
	}
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"last_message_summary": "状态已改变", "updated_at": time.Now().Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("mutate conversation after preview: %v", err)
	}
	_, err = ConversationService.BatchInheritStoreConversations(request.BatchInheritStoreConversationsRequest{
		PreviewStoreConversationInheritanceRequest: previewReq,
		ConversationIDs: []int64{conversation.ID}, PreviewVersion: preview.PreviewVersion,
		Reason: "过期预览不得执行",
	}, operator, "batch-stale")
	if err == nil || !strings.Contains(err.Error(), "重新预览") {
		t.Fatalf("expected stale preview rejection, got %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if current == nil || current.StoreStaffBindingID != sourceBinding.ID {
		t.Fatalf("stale preview partially changed conversation: %+v", current)
	}
}

func TestRealtimeMessageIncludesStoreConversationSessionNo(t *testing.T) {
	setupMessageWelcomeTestDB(t)
	message := &models.Message{ConversationID: 901, SessionNo: 7, SenderType: enums.IMSenderTypeSystem}
	result := WsService.buildRealtimeMessage(message)
	if result.SessionNo != 7 {
		t.Fatalf("realtime sessionNo=%d, want 7", result.SessionNo)
	}
}

func TestStoreConversationManualInheritanceLinksTargetConflict(t *testing.T) {
	db, store, channel, _, sourceInstance, external, sourceConversation := setupStoreConversationContinuityFixture(t, "inherit-conflict")
	if _, err := ConversationChannelSessionService.PrepareInbound(sourceConversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source session: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "inherit-conflict-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "inherit-conflict-target", "online")
	targetConversation, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *createWelcomeTestAIAgent(t, db, ""), StoreConversationScope{
		StoreID: store.ID, StoreStaffBindingID: targetBinding.ID,
	})
	if err != nil || !created || targetConversation.ID == sourceConversation.ID {
		t.Fatalf("create conflicting target conversation: item=%+v created=%v err=%v", targetConversation, created, err)
	}
	now := time.Now()
	sourceMessage := &models.Message{
		TenantID: sourceConversation.TenantID, ConversationID: sourceConversation.ID, SessionNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "source-history", SeqNo: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	targetMessage := &models.Message{
		TenantID: targetConversation.TenantID, ConversationID: targetConversation.ID, SessionNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "target-history", SeqNo: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(sourceMessage).Error; err != nil {
		t.Fatalf("create source history: %v", err)
	}
	if err := db.Create(targetMessage).Error; err != nil {
		t.Fatalf("create target history: %v", err)
	}
	inherited, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID:            sourceConversation.ID,
		TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID:    targetInstance.ID,
		Reason:                    "测试冲突",
	}, continuityTenantAdmin(store.TenantID), "continuity-conflict")
	if err != nil {
		t.Fatalf("inherit target thread conflict: %v", err)
	}
	if inherited == nil || inherited.ID != targetConversation.ID {
		t.Fatalf("inherit result=%+v, want existing target %d", inherited, targetConversation.ID)
	}
	sourceAfter := ConversationService.Get(sourceConversation.ID)
	if sourceAfter == nil || sourceAfter.Status != enums.IMConversationStatusClosed || sourceAfter.StoreStaffBindingID == targetBinding.ID {
		t.Fatalf("source conversation must remain physical history and close: %+v", sourceAfter)
	}
	targetAfter := ConversationService.Get(targetConversation.ID)
	if targetAfter == nil || targetAfter.StoreStaffBindingID != targetBinding.ID || targetAfter.ThreadKey == nil {
		t.Fatalf("target conversation scope changed unexpectedly: %+v", targetAfter)
	}
	var link models.ConversationContinuityLink
	if err := db.Where("tenant_id = ? AND predecessor_conversation_id = ?", store.TenantID, sourceConversation.ID).Take(&link).Error; err != nil {
		t.Fatalf("load continuity link: %v", err)
	}
	if link.SuccessorConversationID != targetConversation.ID || link.StoreID != store.ID || link.CustomerID != sourceConversation.CustomerID {
		t.Fatalf("continuity link=%+v", link)
	}
	var persistedSource models.Message
	var persistedTarget models.Message
	if err := db.First(&persistedSource, sourceMessage.ID).Error; err != nil || persistedSource.ConversationID != sourceConversation.ID {
		t.Fatalf("source message moved: item=%+v err=%v", persistedSource, err)
	}
	if err := db.First(&persistedTarget, targetMessage.ID).Error; err != nil || persistedTarget.ConversationID != targetConversation.ID {
		t.Fatalf("target message moved: item=%+v err=%v", persistedTarget, err)
	}
}

func TestStoreConversationManualInheritanceRejectsTargetWithExistingPredecessor(t *testing.T) {
	db, store, channel, _, firstInstance, external, firstSource := setupStoreConversationContinuityFixture(t, "inherit-linear-first")
	if _, err := ConversationChannelSessionService.PrepareInbound(firstSource.ID, firstInstance, time.Now()); err != nil {
		t.Fatalf("prepare first source: %v", err)
	}
	targetBinding := createWxWorkProtocolTestBinding(t, db, store, "inherit-linear-target")
	targetInstance := createContinuityTestInstance(t, db, store, channel, targetBinding, "inherit-linear-target", "online")
	target, err := ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID: firstSource.ID, TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID: targetInstance.ID, Reason: "建立第一条线性继承",
	}, continuityTenantAdmin(store.TenantID), "inherit-linear-first")
	if err != nil || target == nil {
		t.Fatalf("create first inheritance: target=%+v err=%v", target, err)
	}

	secondBinding := createWxWorkProtocolTestBinding(t, db, store, "inherit-linear-second")
	secondInstance := createContinuityTestInstance(t, db, store, channel, secondBinding, "inherit-linear-second", "online")
	secondSource, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(
		external, channel.ID, *createWelcomeTestAIAgent(t, db, ""),
		StoreConversationScope{StoreID: store.ID, StoreStaffBindingID: secondBinding.ID},
	)
	if err != nil || !created {
		t.Fatalf("create second source: item=%+v created=%v err=%v", secondSource, created, err)
	}
	if _, err := ConversationChannelSessionService.PrepareInbound(secondSource.ID, secondInstance, time.Now()); err != nil {
		t.Fatalf("prepare second source: %v", err)
	}
	_, err = ConversationService.InheritStoreConversation(request.InheritStoreConversationRequest{
		ConversationID: secondSource.ID, TargetStoreStaffBindingID: targetBinding.ID,
		TargetWxWorkInstanceID: targetInstance.ID, Reason: "不得形成多个前序",
	}, continuityTenantAdmin(store.TenantID), "inherit-linear-second")
	if err == nil || !strings.Contains(err.Error(), "已经继承了其他前序会话") {
		t.Fatalf("multiple predecessor error=%v", err)
	}
	if current := ConversationService.Get(secondSource.ID); current == nil || current.Status == enums.IMConversationStatusClosed {
		t.Fatalf("failed inheritance changed second source: %+v", current)
	}
	var incomingCount int64
	if err := db.Model(&models.ConversationContinuityLink{}).
		Where("tenant_id = ? AND successor_conversation_id = ?", store.TenantID, target.ID).
		Count(&incomingCount).Error; err != nil || incomingCount != 1 {
		t.Fatalf("target incoming link count=%d err=%v", incomingCount, err)
	}
}

func TestStoreConversationRelatedListRespectsInstanceDataScope(t *testing.T) {
	db, store, channel, _, sourceInstance, external, sourceConversation := setupStoreConversationContinuityFixture(t, "related-scope")
	if _, err := ConversationChannelSessionService.PrepareInbound(sourceConversation.ID, sourceInstance, time.Now()); err != nil {
		t.Fatalf("prepare source route: %v", err)
	}
	relatedBinding := createWxWorkProtocolTestBinding(t, db, store, "related-scope-target")
	relatedInstance := createContinuityTestInstance(t, db, store, channel, relatedBinding, "related-scope-target", "online")
	related, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(
		external, channel.ID, *createWelcomeTestAIAgent(t, db, ""),
		StoreConversationScope{StoreID: store.ID, StoreStaffBindingID: relatedBinding.ID},
	)
	if err != nil || !created {
		t.Fatalf("create related conversation: item=%+v created=%v err=%v", related, created, err)
	}
	if _, err := ConversationChannelSessionService.PrepareInbound(related.ID, relatedInstance, time.Now()); err != nil {
		t.Fatalf("prepare related route: %v", err)
	}
	leaderID := int64(66001)
	team := &models.AgentTeam{
		TenantID: store.TenantID, Name: "仅源实例客服组", LeaderUserID: leaderID,
		StoreScopeIDs: fmt.Sprintf("%d", store.ID), WxWorkInstanceScopeIDs: fmt.Sprintf("%d", sourceInstance.ID),
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create scoped team: %v", err)
	}
	operator := &dto.AuthPrincipal{
		UserID: leaderID, TenantID: store.TenantID, ActiveTenantID: store.TenantID,
		Roles: []string{constants.RoleCodeCsTeamLeader}, Permissions: []string{constants.PermissionConversationView.Code},
	}
	if items := ConversationService.ListRelatedStoreConversations(sourceConversation, operator); len(items) != 0 {
		t.Fatalf("related list leaked conversation outside instance scope: %+v", items)
	}
	if items := ConversationService.ListRelatedStoreConversations(sourceConversation, continuityTenantAdmin(store.TenantID)); len(items) != 1 || items[0].ID != related.ID {
		t.Fatalf("tenant admin related list=%+v, want conversation %d", items, related.ID)
	}
}

func TestStoreConversationLinkCustomerUpdatesCanonicalScopeAtomically(t *testing.T) {
	db, store, _, _, instance, external, conversation := setupStoreConversationContinuityFixture(t, "link-customer-success")
	sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now())
	if err != nil {
		t.Fatalf("prepare route: %v", err)
	}
	if _, err := MessageService.SendCustomerMessageInSession(conversation.ID, "link-customer-message", enums.IMMessageTypeText, "确认客户身份", "", external, sessionNo); err != nil {
		t.Fatalf("create source relation: %v", err)
	}
	sourceCustomerID := conversation.CustomerID
	target := createContinuityTestCustomer(t, db, store.TenantID, "link-customer-target")
	if err := ConversationService.LinkConversationCustomer(conversation.ID, target.ID, continuityTenantAdmin(store.TenantID)); err != nil {
		t.Fatalf("link conversation customer: %v", err)
	}

	updated := ConversationService.Get(conversation.ID)
	wantThreadKey := buildStoreConversationThreadKey(store.TenantID, store.ID, target.ID, conversation.StoreStaffBindingID)
	if updated == nil || updated.CustomerID != target.ID || updated.ThreadKey == nil || *updated.ThreadKey != wantThreadKey {
		t.Fatalf("conversation customer scope not updated: %+v", updated)
	}
	participant := repositories.ConversationParticipantRepository.Take(db, "tenant_id = ? AND conversation_id = ? AND participant_type = ?", store.TenantID, conversation.ID, enums.IMParticipantTypeCustomer)
	if participant == nil || participant.ParticipantID != target.ID {
		t.Fatalf("conversation participant not updated: %+v", participant)
	}
	identity := repositories.CustomerIdentityRepository.GetByInTenant(db, store.TenantID, enums.ExternalSourceWxWorkProtocol, external.ExternalID)
	if identity == nil || identity.CustomerID != target.ID {
		t.Fatalf("canonical identity not updated: %+v", identity)
	}
	if relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(db, store.TenantID, target.ID, store.ID); relation == nil || relation.LastConversationID != conversation.ID || relation.WxWorkInstanceID != instance.ID {
		t.Fatalf("target store customer relation not updated: %+v", relation)
	}
	if relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(db, store.TenantID, sourceCustomerID, store.ID); relation != nil {
		t.Fatalf("source store customer relation was not moved: %+v", relation)
	}
	var event models.ConversationEventLog
	if err := db.Where("conversation_id = ? AND content = ?", conversation.ID, "人工确认会话客户身份关联").Take(&event).Error; err != nil {
		t.Fatalf("find link audit event: %v", err)
	}
	if !strings.Contains(event.Payload, `"mappingMode":"operator_confirmed_cross_namespace"`) || strings.Contains(event.Payload, external.ExternalID) || strings.Contains(event.Content, external.ExternalID) {
		t.Fatalf("link audit is missing mapping mode or leaked external identity: content=%q payload=%q", event.Content, event.Payload)
	}
}

func TestStoreConversationLinkCustomerConflictRollsBackAllScopes(t *testing.T) {
	db, store, channel, binding, instance, external, conversation := setupStoreConversationContinuityFixture(t, "link-customer-conflict")
	sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now())
	if err != nil {
		t.Fatalf("prepare route: %v", err)
	}
	if _, err := MessageService.SendCustomerMessageInSession(conversation.ID, "link-conflict-message", enums.IMMessageTypeText, "原客户消息", "", external, sessionNo); err != nil {
		t.Fatalf("create source relation: %v", err)
	}
	targetConversation, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(
		continuityExternalUser("link-conflict-target"), channel.ID, *createWelcomeTestAIAgent(t, db, ""),
		StoreConversationScope{StoreID: store.ID, StoreStaffBindingID: binding.ID},
	)
	if err != nil || !created {
		t.Fatalf("create target conflict: item=%+v created=%v err=%v", targetConversation, created, err)
	}
	sourceCustomerID := conversation.CustomerID
	sourceThreadKey := *conversation.ThreadKey
	err = ConversationService.LinkConversationCustomer(conversation.ID, targetConversation.CustomerID, continuityTenantAdmin(store.TenantID))
	if err == nil || !strings.Contains(err.Error(), "已有独立会话") {
		t.Fatalf("expected thread conflict, got %v", err)
	}
	assertStoreConversationCustomerScope(t, db, conversation.ID, sourceCustomerID, sourceThreadKey, external.ExternalID, store.ID)
}

func TestStoreConversationLinkCustomerIdentityConflictRollsBackAllScopes(t *testing.T) {
	db, store, _, _, instance, external, conversation := setupStoreConversationContinuityFixture(t, "link-identity-conflict")
	sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, time.Now())
	if err != nil {
		t.Fatalf("prepare route: %v", err)
	}
	if _, err := MessageService.SendCustomerMessageInSession(conversation.ID, "link-identity-conflict-message", enums.IMMessageTypeText, "原客户消息", "", external, sessionNo); err != nil {
		t.Fatalf("create source relation: %v", err)
	}
	third := createContinuityTestCustomer(t, db, store.TenantID, "link-identity-third")
	identity := repositories.CustomerIdentityRepository.GetByInTenant(db, store.TenantID, enums.ExternalSourceWxWorkProtocol, external.ExternalID)
	if identity == nil {
		t.Fatal("source identity missing")
	}
	if err := repositories.CustomerIdentityRepository.UpdatesInTenant(db, identity.ID, store.TenantID, map[string]any{"customer_id": third.ID}); err != nil {
		t.Fatalf("create conflicting identity ownership: %v", err)
	}
	target := createContinuityTestCustomer(t, db, store.TenantID, "link-identity-target")
	sourceCustomerID := conversation.CustomerID
	sourceThreadKey := *conversation.ThreadKey
	err = ConversationService.LinkConversationCustomer(conversation.ID, target.ID, continuityTenantAdmin(store.TenantID))
	if err == nil || !strings.Contains(err.Error(), "已关联其他客户") {
		t.Fatalf("expected identity conflict, got %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if current == nil || current.CustomerID != sourceCustomerID || current.ThreadKey == nil || *current.ThreadKey != sourceThreadKey {
		t.Fatalf("identity conflict changed conversation: %+v", current)
	}
	participant := repositories.ConversationParticipantRepository.Take(db, "conversation_id = ? AND participant_type = ?", conversation.ID, enums.IMParticipantTypeCustomer)
	if participant == nil || participant.ParticipantID != 0 {
		t.Fatalf("identity conflict changed participant: %+v", participant)
	}
	if relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(db, store.TenantID, sourceCustomerID, store.ID); relation == nil || relation.LastConversationID != conversation.ID {
		t.Fatalf("identity conflict changed source store relation: %+v", relation)
	}
}

func assertStoreConversationCustomerScope(t *testing.T, db *gorm.DB, conversationID, customerID int64, threadKey, externalID string, storeID int64) {
	t.Helper()
	current := ConversationService.Get(conversationID)
	if current == nil || current.CustomerID != customerID || current.ThreadKey == nil || *current.ThreadKey != threadKey {
		t.Fatalf("conversation scope changed after rollback: %+v", current)
	}
	participant := repositories.ConversationParticipantRepository.Take(db, "conversation_id = ? AND participant_type = ?", conversationID, enums.IMParticipantTypeCustomer)
	if participant == nil || participant.ParticipantID != 0 {
		t.Fatalf("participant changed after rollback: %+v", participant)
	}
	identity := repositories.CustomerIdentityRepository.GetByInTenant(db, current.TenantID, enums.ExternalSourceWxWorkProtocol, externalID)
	if identity == nil || identity.CustomerID != customerID {
		t.Fatalf("identity changed after rollback: %+v", identity)
	}
	if relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(db, current.TenantID, customerID, storeID); relation == nil || relation.LastConversationID != conversationID {
		t.Fatalf("store relation changed after rollback: %+v", relation)
	}
}

func setupStoreConversationContinuityFixture(t *testing.T, suffix string) (*gorm.DB, *models.Store, *models.Channel, *models.StoreStaffBinding, *models.WxWorkProtocolInstance, openidentity.ExternalUser, *models.Conversation) {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	if err := db.AutoMigrate(
		&models.MiniProgramIdentity{},
		&models.StoreArrivalConnection{},
		&models.ArrivalStoreBinding{},
		&models.ArrivalBindingTicket{},
		&models.ArrivalAuditLog{},
	); err != nil {
		t.Fatalf("migrate arrival continuity fixtures: %v", err)
	}
	store := createContinuityTestStore(t, db, 101, suffix)
	channel := createContinuityTestChannel(t, db, 101, suffix)
	binding := createWxWorkProtocolTestBinding(t, db, store, suffix)
	instance := createContinuityTestInstance(t, db, store, channel, binding, suffix, "online")
	external := continuityExternalUser("customer-" + suffix)
	conversation, created, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *createWelcomeTestAIAgent(t, db, ""), StoreConversationScope{
		StoreID: store.ID, StoreStaffBindingID: binding.ID,
	})
	if err != nil || !created {
		t.Fatalf("create store conversation: item=%+v created=%v err=%v", conversation, created, err)
	}
	return db, store, channel, binding, instance, external, conversation
}

func createContinuityTestStore(t *testing.T, db *gorm.DB, tenantID int64, suffix string) *models.Store {
	t.Helper()
	now := time.Now()
	store := &models.Store{
		TenantID: tenantID, StoreCode: "continuity-" + suffix, Name: "连续会话测试门店", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create continuity store: %v", err)
	}
	return store
}

func createContinuityTestChannel(t *testing.T, db *gorm.DB, tenantID int64, suffix string) *models.Channel {
	t.Helper()
	now := time.Now()
	channel := &models.Channel{
		TenantID: tenantID, Name: "企微员工号", ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID: "continuity-" + suffix, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create continuity channel: %v", err)
	}
	return channel
}

func createContinuityTestInstance(t *testing.T, db *gorm.DB, store *models.Store, channel *models.Channel, binding *models.StoreStaffBinding, suffix, health string) *models.WxWorkProtocolInstance {
	t.Helper()
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		TenantID: store.TenantID, Guid: "continuity-guid-" + suffix, ChannelID: channel.ID,
		EmployeeUserID: "continuity-user-" + suffix, EmployeeName: "连续会话员工",
		StoreID: store.ID, StoreStaffBindingID: binding.ID, HealthStatus: health, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create continuity instance: %v", err)
	}
	return instance
}

func createContinuityTestCustomer(t *testing.T, db *gorm.DB, tenantID int64, suffix string) *models.Customer {
	t.Helper()
	now := time.Now()
	customer := &models.Customer{
		TenantID: tenantID, Name: "连续会话客户-" + suffix, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create continuity customer: %v", err)
	}
	return customer
}

func continuityExternalUser(suffix string) openidentity.ExternalUser {
	return openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     canonicalWxWorkProtocolExternalID("continuity-external-" + suffix),
		ExternalName:   "连续会话客户",
	}
}

func continuityTenantAdmin(tenantID int64) *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID: tenantID + 9000, ActiveTenantID: tenantID, TenantID: tenantID,
		Username: "continuity-supervisor", Roles: []string{constants.RoleCodeTenantAdmin},
		Permissions: []string{constants.PermissionConversationInherit.Code},
	}
}
