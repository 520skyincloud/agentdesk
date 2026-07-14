package migration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillConversationAndTicketDomainTenantsCoversAllChildrenAndIsIdempotent(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	legacy := createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createConversationTicketTenant(t, db, "conversation-domain-a")
	channelA := createConversationTicketChannel(t, db, tenantA.ID, "conversation-channel-a")
	customerA := createConversationTicketCustomer(t, db, tenantA.ID, "conversation-customer-a")
	userA := createConversationTicketUser(t, db, tenantA.ID, "conversation-user-a")
	platformUser := createConversationTicketUser(t, db, 0, "conversation-platform-user")
	teamA := createConversationTicketTeam(t, db, tenantA.ID, "conversation-team-a")
	squadA := createConversationTicketSquad(t, db, tenantA.ID, teamA.ID, "conversation-squad-a")
	storeA := createConversationTicketStore(t, db, tenantA.ID, "conversation-store-a")
	wxWorkA := createConversationTicketWxWork(t, db, tenantA.ID, storeA.ID, channelA.ID, "conversation-wxwork-a")

	conversationA := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, teamA.ID, userA.ID, "tenant-a-conversation")
	legacyConversation := createConversationTicketConversation(t, db, 0, 0, 0, 0, 0, "legacy-conversation")
	messageA := createConversationTicketMessage(t, db, 0, conversationA.ID, 1, 0, "message-a")
	replyA := createConversationTicketMessage(t, db, 0, conversationA.ID, 2, messageA.ID, "reply-a")
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversationA.ID).Update("last_message_id", replyA.ID).Error; err != nil {
		t.Fatalf("set conversation last message: %v", err)
	}
	conversationA.LastMessageID = replyA.ID

	route := &models.ConversationRouteState{
		ConversationID: conversationA.ID, StoreID: storeA.ID, WxWorkInstanceID: wxWorkA.ID,
		RouteStatus: enums.ConversationRouteStatusAIServing, AuditFields: conversationTicketAuditFields(),
	}
	summary := &models.ConversationSessionSummary{
		ConversationID: conversationA.ID, SessionNo: 1, WxWorkInstanceID: wxWorkA.ID, StoreID: storeA.ID,
		CustomerID: customerA.ID, LastMessageID: replyA.ID, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	syncLog := &models.MessageSyncLog{
		ConversationID: conversationA.ID, MessageID: messageA.ID, Direction: enums.MessageSyncDirectionWecomToAgentDesk,
		SyncStatus: enums.MessageSyncStatusSuccess, AuditFields: conversationTicketAuditFields(),
	}
	participant := &models.ConversationParticipant{
		ConversationID: conversationA.ID, ParticipantType: "customer", ParticipantID: customerA.ID,
		Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	readState := &models.ConversationReadState{
		ConversationID: conversationA.ID, ReaderType: enums.IMSenderTypeAgent, ReaderID: userA.ID,
		LastReadMessageID: replyA.ID, LastReadSeqNo: 2, AuditFields: conversationTicketAuditFields(),
	}
	wxMapping := &models.WxWorkKFConversation{
		ConversationID: conversationA.ID, ChannelID: channelA.ID, OpenKfID: "open-kf-a", ExternalUserID: "external-a",
		Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	wxMessageRef := &models.WxWorkKFMessageRef{
		ConversationID: conversationA.ID, MessageID: messageA.ID, WxMsgID: "wx-message-a",
		Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkKF, ConversationID: conversationA.ID, MessageID: replyA.ID,
		SendStatus: "pending", AuditFields: conversationTicketAuditFields(),
	}
	storeRoomOutbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversationA.ID, MessageID: -time.Now().UnixNano(),
		Payload: `{"kind":"store_room_handoff_notice"}`, SendStatus: "pending", AuditFields: conversationTicketAuditFields(),
	}
	assignment := &models.ConversationAssignment{
		ConversationID: conversationA.ID, SquadID: squadA.ID, ToUserID: userA.ID,
		Status: enums.IMAssignmentStatusActive, CreatedAt: time.Now(), OperatorID: platformUser.ID,
	}
	eventLog := &models.ConversationEventLog{
		ConversationID: conversationA.ID, EventType: enums.IMEventTypeCreate,
		OperatorType: enums.IMSenderTypeSystem, CreatedAt: time.Now(),
	}
	interrupt := &models.ConversationInterrupt{
		ConversationID: conversationA.ID, SourceMessageID: messageA.ID, LastResumeMessageID: replyA.ID,
		CheckPointID: "checkpoint-a", InterruptID: "interrupt-a", Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	detachedSyncLog := &models.MessageSyncLog{
		Direction: enums.MessageSyncDirectionWecomToAgentDesk, SyncStatus: enums.MessageSyncStatusPending,
		Payload: "raw protocol notification", AuditFields: conversationTicketAuditFields(),
	}
	detachedCheckpoint := &models.ConversationInterrupt{
		CheckPointID: "detached-checkpoint", CheckPointData: "checkpoint-data", Status: "checkpointed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	for _, item := range []any{route, summary, syncLog, participant, readState, wxMapping, wxMessageRef, outbox, storeRoomOutbox, assignment, eventLog, interrupt, detachedSyncLog, detachedCheckpoint} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create conversation child %T: %v", item, err)
		}
	}

	ticketA := createConversationTicketTicket(t, db, 0, "TICKET-A", customerA.ID, conversationA.ID, userA.ID)
	legacyTicket := createConversationTicketTicket(t, db, 0, "TICKET-LEGACY", 0, 0, 0)
	progress := &models.TicketProgress{TicketID: ticketA.ID, Content: "处理中", AuthorID: userA.ID, CreatedAt: time.Now()}
	tenantView := &models.TicketView{UserID: userA.ID, Name: "租户视图", FiltersJSON: `{}`, AuditFields: conversationTicketAuditFields()}
	platformView := &models.TicketView{UserID: platformUser.ID, Name: "历史平台视图", FiltersJSON: `{}`, AuditFields: conversationTicketAuditFields()}
	for _, item := range []any{progress, tenantView, platformView} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create ticket child %T: %v", item, err)
		}
	}

	rootTag := &models.Tag{TenantID: tenantA.ID, Name: "服务问题", Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(rootTag).Error; err != nil {
		t.Fatalf("create root tag: %v", err)
	}
	childTag := &models.Tag{ParentID: rootTag.ID, Name: "清洁", Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	standaloneTag := &models.Tag{Name: "历史独立标签", Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(childTag).Error; err != nil {
		t.Fatalf("create child tag: %v", err)
	}
	if err := db.Create(standaloneTag).Error; err != nil {
		t.Fatalf("create standalone tag: %v", err)
	}
	conversationTag := &models.ConversationTag{ConversationID: conversationA.ID, TagID: rootTag.ID, AuditFields: conversationTicketAuditFields()}
	ticketTag := &models.TicketTag{TicketID: ticketA.ID, TagID: childTag.ID, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(conversationTag).Error; err != nil {
		t.Fatalf("create conversation tag: %v", err)
	}
	if err := db.Create(ticketTag).Error; err != nil {
		t.Fatalf("create ticket tag: %v", err)
	}
	relation := &models.StoreCustomerRelation{
		TenantID: tenantA.ID, CustomerID: customerA.ID, StoreID: storeA.ID, WxWorkInstanceID: wxWorkA.ID,
		LastConversationID: conversationA.ID, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create store customer relation: %v", err)
	}

	if err := db.Transaction(backfillConversationAndTicketDomainTenants); err != nil {
		t.Fatalf("backfill conversation domain: %v", err)
	}
	if err := db.Transaction(backfillConversationAndTicketDomainTenants); err != nil {
		t.Fatalf("repeat conversation domain backfill: %v", err)
	}

	tenantAItems := []struct {
		model any
		id    int64
	}{
		{model: &models.Conversation{}, id: conversationA.ID},
		{model: &models.Message{}, id: messageA.ID},
		{model: &models.Message{}, id: replyA.ID},
		{model: &models.ConversationRouteState{}, id: route.ID},
		{model: &models.ConversationSessionSummary{}, id: summary.ID},
		{model: &models.MessageSyncLog{}, id: syncLog.ID},
		{model: &models.ConversationParticipant{}, id: participant.ID},
		{model: &models.ConversationReadState{}, id: readState.ID},
		{model: &models.WxWorkKFConversation{}, id: wxMapping.ID},
		{model: &models.WxWorkKFMessageRef{}, id: wxMessageRef.ID},
		{model: &models.ChannelMessageOutbox{}, id: outbox.ID},
		{model: &models.ChannelMessageOutbox{}, id: storeRoomOutbox.ID},
		{model: &models.ConversationAssignment{}, id: assignment.ID},
		{model: &models.ConversationEventLog{}, id: eventLog.ID},
		{model: &models.ConversationInterrupt{}, id: interrupt.ID},
		{model: &models.Ticket{}, id: ticketA.ID},
		{model: &models.TicketProgress{}, id: progress.ID},
		{model: &models.TicketView{}, id: tenantView.ID},
		{model: &models.Tag{}, id: rootTag.ID},
		{model: &models.Tag{}, id: childTag.ID},
		{model: &models.ConversationTag{}, id: conversationTag.ID},
		{model: &models.TicketTag{}, id: ticketTag.ID},
	}
	for _, item := range tenantAItems {
		assertConversationTicketTenant(t, db, item.model, item.id, tenantA.ID)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, legacyConversation.ID, legacy.ID)
	assertConversationTicketTenant(t, db, &models.Ticket{}, legacyTicket.ID, legacy.ID)
	assertConversationTicketTenant(t, db, &models.TicketView{}, platformView.ID, legacy.ID)
	assertConversationTicketTenant(t, db, &models.Tag{}, standaloneTag.ID, legacy.ID)
	assertConversationTicketTenant(t, db, &models.MessageSyncLog{}, detachedSyncLog.ID, 0)
	assertConversationTicketTenant(t, db, &models.ConversationInterrupt{}, detachedCheckpoint.ID, 0)
}

func TestBackfillConversationAndTicketDomainTenantsRejectsUnknownSyntheticOutbox(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenant := createConversationTicketTenant(t, db, "synthetic-outbox")
	channel := createConversationTicketChannel(t, db, tenant.ID, "synthetic-outbox-channel")
	customer := createConversationTicketCustomer(t, db, tenant.ID, "synthetic-outbox-customer")
	conversation := createConversationTicketConversation(t, db, 0, channel.ID, customer.ID, 0, 0, "synthetic-outbox-conversation")
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: -99,
		Payload: `{"kind":"unknown"}`, SendStatus: "pending", AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create unknown synthetic outbox: %v", err)
	}

	err := db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "unknown synthetic message") {
		t.Fatalf("backfill error=%v want unknown synthetic outbox rejection", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, conversation.ID, 0)
	assertConversationTicketTenant(t, db, &models.ChannelMessageOutbox{}, outbox.ID, 0)
}

func TestBackfillConversationAndTicketDomainTenantsRejectsConversationConflictAndRollsBack(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createConversationTicketTenant(t, db, "conversation-conflict-a")
	tenantB := createConversationTicketTenant(t, db, "conversation-conflict-b")
	channelA := createConversationTicketChannel(t, db, tenantA.ID, "conversation-conflict-channel-a")
	customerA := createConversationTicketCustomer(t, db, tenantA.ID, "conversation-conflict-customer-a")
	customerB := createConversationTicketCustomer(t, db, tenantB.ID, "conversation-conflict-customer-b")
	good := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "good-before-conflict")
	conflict := createConversationTicketConversation(t, db, 0, channelA.ID, customerB.ID, 0, 0, "cross-tenant-conversation")

	err := db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with customer") {
		t.Fatalf("backfill error=%v want channel/customer conflict", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, good.ID, 0)
	assertConversationTicketTenant(t, db, &models.Conversation{}, conflict.ID, 0)
}

func TestBackfillConversationAndTicketDomainTenantsRejectsCrossTenantMessageReference(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createConversationTicketTenant(t, db, "message-reference-a")
	tenantB := createConversationTicketTenant(t, db, "message-reference-b")
	channelA := createConversationTicketChannel(t, db, tenantA.ID, "message-channel-a")
	channelB := createConversationTicketChannel(t, db, tenantB.ID, "message-channel-b")
	customerA := createConversationTicketCustomer(t, db, tenantA.ID, "message-customer-a")
	customerB := createConversationTicketCustomer(t, db, tenantB.ID, "message-customer-b")
	conversationA := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "message-conversation-a")
	conversationB := createConversationTicketConversation(t, db, 0, channelB.ID, customerB.ID, 0, 0, "message-conversation-b")
	messageB := createConversationTicketMessage(t, db, 0, conversationB.ID, 1, 0, "message-b")
	messageA := createConversationTicketMessage(t, db, 0, conversationA.ID, 1, messageB.ID, "message-a-quotes-b")

	err := db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "quoted message") {
		t.Fatalf("backfill error=%v want quoted message conflict", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, conversationA.ID, 0)
	assertConversationTicketTenant(t, db, &models.Message{}, messageA.ID, 0)
}

func TestBackfillConversationAndTicketDomainTenantsRejectsSharedTagAcrossTenants(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createConversationTicketTenant(t, db, "tag-conflict-a")
	tenantB := createConversationTicketTenant(t, db, "tag-conflict-b")
	channelA := createConversationTicketChannel(t, db, tenantA.ID, "tag-channel-a")
	customerA := createConversationTicketCustomer(t, db, tenantA.ID, "tag-customer-a")
	customerB := createConversationTicketCustomer(t, db, tenantB.ID, "tag-customer-b")
	conversationA := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "tag-conversation-a")
	ticketB := createConversationTicketTicket(t, db, 0, "TAG-TICKET-B", customerB.ID, 0, 0)
	tag := &models.Tag{Name: "错误共享标签", Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(tag).Error; err != nil {
		t.Fatalf("create shared tag: %v", err)
	}
	if err := db.Create(&models.ConversationTag{ConversationID: conversationA.ID, TagID: tag.ID, AuditFields: conversationTicketAuditFields()}).Error; err != nil {
		t.Fatalf("create conversation tag: %v", err)
	}
	if err := db.Create(&models.TicketTag{TicketID: ticketB.ID, TagID: tag.ID, AuditFields: conversationTicketAuditFields()}).Error; err != nil {
		t.Fatalf("create ticket tag: %v", err)
	}

	err := db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "tag component") {
		t.Fatalf("backfill error=%v want shared tag conflict", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, conversationA.ID, 0)
	assertConversationTicketTenant(t, db, &models.Ticket{}, ticketB.ID, 0)
	assertConversationTicketTenant(t, db, &models.Tag{}, tag.ID, 0)
}

func TestBackfillConversationAndTicketDomainTenantsRejectsCustomerRelationshipMismatch(t *testing.T) {
	t.Run("ticket conversation customer", func(t *testing.T) {
		db := setupConversationTicketTenantBackfillDB(t)
		createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
		tenantA := createConversationTicketTenant(t, db, "ticket-customer-mismatch")
		channelA := createConversationTicketChannel(t, db, tenantA.ID, "ticket-customer-channel")
		customerA := createConversationTicketCustomer(t, db, tenantA.ID, "ticket-conversation-customer")
		otherCustomerA := createConversationTicketCustomer(t, db, tenantA.ID, "ticket-other-customer")
		conversation := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "ticket-customer-conversation")
		ticket := createConversationTicketTicket(t, db, 0, "TICKET-CUSTOMER-MISMATCH", otherCustomerA.ID, conversation.ID, 0)

		err := db.Transaction(backfillConversationAndTicketDomainTenants)
		if err == nil || !strings.Contains(err.Error(), "conflicts with conversation") {
			t.Fatalf("backfill error=%v want ticket customer mismatch", err)
		}
		assertConversationTicketTenant(t, db, &models.Conversation{}, conversation.ID, 0)
		assertConversationTicketTenant(t, db, &models.Ticket{}, ticket.ID, 0)
	})

	t.Run("store relation last conversation customer", func(t *testing.T) {
		db := setupConversationTicketTenantBackfillDB(t)
		createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
		tenantA := createConversationTicketTenant(t, db, "relation-customer-mismatch")
		channelA := createConversationTicketChannel(t, db, tenantA.ID, "relation-customer-channel")
		customerA := createConversationTicketCustomer(t, db, tenantA.ID, "relation-conversation-customer")
		otherCustomerA := createConversationTicketCustomer(t, db, tenantA.ID, "relation-other-customer")
		storeA := createConversationTicketStore(t, db, tenantA.ID, "relation-customer-store")
		conversation := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "relation-customer-conversation")
		relation := &models.StoreCustomerRelation{
			TenantID: tenantA.ID, CustomerID: otherCustomerA.ID, StoreID: storeA.ID,
			LastConversationID: conversation.ID, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
		}
		if err := db.Create(relation).Error; err != nil {
			t.Fatalf("create mismatched relation: %v", err)
		}

		err := db.Transaction(backfillConversationAndTicketDomainTenants)
		if err == nil || !strings.Contains(err.Error(), "conflicts with last conversation") {
			t.Fatalf("backfill error=%v want relation customer mismatch", err)
		}
		assertConversationTicketTenant(t, db, &models.Conversation{}, conversation.ID, 0)
	})
}

func TestBackfillConversationAndTicketDomainTenantsRejectsOrphansAndInvalidExplicitTenant(t *testing.T) {
	db := setupConversationTicketTenantBackfillDB(t)
	createConversationTicketTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createConversationTicketTenant(t, db, "conversation-orphan-a")
	channelA := createConversationTicketChannel(t, db, tenantA.ID, "conversation-orphan-channel")
	customerA := createConversationTicketCustomer(t, db, tenantA.ID, "conversation-orphan-customer")
	conversation := createConversationTicketConversation(t, db, 0, channelA.ID, customerA.ID, 0, 0, "conversation-before-orphan")
	orphanMessage := createConversationTicketMessage(t, db, 0, 999999, 1, 0, "orphan-message")

	err := db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing conversation") {
		t.Fatalf("backfill error=%v want orphan message rejection", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, conversation.ID, 0)
	assertConversationTicketTenant(t, db, &models.Message{}, orphanMessage.ID, 0)

	if err := db.Delete(orphanMessage).Error; err != nil {
		t.Fatalf("delete orphan message: %v", err)
	}
	invalidTicket := createConversationTicketTicket(t, db, 888888, "INVALID-TENANT-TICKET", 0, 0, 0)
	err = db.Transaction(backfillConversationAndTicketDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing tenant") {
		t.Fatalf("backfill error=%v want invalid explicit tenant rejection", err)
	}
	assertConversationTicketTenant(t, db, &models.Conversation{}, conversation.ID, 0)
	assertConversationTicketTenant(t, db, &models.Ticket{}, invalidTicket.ID, invalidTicket.TenantID)
}

func setupConversationTicketTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation-ticket-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Channel{}, &models.Customer{}, &models.User{}, &models.AgentTeam{}, &models.AgentTeamSquad{},
		&models.Store{}, &models.WxWorkProtocolInstance{}, &models.StoreCustomerRelation{},
		&models.Conversation{}, &models.Message{}, &models.ConversationRouteState{}, &models.ConversationSessionSummary{},
		&models.MessageSyncLog{}, &models.ConversationParticipant{}, &models.ConversationReadState{},
		&models.WxWorkKFConversation{}, &models.WxWorkKFMessageRef{}, &models.ChannelMessageOutbox{},
		&models.ConversationAssignment{}, &models.ConversationEventLog{}, &models.ConversationInterrupt{},
		&models.Ticket{}, &models.TicketProgress{}, &models.TicketView{},
		&models.Tag{}, &models.ConversationTag{}, &models.TicketTag{},
	); err != nil {
		t.Fatalf("migrate conversation and ticket tenant tables: %v", err)
	}
	return db
}

func createConversationTicketTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk,
		AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createConversationTicketChannel(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Channel {
	t.Helper()
	item := &models.Channel{
		TenantID: tenantID, Name: name, ChannelType: enums.ChannelTypeWeb, ChannelID: name,
		Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return item
}

func createConversationTicketCustomer(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Customer {
	t.Helper()
	item := &models.Customer{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create customer %s: %v", name, err)
	}
	return item
}

func createConversationTicketUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	item := &models.User{
		TenantID: tenantID, Username: username, Nickname: username, Password: "test", Status: enums.StatusOk,
		AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return item
}

func createConversationTicketTeam(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.AgentTeam {
	t.Helper()
	item := &models.AgentTeam{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
	return item
}

func createConversationTicketSquad(t *testing.T, db *gorm.DB, tenantID, teamID int64, name string) *models.AgentTeamSquad {
	t.Helper()
	item := &models.AgentTeamSquad{TenantID: tenantID, TeamID: teamID, Name: name, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create squad %s: %v", name, err)
	}
	return item
}

func createConversationTicketStore(t *testing.T, db *gorm.DB, tenantID int64, code string) *models.Store {
	t.Helper()
	item := &models.Store{TenantID: tenantID, StoreCode: code, Name: code, Status: enums.StatusOk, AuditFields: conversationTicketAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return item
}

func createConversationTicketWxWork(t *testing.T, db *gorm.DB, tenantID, storeID, channelID int64, guid string) *models.WxWorkProtocolInstance {
	t.Helper()
	item := &models.WxWorkProtocolInstance{
		TenantID: tenantID, StoreID: storeID, ChannelID: channelID, Guid: guid,
		Status: enums.StatusOk, AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create wxwork %s: %v", guid, err)
	}
	return item
}

func createConversationTicketConversation(t *testing.T, db *gorm.DB, tenantID, channelID, customerID, teamID, assigneeID int64, name string) *models.Conversation {
	t.Helper()
	now := time.Now()
	item := &models.Conversation{
		TenantID: tenantID, ChannelID: channelID, CustomerID: customerID, CustomerName: name,
		CurrentTeamID: teamID, CurrentAssigneeID: assigneeID, Status: enums.IMConversationStatusActive,
		LastMessageAt: now, LastActiveAt: now, AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create conversation %s: %v", name, err)
	}
	return item
}

func createConversationTicketMessage(t *testing.T, db *gorm.DB, tenantID, conversationID, seqNo, quotedMessageID int64, clientMsgID string) *models.Message {
	t.Helper()
	item := &models.Message{
		TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: clientMsgID,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: clientMsgID,
		SeqNo: seqNo, SendStatus: enums.IMMessageStatusSent, QuotedMessageID: quotedMessageID,
		AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create message %s: %v", clientMsgID, err)
	}
	return item
}

func createConversationTicketTicket(t *testing.T, db *gorm.DB, tenantID int64, ticketNo string, customerID, conversationID, assigneeID int64) *models.Ticket {
	t.Helper()
	item := &models.Ticket{
		TenantID: tenantID, TicketNo: ticketNo, Title: ticketNo, Description: ticketNo,
		Source: enums.TicketSourceManual, CustomerID: customerID, ConversationID: conversationID,
		CurrentAssigneeID: assigneeID, Status: enums.TicketStatusPending, AuditFields: conversationTicketAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create ticket %s: %v", ticketNo, err)
	}
	return item
}

func assertConversationTicketTenant(t *testing.T, db *gorm.DB, model any, id, wantTenantID int64) {
	t.Helper()
	var row struct {
		TenantID int64
	}
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read tenant for %T %d: %v", model, id, err)
	}
	if row.TenantID != wantTenantID {
		t.Fatalf("%T %d tenant=%d want=%d", model, id, row.TenantID, wantTenantID)
	}
}

func conversationTicketAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
