package migration

import (
	"encoding/json"
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(45, "backfill conversation and ticket domain tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillConversationAndTicketDomainTenants(ctx.Tx)
		})
	})
}

type conversationDomainTenantResolver struct {
	resource       string
	resourceID     int64
	validTenantIDs map[int64]struct{}
	tenantID       int64
}

type tagTenantUnion struct {
	parents map[int64]int64
}

func backfillConversationAndTicketDomainTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before conversation tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	channelTenants, err := loadConversationDomainTenantIDs(tx, &models.Channel{})
	if err != nil {
		return err
	}
	customerTenants, err := loadConversationDomainTenantIDs(tx, &models.Customer{})
	if err != nil {
		return err
	}
	teamTenants, err := loadConversationDomainTenantIDs(tx, &models.AgentTeam{})
	if err != nil {
		return err
	}
	squadTenants, err := loadConversationDomainTenantIDs(tx, &models.AgentTeamSquad{})
	if err != nil {
		return err
	}
	storeTenants, err := loadConversationDomainTenantIDs(tx, &models.Store{})
	if err != nil {
		return err
	}
	wxWorkInstanceTenants, err := loadConversationDomainTenantIDs(tx, &models.WxWorkProtocolInstance{})
	if err != nil {
		return err
	}
	userTenants, err := loadConversationDomainTenantIDs(tx, &models.User{})
	if err != nil {
		return err
	}

	conversationTenants, conversations, err := backfillConversationTenants(
		tx,
		legacyTenant.ID,
		validTenantIDs,
		channelTenants,
		customerTenants,
		teamTenants,
		userTenants,
	)
	if err != nil {
		return err
	}
	conversationCustomerIDs := make(map[int64]int64, len(conversations))
	for i := range conversations {
		conversationCustomerIDs[conversations[i].ID] = conversations[i].CustomerID
	}
	messageTenants, messages, err := backfillMessageTenants(tx, validTenantIDs, conversationTenants)
	if err != nil {
		return err
	}
	if err := validateConversationMessageReferences(conversations, conversationTenants, messages, messageTenants); err != nil {
		return err
	}
	if err := backfillConversationChildren(
		tx,
		validTenantIDs,
		conversationTenants,
		messageTenants,
		customerTenants,
		storeTenants,
		wxWorkInstanceTenants,
		channelTenants,
		squadTenants,
		userTenants,
	); err != nil {
		return err
	}
	if err := validateStoreCustomerRelationConversationTenants(tx, conversationTenants, conversationCustomerIDs); err != nil {
		return err
	}

	ticketTenants, err := backfillTicketTenants(
		tx,
		legacyTenant.ID,
		validTenantIDs,
		customerTenants,
		conversationTenants,
		conversationCustomerIDs,
		userTenants,
	)
	if err != nil {
		return err
	}
	if err := backfillTicketChildren(tx, legacyTenant.ID, validTenantIDs, ticketTenants, userTenants); err != nil {
		return err
	}
	return backfillSharedTagTenants(tx, legacyTenant.ID, validTenantIDs, conversationTenants, ticketTenants)
}

func backfillConversationTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	channelTenants map[int64]int64,
	customerTenants map[int64]int64,
	teamTenants map[int64]int64,
	userTenants map[int64]int64,
) (map[int64]int64, []models.Conversation, error) {
	var list []models.Conversation
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, nil, err
	}
	result := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		resolver := newConversationDomainTenantResolver("conversation", item.ID, item.TenantID, validTenantIDs)
		if item.ChannelID > 0 {
			if err := resolver.mergeReference("channel", item.ChannelID, channelTenants); err != nil {
				return nil, nil, err
			}
		}
		if item.CustomerID > 0 {
			if err := resolver.mergeReference("customer", item.CustomerID, customerTenants); err != nil {
				return nil, nil, err
			}
		}
		if item.CurrentTeamID > 0 {
			if err := resolver.mergeReference("agent team", item.CurrentTeamID, teamTenants); err != nil {
				return nil, nil, err
			}
		}
		if err := mergeConversationActorTenant(resolver, "current assignee", item.CurrentAssigneeID, userTenants); err != nil {
			return nil, nil, err
		}
		if err := mergeConversationActorTenant(resolver, "closed by", item.ClosedBy, userTenants); err != nil {
			return nil, nil, err
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return nil, nil, err
		}
		if err := assignConversationDomainTenant(tx, &models.Conversation{}, "conversation", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, nil, err
		}
		item.TenantID = tenantID
		result[item.ID] = tenantID
	}
	return result, list, nil
}

func backfillMessageTenants(
	tx *gorm.DB,
	validTenantIDs map[int64]struct{},
	conversationTenants map[int64]int64,
) (map[int64]int64, []models.Message, error) {
	var list []models.Message
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, nil, err
	}
	result := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("message", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return nil, nil, err
		}
		if err := assignConversationDomainTenant(tx, &models.Message{}, "message", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, nil, err
		}
		item.TenantID = tenantID
		result[item.ID] = tenantID
	}
	return result, list, nil
}

func validateConversationMessageReferences(
	conversations []models.Conversation,
	conversationTenants map[int64]int64,
	messages []models.Message,
	messageTenants map[int64]int64,
) error {
	for i := range conversations {
		item := &conversations[i]
		if item.LastMessageID <= 0 {
			continue
		}
		if err := validateConversationDomainReference("conversation", item.ID, conversationTenants[item.ID], "last message", item.LastMessageID, messageTenants); err != nil {
			return err
		}
	}
	for i := range messages {
		item := &messages[i]
		if item.QuotedMessageID <= 0 {
			continue
		}
		if err := validateConversationDomainReference("message", item.ID, messageTenants[item.ID], "quoted message", item.QuotedMessageID, messageTenants); err != nil {
			return err
		}
	}
	return nil
}

func backfillConversationChildren(
	tx *gorm.DB,
	validTenantIDs map[int64]struct{},
	conversationTenants map[int64]int64,
	messageTenants map[int64]int64,
	customerTenants map[int64]int64,
	storeTenants map[int64]int64,
	wxWorkInstanceTenants map[int64]int64,
	channelTenants map[int64]int64,
	squadTenants map[int64]int64,
	userTenants map[int64]int64,
) error {
	var routeStates []models.ConversationRouteState
	if err := tx.Order("id ASC").Find(&routeStates).Error; err != nil {
		return err
	}
	for i := range routeStates {
		item := &routeStates[i]
		tenantID, err := requiredConversationDomainParentTenant("conversation route state", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation route state", item.ID, tenantID, "store", item.StoreID, storeTenants); err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation route state", item.ID, tenantID, "wxwork instance", item.WxWorkInstanceID, wxWorkInstanceTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.ConversationRouteState{}, "conversation route state", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var summaries []models.ConversationSessionSummary
	if err := tx.Order("id ASC").Find(&summaries).Error; err != nil {
		return err
	}
	for i := range summaries {
		item := &summaries[i]
		tenantID, err := requiredConversationDomainParentTenant("conversation session summary", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		checks := []struct {
			name string
			id   int64
			refs map[int64]int64
		}{
			{name: "wxwork instance", id: item.WxWorkInstanceID, refs: wxWorkInstanceTenants},
			{name: "store", id: item.StoreID, refs: storeTenants},
			{name: "customer", id: item.CustomerID, refs: customerTenants},
			{name: "last message", id: item.LastMessageID, refs: messageTenants},
		}
		for _, check := range checks {
			if err := validateOptionalConversationDomainReference("conversation session summary", item.ID, tenantID, check.name, check.id, check.refs); err != nil {
				return err
			}
		}
		if err := assignConversationDomainTenant(tx, &models.ConversationSessionSummary{}, "conversation session summary", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var syncLogs []models.MessageSyncLog
	if err := tx.Order("id ASC").Find(&syncLogs).Error; err != nil {
		return err
	}
	for i := range syncLogs {
		item := &syncLogs[i]
		if item.ConversationID <= 0 {
			if item.MessageID > 0 || item.TenantID > 0 {
				return fmt.Errorf("message sync log %d has tenant or message without conversation", item.ID)
			}
			continue
		}
		tenantID, err := requiredConversationDomainParentTenant("message sync log", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("message sync log", item.ID, tenantID, "message", item.MessageID, messageTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.MessageSyncLog{}, "message sync log", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	if err := backfillSimpleConversationChildren[models.ConversationParticipant](tx, "conversation participant", conversationTenants, validTenantIDs, func(item models.ConversationParticipant) (int64, int64, int64) {
		return item.ID, item.ConversationID, item.TenantID
	}); err != nil {
		return err
	}

	var readStates []models.ConversationReadState
	if err := tx.Order("id ASC").Find(&readStates).Error; err != nil {
		return err
	}
	for i := range readStates {
		item := &readStates[i]
		tenantID, err := requiredConversationDomainParentTenant("conversation read state", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation read state", item.ID, tenantID, "last read message", item.LastReadMessageID, messageTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.ConversationReadState{}, "conversation read state", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var mappings []models.WxWorkKFConversation
	if err := tx.Order("id ASC").Find(&mappings).Error; err != nil {
		return err
	}
	for i := range mappings {
		item := &mappings[i]
		tenantID, err := requiredConversationDomainParentTenant("wxwork kf conversation", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("wxwork kf conversation", item.ID, tenantID, "channel", item.ChannelID, channelTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.WxWorkKFConversation{}, "wxwork kf conversation", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var messageRefs []models.WxWorkKFMessageRef
	if err := tx.Order("id ASC").Find(&messageRefs).Error; err != nil {
		return err
	}
	for i := range messageRefs {
		item := &messageRefs[i]
		tenantID, err := requiredConversationDomainParentTenant("wxwork kf message ref", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("wxwork kf message ref", item.ID, tenantID, "message", item.MessageID, messageTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.WxWorkKFMessageRef{}, "wxwork kf message ref", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var outbox []models.ChannelMessageOutbox
	if err := tx.Order("id ASC").Find(&outbox).Error; err != nil {
		return err
	}
	for i := range outbox {
		item := &outbox[i]
		tenantID, err := requiredConversationDomainParentTenant("channel message outbox", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if item.MessageID == 0 {
			return fmt.Errorf("channel message outbox %d has no message", item.ID)
		}
		if item.MessageID < 0 {
			if !isStoreRoomHandoffNoticeOutbox(*item) {
				return fmt.Errorf("channel message outbox %d has unknown synthetic message %d", item.ID, item.MessageID)
			}
		} else {
			if err := validateConversationDomainReference("channel message outbox", item.ID, tenantID, "message", item.MessageID, messageTenants); err != nil {
				return err
			}
		}
		if err := assignConversationDomainTenant(tx, &models.ChannelMessageOutbox{}, "channel message outbox", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var assignments []models.ConversationAssignment
	if err := tx.Order("id ASC").Find(&assignments).Error; err != nil {
		return err
	}
	for i := range assignments {
		item := &assignments[i]
		tenantID, err := requiredConversationDomainParentTenant("conversation assignment", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation assignment", item.ID, tenantID, "agent team squad", item.SquadID, squadTenants); err != nil {
			return err
		}
		for _, actor := range []struct {
			name string
			id   int64
		}{{name: "from user", id: item.FromUserID}, {name: "to user", id: item.ToUserID}, {name: "operator", id: item.OperatorID}} {
			if err := validateConversationActorTenant("conversation assignment", item.ID, tenantID, actor.name, actor.id, userTenants); err != nil {
				return err
			}
		}
		if err := assignConversationDomainTenant(tx, &models.ConversationAssignment{}, "conversation assignment", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	if err := backfillSimpleConversationChildren[models.ConversationEventLog](tx, "conversation event log", conversationTenants, validTenantIDs, func(item models.ConversationEventLog) (int64, int64, int64) {
		return item.ID, item.ConversationID, item.TenantID
	}); err != nil {
		return err
	}

	var interrupts []models.ConversationInterrupt
	if err := tx.Order("id ASC").Find(&interrupts).Error; err != nil {
		return err
	}
	for i := range interrupts {
		item := &interrupts[i]
		if item.ConversationID <= 0 {
			if item.SourceMessageID > 0 || item.LastResumeMessageID > 0 || item.TenantID > 0 {
				return fmt.Errorf("conversation interrupt %d has tenant or message without conversation", item.ID)
			}
			continue
		}
		tenantID, err := requiredConversationDomainParentTenant("conversation interrupt", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation interrupt", item.ID, tenantID, "source message", item.SourceMessageID, messageTenants); err != nil {
			return err
		}
		if err := validateOptionalConversationDomainReference("conversation interrupt", item.ID, tenantID, "last resume message", item.LastResumeMessageID, messageTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.ConversationInterrupt{}, "conversation interrupt", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func isStoreRoomHandoffNoticeOutbox(item models.ChannelMessageOutbox) bool {
	if item.ChannelType != "wxwork_protocol" || item.ConversationID <= 0 {
		return false
	}
	payload := struct {
		Kind string `json:"kind"`
	}{}
	return json.Unmarshal([]byte(item.Payload), &payload) == nil && payload.Kind == "store_room_handoff_notice"
}

func backfillSimpleConversationChildren[T any](
	tx *gorm.DB,
	resource string,
	conversationTenants map[int64]int64,
	validTenantIDs map[int64]struct{},
	fields func(T) (id, conversationID, tenantID int64),
) error {
	var list []T
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	var model T
	for i := range list {
		id, conversationID, currentTenantID := fields(list[i])
		tenantID, err := requiredConversationDomainParentTenant(resource, id, "conversation", conversationID, conversationTenants)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &model, resource, id, currentTenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func validateStoreCustomerRelationConversationTenants(tx *gorm.DB, conversationTenants, conversationCustomerIDs map[int64]int64) error {
	var relations []models.StoreCustomerRelation
	if err := tx.Order("id ASC").Find(&relations).Error; err != nil {
		return err
	}
	for i := range relations {
		item := &relations[i]
		if item.LastConversationID <= 0 {
			continue
		}
		if err := validateConversationDomainReference("store customer relation", item.ID, item.TenantID, "last conversation", item.LastConversationID, conversationTenants); err != nil {
			return err
		}
		if conversationCustomerID := conversationCustomerIDs[item.LastConversationID]; conversationCustomerID > 0 && conversationCustomerID != item.CustomerID {
			return fmt.Errorf("store customer relation %d customer %d conflicts with last conversation %d customer %d", item.ID, item.CustomerID, item.LastConversationID, conversationCustomerID)
		}
	}
	return nil
}

func backfillTicketTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	customerTenants map[int64]int64,
	conversationTenants map[int64]int64,
	conversationCustomerIDs map[int64]int64,
	userTenants map[int64]int64,
) (map[int64]int64, error) {
	var list []models.Ticket
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		resolver := newConversationDomainTenantResolver("ticket", item.ID, item.TenantID, validTenantIDs)
		if item.CustomerID > 0 {
			if err := resolver.mergeReference("customer", item.CustomerID, customerTenants); err != nil {
				return nil, err
			}
		}
		if item.ConversationID > 0 {
			if err := resolver.mergeReference("conversation", item.ConversationID, conversationTenants); err != nil {
				return nil, err
			}
			if item.CustomerID > 0 {
				conversationCustomerID := conversationCustomerIDs[item.ConversationID]
				if conversationCustomerID > 0 && conversationCustomerID != item.CustomerID {
					return nil, fmt.Errorf("ticket %d customer %d conflicts with conversation %d customer %d", item.ID, item.CustomerID, item.ConversationID, conversationCustomerID)
				}
			}
		}
		if err := mergeConversationActorTenant(resolver, "current assignee", item.CurrentAssigneeID, userTenants); err != nil {
			return nil, err
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return nil, err
		}
		if err := assignConversationDomainTenant(tx, &models.Ticket{}, "ticket", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, err
		}
		result[item.ID] = tenantID
	}
	return result, nil
}

func backfillTicketChildren(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	ticketTenants map[int64]int64,
	userTenants map[int64]int64,
) error {
	var progresses []models.TicketProgress
	if err := tx.Order("id ASC").Find(&progresses).Error; err != nil {
		return err
	}
	for i := range progresses {
		item := &progresses[i]
		tenantID, err := requiredConversationDomainParentTenant("ticket progress", item.ID, "ticket", item.TicketID, ticketTenants)
		if err != nil {
			return err
		}
		if err := validateConversationActorTenant("ticket progress", item.ID, tenantID, "author", item.AuthorID, userTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.TicketProgress{}, "ticket progress", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}

	var views []models.TicketView
	if err := tx.Order("id ASC").Find(&views).Error; err != nil {
		return err
	}
	for i := range views {
		item := &views[i]
		if item.UserID <= 0 {
			return fmt.Errorf("ticket view %d has no user", item.ID)
		}
		userTenantID, ok := userTenants[item.UserID]
		if !ok {
			return fmt.Errorf("ticket view %d references missing user %d", item.ID, item.UserID)
		}
		resolver := newConversationDomainTenantResolver("ticket view", item.ID, item.TenantID, validTenantIDs)
		if userTenantID > 0 {
			if err := resolver.merge("user", item.UserID, userTenantID); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.TicketView{}, "ticket view", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillSharedTagTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	conversationTenants map[int64]int64,
	ticketTenants map[int64]int64,
) error {
	var tags []models.Tag
	if err := tx.Order("id ASC").Find(&tags).Error; err != nil {
		return err
	}
	var conversationTags []legacyConversationTag
	if tx.Migrator().HasTable(&legacyConversationTag{}) {
		if err := tx.Order("id ASC").Find(&conversationTags).Error; err != nil {
			return err
		}
	}
	var ticketTags []models.TicketTag
	if err := tx.Order("id ASC").Find(&ticketTags).Error; err != nil {
		return err
	}

	union := newTagTenantUnion(tags)
	tagsByID := make(map[int64]models.Tag, len(tags))
	for i := range tags {
		item := tags[i]
		tagsByID[item.ID] = item
		if item.ParentID <= 0 {
			continue
		}
		if _, ok := union.parents[item.ParentID]; !ok {
			return fmt.Errorf("tag %d references missing parent tag %d", item.ID, item.ParentID)
		}
		union.join(item.ID, item.ParentID)
	}

	resolvers := make(map[int64]*conversationDomainTenantResolver)
	resolverForTag := func(tagID int64) (*conversationDomainTenantResolver, error) {
		if _, ok := tagsByID[tagID]; !ok {
			return nil, fmt.Errorf("references missing tag %d", tagID)
		}
		root := union.find(tagID)
		resolver := resolvers[root]
		if resolver == nil {
			resolver = newConversationDomainTenantResolver("tag component", root, 0, validTenantIDs)
			resolvers[root] = resolver
		}
		return resolver, nil
	}
	for i := range tags {
		item := tags[i]
		resolver, err := resolverForTag(item.ID)
		if err != nil {
			return err
		}
		if item.TenantID > 0 {
			if err := resolver.merge("tag", item.ID, item.TenantID); err != nil {
				return err
			}
		}
	}
	for i := range conversationTags {
		item := conversationTags[i]
		tenantID, err := requiredConversationDomainParentTenant("conversation tag", item.ID, "conversation", item.ConversationID, conversationTenants)
		if err != nil {
			return err
		}
		resolver, err := resolverForTag(item.TagID)
		if err != nil {
			return fmt.Errorf("conversation tag %d %w", item.ID, err)
		}
		if err := resolver.merge("conversation", item.ConversationID, tenantID); err != nil {
			return err
		}
		if item.TenantID > 0 {
			if err := resolver.merge("conversation tag", item.ID, item.TenantID); err != nil {
				return err
			}
		}
	}
	for i := range ticketTags {
		item := ticketTags[i]
		tenantID, err := requiredConversationDomainParentTenant("ticket tag", item.ID, "ticket", item.TicketID, ticketTenants)
		if err != nil {
			return err
		}
		resolver, err := resolverForTag(item.TagID)
		if err != nil {
			return fmt.Errorf("ticket tag %d %w", item.ID, err)
		}
		if err := resolver.merge("ticket", item.TicketID, tenantID); err != nil {
			return err
		}
		if item.TenantID > 0 {
			if err := resolver.merge("ticket tag", item.ID, item.TenantID); err != nil {
				return err
			}
		}
	}

	componentTenants := make(map[int64]int64, len(resolvers))
	for i := range tags {
		root := union.find(tags[i].ID)
		if _, ok := componentTenants[root]; ok {
			continue
		}
		resolver := resolvers[root]
		if resolver == nil {
			resolver = newConversationDomainTenantResolver("tag component", root, 0, validTenantIDs)
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		componentTenants[root] = tenantID
	}
	tagTenants := make(map[int64]int64, len(tags))
	for i := range tags {
		item := &tags[i]
		tenantID := componentTenants[union.find(item.ID)]
		if err := assignConversationDomainTenant(tx, &models.Tag{}, "tag", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
		tagTenants[item.ID] = tenantID
	}
	for i := range conversationTags {
		item := &conversationTags[i]
		tenantID := conversationTenants[item.ConversationID]
		if err := validateConversationDomainReference("conversation tag", item.ID, tenantID, "tag", item.TagID, tagTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &legacyConversationTag{}, "conversation tag", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	for i := range ticketTags {
		item := &ticketTags[i]
		tenantID := ticketTenants[item.TicketID]
		if err := validateConversationDomainReference("ticket tag", item.ID, tenantID, "tag", item.TagID, tagTenants); err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.TicketTag{}, "ticket tag", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func loadConversationDomainTenantIDs(tx *gorm.DB, model any) (map[int64]int64, error) {
	var rows []struct {
		ID       int64
		TenantID int64
	}
	if err := tx.Model(model).Select("id", "tenant_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(rows))
	for i := range rows {
		result[rows[i].ID] = rows[i].TenantID
	}
	return result, nil
}

func newConversationDomainTenantResolver(resource string, resourceID, explicitTenantID int64, validTenantIDs map[int64]struct{}) *conversationDomainTenantResolver {
	return &conversationDomainTenantResolver{
		resource:       resource,
		resourceID:     resourceID,
		validTenantIDs: validTenantIDs,
		tenantID:       explicitTenantID,
	}
}

func (r *conversationDomainTenantResolver) mergeReference(source string, sourceID int64, tenantIDs map[int64]int64) error {
	tenantID, ok := tenantIDs[sourceID]
	if !ok {
		return fmt.Errorf("%s %d references missing %s %d", r.resource, r.resourceID, source, sourceID)
	}
	return r.merge(source, sourceID, tenantID)
}

func (r *conversationDomainTenantResolver) merge(source string, sourceID, tenantID int64) error {
	if tenantID <= 0 {
		return fmt.Errorf("%s %d %s %d has no tenant", r.resource, r.resourceID, source, sourceID)
	}
	if _, ok := r.validTenantIDs[tenantID]; !ok {
		return fmt.Errorf("%s %d %s %d references missing tenant %d", r.resource, r.resourceID, source, sourceID, tenantID)
	}
	if r.tenantID == 0 {
		r.tenantID = tenantID
		return nil
	}
	if _, ok := r.validTenantIDs[r.tenantID]; !ok {
		return fmt.Errorf("%s %d references missing tenant %d", r.resource, r.resourceID, r.tenantID)
	}
	if r.tenantID != tenantID {
		return fmt.Errorf("%s %d tenant %d conflicts with %s %d tenant %d", r.resource, r.resourceID, r.tenantID, source, sourceID, tenantID)
	}
	return nil
}

func (r *conversationDomainTenantResolver) resolve(legacyTenantID int64) (int64, error) {
	if r.tenantID == 0 {
		r.tenantID = legacyTenantID
	}
	if _, ok := r.validTenantIDs[r.tenantID]; !ok {
		return 0, fmt.Errorf("%s %d references missing tenant %d", r.resource, r.resourceID, r.tenantID)
	}
	return r.tenantID, nil
}

func mergeConversationActorTenant(resolver *conversationDomainTenantResolver, source string, userID int64, userTenants map[int64]int64) error {
	if userID <= 0 {
		return nil
	}
	tenantID, ok := userTenants[userID]
	if !ok {
		return fmt.Errorf("%s %d references missing %s user %d", resolver.resource, resolver.resourceID, source, userID)
	}
	if tenantID == 0 {
		return nil
	}
	return resolver.merge(source, userID, tenantID)
}

func validateConversationActorTenant(resource string, resourceID, tenantID int64, source string, userID int64, userTenants map[int64]int64) error {
	if userID <= 0 {
		return nil
	}
	userTenantID, ok := userTenants[userID]
	if !ok {
		return fmt.Errorf("%s %d references missing %s user %d", resource, resourceID, source, userID)
	}
	if userTenantID > 0 && userTenantID != tenantID {
		return fmt.Errorf("%s %d tenant %d conflicts with %s user %d tenant %d", resource, resourceID, tenantID, source, userID, userTenantID)
	}
	return nil
}

func requiredConversationDomainParentTenant(resource string, resourceID int64, parentName string, parentID int64, parentTenants map[int64]int64) (int64, error) {
	if parentID <= 0 {
		return 0, fmt.Errorf("%s %d has no %s", resource, resourceID, parentName)
	}
	tenantID, ok := parentTenants[parentID]
	if !ok {
		return 0, fmt.Errorf("%s %d references missing %s %d", resource, resourceID, parentName, parentID)
	}
	if tenantID <= 0 {
		return 0, fmt.Errorf("%s %d %s %d has no tenant", resource, resourceID, parentName, parentID)
	}
	return tenantID, nil
}

func validateOptionalConversationDomainReference(resource string, resourceID, tenantID int64, referenceName string, referenceID int64, referenceTenants map[int64]int64) error {
	if referenceID <= 0 {
		return nil
	}
	return validateConversationDomainReference(resource, resourceID, tenantID, referenceName, referenceID, referenceTenants)
}

func validateConversationDomainReference(resource string, resourceID, tenantID int64, referenceName string, referenceID int64, referenceTenants map[int64]int64) error {
	referenceTenantID, ok := referenceTenants[referenceID]
	if !ok {
		return fmt.Errorf("%s %d references missing %s %d", resource, resourceID, referenceName, referenceID)
	}
	if referenceTenantID != tenantID {
		return fmt.Errorf("%s %d tenant %d conflicts with %s %d tenant %d", resource, resourceID, tenantID, referenceName, referenceID, referenceTenantID)
	}
	return nil
}

func assignConversationDomainTenant(tx *gorm.DB, model any, resource string, id, currentTenantID, tenantID int64, validTenantIDs map[int64]struct{}) error {
	if _, ok := validTenantIDs[tenantID]; !ok {
		return fmt.Errorf("%s %d references missing tenant %d", resource, id, tenantID)
	}
	if currentTenantID > 0 {
		if _, ok := validTenantIDs[currentTenantID]; !ok {
			return fmt.Errorf("%s %d references missing tenant %d", resource, id, currentTenantID)
		}
		if currentTenantID != tenantID {
			return fmt.Errorf("%s %d tenant %d conflicts with resolved tenant %d", resource, id, currentTenantID, tenantID)
		}
		return nil
	}
	result := tx.Model(model).Where("id = ? AND tenant_id = ?", id, 0).Update("tenant_id", tenantID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%s %d tenant backfill did not update the expected row", resource, id)
	}
	return nil
}

func newTagTenantUnion(tags []models.Tag) *tagTenantUnion {
	parents := make(map[int64]int64, len(tags))
	for i := range tags {
		parents[tags[i].ID] = tags[i].ID
	}
	return &tagTenantUnion{parents: parents}
}

func (u *tagTenantUnion) find(id int64) int64 {
	parent, ok := u.parents[id]
	if !ok {
		return 0
	}
	if parent == id {
		return id
	}
	root := u.find(parent)
	u.parents[id] = root
	return root
}

func (u *tagTenantUnion) join(left, right int64) {
	leftRoot := u.find(left)
	rightRoot := u.find(right)
	if leftRoot == 0 || rightRoot == 0 || leftRoot == rightRoot {
		return
	}
	if leftRoot < rightRoot {
		u.parents[rightRoot] = leftRoot
		return
	}
	u.parents[leftRoot] = rightRoot
}
