package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestWxWorkKFRuntimeTenantIsolation(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	if err := fixture.db.AutoMigrate(
		&models.WxWorkKFSyncState{},
		&models.WxWorkKFConversation{},
		&models.WxWorkKFMessageRef{},
		&models.ChannelMessageOutbox{},
	); err != nil {
		t.Fatalf("migrate wxwork kf tenant tables: %v", err)
	}

	now := time.Now()
	kfChannelA := &models.Channel{
		TenantID: fixture.adminA.ActiveTenantID, Name: "A租户企业微信客服", ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID: "wxwork-kf-runtime-a", ConfigJSON: `{"openKfId":"runtime-open-a"}`, Status: enums.StatusOk,
	}
	kfChannelB := &models.Channel{
		TenantID: fixture.adminB.ActiveTenantID, Name: "B租户企业微信客服", ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID: "wxwork-kf-runtime-b", ConfigJSON: `{"openKfId":"runtime-open-b"}`, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(kfChannelA).Error; err != nil {
		t.Fatalf("create tenant A kf channel: %v", err)
	}
	if err := fixture.db.Create(kfChannelB).Error; err != nil {
		t.Fatalf("create tenant B kf channel: %v", err)
	}

	conversationA := &models.Conversation{TenantID: fixture.adminA.ActiveTenantID, ChannelID: kfChannelA.ID, CustomerName: "A客户", Status: enums.IMConversationStatusAIServing, LastActiveAt: now, LastMessageAt: now}
	conversationB := &models.Conversation{TenantID: fixture.adminB.ActiveTenantID, ChannelID: kfChannelB.ID, CustomerName: "B客户", Status: enums.IMConversationStatusAIServing, LastActiveAt: now, LastMessageAt: now}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}

	if err := WxWorkKFInboundService.upsertConversationMapping(conversationA.ID, kfChannelA.ID, "runtime-open-a", "external-a", "", enums.WxWorkKFSessionStatusActive, 0, "", ""); err != nil {
		t.Fatalf("create tenant A kf mapping: %v", err)
	}
	mappingA := WxWorkKFConversationService.GetByConversationIDInTenant(conversationA.ID, fixture.adminA.ActiveTenantID)
	if mappingA == nil || mappingA.TenantID != fixture.adminA.ActiveTenantID || mappingA.ChannelID != kfChannelA.ID {
		t.Fatalf("unexpected tenant A mapping: %+v", mappingA)
	}
	if err := WxWorkKFInboundService.upsertConversationMapping(conversationA.ID, kfChannelB.ID, "runtime-open-b", "external-b", "", enums.WxWorkKFSessionStatusActive, 0, "", ""); err == nil {
		t.Fatal("tenant A conversation must reject tenant B kf channel mapping")
	}
	mappingA = WxWorkKFConversationService.GetByConversationIDInTenant(conversationA.ID, fixture.adminA.ActiveTenantID)
	if mappingA == nil || mappingA.ChannelID != kfChannelA.ID {
		t.Fatalf("tenant A mapping changed after cross-tenant update: %+v", mappingA)
	}

	if err := WxWorkKFInboundService.saveNextCursor(fixture.adminA.ActiveTenantID, "runtime-open-a", "cursor-a"); err != nil {
		t.Fatalf("save tenant A cursor: %v", err)
	}
	if err := WxWorkKFInboundService.saveNextCursor(fixture.adminB.ActiveTenantID, "runtime-open-b", "cursor-b"); err != nil {
		t.Fatalf("save tenant B cursor: %v", err)
	}
	stateB := WxWorkKFSyncStateService.GetByOpenKfIDInTenant("runtime-open-b", fixture.adminB.ActiveTenantID)
	if stateB == nil || stateB.NextCursor != "cursor-b" {
		t.Fatalf("unexpected tenant B cursor: %+v", stateB)
	}
	if WxWorkKFSyncStateService.GetByOpenKfIDInTenant("runtime-open-b", fixture.adminA.ActiveTenantID) != nil {
		t.Fatal("tenant A must not read tenant B sync cursor")
	}
	if err := WxWorkKFSyncStateService.UpdatesInTenant(stateB.ID, fixture.adminA.ActiveTenantID, map[string]any{"next_cursor": "cross-tenant"}); err != nil {
		t.Fatalf("foreign cursor update returned database error: %v", err)
	}
	stateB = WxWorkKFSyncStateService.GetByOpenKfIDInTenant("runtime-open-b", fixture.adminB.ActiveTenantID)
	if stateB == nil || stateB.NextCursor != "cursor-b" {
		t.Fatalf("tenant B cursor changed through tenant A condition: %+v", stateB)
	}

	messageB := createWxWorkKFTenantMessage(t, fixture, conversationB.ID, 1, "kf-message-b")
	if err := ChannelMessageOutboxService.Create(&models.ChannelMessageOutbox{
		TenantID:       fixture.adminA.ActiveTenantID,
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: conversationB.ID,
		MessageID:      messageB.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
	}); err == nil {
		t.Fatal("tenant A outbox must not target tenant B conversation")
	}

	validOutboxB := &models.ChannelMessageOutbox{
		TenantID:       fixture.adminB.ActiveTenantID,
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: conversationB.ID,
		MessageID:      messageB.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
	}
	if err := ChannelMessageOutboxService.Create(validOutboxB); err != nil {
		t.Fatalf("create tenant B outbox: %v", err)
	}
	claimed, err := ChannelMessageOutboxService.TryMarkSending(validOutboxB.ID, fixture.adminA.ActiveTenantID)
	if err != nil || claimed {
		t.Fatalf("tenant A claimed tenant B outbox: claimed=%v err=%v", claimed, err)
	}
	currentOutboxB := ChannelMessageOutboxService.GetInTenant(validOutboxB.ID, fixture.adminB.ActiveTenantID)
	if currentOutboxB == nil || currentOutboxB.SendStatus != string(enums.ChannelMessageOutboxStatusPending) {
		t.Fatalf("tenant B outbox changed through tenant A claim: %+v", currentOutboxB)
	}

	messageB2 := createWxWorkKFTenantMessage(t, fixture, conversationB.ID, 2, "kf-message-b-2")
	corruptOutboxA := &models.ChannelMessageOutbox{
		TenantID:       fixture.adminA.ActiveTenantID,
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: conversationB.ID,
		MessageID:      messageB2.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
	}
	if err := fixture.db.Create(corruptOutboxA).Error; err != nil {
		t.Fatalf("create intentionally corrupt outbox: %v", err)
	}
	if err := WxWorkKFOutboundService.processOutbox(corruptOutboxA.ID, fixture.adminA.ActiveTenantID); err != nil {
		t.Fatalf("process corrupt tenant A outbox: %v", err)
	}
	corruptOutboxA = ChannelMessageOutboxService.GetInTenant(corruptOutboxA.ID, fixture.adminA.ActiveTenantID)
	if corruptOutboxA == nil || corruptOutboxA.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || !strings.Contains(corruptOutboxA.LastError, "平台消息不存在") {
		t.Fatalf("corrupt outbox was not failed safely: %+v", corruptOutboxA)
	}
	unchangedMessageB2 := repositories.MessageRepository.GetInTenant(fixture.db, messageB2.ID, fixture.adminB.ActiveTenantID)
	if unchangedMessageB2 == nil || unchangedMessageB2.Content != messageB2.Content {
		t.Fatalf("tenant B message changed while rejecting corrupt outbox: %+v", unchangedMessageB2)
	}
}

func TestWxWorkCLIBridgePollsOnlyChannelTenant(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	if err := fixture.db.AutoMigrate(&models.WxWorkKFConversation{}, &models.WxWorkKFMessageRef{}, &models.ChannelMessageOutbox{}); err != nil {
		t.Fatalf("migrate wxwork cli tenant tables: %v", err)
	}

	now := time.Now()
	channelA := &models.Channel{TenantID: fixture.adminA.ActiveTenantID, Name: "A租户CLI", ChannelType: enums.ChannelTypeWxWorkCLI, ChannelID: "wxwork-cli-a", ConfigJSON: `{"bridgeToken":"token-a","defaultChatType":1}`, Status: enums.StatusOk}
	channelB := &models.Channel{TenantID: fixture.adminB.ActiveTenantID, Name: "B租户CLI", ChannelType: enums.ChannelTypeWxWorkCLI, ChannelID: "wxwork-cli-b", ConfigJSON: `{"bridgeToken":"token-b","defaultChatType":1}`, Status: enums.StatusOk}
	if err := fixture.db.Create(channelA).Error; err != nil {
		t.Fatalf("create tenant A cli channel: %v", err)
	}
	if err := fixture.db.Create(channelB).Error; err != nil {
		t.Fatalf("create tenant B cli channel: %v", err)
	}
	conversationA := &models.Conversation{TenantID: fixture.adminA.ActiveTenantID, ChannelID: channelA.ID, CustomerName: "A CLI客户", Status: enums.IMConversationStatusAIServing, LastActiveAt: now, LastMessageAt: now}
	conversationB := &models.Conversation{TenantID: fixture.adminB.ActiveTenantID, ChannelID: channelB.ID, CustomerName: "B CLI客户", Status: enums.IMConversationStatusAIServing, LastActiveAt: now, LastMessageAt: now}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A cli conversation: %v", err)
	}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B cli conversation: %v", err)
	}
	for _, mapping := range []*models.WxWorkKFConversation{
		{TenantID: fixture.adminA.ActiveTenantID, ConversationID: conversationA.ID, ChannelID: channelA.ID, OpenKfID: "wxwork_cli:single", ExternalUserID: "chat-a", Status: enums.StatusOk},
		{TenantID: fixture.adminB.ActiveTenantID, ConversationID: conversationB.ID, ChannelID: channelB.ID, OpenKfID: "wxwork_cli:single", ExternalUserID: "chat-b", Status: enums.StatusOk},
	} {
		if err := fixture.db.Create(mapping).Error; err != nil {
			t.Fatalf("create cli mapping: %v", err)
		}
	}
	messageA := &models.Message{TenantID: fixture.adminA.ActiveTenantID, ConversationID: conversationA.ID, ClientMsgID: "cli-message-a", SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "A回复", SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	messageB := &models.Message{TenantID: fixture.adminB.ActiveTenantID, ConversationID: conversationB.ID, ClientMsgID: "cli-message-b", SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "B回复", SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	if err := fixture.db.Create(messageA).Error; err != nil {
		t.Fatalf("create tenant A cli message: %v", err)
	}
	if err := fixture.db.Create(messageB).Error; err != nil {
		t.Fatalf("create tenant B cli message: %v", err)
	}
	outboxA := &models.ChannelMessageOutbox{TenantID: fixture.adminA.ActiveTenantID, ChannelType: enums.ChannelTypeWxWorkCLI, ConversationID: conversationA.ID, MessageID: messageA.ID, SendStatus: string(enums.ChannelMessageOutboxStatusPending)}
	outboxB := &models.ChannelMessageOutbox{TenantID: fixture.adminB.ActiveTenantID, ChannelType: enums.ChannelTypeWxWorkCLI, ConversationID: conversationB.ID, MessageID: messageB.ID, SendStatus: string(enums.ChannelMessageOutboxStatusPending)}
	if err := ChannelMessageOutboxService.Create(outboxA); err != nil {
		t.Fatalf("create tenant A cli outbox: %v", err)
	}
	if err := ChannelMessageOutboxService.Create(outboxB); err != nil {
		t.Fatalf("create tenant B cli outbox: %v", err)
	}

	result, err := WxWorkCLIBridgeService.PollOutbox(request.WxWorkCLIOutboxPollRequest{ChannelID: channelA.ChannelID, BridgeToken: "token-a", Limit: 20})
	if err != nil {
		t.Fatalf("poll tenant A cli outbox: %v", err)
	}
	if result == nil || len(result.Items) != 1 || result.Items[0].OutboxID != outboxA.ID {
		t.Fatalf("tenant A cli poll returned foreign tasks: %+v", result)
	}
	currentA := ChannelMessageOutboxService.GetInTenant(outboxA.ID, fixture.adminA.ActiveTenantID)
	currentB := ChannelMessageOutboxService.GetInTenant(outboxB.ID, fixture.adminB.ActiveTenantID)
	if currentA == nil || currentA.SendStatus != string(enums.ChannelMessageOutboxStatusSending) {
		t.Fatalf("tenant A outbox was not claimed: %+v", currentA)
	}
	if currentB == nil || currentB.SendStatus != string(enums.ChannelMessageOutboxStatusPending) {
		t.Fatalf("tenant B outbox changed during tenant A poll: %+v", currentB)
	}
	if err := WxWorkCLIBridgeService.MarkOutboxSent(request.WxWorkCLIOutboxSentRequest{ChannelID: channelA.ChannelID, BridgeToken: "token-a", OutboxID: outboxB.ID, ExternalMsgID: "foreign"}); err == nil {
		t.Fatal("tenant A cli credentials must not complete tenant B outbox")
	}
	currentB = ChannelMessageOutboxService.GetInTenant(outboxB.ID, fixture.adminB.ActiveTenantID)
	if currentB == nil || currentB.SendStatus != string(enums.ChannelMessageOutboxStatusPending) {
		t.Fatalf("tenant B outbox changed after foreign completion: %+v", currentB)
	}
}

func createWxWorkKFTenantMessage(t *testing.T, fixture conversationRuntimeTenantFixture, conversationID, seqNo int64, clientMsgID string) *models.Message {
	t.Helper()
	now := time.Now()
	item := &models.Message{
		TenantID:       fixture.adminB.ActiveTenantID,
		ConversationID: conversationID,
		ClientMsgID:    clientMsgID,
		SenderType:     enums.IMSenderTypeAgent,
		MessageType:    enums.IMMessageTypeText,
		Content:        clientMsgID,
		SeqNo:          seqNo,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
	}
	if err := fixture.db.Create(item).Error; err != nil {
		t.Fatalf("create message %s: %v", clientMsgID, err)
	}
	return item
}
