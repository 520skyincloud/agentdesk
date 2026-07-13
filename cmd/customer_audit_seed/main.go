package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	defaultBatch    = "customer-audit-v1"
	defaultPassword = "123456"

	companyName = "丽斯未来酒店"
	channelName = "丽斯未来酒店测试企微员工号渠道"

	usernamePrefix       = "test_customer_audit_"
	storeCodePrefix      = "test_customer_audit_store_"
	agentCodePrefix      = "test_customer_audit_agent_"
	wxWorkGUIDPrefix     = "test-customer-audit-guid-"
	wxWorkBridgeIDPrefix = "test-customer-audit-bridge-"
)

type seedContext struct {
	db           *gorm.DB
	batch        string
	marker       string
	passwordHash string
	now          time.Time
	audit        models.AuditFields
	roles        map[string]*models.Role
	company      *models.Company
	channel      *models.Channel
	stores       []*models.Store
	leaders      []*models.User
	agents       []*models.User
	storeStaff   []*models.User
	teams        []*models.AgentTeam
	wxInstances  []*models.WxWorkProtocolInstance
	customers    []*models.Customer
}

type report struct {
	Batch                      string `json:"batch"`
	Marker                     string `json:"marker"`
	CompanyMarked              int64  `json:"companyMarked"`
	CompanyNameExists          bool   `json:"companyNameExists"`
	Channel                    int64  `json:"channel"`
	Stores                     int64  `json:"stores"`
	CSLeaders                  int64  `json:"csLeaders"`
	CSUsers                    int64  `json:"csUsers"`
	StoreStaffUsers            int64  `json:"storeStaffUsers"`
	AgentTeams                 int64  `json:"agentTeams"`
	AgentProfiles              int64  `json:"agentProfiles"`
	StoreStaffBindings         int64  `json:"storeStaffBindings"`
	WxWorkInstances            int64  `json:"wxWorkInstances"`
	Customers                  int64  `json:"customers"`
	CustomerContacts           int64  `json:"customerContacts"`
	CustomerIdentities         int64  `json:"customerIdentities"`
	StoreCustomerRels          int64  `json:"storeCustomerRelations"`
	SimulatedConversations     int64  `json:"simulatedConversations"`
	SimulatedMessages          int64  `json:"simulatedMessages"`
	SimulatedAssignments       int64  `json:"simulatedAssignments"`
	SimulatedCurrentlyAssigned int64  `json:"simulatedCurrentlyAssigned"`
	SimulatedAssignedAgents    int64  `json:"simulatedAssignedAgents"`
	SimulatedNeedReply         int64  `json:"simulatedNeedReply"`
	SimulatedAIServing         int64  `json:"simulatedAiServing"`
	SimulatedPending           int64  `json:"simulatedPending"`
	SimulatedActive            int64  `json:"simulatedActive"`
	SimulatedClosed            int64  `json:"simulatedClosed"`
	ExpectedCoreComplete       bool   `json:"expectedCoreComplete"`
	ExpectedSimulationComplete bool   `json:"expectedSimulationComplete"`
	SimulationBaselineIntact   bool   `json:"simulationBaselineIntact"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("customer audit seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	action := flag.String("action", "report", "action: seed, cleanup, report")
	batch := flag.String("batch", defaultBatch, "test data batch")
	password := flag.String("password", defaultPassword, "test account password")
	flag.Parse()

	normalizedBatch := strings.TrimSpace(*batch)
	if normalizedBatch == "" {
		return fmt.Errorf("batch cannot be empty")
	}

	db, err := initDB(*configPath)
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(*action)) {
	case "seed":
		return seed(db, normalizedBatch, strings.TrimSpace(*password))
	case "cleanup":
		return cleanup(db, normalizedBatch)
	case "report":
		return printReport(db, normalizedBatch)
	default:
		return fmt.Errorf("unsupported action %q, expected seed, cleanup, or report", *action)
	}
}

func initDB(configPath string) (*gorm.DB, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config failed: %w", err)
	}
	db, err := bootstrap.InitDB(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("init db failed: %w", err)
	}
	if err := bootstrap.InitMigrations(); err != nil {
		return nil, fmt.Errorf("run migrations failed: %w", err)
	}
	return db, nil
}

func seed(db *gorm.DB, batch, password string) error {
	if password == "" {
		password = defaultPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash test password failed: %w", err)
	}

	ctx := &seedContext{
		db:           db,
		batch:        batch,
		marker:       marker(batch),
		passwordHash: string(hash),
		now:          time.Now(),
		audit:        auditFields(),
	}

	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		ctx.db = tx.Tx
		if err := ctx.loadRoles(); err != nil {
			return err
		}
		if err := ctx.upsertCompany(); err != nil {
			return err
		}
		if err := ctx.upsertChannel(); err != nil {
			return err
		}
		if err := ctx.upsertStores(); err != nil {
			return err
		}
		if err := ctx.upsertUsers(); err != nil {
			return err
		}
		if err := ctx.upsertTeams(); err != nil {
			return err
		}
		if err := ctx.upsertAgentProfiles(); err != nil {
			return err
		}
		if err := ctx.upsertStoreBindingsAndInstances(); err != nil {
			return err
		}
		if err := ctx.syncTeamWxWorkInstanceScopes(); err != nil {
			return err
		}
		if err := ctx.upsertCustomers(); err != nil {
			return err
		}
		if err := ctx.upsertSimulationConversations(); err != nil {
			return err
		}
		return nil
	})
}

func cleanup(db *gorm.DB, batch string) error {
	m := marker(batch)
	userPattern := usernamePrefix + "%"
	remarkPattern := likeMarker(m)

	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		db := tx.Tx
		userSubquery := db.Model(&models.User{}).Select("id").Where("remark LIKE ?", remarkPattern)
		customerSubquery := db.Model(&models.Customer{}).Select("id").Where("remark LIKE ?", remarkPattern)
		storeSubquery := db.Model(&models.Store{}).Select("id").Where("remark LIKE ?", remarkPattern)

		steps := []struct {
			name string
			fn   func() error
		}{
			{"simulation conversations", func() error {
				return deleteSimulationConversations(db, m)
			}},
			{"login credential logs", func() error {
				return db.Where("principal LIKE ?", userPattern).Delete(&models.LoginCredentialLog{}).Error
			}},
			{"login sessions", func() error {
				return db.Where("user_id IN (?)", userSubquery).Delete(&models.LoginSession{}).Error
			}},
			{"customer contacts", func() error {
				return db.Where("customer_id IN (?) OR remark LIKE ?", customerSubquery, remarkPattern).Delete(&models.CustomerContact{}).Error
			}},
			{"customer identities", func() error {
				return db.Where("customer_id IN (?) OR raw_profile LIKE ?", customerSubquery, remarkPattern).Delete(&models.CustomerIdentity{}).Error
			}},
			{"store customer relations", func() error {
				return db.Where("customer_id IN (?) OR store_id IN (?) OR stable_notes LIKE ?", customerSubquery, storeSubquery, remarkPattern).Delete(&models.StoreCustomerRelation{}).Error
			}},
			{"wxwork instances", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.WxWorkProtocolInstance{}).Error
			}},
			{"store staff bindings", func() error {
				return db.Where("remark LIKE ? OR store_id IN (?)", remarkPattern, storeSubquery).Delete(&models.StoreStaffBinding{}).Error
			}},
			{"agent profiles", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.AgentProfile{}).Error
			}},
			{"agent teams", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.AgentTeam{}).Error
			}},
			{"user roles", func() error {
				return db.Where("user_id IN (?)", userSubquery).Delete(&models.UserRole{}).Error
			}},
			{"users", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.User{}).Error
			}},
			{"customers", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.Customer{}).Error
			}},
			{"stores", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.Store{}).Error
			}},
			{"channel", func() error {
				return db.Where("remark LIKE ? AND name = ?", remarkPattern, channelName).Delete(&models.Channel{}).Error
			}},
			{"company", func() error {
				return db.Where("remark LIKE ? AND name = ?", remarkPattern, companyName).Delete(&models.Company{}).Error
			}},
		}

		for _, step := range steps {
			if err := step.fn(); err != nil {
				return fmt.Errorf("cleanup %s failed: %w", step.name, err)
			}
		}
		return nil
	})
}

func printReport(db *gorm.DB, batch string) error {
	r := buildReport(db, batch)
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func buildReport(db *gorm.DB, batch string) report {
	m := marker(batch)
	remarkPattern := likeMarker(m)

	r := report{
		Batch:  batch,
		Marker: m,
	}
	r.CompanyMarked = count(db, &models.Company{}, "remark LIKE ? AND name = ?", remarkPattern, companyName)
	r.CompanyNameExists = count(db, &models.Company{}, "name = ?", companyName) > 0
	r.Channel = count(db, &models.Channel{}, "remark LIKE ? AND name = ?", remarkPattern, channelName)
	r.Stores = count(db, &models.Store{}, "remark LIKE ? AND store_code LIKE ?", remarkPattern, storeCodePrefix+"%")
	r.CSLeaders = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"cs_leader_%")
	r.CSUsers = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"cs_user_%")
	r.StoreStaffUsers = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"store_staff_%")
	r.AgentTeams = count(db, &models.AgentTeam{}, "remark LIKE ?", remarkPattern)
	r.AgentProfiles = count(db, &models.AgentProfile{}, "remark LIKE ? AND agent_code LIKE ?", remarkPattern, agentCodePrefix+"%")
	r.StoreStaffBindings = count(db, &models.StoreStaffBinding{}, "remark LIKE ?", remarkPattern)
	r.WxWorkInstances = count(db, &models.WxWorkProtocolInstance{}, "remark LIKE ? AND guid LIKE ?", remarkPattern, wxWorkGUIDPrefix+"%")
	r.Customers = count(db, &models.Customer{}, "remark LIKE ?", remarkPattern)
	r.CustomerContacts = count(db, &models.CustomerContact{}, "remark LIKE ?", remarkPattern)
	r.CustomerIdentities = count(db, &models.CustomerIdentity{}, "raw_profile LIKE ?", remarkPattern)
	r.StoreCustomerRels = count(db, &models.StoreCustomerRelation{}, "stable_notes LIKE ?", remarkPattern)
	simulationConversationSubquery := db.Model(&models.ConversationRouteState{}).
		Select("conversation_id").
		Where("remark LIKE ?", remarkPattern)
	r.SimulatedConversations = count(db, &models.Conversation{}, "id IN (?)", simulationConversationSubquery)
	r.SimulatedMessages = count(db, &models.Message{}, "conversation_id IN (?)", simulationConversationSubquery)
	r.SimulatedAssignments = count(db, &models.ConversationAssignment{}, "conversation_id IN (?)", simulationConversationSubquery)
	r.SimulatedCurrentlyAssigned = count(db, &models.Conversation{}, "id IN (?) AND status = ? AND current_assignee_id > 0", simulationConversationSubquery, enums.IMConversationStatusActive)
	db.Model(&models.Conversation{}).
		Where("id IN (?) AND status = ? AND current_assignee_id > 0", simulationConversationSubquery, enums.IMConversationStatusActive).
		Distinct("current_assignee_id").
		Count(&r.SimulatedAssignedAgents)
	r.SimulatedNeedReply = count(db, &models.ConversationRouteState{}, "remark LIKE ? AND need_human_follow_up = ?", remarkPattern, true)
	r.SimulatedAIServing = count(db, &models.Conversation{}, "id IN (?) AND status = ?", simulationConversationSubquery, enums.IMConversationStatusAIServing)
	r.SimulatedPending = count(db, &models.Conversation{}, "id IN (?) AND status = ?", simulationConversationSubquery, enums.IMConversationStatusPending)
	r.SimulatedActive = count(db, &models.Conversation{}, "id IN (?) AND status = ?", simulationConversationSubquery, enums.IMConversationStatusActive)
	r.SimulatedClosed = count(db, &models.Conversation{}, "id IN (?) AND status = ?", simulationConversationSubquery, enums.IMConversationStatusClosed)
	r.ExpectedCoreComplete = r.CompanyNameExists &&
		r.Channel == 1 &&
		r.Stores == 100 &&
		r.CSLeaders == 3 &&
		r.CSUsers == 12 &&
		r.StoreStaffUsers == 100 &&
		r.AgentTeams == 3 &&
		r.AgentProfiles == 12 &&
		r.StoreStaffBindings == 100 &&
		r.WxWorkInstances == 100 &&
		r.Customers == 500
	r.ExpectedSimulationComplete = r.SimulatedConversations == expectedSimulationConversationCount &&
		r.SimulatedMessages >= expectedSimulationMessageCount &&
		r.SimulatedAssignments >= expectedSimulationAssignmentCount
	r.SimulationBaselineIntact = r.SimulatedConversations == expectedSimulationConversationCount &&
		r.SimulatedMessages == expectedSimulationMessageCount &&
		r.SimulatedAssignments == expectedSimulationAssignmentCount &&
		r.SimulatedCurrentlyAssigned == 18 &&
		r.SimulatedAssignedAgents == 12 &&
		r.SimulatedNeedReply == expectedSimulationNeedReplyCount &&
		r.SimulatedAIServing == 6 &&
		r.SimulatedPending == 9 &&
		r.SimulatedActive == 18 &&
		r.SimulatedClosed == 3
	return r
}

func (ctx *seedContext) loadRoles() error {
	required := []string{
		constants.RoleCodeCsTeamLeader,
		constants.RoleCodeCsUser,
		constants.RoleCodeStoreStaff,
	}
	ctx.roles = make(map[string]*models.Role, len(required))
	for _, code := range required {
		role := &models.Role{}
		if err := ctx.db.Where("code = ? AND status = ?", code, enums.StatusOk).Take(role).Error; err != nil {
			return fmt.Errorf("required role %s not found or disabled", code)
		}
		ctx.roles[code] = role
	}
	return nil
}

func (ctx *seedContext) upsertCompany() error {
	item := &models.Company{}
	err := ctx.db.Where("name = ?", companyName).Take(item).Error
	if err == nil {
		ctx.company = item
		if strings.Contains(item.Remark, ctx.marker) {
			return ctx.db.Model(item).Updates(map[string]any{
				"code":             "lissi-future-hotel",
				"status":           enums.StatusOk,
				"updated_at":       ctx.now,
				"update_user_id":   constants.SystemAuditUserID,
				"update_user_name": constants.SystemAuditUserName,
			}).Error
		}
		return nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	item = &models.Company{
		Name:        companyName,
		Code:        "lissi-future-hotel",
		Status:      enums.StatusOk,
		Remark:      ctx.seedRemark("测试公司"),
		AuditFields: ctx.audit,
	}
	if err := ctx.db.Create(item).Error; err != nil {
		return err
	}
	ctx.company = item
	return nil
}

func (ctx *seedContext) upsertChannel() error {
	cfg := dto.WxWorkProtocolChannelConfig{
		AppKey:        "test_customer_audit_app_key",
		AppSecret:     "test_customer_audit_app_secret",
		BaseURL:       "https://chat-api.juhebot.com/open/GuidRequest",
		CallbackToken: "test_customer_audit_callback_token",
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	item := &models.Channel{}
	err = ctx.db.Where("name = ? AND channel_type = ?", channelName, enums.ChannelTypeWxWorkProtocol).Take(item).Error
	updates := map[string]any{
		"channel_type":     enums.ChannelTypeWxWorkProtocol,
		"channel_id":       "test_customer_audit_wxwork_protocol",
		"ai_agent_id":      0,
		"config_json":      string(raw),
		"status":           enums.StatusOk,
		"remark":           ctx.seedRemark("测试企微员工号协议渠道"),
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err == nil {
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return err
		}
		ctx.channel = item
		return nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	item = &models.Channel{
		Name:        channelName,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "test_customer_audit_wxwork_protocol",
		AIAgentID:   0,
		ConfigJSON:  string(raw),
		Status:      enums.StatusOk,
		Remark:      ctx.seedRemark("测试企微员工号协议渠道"),
		AuditFields: ctx.audit,
	}
	if err := ctx.db.Create(item).Error; err != nil {
		return err
	}
	ctx.channel = item
	return nil
}

func (ctx *seedContext) upsertStores() error {
	ctx.stores = make([]*models.Store, 0, 100)
	for i := 1; i <= 100; i++ {
		code := fmt.Sprintf("%s%03d", storeCodePrefix, i)
		name := fmt.Sprintf("%s测试门店%03d", companyName, i)
		item := &models.Store{}
		err := ctx.db.Where("store_code = ?", code).Take(item).Error
		updates := map[string]any{
			"name":             name,
			"brand_name":       companyName,
			"company_id":       ctx.company.ID,
			"status":           enums.StatusOk,
			"remark":           ctx.seedRemark("测试分门店"),
			"updated_at":       ctx.now,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			ctx.stores = append(ctx.stores, item)
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.Store{
			StoreCode:   code,
			Name:        name,
			BrandName:   companyName,
			CompanyID:   ctx.company.ID,
			Status:      enums.StatusOk,
			Remark:      ctx.seedRemark("测试分门店"),
			AuditFields: ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return err
		}
		ctx.stores = append(ctx.stores, item)
	}
	return nil
}

func (ctx *seedContext) upsertUsers() error {
	ctx.leaders = make([]*models.User, 0, 3)
	ctx.agents = make([]*models.User, 0, 12)
	ctx.storeStaff = make([]*models.User, 0, 100)

	for i := 1; i <= 3; i++ {
		user, err := ctx.upsertUser(
			fmt.Sprintf("%scs_leader_%03d", usernamePrefix, i),
			fmt.Sprintf("测试客服组长%03d", i),
			ctx.roles[constants.RoleCodeCsTeamLeader].ID,
			"测试客服组长账号",
		)
		if err != nil {
			return err
		}
		ctx.leaders = append(ctx.leaders, user)
	}
	for i := 1; i <= 12; i++ {
		user, err := ctx.upsertUser(
			fmt.Sprintf("%scs_user_%03d", usernamePrefix, i),
			fmt.Sprintf("测试客服%03d", i),
			ctx.roles[constants.RoleCodeCsUser].ID,
			"测试客服账号",
		)
		if err != nil {
			return err
		}
		ctx.agents = append(ctx.agents, user)
	}
	for i := 1; i <= 100; i++ {
		user, err := ctx.upsertUser(
			fmt.Sprintf("%sstore_staff_%03d", usernamePrefix, i),
			fmt.Sprintf("测试门店员工%03d", i),
			ctx.roles[constants.RoleCodeStoreStaff].ID,
			"测试门店员工账号",
		)
		if err != nil {
			return err
		}
		ctx.storeStaff = append(ctx.storeStaff, user)
	}
	return nil
}

func (ctx *seedContext) upsertUser(username, nickname string, roleID int64, remark string) (*models.User, error) {
	item := &models.User{}
	err := ctx.db.Where("username = ?", username).Take(item).Error
	updates := map[string]any{
		"nickname":         nickname,
		"password":         ctx.passwordHash,
		"status":           enums.StatusOk,
		"deleted_at":       nil,
		"remark":           ctx.seedRemark(remark),
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err == nil {
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
	} else if err == gorm.ErrRecordNotFound {
		item = &models.User{
			Username:    username,
			Nickname:    nickname,
			Password:    ctx.passwordHash,
			Status:      enums.StatusOk,
			Remark:      ctx.seedRemark(remark),
			AuditFields: ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err := ctx.replaceUserRole(item.ID, roleID); err != nil {
		return nil, err
	}
	return item, nil
}

func (ctx *seedContext) replaceUserRole(userID, roleID int64) error {
	if err := ctx.db.Where("user_id = ?", userID).Delete(&models.UserRole{}).Error; err != nil {
		return err
	}
	return ctx.db.Create(&models.UserRole{
		UserID:      userID,
		RoleID:      roleID,
		AuditFields: ctx.audit,
	}).Error
}

func (ctx *seedContext) upsertTeams() error {
	ctx.teams = make([]*models.AgentTeam, 0, 3)
	ranges := [][2]int{{1, 34}, {35, 67}, {68, 100}}
	for i := 1; i <= 3; i++ {
		teamName := fmt.Sprintf("测试客服组%03d", i)
		storeIDs := make([]int64, 0, ranges[i-1][1]-ranges[i-1][0]+1)
		for storeIndex := ranges[i-1][0]; storeIndex <= ranges[i-1][1]; storeIndex++ {
			storeIDs = append(storeIDs, ctx.stores[storeIndex-1].ID)
		}
		item := &models.AgentTeam{}
		err := ctx.db.Where("name = ?", teamName).Take(item).Error
		updates := map[string]any{
			"leader_user_id":    ctx.leaders[i-1].ID,
			"company_scope_ids": fmt.Sprintf("%d", ctx.company.ID),
			"store_scope_ids":   joinInt64s(storeIDs),
			"status":            enums.StatusOk,
			"description":       fmt.Sprintf("负责%s测试门店%03d-%03d", companyName, ranges[i-1][0], ranges[i-1][1]),
			"remark":            ctx.seedRemark("测试客服组"),
			"updated_at":        ctx.now,
			"update_user_id":    constants.SystemAuditUserID,
			"update_user_name":  constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			ctx.teams = append(ctx.teams, item)
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.AgentTeam{
			Name:            teamName,
			LeaderUserID:    ctx.leaders[i-1].ID,
			CompanyScopeIDs: fmt.Sprintf("%d", ctx.company.ID),
			StoreScopeIDs:   joinInt64s(storeIDs),
			Status:          enums.StatusOk,
			Description:     fmt.Sprintf("负责%s测试门店%03d-%03d", companyName, ranges[i-1][0], ranges[i-1][1]),
			Remark:          ctx.seedRemark("测试客服组"),
			AuditFields:     ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return err
		}
		ctx.teams = append(ctx.teams, item)
	}
	return nil
}

func (ctx *seedContext) upsertAgentProfiles() error {
	for i, user := range ctx.agents {
		team := ctx.teams[i/4]
		code := fmt.Sprintf("%s%03d", agentCodePrefix, i+1)
		displayName := fmt.Sprintf("测试客服%03d", i+1)
		item := &models.AgentProfile{}
		err := ctx.db.Where("user_id = ? OR agent_code = ?", user.ID, code).Take(item).Error
		updates := map[string]any{
			"user_id":                 user.ID,
			"team_id":                 team.ID,
			"agent_code":              code,
			"display_name":            displayName,
			"service_status":          enums.ServiceStatusIdle,
			"max_concurrent_count":    20,
			"priority_level":          10 - (i % 4),
			"auto_assign_enabled":     true,
			"receive_offline_message": true,
			"status":                  enums.StatusOk,
			"remark":                  ctx.seedRemark("测试总部客服档案"),
			"updated_at":              ctx.now,
			"update_user_id":          constants.SystemAuditUserID,
			"update_user_name":        constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.AgentProfile{
			UserID:                user.ID,
			TeamID:                team.ID,
			AgentCode:             code,
			DisplayName:           displayName,
			ServiceStatus:         enums.ServiceStatusIdle,
			MaxConcurrentCount:    20,
			PriorityLevel:         10 - (i % 4),
			AutoAssignEnabled:     true,
			ReceiveOfflineMessage: true,
			Status:                enums.StatusOk,
			Remark:                ctx.seedRemark("测试总部客服档案"),
			AuditFields:           ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return err
		}
	}
	return nil
}

func (ctx *seedContext) upsertStoreBindingsAndInstances() error {
	ctx.wxInstances = make([]*models.WxWorkProtocolInstance, 0, 100)
	for i, store := range ctx.stores {
		staff := ctx.storeStaff[i]
		binding, err := ctx.upsertStoreStaffBinding(i+1, store, staff)
		if err != nil {
			return err
		}
		instance, err := ctx.upsertWxWorkInstance(i+1, store, binding)
		if err != nil {
			return err
		}
		ctx.wxInstances = append(ctx.wxInstances, instance)
	}
	return nil
}

func (ctx *seedContext) upsertStoreStaffBinding(index int, store *models.Store, staff *models.User) (*models.StoreStaffBinding, error) {
	agentTeamID := ctx.seedAgentTeamID(index)
	item := &models.StoreStaffBinding{}
	err := ctx.db.Where("store_id = ?", store.ID).Take(item).Error
	updates := map[string]any{
		"user_id":                staff.ID,
		"agent_team_id":          agentTeamID,
		"company_id":             ctx.company.ID,
		"managed_mode":           constants.StoreManagedModeSemi,
		"fallback_to_hq":         true,
		"manual_timeout_minutes": 10,
		"status":                 enums.StatusOk,
		"remark":                 ctx.seedRemark(fmt.Sprintf("测试门店员工绑定%03d", index)),
		"updated_at":             ctx.now,
		"update_user_id":         constants.SystemAuditUserID,
		"update_user_name":       constants.SystemAuditUserName,
	}
	if err == nil {
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
		item.AgentTeamID = agentTeamID
		return item, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	item = &models.StoreStaffBinding{
		UserID:               staff.ID,
		AgentTeamID:          agentTeamID,
		CompanyID:            ctx.company.ID,
		StoreID:              store.ID,
		ManagedMode:          constants.StoreManagedModeSemi,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: 10,
		Status:               enums.StatusOk,
		Remark:               ctx.seedRemark(fmt.Sprintf("测试门店员工绑定%03d", index)),
		AuditFields:          ctx.audit,
	}
	if err := ctx.db.Create(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func (ctx *seedContext) upsertWxWorkInstance(index int, store *models.Store, binding *models.StoreStaffBinding) (*models.WxWorkProtocolInstance, error) {
	guid := fmt.Sprintf("%s%03d", wxWorkGUIDPrefix, index)
	employeeUserID := fmt.Sprintf("test_customer_audit_employee_%03d", index)
	agentTeamID := binding.AgentTeamID
	item := &models.WxWorkProtocolInstance{}
	err := ctx.db.Where("guid = ?", guid).Take(item).Error
	updates := map[string]any{
		"agent_team_id":                      agentTeamID,
		"channel_id":                         ctx.channel.ID,
		"employee_user_id":                   employeeUserID,
		"employee_name":                      "客服",
		"company_id":                         ctx.company.ID,
		"store_id":                           store.ID,
		"store_staff_binding_id":             binding.ID,
		"store_navigation_name":              store.Name,
		"bridge_id":                          fmt.Sprintf("%s%03d", wxWorkBridgeIDPrefix, index),
		"staff_user_ids":                     employeeUserID,
		"fallback_to_hq":                     true,
		"manual_timeout_minutes":             10,
		"ai_reply_enabled":                   true,
		"auto_accept_friend_request":         false,
		"context_max_messages":               30,
		"context_max_tokens":                 8000,
		"context_compression_enabled":        true,
		"health_status":                      "unknown",
		"status":                             enums.StatusOk,
		"welcome_message":                    "您好，我是酒店客服，请问有什么可以帮您？",
		"welcome_send_mini_program":          false,
		"welcome_ask_location":               false,
		"auto_accept_friend_remark_template": "",
		"remark":                             ctx.seedRemark(fmt.Sprintf("测试企微员工号实例%03d；占位数据，非真实登录实例", index)),
		"updated_at":                         ctx.now,
		"update_user_id":                     constants.SystemAuditUserID,
		"update_user_name":                   constants.SystemAuditUserName,
	}
	if err == nil {
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
		return item, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	item = &models.WxWorkProtocolInstance{
		AgentTeamID:               agentTeamID,
		Guid:                      guid,
		ChannelID:                 ctx.channel.ID,
		EmployeeUserID:            employeeUserID,
		EmployeeName:              "客服",
		CompanyID:                 ctx.company.ID,
		StoreID:                   store.ID,
		StoreStaffBindingID:       binding.ID,
		StoreNavigationName:       store.Name,
		BridgeID:                  fmt.Sprintf("%s%03d", wxWorkBridgeIDPrefix, index),
		StaffUserIDs:              employeeUserID,
		FallbackToHQ:              true,
		ManualTimeoutMinutes:      10,
		AIReplyEnabled:            true,
		ContextMaxMessages:        30,
		ContextMaxTokens:          8000,
		ContextCompressionEnabled: true,
		HealthStatus:              "unknown",
		Status:                    enums.StatusOk,
		WelcomeMessage:            "您好，我是酒店客服，请问有什么可以帮您？",
		WelcomeSendMiniProgram:    false,
		WelcomeAskLocation:        false,
		Remark:                    ctx.seedRemark(fmt.Sprintf("测试企微员工号实例%03d；占位数据，非真实登录实例", index)),
		AuditFields:               ctx.audit,
	}
	if err := ctx.db.Create(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func (ctx *seedContext) seedAgentTeamID(index int) int64 {
	teamIndex := 0
	if index > 67 {
		teamIndex = 2
	} else if index > 34 {
		teamIndex = 1
	}
	return ctx.teams[teamIndex].ID
}

func (ctx *seedContext) syncTeamWxWorkInstanceScopes() error {
	if len(ctx.teams) < 3 || len(ctx.wxInstances) < 100 {
		return nil
	}
	ranges := [][2]int{{1, 34}, {35, 67}, {68, 100}}
	for i, team := range ctx.teams {
		if i >= len(ranges) {
			break
		}
		instanceIDs := make([]int64, 0, ranges[i][1]-ranges[i][0]+1)
		for instanceIndex := ranges[i][0]; instanceIndex <= ranges[i][1]; instanceIndex++ {
			instanceIDs = append(instanceIDs, ctx.wxInstances[instanceIndex-1].ID)
		}
		joined := joinInt64s(instanceIDs)
		if err := ctx.db.Model(team).Updates(map[string]any{
			"wx_work_instance_scope_ids": joined,
			"updated_at":                 ctx.now,
			"update_user_id":             constants.SystemAuditUserID,
			"update_user_name":           constants.SystemAuditUserName,
		}).Error; err != nil {
			return err
		}
		team.WxWorkInstanceScopeIDs = joined
	}
	return nil
}

func (ctx *seedContext) upsertCustomers() error {
	ctx.customers = make([]*models.Customer, 0, 500)
	for i := 1; i <= 500; i++ {
		customer, err := ctx.upsertCustomer(i)
		if err != nil {
			return err
		}
		if err := ctx.upsertCustomerContact(i, customer); err != nil {
			return err
		}
		if err := ctx.upsertCustomerIdentity(i, customer); err != nil {
			return err
		}
		ctx.customers = append(ctx.customers, customer)
		for _, storeIndex := range customerStoreIndexes(i) {
			if err := ctx.upsertStoreCustomerRelation(i, storeIndex, customer); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ctx *seedContext) upsertCustomer(index int) (*models.Customer, error) {
	name := fmt.Sprintf("测试顾客%03d", index)
	item := &models.Customer{}
	err := ctx.db.Where("name = ? AND remark LIKE ?", name, likeMarker(ctx.marker)).Take(item).Error
	gender := enums.GenderUnknown
	if index%3 == 1 {
		gender = enums.GenderMale
	} else if index%3 == 2 {
		gender = enums.GenderFemale
	}
	mobile := fmt.Sprintf("199%08d", index)
	email := fmt.Sprintf("test_customer_audit_%03d@example.test", index)
	updates := map[string]any{
		"gender":           gender,
		"company_id":       ctx.company.ID,
		"primary_mobile":   mobile,
		"primary_email":    email,
		"status":           enums.StatusOk,
		"remark":           ctx.seedRemark("测试顾客"),
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err == nil {
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
		return item, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	item = &models.Customer{
		Name:          name,
		Gender:        gender,
		CompanyID:     ctx.company.ID,
		PrimaryMobile: mobile,
		PrimaryEmail:  email,
		Status:        enums.StatusOk,
		Remark:        ctx.seedRemark("测试顾客"),
		AuditFields:   ctx.audit,
	}
	if err := ctx.db.Create(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func (ctx *seedContext) upsertCustomerContact(index int, customer *models.Customer) error {
	value := fmt.Sprintf("199%08d", index)
	item := &models.CustomerContact{}
	err := ctx.db.Where("customer_id = ? AND contact_type = ? AND contact_value = ?", customer.ID, enums.ContactTypeMobile, value).Take(item).Error
	updates := map[string]any{
		"is_primary":       true,
		"is_verified":      false,
		"source":           "test_seed",
		"status":           enums.StatusOk,
		"remark":           ctx.seedRemark("测试顾客手机号"),
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err == nil {
		return ctx.db.Model(item).Updates(updates).Error
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return ctx.db.Create(&models.CustomerContact{
		CustomerID:   customer.ID,
		ContactType:  enums.ContactTypeMobile,
		ContactValue: value,
		IsPrimary:    true,
		Source:       "test_seed",
		Status:       enums.StatusOk,
		Remark:       ctx.seedRemark("测试顾客手机号"),
		AuditFields:  ctx.audit,
	}).Error
}

func (ctx *seedContext) upsertCustomerIdentity(index int, customer *models.Customer) error {
	externalID := fmt.Sprintf("test_customer_audit_customer_%03d", index)
	rawProfile := fmt.Sprintf(`{"%s":true,"batch":%q,"name":%q}`, ctx.marker, ctx.batch, customer.Name)
	item := &models.CustomerIdentity{}
	err := ctx.db.Where("customer_id = ? AND external_source = ? AND external_id = ?", customer.ID, enums.ExternalSourceWxWorkProtocol, externalID).Take(item).Error
	updates := map[string]any{
		"raw_profile":      rawProfile,
		"status":           enums.StatusOk,
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err == nil {
		return ctx.db.Model(item).Updates(updates).Error
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return ctx.db.Create(&models.CustomerIdentity{
		CustomerID:     customer.ID,
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     externalID,
		RawProfile:     rawProfile,
		Status:         enums.StatusOk,
		AuditFields:    ctx.audit,
	}).Error
}

func (ctx *seedContext) upsertStoreCustomerRelation(customerIndex int, storeIndex int, customer *models.Customer) error {
	store := ctx.stores[storeIndex-1]
	instance := ctx.wxInstances[storeIndex-1]
	item := &models.StoreCustomerRelation{}
	err := ctx.db.Where("customer_id = ? AND store_id = ?", customer.ID, store.ID).Take(item).Error
	notes := ctx.seedRemark(fmt.Sprintf("测试顾客%03d关联门店%03d", customerIndex, storeIndex))
	updates := map[string]any{
		"wx_work_instance_id": instance.ID,
		"last_active_at":      ctx.now,
		"visit_count":         relationVisitCount(customerIndex),
		"tags":                relationTags(customerIndex),
		"stable_notes":        notes,
		"status":              enums.StatusOk,
		"updated_at":          ctx.now,
		"update_user_id":      constants.SystemAuditUserID,
		"update_user_name":    constants.SystemAuditUserName,
	}
	if instance != nil {
		updates["wx_work_instance_id"] = instance.ID
	}
	if err == nil {
		return ctx.db.Model(item).Updates(updates).Error
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return ctx.db.Create(&models.StoreCustomerRelation{
		CustomerID:       customer.ID,
		StoreID:          store.ID,
		WxWorkInstanceID: instance.ID,
		LastActiveAt:     &ctx.now,
		VisitCount:       relationVisitCount(customerIndex),
		Tags:             relationTags(customerIndex),
		StableNotes:      notes,
		Status:           enums.StatusOk,
		AuditFields:      ctx.audit,
	}).Error
}

func customerStoreIndexes(customerIndex int) []int {
	base := ((customerIndex - 1) % 100) + 1
	if customerIndex <= 350 {
		return []int{base}
	}
	if customerIndex <= 450 {
		return uniqueStoreIndexes(base, 2+(customerIndex%2))
	}
	return uniqueStoreIndexes(base, 3+(customerIndex%3))
}

func uniqueStoreIndexes(base int, count int) []int {
	ret := make([]int, 0, count)
	seen := map[int]bool{}
	for i := 0; len(ret) < count; i++ {
		next := ((base - 1 + i*17) % 100) + 1
		if seen[next] {
			continue
		}
		seen[next] = true
		ret = append(ret, next)
	}
	return ret
}

func relationVisitCount(customerIndex int) int {
	if customerIndex > 450 {
		return 6 + customerIndex%5
	}
	if customerIndex > 350 {
		return 2 + customerIndex%3
	}
	return 1
}

func relationTags(customerIndex int) string {
	switch {
	case customerIndex > 450:
		return "测试高频客户,测试VIP客户"
	case customerIndex > 350:
		return "测试多店客户"
	case customerIndex%10 == 0:
		return "测试待回访"
	default:
		return "测试普通咨询"
	}
}

func (ctx *seedContext) seedRemark(label string) string {
	return fmt.Sprintf("%s %s", ctx.marker, label)
}

func auditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{
		CreatedAt:      now,
		CreateUserID:   constants.SystemAuditUserID,
		CreateUserName: constants.SystemAuditUserName,
		UpdatedAt:      now,
		UpdateUserID:   constants.SystemAuditUserID,
		UpdateUserName: constants.SystemAuditUserName,
	}
}

func marker(batch string) string {
	return "TEST_SEED:" + batch
}

func likeMarker(value string) string {
	return "%" + value + "%"
}

func count(db *gorm.DB, model any, query string, args ...any) int64 {
	var total int64
	db.Model(model).Where(query, args...).Count(&total)
	return total
}

func joinInt64s(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ",")
}
