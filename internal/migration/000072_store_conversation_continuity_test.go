package migration

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestStoreConversationContinuityMigrationSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), storeContinuityMigrationGORMConfig("s72_"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runStoreConversationContinuityMigrationScenario(t, db, "s72_")
}

func TestStoreConversationContinuityMigrationMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := fmt.Sprintf("s72_%d_", time.Now().UnixNano())
	db, err := gorm.Open(mysql.Open(dsn), storeContinuityMigrationGORMConfig(prefix))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runStoreConversationContinuityMigrationScenario(t, db, prefix)
}

func runStoreConversationContinuityMigrationScenario(t *testing.T, db *gorm.DB, prefix string) {
	t.Helper()
	fixtures := storeContinuityMigrationModels()
	for i := len(fixtures) - 1; i >= 0; i-- {
		_ = db.Migrator().DropTable(fixtures[i])
	}
	if err := db.AutoMigrate(fixtures...); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}
	handoffTable := prefix + "wx_work_customer_handoff_setting"
	if err := db.Exec("ALTER TABLE " + handoffTable + " ADD COLUMN wx_work_instance_id bigint NOT NULL DEFAULT 0").Error; err != nil {
		t.Fatalf("add legacy customer handoff instance column: %v", err)
	}
	t.Cleanup(func() {
		for i := len(fixtures) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(fixtures[i]); err != nil {
				t.Errorf("drop fixture %T: %v", fixtures[i], err)
			}
		}
	})
	if _, err := ensureRoles(db); err != nil {
		t.Fatalf("seed roles: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	user := &models.User{TenantID: 101, Username: "migration-store-staff", Nickname: "迁移员工", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	store := &models.Store{TenantID: 101, StoreCode: "migration-store", Name: "迁移门店", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	activeUserID := user.ID
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, ActiveUserID: &activeUserID, StoreID: store.ID, Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	channel := &models.Channel{TenantID: 101, Name: "企微员工号", ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "migration-channel", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "migration-guid", ChannelID: channel.ID, EmployeeUserID: "protocol-private-user", EmployeeName: "企微员工",
		StoreID: store.ID, StoreStaffBindingID: binding.ID, HealthStatus: "online", Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	customer := &models.Customer{TenantID: 101, Name: "迁移客户", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := db.Create(&models.CustomerIdentity{
		TenantID: 101, CustomerID: customer.ID, ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID: "wxwork_protocol:migration-guid:external-customer", Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}).Error; err != nil {
		t.Fatalf("create legacy identity: %v", err)
	}
	conversation := &models.Conversation{
		TenantID: 101, ChannelID: channel.ID, CustomerID: customer.ID, CustomerName: customer.Name,
		Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst,
		LastActiveAt: now.Add(time.Hour), LastMessageAt: now.Add(time.Hour), AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create historical conversation: %v", err)
	}
	startedAt := now
	route := &models.ConversationRouteState{
		TenantID: 101, ConversationID: conversation.ID, StoreID: store.ID, WxWorkInstanceID: instance.ID,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 2, SessionStartedAt: &startedAt,
		AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create historical route: %v", err)
	}
	if err := db.Create(&models.ConversationParticipant{
		TenantID: 101, ConversationID: conversation.ID, ParticipantType: string(enums.IMParticipantTypeCustomer),
		ExternalParticipantID: "wxwork_protocol:migration-guid:external-customer", Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}
	if err := db.Create(&models.WxWorkKFConversation{
		TenantID: 101, ConversationID: conversation.ID, ChannelID: channel.ID,
		OpenKfID: "wx_protocol:migration-guid:single", ExternalUserID: "external-customer", Status: enums.StatusOk,
		AuditFields: migration72TestAudit(now),
	}).Error; err != nil {
		t.Fatalf("create protocol mapping: %v", err)
	}
	if err := db.Table(handoffTable).Create(map[string]any{
		"tenant_id": 101, "customer_id": customer.ID, "wx_work_instance_id": instance.ID,
		"auto_handoff_enabled": false, "created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("create legacy customer handoff setting: %v", err)
	}
	messages := []models.Message{
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ClientMsgID: "old-session", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "历史", SeqNo: 1, SendStatus: enums.IMMessageStatusSent, AuditFields: migration72TestAudit(now)},
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 2, ClientMsgID: "current-session", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "当前", SeqNo: 2, SendStatus: enums.IMMessageStatusSent, AuditFields: migration72TestAudit(now.Add(time.Hour))},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("create historical messages: %v", err)
	}
	attribution := seedStoreBindingAttributionRecords(t, db, 101, store.ID, instance.ID, conversation.ID, messages[1].ID, now)
	arrivalConnection := &models.StoreArrivalConnection{
		TenantID: 101, StoreID: store.ID, StoreScene: "migration-scene",
		ContactProviderMode: enums.ArrivalContactProviderModeStaticPluginTicket,
		StaticContactPlugID: "migration-plug", WxWorkProtocolInstanceID: instance.ID,
		ConnectionStatus: enums.ArrivalConnectionStatusActive, Status: enums.StatusOk,
		AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(arrivalConnection).Error; err != nil {
		t.Fatalf("create historical arrival connection: %v", err)
	}
	arrivalBinding := &models.ArrivalStoreBinding{
		TenantID: 101, StoreID: store.ID, MiniProgramIdentityID: 991,
		WxWorkProtocolInstanceID: instance.ID, CustomerID: customer.ID, ConversationID: conversation.ID,
		BindingProofType: enums.ArrivalBindingProofTypeCardTicket, BindingStatus: enums.ArrivalBindingStatusBound,
		Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(arrivalBinding).Error; err != nil {
		t.Fatalf("create historical arrival binding: %v", err)
	}
	arrivalTicket := &models.ArrivalBindingTicket{
		TenantID: 101, StoreID: store.ID, WxWorkProtocolInstanceID: instance.ID,
		CustomerID: customer.ID, ConversationID: conversation.ID, TicketHash: "migration-ticket",
		TokenEntropyHash: "migration-ticket-entropy", TicketStatus: enums.ArrivalBindingTicketStatusPending,
		ExpiresAt: now.Add(time.Hour), Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(arrivalTicket).Error; err != nil {
		t.Fatalf("create historical arrival ticket: %v", err)
	}

	for pass := 1; pass <= 2; pass++ {
		if err := migrateStoreConversationContinuity(db); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
	}
	var migrated models.Conversation
	if err := db.First(&migrated, conversation.ID).Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	wantThreadKey := fmt.Sprintf("store:%d:%d:%d:%d", 101, store.ID, customer.ID, binding.ID)
	if migrated.StoreID != store.ID || migrated.StoreStaffBindingID != binding.ID || migrated.ThreadKey == nil || *migrated.ThreadKey != wantThreadKey {
		t.Fatalf("conversation scope not backfilled: %+v", migrated)
	}
	var migratedRoute models.ConversationRouteState
	if err := db.First(&migratedRoute, route.ID).Error; err != nil {
		t.Fatalf("reload route: %v", err)
	}
	if migratedRoute.StoreStaffBindingID != binding.ID || migratedRoute.WxWorkInstanceID != instance.ID || migratedRoute.SessionNo != 2 {
		t.Fatalf("route scope not backfilled: %+v", migratedRoute)
	}
	var sessions []models.ConversationChannelSession
	if err := db.Where("conversation_id = ?", conversation.ID).Order("session_no ASC").Find(&sessions).Error; err != nil {
		t.Fatalf("load channel sessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].WxWorkInstanceID != 0 || sessions[0].EndedAt == nil || sessions[1].WxWorkInstanceID != instance.ID {
		t.Fatalf("historical channel sessions not safely backfilled: %+v", sessions)
	}
	var relation models.StoreCustomerRelation
	if err := db.Where("tenant_id = ? AND customer_id = ? AND store_id = ?", 101, customer.ID, store.ID).Take(&relation).Error; err != nil {
		t.Fatalf("store customer relation missing: %v", err)
	}
	if relation.LastConversationID != conversation.ID || relation.WxWorkInstanceID != instance.ID {
		t.Fatalf("unexpected store customer relation: %+v", relation)
	}
	var handoffSetting models.WxWorkCustomerHandoffSetting
	if err := db.Where("tenant_id = ? AND customer_id = ?", 101, customer.ID).Take(&handoffSetting).Error; err != nil {
		t.Fatalf("customer handoff setting missing: %v", err)
	}
	if handoffSetting.StoreStaffBindingID == nil || *handoffSetting.StoreStaffBindingID != binding.ID || handoffSetting.AutoHandoffEnabled {
		t.Fatalf("customer handoff setting was not migrated to Store staff binding: %+v", handoffSetting)
	}
	var canonicalCount int64
	if err := db.Model(&models.CustomerIdentity{}).
		Where("tenant_id = ? AND external_source = ? AND external_id = ? AND customer_id = ?", 101, enums.ExternalSourceWxWorkProtocol, "wxwork_protocol:external-customer", customer.ID).
		Count(&canonicalCount).Error; err != nil || canonicalCount != 1 {
		t.Fatalf("canonical protocol identity count=%d err=%v", canonicalCount, err)
	}
	assertStoreBindingAttributionRecords(t, db, attribution, binding.ID)
	for _, item := range []struct {
		name  string
		model any
		id    int64
	}{
		{name: "connection", model: &models.StoreArrivalConnection{}, id: arrivalConnection.ID},
		{name: "binding", model: &models.ArrivalStoreBinding{}, id: arrivalBinding.ID},
		{name: "ticket", model: &models.ArrivalBindingTicket{}, id: arrivalTicket.ID},
	} {
		var row struct{ StoreStaffBindingID int64 }
		if err := db.Model(item.model).Select("store_staff_binding_id").Where("id = ?", item.id).Take(&row).Error; err != nil {
			t.Fatalf("reload arrival %s attribution: %v", item.name, err)
		}
		if row.StoreStaffBindingID != binding.ID {
			t.Fatalf("arrival %s binding=%d want=%d", item.name, row.StoreStaffBindingID, binding.ID)
		}
	}
	assertStoreContinuityPermissions(t, db, prefix)
}

func TestStoreConversationContinuityMigrationRejectsDuplicateThread(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), storeContinuityMigrationGORMConfig("d72_"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	fixtures := storeContinuityMigrationModels()
	if err := db.AutoMigrate(fixtures...); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}
	if _, err := ensureRoles(db); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	now := time.Now()
	user := &models.User{TenantID: 101, Username: "duplicate-staff", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	store := &models.Store{TenantID: 101, StoreCode: "duplicate-store", Name: "重复门店", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	channel := &models.Channel{TenantID: 101, Name: "企微员工号", ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "duplicate-channel", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	customer := &models.Customer{TenantID: 101, Name: "重复客户", Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	for _, item := range []any{user, store, channel, customer} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %T: %v", item, err)
		}
	}
	activeUserID := user.ID
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, ActiveUserID: &activeUserID, StoreID: store.ID, Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "duplicate-guid", ChannelID: channel.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID, Status: enums.StatusOk, AuditFields: migration72TestAudit(now)}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	for index := 1; index <= 2; index++ {
		conversation := &models.Conversation{TenantID: 101, ChannelID: channel.ID, CustomerID: customer.ID, Status: enums.IMConversationStatusAIServing, AuditFields: migration72TestAudit(now)}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create conversation %d: %v", index, err)
		}
		if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversation.ID, WxWorkInstanceID: instance.ID, SessionNo: 1, AuditFields: migration72TestAudit(now)}).Error; err != nil {
			t.Fatalf("create route %d: %v", index, err)
		}
		if err := db.Create(&models.WxWorkKFConversation{
			TenantID: 101, ConversationID: conversation.ID, ChannelID: channel.ID,
			OpenKfID: "wx_protocol:duplicate-guid:single", ExternalUserID: fmt.Sprintf("external-%d", index), Status: enums.StatusOk,
			AuditFields: migration72TestAudit(now),
		}).Error; err != nil {
			t.Fatalf("create mapping %d: %v", index, err)
		}
	}
	if err := migrateStoreConversationContinuity(db); err == nil || !strings.Contains(err.Error(), "same store thread") {
		t.Fatalf("duplicate thread migration error=%v", err)
	}
	var scopedCount int64
	if err := db.Model(&models.Conversation{}).Where("store_id <> 0 OR thread_key IS NOT NULL").Count(&scopedCount).Error; err != nil {
		t.Fatalf("count rolled-back conversations: %v", err)
	}
	if scopedCount != 0 {
		t.Fatalf("failed migration must roll back all conversation updates, scoped=%d", scopedCount)
	}
}

func storeContinuityMigrationModels() []any {
	return []any{
		&models.Permission{}, &models.Role{}, &models.RolePermission{},
		&models.User{}, &models.Store{}, &models.StoreStaffBinding{}, &models.Channel{}, &models.WxWorkProtocolInstance{},
		&models.Customer{}, &models.CustomerIdentity{}, &models.StoreCustomerRelation{}, &models.WxWorkCustomerHandoffSetting{},
		&models.Conversation{}, &models.ConversationRouteState{}, &models.ConversationChannelSession{},
		&models.ConversationParticipant{}, &models.Message{}, &models.WxWorkKFConversation{},
		&models.StoreModelCredential{}, &models.StoreModelCredentialAuditLog{}, &models.ModelProfileTestRun{},
		&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}, &models.KnowledgeBase{},
		&models.FastGPTStoreTenant{}, &models.FastGPTDatasetJob{}, &models.FastGPTUsageSyncState{},
		&models.StoreArrivalConnection{}, &models.ArrivalStoreBinding{}, &models.ArrivalBindingTicket{},
	}
}

func storeContinuityMigrationGORMConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: prefix, SingularTable: true},
	}
}

func migration72TestAudit(now time.Time) models.AuditFields {
	return models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "test", UpdateUserName: "test"}
}

func assertStoreContinuityPermissions(t *testing.T, db *gorm.DB, prefix string) {
	t.Helper()
	for _, permission := range storeConversationContinuityPermissionSpecs {
		var count int64
		if err := db.Model(&models.Permission{}).Where("code = ?", permission.Code).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("permission %s count=%d err=%v", permission.Code, count, err)
		}
	}
	for _, permissionCode := range []string{constants.PermissionConversationRelatedView.Code, constants.PermissionConversationInherit.Code} {
		for _, roleCode := range []string{constants.RoleCodeSuperAdmin, constants.RoleCodeAdmin, constants.RoleCodeTenantAdmin, constants.RoleCodeCsTeamLeader} {
			var count int64
			if err := db.Table(prefix+"role_permission AS rp").
				Joins("JOIN "+prefix+"role AS r ON r.id = rp.role_id").
				Joins("JOIN "+prefix+"permission AS p ON p.id = rp.permission_id").
				Where("r.code = ? AND p.code = ?", roleCode, permissionCode).
				Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("role=%s permission=%s count=%d err=%v", roleCode, permissionCode, count, err)
			}
		}
	}
	var memberCount int64
	if err := db.Table(prefix+"role_permission AS rp").
		Joins("JOIN "+prefix+"role AS r ON r.id = rp.role_id").
		Joins("JOIN "+prefix+"permission AS p ON p.id = rp.permission_id").
		Where("r.code IN ? AND p.code = ?", []string{constants.RoleCodeCsUser, constants.RoleCodeStoreStaff}, constants.PermissionConversationRelatedView.Code).
		Count(&memberCount).Error; err != nil || memberCount != 0 {
		t.Fatalf("member roles related-view permission count=%d err=%v", memberCount, err)
	}
}
