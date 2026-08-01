package migration

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const storeConversationContinuityMigrationRemark = "initialize stable store conversation continuity"

var storeConversationContinuityPermissionSpecs = []constants.Permission{
	constants.PermissionStoreView,
	constants.PermissionStoreCreate,
	constants.PermissionStoreUpdate,
	constants.PermissionConversationRelatedView,
	constants.PermissionConversationInherit,
}

func init() {
	register(72, storeConversationContinuityMigrationRemark, func() error {
		return migrateStoreConversationContinuity(sqls.DB())
	})
}

func migrateStoreConversationContinuity(db *gorm.DB) error {
	if db == nil {
		return errors.New("store conversation continuity migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := syncStoreConversationContinuityPermissions(tx); err != nil {
			return err
		}
		if err := backfillStoreBindingAttribution(tx); err != nil {
			return err
		}
		if err := backfillStoreStaffCustomerHandoffSettings(tx); err != nil {
			return err
		}
		var conversations []models.Conversation
		if err := tx.Order("id ASC").Find(&conversations).Error; err != nil {
			return fmt.Errorf("load conversations: %w", err)
		}
		seenThreadKeys := make(map[string]int64)
		for i := range conversations {
			if err := backfillStoreConversation(tx, &conversations[i], seenThreadKeys); err != nil {
				return err
			}
		}
		return backfillArrivalStoreStaffAttribution(tx)
	})
}

type legacyCustomerHandoffSettingRow struct {
	ID                  int64
	TenantID            int64
	CustomerID          int64
	WxWorkInstanceID    int64
	StoreStaffBindingID *int64
	AutoHandoffEnabled  bool
	UpdatedAt           time.Time
}

func backfillStoreStaffCustomerHandoffSettings(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasTable(&models.WxWorkCustomerHandoffSetting{}) {
		return nil
	}
	table := tx.NamingStrategy.TableName("WxWorkCustomerHandoffSetting")
	hasLegacyInstanceColumn := tx.Migrator().HasColumn(&models.WxWorkCustomerHandoffSetting{}, "wx_work_instance_id")
	selectFields := "id, tenant_id, customer_id, store_staff_binding_id, auto_handoff_enabled, updated_at, 0 AS wx_work_instance_id"
	if hasLegacyInstanceColumn {
		selectFields = "id, tenant_id, customer_id, store_staff_binding_id, auto_handoff_enabled, updated_at, wx_work_instance_id"
	}
	var rows []legacyCustomerHandoffSettingRow
	if err := tx.Table(table).Select(selectFields).Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return fmt.Errorf("load customer handoff settings: %w", err)
	}
	type resolvedRow struct {
		row       legacyCustomerHandoffSettingRow
		bindingID int64
	}
	groups := make(map[string][]resolvedRow)
	for i := range rows {
		row := rows[i]
		bindingID := int64(0)
		if row.StoreStaffBindingID != nil {
			bindingID = *row.StoreStaffBindingID
		}
		if row.WxWorkInstanceID > 0 {
			instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(tx, row.WxWorkInstanceID, row.TenantID)
			if instance == nil || instance.StoreStaffBindingID <= 0 {
				return fmt.Errorf("customer handoff setting %d references an invalid protocol instance", row.ID)
			}
			if bindingID > 0 && bindingID != instance.StoreStaffBindingID {
				return fmt.Errorf("customer handoff setting %d has conflicting Store staff attribution", row.ID)
			}
			bindingID = instance.StoreStaffBindingID
		}
		binding := repositories.StoreStaffBindingRepository.GetInTenant(tx, bindingID, row.TenantID)
		if binding == nil || binding.Status == enums.StatusDeleted {
			return fmt.Errorf("customer handoff setting %d cannot resolve an active Store staff binding", row.ID)
		}
		customer := repositories.CustomerRepository.GetInTenant(tx, row.CustomerID, row.TenantID)
		if customer == nil || customer.Status == enums.StatusDeleted {
			return fmt.Errorf("customer handoff setting %d references an invalid customer", row.ID)
		}
		key := fmt.Sprintf("%d:%d:%d", row.TenantID, row.CustomerID, bindingID)
		groups[key] = append(groups[key], resolvedRow{row: row, bindingID: bindingID})
	}
	for _, group := range groups {
		winner := group[0]
		for i := 1; i < len(group); i++ {
			if err := tx.Delete(&models.WxWorkCustomerHandoffSetting{}, group[i].row.ID).Error; err != nil {
				return fmt.Errorf("remove duplicate customer handoff setting %d: %w", group[i].row.ID, err)
			}
		}
		if winner.row.StoreStaffBindingID == nil || *winner.row.StoreStaffBindingID != winner.bindingID {
			if err := repositories.WxWorkCustomerHandoffSettingRepository.UpdatesInTenant(tx, winner.row.ID, winner.row.TenantID, map[string]any{
				"store_staff_binding_id": winner.bindingID,
				"updated_at":             time.Now(),
				"update_user_id":         constants.SystemAuditUserID,
				"update_user_name":       constants.SystemAuditUserName,
			}); err != nil {
				return fmt.Errorf("backfill customer handoff setting %d: %w", winner.row.ID, err)
			}
		}
	}
	return validateStoreStaffCustomerHandoffSettings(tx)
}

func validateStoreStaffCustomerHandoffSettings(tx *gorm.DB) error {
	var settings []models.WxWorkCustomerHandoffSetting
	if err := tx.Order("id ASC").Find(&settings).Error; err != nil {
		return fmt.Errorf("validate customer handoff settings: %w", err)
	}
	for i := range settings {
		setting := &settings[i]
		if setting.StoreStaffBindingID == nil || *setting.StoreStaffBindingID <= 0 {
			return fmt.Errorf("customer handoff setting %d has no Store staff binding", setting.ID)
		}
		binding := repositories.StoreStaffBindingRepository.GetInTenant(tx, *setting.StoreStaffBindingID, setting.TenantID)
		if binding == nil || binding.Status == enums.StatusDeleted {
			return fmt.Errorf("customer handoff setting %d references an invalid Store staff binding", setting.ID)
		}
	}
	return nil
}

func syncStoreConversationContinuityPermissions(tx *gorm.DB) error {
	permissions, err := ensurePermissionSpecs(tx, storeConversationContinuityPermissionSpecs)
	if err != nil {
		return err
	}
	permissionCodes := make(map[string]struct{}, len(storeConversationContinuityPermissionSpecs))
	for _, permission := range storeConversationContinuityPermissionSpecs {
		permissionCodes[permission.Code] = struct{}{}
	}
	roles := make(map[string]*models.Role)
	for roleCode, rolePermissions := range constants.RolePermissions {
		for _, permission := range rolePermissions {
			if _, ok := permissionCodes[permission.Code]; !ok {
				continue
			}
			role := repositories.RoleRepository.GetByCode(tx, roleCode)
			if role == nil {
				return errors.New("builtin role not found: " + roleCode)
			}
			roles[roleCode] = role
			break
		}
	}
	return ensureRolePermissionsByCode(tx, roles, permissions, permissionCodes)
}

func backfillStoreConversation(tx *gorm.DB, conversation *models.Conversation, seenThreadKeys map[string]int64) error {
	if conversation == nil || conversation.ID <= 0 || conversation.TenantID <= 0 {
		return nil
	}
	channel := repositories.ChannelRepository.GetInTenant(tx, conversation.ChannelID, conversation.TenantID)
	if channel == nil {
		return fmt.Errorf("conversation %d has no channel in tenant %d", conversation.ID, conversation.TenantID)
	}
	if channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return validateNonProtocolConversationScope(conversation)
	}

	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(tx, conversation.ID, conversation.TenantID)
	mapping := repositories.WxWorkKFConversationRepository.FindOne(tx, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID))
	instance, err := resolveHistoricalProtocolInstance(tx, conversation, route, mapping)
	if err != nil {
		return err
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(tx, instance.StoreStaffBindingID, conversation.TenantID)
	if binding == nil || binding.Status == enums.StatusDeleted || binding.StoreID != instance.StoreID {
		return fmt.Errorf("conversation %d resolves to invalid store staff binding", conversation.ID)
	}
	store := repositories.StoreRepository.GetInTenant(tx, instance.StoreID, conversation.TenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return fmt.Errorf("conversation %d resolves to invalid store", conversation.ID)
	}
	user := repositories.UserRepository.GetInTenant(tx, binding.UserID, conversation.TenantID)
	if user == nil || user.DeletedAt != nil {
		return fmt.Errorf("conversation %d resolves to invalid store staff user", conversation.ID)
	}
	if instance.StoreID != store.ID || instance.StoreStaffBindingID != binding.ID || instance.ChannelID != channel.ID {
		return fmt.Errorf("conversation %d protocol instance scope is inconsistent", conversation.ID)
	}

	sessionNo := 1
	if route != nil && route.SessionNo > 0 {
		sessionNo = route.SessionNo
	}
	threadKey := fmt.Sprintf("store:%d:%d:%d:%d", conversation.TenantID, store.ID, conversation.CustomerID, binding.ID)
	if previousID, exists := seenThreadKeys[threadKey]; exists && previousID != conversation.ID {
		return fmt.Errorf("conversations %d and %d resolve to the same store thread", previousID, conversation.ID)
	}
	seenThreadKeys[threadKey] = conversation.ID
	var conflict models.Conversation
	result := tx.Where("tenant_id = ? AND thread_key = ? AND id <> ?", conversation.TenantID, threadKey, conversation.ID).Take(&conflict)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	}
	if result.Error == nil {
		return fmt.Errorf("conversation %d conflicts with existing store thread %d", conversation.ID, conflict.ID)
	}

	now := time.Now()
	if err := repositories.ConversationRepository.UpdatesInTenant(tx, conversation.ID, conversation.TenantID, map[string]any{
		"store_id":               store.ID,
		"store_staff_binding_id": binding.ID,
		"thread_key":             threadKey,
		"updated_at":             now,
		"update_user_id":         constants.SystemAuditUserID,
		"update_user_name":       constants.SystemAuditUserName,
	}); err != nil {
		return fmt.Errorf("backfill conversation %d scope: %w", conversation.ID, err)
	}
	conversation.StoreID = store.ID
	conversation.StoreStaffBindingID = binding.ID
	conversation.ThreadKey = &threadKey

	if route == nil {
		route = buildHistoricalConversationRoute(conversation, store, instance, sessionNo, now)
		if err := repositories.ConversationRouteStateRepository.Create(tx, route); err != nil {
			return fmt.Errorf("create conversation %d route: %w", conversation.ID, err)
		}
	} else if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(tx, route.ID, conversation.TenantID, map[string]any{
		"store_id":               store.ID,
		"store_staff_binding_id": binding.ID,
		"knowledge_base_id":      store.KnowledgeBaseID,
		"wx_work_instance_id":    instance.ID,
		"session_no":             sessionNo,
		"updated_at":             now,
		"update_user_id":         constants.SystemAuditUserID,
		"update_user_name":       constants.SystemAuditUserName,
	}); err != nil {
		return fmt.Errorf("backfill conversation %d route: %w", conversation.ID, err)
	}
	route.StoreID = store.ID
	route.StoreStaffBindingID = binding.ID
	route.KnowledgeBaseID = store.KnowledgeBaseID
	route.WxWorkInstanceID = instance.ID
	route.SessionNo = sessionNo

	if err := backfillConversationChannelSessions(tx, conversation, route, binding, user, instance); err != nil {
		return err
	}
	if err := backfillStoreCustomerRelation(tx, conversation, instance.ID); err != nil {
		return err
	}
	if err := backfillCanonicalProtocolIdentity(tx, conversation, instance, mapping); err != nil {
		return err
	}
	return nil
}

func validateNonProtocolConversationScope(conversation *models.Conversation) error {
	if conversation.StoreID == 0 && conversation.StoreStaffBindingID == 0 && conversation.ThreadKey == nil {
		return nil
	}
	return fmt.Errorf("non-protocol conversation %d unexpectedly has store thread scope", conversation.ID)
}

func resolveHistoricalProtocolInstance(tx *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState, mapping *models.WxWorkKFConversation) (*models.WxWorkProtocolInstance, error) {
	if route != nil && route.WxWorkInstanceID > 0 {
		if instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(tx, route.WxWorkInstanceID, conversation.TenantID); instance != nil {
			return instance, nil
		}
		return nil, fmt.Errorf("conversation %d route references a missing protocol instance", conversation.ID)
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(tx, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("channel_id", conversation.ChannelID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	if mapping != nil && strings.TrimSpace(mapping.OpenKfID) != "" {
		matched := make([]models.WxWorkProtocolInstance, 0, 1)
		for i := range instances {
			prefix := "wx_protocol:" + strings.TrimSpace(instances[i].Guid)
			if mapping.OpenKfID == prefix || strings.HasPrefix(mapping.OpenKfID, prefix+":") {
				matched = append(matched, instances[i])
			}
		}
		if len(matched) == 1 {
			return &matched[0], nil
		}
		if len(matched) > 1 {
			return nil, fmt.Errorf("conversation %d mapping resolves to multiple protocol instances", conversation.ID)
		}
	}
	if len(instances) == 1 {
		return &instances[0], nil
	}
	return nil, fmt.Errorf("conversation %d cannot deterministically resolve one protocol instance", conversation.ID)
}

func buildHistoricalConversationRoute(conversation *models.Conversation, store *models.Store, instance *models.WxWorkProtocolInstance, sessionNo int, now time.Time) *models.ConversationRouteState {
	routeStatus := enums.ConversationRouteStatusAIServing
	routeTarget := "ai"
	if conversation.Status == enums.IMConversationStatusClosed {
		routeStatus = enums.ConversationRouteStatusClosed
		routeTarget = "closed"
	}
	startedAt := conversation.CreatedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	return &models.ConversationRouteState{
		TenantID: conversation.TenantID, ConversationID: conversation.ID,
		StoreID: store.ID, StoreStaffBindingID: instance.StoreStaffBindingID,
		KnowledgeBaseID: store.KnowledgeBaseID, WxWorkInstanceID: instance.ID,
		RouteStatus: routeStatus, RouteTarget: routeTarget, SessionNo: sessionNo, SessionStartedAt: &startedAt,
		AuditFields: systemMigrationAudit(now),
	}
}

func backfillConversationChannelSessions(tx *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState, binding *models.StoreStaffBinding, user *models.User, instance *models.WxWorkProtocolInstance) error {
	if err := tx.Model(&models.Message{}).
		Where("tenant_id = ? AND conversation_id = ? AND session_no <= 0", conversation.TenantID, conversation.ID).
		Update("session_no", 1).Error; err != nil {
		return fmt.Errorf("normalize conversation %d message sessions: %w", conversation.ID, err)
	}
	var messages []models.Message
	if err := tx.Select("session_no", "created_at").
		Where("tenant_id = ? AND conversation_id = ?", conversation.TenantID, conversation.ID).
		Order("session_no ASC, id ASC").Find(&messages).Error; err != nil {
		return fmt.Errorf("load conversation %d message sessions: %w", conversation.ID, err)
	}
	type bounds struct{ first, last time.Time }
	bySession := make(map[int]bounds)
	for i := range messages {
		sessionNo := messages[i].SessionNo
		if sessionNo <= 0 {
			sessionNo = 1
		}
		item := bySession[sessionNo]
		if item.first.IsZero() || messages[i].CreatedAt.Before(item.first) {
			item.first = messages[i].CreatedAt
		}
		if item.last.IsZero() || messages[i].CreatedAt.After(item.last) {
			item.last = messages[i].CreatedAt
		}
		bySession[sessionNo] = item
	}
	if _, ok := bySession[route.SessionNo]; !ok {
		startedAt := conversation.CreatedAt
		if route.SessionStartedAt != nil {
			startedAt = *route.SessionStartedAt
		}
		bySession[route.SessionNo] = bounds{first: startedAt, last: startedAt}
	}
	sessionNos := make([]int, 0, len(bySession))
	for sessionNo := range bySession {
		sessionNos = append(sessionNos, sessionNo)
	}
	sort.Ints(sessionNos)
	for _, sessionNo := range sessionNos {
		if existing := repositories.ConversationChannelSessionRepository.TakeByConversationSession(tx, conversation.TenantID, conversation.ID, sessionNo); existing != nil {
			if existing.StoreID != conversation.StoreID || existing.StoreStaffBindingID != conversation.StoreStaffBindingID {
				return fmt.Errorf("conversation %d session %d has inconsistent store scope", conversation.ID, sessionNo)
			}
			continue
		}
		itemBounds := bySession[sessionNo]
		startedAt := itemBounds.first
		if startedAt.IsZero() {
			startedAt = conversation.CreatedAt
		}
		if startedAt.IsZero() {
			startedAt = time.Now()
		}
		status := enums.StatusOk
		var endedAt *time.Time
		wxWorkInstanceID := int64(0)
		employeeName := ""
		if sessionNo < route.SessionNo {
			status = enums.StatusDisabled
			ended := itemBounds.last
			if ended.IsZero() {
				ended = startedAt
			}
			endedAt = &ended
		} else {
			wxWorkInstanceID = instance.ID
			employeeName = strings.TrimSpace(instance.EmployeeName)
			if conversation.Status == enums.IMConversationStatusClosed {
				status = enums.StatusDisabled
				ended := conversation.UpdatedAt
				if conversation.ClosedAt != nil {
					ended = *conversation.ClosedAt
				}
				endedAt = &ended
			}
		}
		item := &models.ConversationChannelSession{
			TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: sessionNo,
			StoreID: conversation.StoreID, StoreStaffBindingID: conversation.StoreStaffBindingID,
			WxWorkInstanceID: wxWorkInstanceID, ChannelID: conversation.ChannelID,
			StartReason: "historical_backfill", StoreStaffDisplayName: firstNonBlankMigration(user.Nickname, user.Username),
			WxWorkEmployeeDisplayName: employeeName, StartedAt: startedAt, EndedAt: endedAt, Status: status,
			AuditFields: systemMigrationAudit(time.Now()),
		}
		if err := repositories.ConversationChannelSessionRepository.Create(tx, item); err != nil {
			return fmt.Errorf("backfill conversation %d session %d: %w", conversation.ID, sessionNo, err)
		}
	}
	return nil
}

func backfillStoreCustomerRelation(tx *gorm.DB, conversation *models.Conversation, instanceID int64) error {
	relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(tx, conversation.TenantID, conversation.CustomerID, conversation.StoreID)
	lastActiveAt := conversation.LastActiveAt
	if lastActiveAt.IsZero() {
		lastActiveAt = conversation.UpdatedAt
	}
	if relation == nil {
		return repositories.StoreCustomerRelationRepository.Create(tx, &models.StoreCustomerRelation{
			TenantID: conversation.TenantID, CustomerID: conversation.CustomerID, StoreID: conversation.StoreID,
			WxWorkInstanceID: instanceID, LastConversationID: conversation.ID, LastActiveAt: &lastActiveAt,
			VisitCount: 1, Status: enums.StatusOk, AuditFields: systemMigrationAudit(time.Now()),
		})
	}
	if relation.LastActiveAt != nil && !lastActiveAt.After(*relation.LastActiveAt) {
		return nil
	}
	return repositories.StoreCustomerRelationRepository.UpdatesInTenant(tx, relation.ID, conversation.TenantID, map[string]any{
		"wx_work_instance_id": instanceID, "last_conversation_id": conversation.ID, "last_active_at": lastActiveAt,
		"status": enums.StatusOk, "updated_at": time.Now(), "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	})
}

func backfillCanonicalProtocolIdentity(tx *gorm.DB, conversation *models.Conversation, instance *models.WxWorkProtocolInstance, mapping *models.WxWorkKFConversation) error {
	externalID := ""
	if mapping != nil {
		externalID = strings.TrimSpace(mapping.ExternalUserID)
	}
	if externalID == "" {
		participant := repositories.ConversationParticipantRepository.Take(tx,
			"tenant_id = ? AND conversation_id = ? AND participant_type = ? AND status <> ?",
			conversation.TenantID, conversation.ID, enums.IMParticipantTypeCustomer, enums.StatusDeleted,
		)
		if participant != nil {
			externalID = historicalProtocolExternalID(participant.ExternalParticipantID, instance.Guid)
		}
	}
	if externalID == "" {
		return fmt.Errorf("conversation %d has no deterministic protocol customer identity", conversation.ID)
	}
	canonical := "wxwork_protocol:" + externalID
	identity := repositories.CustomerIdentityRepository.GetByInTenant(tx, conversation.TenantID, enums.ExternalSourceWxWorkProtocol, canonical)
	if identity != nil {
		if identity.CustomerID != conversation.CustomerID {
			return fmt.Errorf("conversation %d canonical protocol identity belongs to another customer", conversation.ID)
		}
		return nil
	}
	return repositories.CustomerIdentityRepository.Create(tx, &models.CustomerIdentity{
		TenantID: conversation.TenantID, CustomerID: conversation.CustomerID,
		ExternalSource: enums.ExternalSourceWxWorkProtocol, ExternalID: canonical,
		Status: enums.StatusOk, AuditFields: systemMigrationAudit(time.Now()),
	})
}

func historicalProtocolExternalID(value, guid string) string {
	value = strings.TrimSpace(value)
	guid = strings.TrimSpace(guid)
	legacyPrefix := "wxwork_protocol:" + guid + ":"
	if guid != "" && strings.HasPrefix(value, legacyPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(value, legacyPrefix))
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "wxwork_protocol:"))
}

func systemMigrationAudit(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdatedAt: now, UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}

func firstNonBlankMigration(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
