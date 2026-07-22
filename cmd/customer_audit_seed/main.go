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
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	defaultBatch                  = "customer-audit-v1"
	defaultPassword               = "123456"
	retiredDispatchModelUsageCode = "dispatch_decision_llm"

	tenantLegalName = "丽斯未来酒店"
	channelName     = "丽斯未来酒店测试企微员工号渠道"
	aiAgentName     = "丽斯未来酒店仿真测试接待策略"

	tenantShortName        = "丽斯未来测试"
	tenantRegistrationType = "simulation_test_id"
	tenantRegistrationNo   = "LISSI-FUTURE-HOTEL-TEST-001"
	tenantSupervisorName   = "test_customer_audit_tenant_admin"

	usernamePrefix       = "test_customer_audit_"
	storeCodePrefix      = "test_customer_audit_store_"
	agentCodePrefix      = "test_customer_audit_agent_"
	wxWorkGUIDPrefix     = "test-customer-audit-guid-"
	wxWorkBridgeIDPrefix = "test-customer-audit-bridge-"
)

type seedContext struct {
	db                   *gorm.DB
	tenant               *models.Tenant
	batch                string
	marker               string
	passwordHash         string
	now                  time.Time
	audit                models.AuditFields
	roles                map[string]*models.Role
	supervisor           *models.User
	defaultTeam          *models.AgentTeam
	invitation           *models.TenantInvitation
	aiConfig             *models.AIConfig
	aiAgent              *models.AIAgent
	qualityTemplate      *models.QualityTemplate
	qualityTemplateItems []models.QualityTemplateItem
	channel              *models.Channel
	stores               []*models.Store
	leaders              []*models.User
	agents               []*models.User
	storeStaff           []*models.User
	teams                []*models.AgentTeam
	wxInstances          []*models.WxWorkProtocolInstance
	customers            []*models.Customer
}

type seedOptions struct {
	AIConfigID   int64
	AIConfigName string
}

type report struct {
	Batch                       string `json:"batch"`
	Marker                      string `json:"marker"`
	Tenant                      int64  `json:"tenant"`
	TenantSupervisor            int64  `json:"tenantSupervisor"`
	TenantInvitation            int64  `json:"tenantInvitation"`
	DefaultAgentTeam            int64  `json:"defaultAgentTeam"`
	AIAgent                     int64  `json:"aiAgent"`
	TenantDefaultConfigID       int64  `json:"tenantDefaultConfigId"`
	TenantDefaultConfigName     string `json:"tenantDefaultConfigName"`
	TenantDefaultModelName      string `json:"tenantDefaultModelName"`
	ModelConfigReused           bool   `json:"modelConfigReused"`
	ChannelAIAgentBound         bool   `json:"channelAiAgentBound"`
	SimulationAIAgentBound      int64  `json:"simulationAiAgentBound"`
	LegacyCompanyRows           int64  `json:"legacyCompanyRows"`
	LegacyCompanyReferences     int64  `json:"legacyCompanyReferences"`
	Channel                     int64  `json:"channel"`
	Stores                      int64  `json:"stores"`
	CSLeaders                   int64  `json:"csLeaders"`
	CSUsers                     int64  `json:"csUsers"`
	StoreStaffUsers             int64  `json:"storeStaffUsers"`
	AgentTeams                  int64  `json:"agentTeams"`
	RuleAgentTeams              int64  `json:"ruleAgentTeams"`
	AgentTeamSchedules          int64  `json:"agentTeamSchedules"`
	AgentProfiles               int64  `json:"agentProfiles"`
	ActiveDispatchModelSettings int64  `json:"activeDispatchModelSettings"`
	StoreStaffBindings          int64  `json:"storeStaffBindings"`
	WxWorkInstances             int64  `json:"wxWorkInstances"`
	Customers                   int64  `json:"customers"`
	CustomerContacts            int64  `json:"customerContacts"`
	CustomerIdentities          int64  `json:"customerIdentities"`
	StoreCustomerRels           int64  `json:"storeCustomerRelations"`
	SimulatedConversations      int64  `json:"simulatedConversations"`
	SimulatedMessages           int64  `json:"simulatedMessages"`
	SimulatedAssignments        int64  `json:"simulatedAssignments"`
	SimulatedCurrentlyAssigned  int64  `json:"simulatedCurrentlyAssigned"`
	SimulatedAssignedAgents     int64  `json:"simulatedAssignedAgents"`
	SimulatedNeedReply          int64  `json:"simulatedNeedReply"`
	SimulatedAIServing          int64  `json:"simulatedAiServing"`
	SimulatedPending            int64  `json:"simulatedPending"`
	SimulatedActive             int64  `json:"simulatedActive"`
	SimulatedClosed             int64  `json:"simulatedClosed"`
	ServiceSessions             int64  `json:"serviceSessions"`
	ResponseSpans               int64  `json:"responseSpans"`
	WaitingResponseSpans        int64  `json:"waitingResponseSpans"`
	RepliedResponseSpans        int64  `json:"repliedResponseSpans"`
	PresenceSessions            int64  `json:"presenceSessions"`
	QualityTemplates            int64  `json:"qualityTemplates"`
	QualityTemplateItems        int64  `json:"qualityTemplateItems"`
	QualityInspections          int64  `json:"qualityInspections"`
	CompletedInspections        int64  `json:"completedInspections"`
	QualityInspectionItems      int64  `json:"qualityInspectionItems"`
	Evaluations                 int64  `json:"evaluations"`
	SubmittedEvaluations        int64  `json:"submittedEvaluations"`
	DispatchDecisionLogs        int64  `json:"dispatchDecisionLogs"`
	SelectedDispatchDecisions   int64  `json:"selectedDispatchDecisions"`
	FallbackDispatchDecisions   int64  `json:"fallbackDispatchDecisions"`
	FailedDispatchDecisions     int64  `json:"failedDispatchDecisions"`
	OverrideDispatchDecisions   int64  `json:"overrideDispatchDecisions"`
	AnalyticsPolicies           int64  `json:"analyticsPolicies"`
	ExpectedCoreComplete        bool   `json:"expectedCoreComplete"`
	ExpectedSimulationComplete  bool   `json:"expectedSimulationComplete"`
	SimulationBaselineIntact    bool   `json:"simulationBaselineIntact"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("customer audit seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	action := flag.String("action", "report", "action: seed, cleanup, report, keepalive")
	batch := flag.String("batch", defaultBatch, "test data batch")
	password := flag.String("password", defaultPassword, "test account password")
	aiConfigID := flag.Int64("ai-config-id", 0, "existing enabled LLM AI config ID to reuse")
	aiConfigName := flag.String("ai-config-name", "", "existing enabled LLM AI config name to reuse")
	keepaliveInterval := flag.Duration("keepalive-interval", defaultSimulationPresenceKeepaliveInterval, "simulation agent presence keepalive interval")
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
		return seedWithOptions(db, normalizedBatch, strings.TrimSpace(*password), seedOptions{
			AIConfigID:   *aiConfigID,
			AIConfigName: strings.TrimSpace(*aiConfigName),
		})
	case "cleanup":
		return cleanup(db, normalizedBatch)
	case "report":
		return printReport(db, normalizedBatch)
	case "keepalive":
		return runSimulationPresenceKeepalive(db, normalizedBatch, *keepaliveInterval)
	default:
		return fmt.Errorf("unsupported action %q, expected seed, cleanup, report, or keepalive", *action)
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
	config.SetCurrent(cfg)
	return db, nil
}

func seed(db *gorm.DB, batch, password string) error {
	return seedWithOptions(db, batch, password, seedOptions{})
}

func seedWithOptions(db *gorm.DB, batch, password string, options seedOptions) error {
	if password == "" {
		password = defaultPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash test password failed: %w", err)
	}
	aiConfig, err := resolveSeedAIConfig(db, options)
	if err != nil {
		return err
	}
	tenant, err := ensureTestTenant(db, batch)
	if err != nil {
		return err
	}
	if err := ensureTestTenantInvitation(tenant); err != nil {
		return err
	}

	ctx := &seedContext{
		db:           db,
		batch:        batch,
		marker:       marker(batch),
		passwordHash: string(hash),
		now:          time.Now(),
		audit:        auditFields(),
		aiConfig:     aiConfig,
	}

	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		ctx.db = tx.Tx
		if err := ctx.loadTenant(); err != nil {
			return err
		}
		if err := ctx.loadRoles(); err != nil {
			return err
		}
		if err := ctx.loadTenantFoundation(); err != nil {
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
		if err := ctx.upsertAgentTeamSchedules(); err != nil {
			return err
		}
		if err := ctx.upsertAIAgent(); err != nil {
			return err
		}
		if err := ctx.upsertChannel(); err != nil {
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
		return ctx.retireLegacySimulationCompany()
	})
}

func (ctx *seedContext) loadTenant() error {
	ctx.tenant = repositories.TenantRepository.GetByRegistration(ctx.db, tenantRegistrationType, tenantRegistrationNo)
	if ctx.tenant == nil || ctx.tenant.Status != enums.StatusOk {
		return fmt.Errorf("%s simulation tenant is missing or disabled", tenantLegalName)
	}
	return nil
}

func ensureTestTenant(db *gorm.DB, batch string) (*models.Tenant, error) {
	if existing := repositories.TenantRepository.GetByRegistration(db, tenantRegistrationType, tenantRegistrationNo); existing != nil {
		if existing.LegalName != tenantLegalName || !strings.Contains(existing.Remark, "仿真测试") {
			return nil, fmt.Errorf("test tenant registration identity is already used by non-seed tenant %d", existing.ID)
		}
		if !strings.Contains(existing.Remark, marker(batch)) {
			return nil, fmt.Errorf("simulation tenant belongs to another seed batch; clean it up before using batch %s", batch)
		}
		return existing, nil
	}

	operator := &dto.AuthPrincipal{
		UserID:            constants.SystemAuditUserID,
		Username:          constants.SystemAuditUserName,
		Roles:             []string{constants.RoleCodeSuperAdmin},
		Permissions:       []string{constants.PermissionTenantCreate.Code},
		IsPlatformAccount: true,
	}
	industryProfile := repositories.ReplyIntentProfileRepository.Take(
		db,
		"code = ? AND status = ?",
		replyintent.DefaultHotelProfileCode,
		enums.StatusOk,
	)
	if industryProfile == nil {
		return nil, fmt.Errorf("published hotel industry profile is missing")
	}
	result, err := services.TenantService.CreateTenant(request.CreateTenantRequest{
		IntentProfileID:  industryProfile.ID,
		LegalName:        tenantLegalName,
		ShortName:        tenantShortName,
		RegistrationType: tenantRegistrationType,
		RegistrationNo:   tenantRegistrationNo,
		ContactName:      "丽斯未来仿真测试联系人",
		ContactMobile:    "19900008848",
		ContactEmail:     "lissi-simulation@example.invalid",
		Address:          "仿真测试地址，不代表真实经营地址",
		Remark:           fmt.Sprintf("%s 仿真测试租户，不用于生产", marker(batch)),
		Supervisor: request.CreateTenantSupervisorRequest{
			Username: tenantSupervisorName,
			Nickname: "丽斯未来测试公司主管",
			Mobile:   "19900008849",
			Email:    "lissi-supervisor@example.invalid",
		},
	}, operator)
	if err != nil {
		return nil, fmt.Errorf("create %s simulation tenant failed: %w", tenantLegalName, err)
	}
	return result.Tenant, nil
}

func ensureTestTenantInvitation(tenant *models.Tenant) error {
	if tenant == nil {
		return fmt.Errorf("simulation tenant is missing")
	}
	current := repositories.TenantInvitationRepository.FindCurrent(sqls.DB(), tenant.ID)
	if current != nil && current.ExpiresAt != nil && current.ExpiresAt.After(time.Now()) {
		return nil
	}
	operator := &dto.AuthPrincipal{
		UserID:         constants.SystemAuditUserID,
		ActiveTenantID: tenant.ID,
		Username:       constants.SystemAuditUserName,
		Permissions:    []string{constants.PermissionTenantInviteRotate.Code},
	}
	if _, _, err := services.TenantInvitationService.Rotate(tenant.ID, operator); err != nil {
		return fmt.Errorf("refresh simulation tenant invitation failed: %w", err)
	}
	return nil
}

func resolveSeedAIConfig(db *gorm.DB, options seedOptions) (*models.AIConfig, error) {
	var item *models.AIConfig
	switch {
	case options.AIConfigID > 0:
		item = repositories.AIConfigRepository.Get(db, options.AIConfigID)
	case options.AIConfigName != "":
		item = repositories.AIConfigRepository.Take(db, "name = ? AND model_type = ? AND status = ?", options.AIConfigName, enums.AIModelTypeLLM, enums.StatusOk)
	default:
		item = repositories.AIConfigRepository.GetEnabled(db, enums.AIModelTypeLLM)
	}
	if item == nil {
		return nil, fmt.Errorf("no reusable LLM model configuration found; configure one or pass --ai-config-id/--ai-config-name")
	}
	if item.Status != enums.StatusOk || item.ModelType != enums.AIModelTypeLLM {
		return nil, fmt.Errorf("AI config %d (%s) must be an enabled LLM configuration", item.ID, item.Name)
	}
	if strings.TrimSpace(item.ModelName) == "" || strings.TrimSpace(item.BaseURL) == "" {
		return nil, fmt.Errorf("AI config %d (%s) is incomplete and cannot be reused", item.ID, item.Name)
	}
	return item, nil
}

func cleanup(db *gorm.DB, batch string) error {
	m := marker(batch)
	userPattern := usernamePrefix + "%"
	remarkPattern := likeMarker(m)

	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		db := tx.Tx
		tenant := repositories.TenantRepository.GetByRegistration(db, tenantRegistrationType, tenantRegistrationNo)
		var tenantID int64
		if tenant != nil && strings.Contains(tenant.Remark, m) {
			tenantID = tenant.ID
		}
		userSubquery := db.Model(&models.User{}).Select("id").Where("remark LIKE ?", remarkPattern)
		customerSubquery := db.Model(&models.Customer{}).Select("id").Where("remark LIKE ?", remarkPattern)
		storeSubquery := db.Model(&models.Store{}).Select("id").Where("remark LIKE ?", remarkPattern)

		steps := []struct {
			name string
			fn   func() error
		}{
			{"simulation conversations", func() error {
				return deleteSimulationConversations(db, m, tenantID)
			}},
			{"analytics presence", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.AgentPresenceSession{}).Error
			}},
			{"analytics quality template items", func() error {
				if tenantID <= 0 {
					return nil
				}
				templateSubquery := db.Model(&models.QualityTemplate{}).Select("id").Where("tenant_id = ?", tenantID)
				return db.Where("tenant_id = ? OR template_id IN (?)", tenantID, templateSubquery).Delete(&models.QualityTemplateItem{}).Error
			}},
			{"analytics quality templates", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.QualityTemplate{}).Error
			}},
			{"analytics policies", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.ServiceAnalyticsPolicy{}).Error
			}},
			{"report view presets", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.ReportViewPreset{}).Error
			}},
			{"login credential logs", func() error {
				return db.Where("principal LIKE ?", userPattern).Delete(&models.LoginCredentialLog{}).Error
			}},
			{"login sessions", func() error {
				return db.Where("user_id IN (?)", userSubquery).Delete(&models.LoginSession{}).Error
			}},
			{"user role change logs", func() error {
				return db.Where("user_id IN (?)", userSubquery).Delete(&models.UserRoleChangeLog{}).Error
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
			{"agent team schedules", func() error {
				return db.Where("remark LIKE ?", remarkPattern).Delete(&models.AgentTeamSchedule{}).Error
			}},
			{"tenant model assignments", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.StoreAIModelSetting{}).Error
			}},
			{"tenant model grants", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.TenantAIModelGrant{}).Error
			}},
			{"ai agent", func() error {
				return db.Where("tenant_id = ? AND name = ?", tenantID, aiAgentName).Delete(&models.AIAgent{}).Error
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
			{"legacy company", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.Company{}).Error
			}},
			{"tenant invitations", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("tenant_id = ?", tenantID).Delete(&models.TenantInvitation{}).Error
			}},
			{"tenant", func() error {
				if tenantID <= 0 {
					return nil
				}
				return db.Where("id = ? AND registration_type = ? AND registration_no = ? AND remark LIKE ?", tenantID, tenantRegistrationType, tenantRegistrationNo, remarkPattern).Delete(&models.Tenant{}).Error
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
	tenant := repositories.TenantRepository.GetByRegistration(db, tenantRegistrationType, tenantRegistrationNo)
	var tenantID int64
	if tenant != nil {
		tenantID = tenant.ID
	}

	r := report{
		Batch:  batch,
		Marker: m,
	}
	r.Tenant = count(db, &models.Tenant{}, "id = ? AND legal_name = ? AND remark LIKE ?", tenantID, tenantLegalName, remarkPattern)
	r.TenantSupervisor = count(db, &models.User{}, "tenant_id = ? AND username = ? AND remark LIKE ?", tenantID, tenantSupervisorName, remarkPattern)
	r.TenantInvitation = count(db, &models.TenantInvitation{}, "tenant_id = ? AND status = ?", tenantID, enums.StatusOk)
	r.DefaultAgentTeam = count(db, &models.AgentTeam{}, "tenant_id = ? AND is_default = ? AND remark LIKE ?", tenantID, true, remarkPattern)
	r.AIAgent = count(db, &models.AIAgent{}, "tenant_id = ? AND name = ?", tenantID, aiAgentName)
	aiAgent := repositories.AIAgentRepository.Take(db, "tenant_id = ? AND name = ?", tenantID, aiAgentName)
	if aiAgent != nil {
		r.ChannelAIAgentBound = count(db, &models.Channel{}, "tenant_id = ? AND name = ? AND ai_agent_id = ?", tenantID, channelName, aiAgent.ID) == 1
	}
	defaultModel := repositories.StoreAIModelSettingRepository.Take(db,
		"tenant_id = ? AND wx_work_instance_id = 0 AND usage_code = ? AND status = ?",
		tenantID, constants.AIModelUsageReplyLLM, enums.StatusOk)
	if defaultModel != nil {
		r.TenantDefaultConfigID = defaultModel.AIConfigID
		if aiConfig := repositories.AIConfigRepository.Get(db, defaultModel.AIConfigID); aiConfig != nil {
			r.TenantDefaultConfigName = aiConfig.Name
			r.TenantDefaultModelName = aiConfig.ModelName
			r.ModelConfigReused = aiConfig.Status == enums.StatusOk && aiConfig.ModelType == enums.AIModelTypeLLM
		}
	}
	r.LegacyCompanyRows = count(db, &models.Company{}, "tenant_id = ?", tenantID)
	r.LegacyCompanyReferences = count(db, &models.Store{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.StoreStaffBinding{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.WxWorkProtocolInstance{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.Customer{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.AgentTeam{}, "tenant_id = ? AND company_scope_ids <> ''", tenantID) +
		count(db, &models.KnowledgeBase{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.StoreAIModelSetting{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.KnowledgeResourceGroup{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.FastGPTStoreTenant{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.FastGPTUsageSyncState{}, "tenant_id = ? AND company_id <> 0", tenantID) +
		count(db, &models.FastGPTDatasetJob{}, "tenant_id = ? AND company_id <> 0", tenantID)
	r.Channel = count(db, &models.Channel{}, "remark LIKE ? AND name = ?", remarkPattern, channelName)
	r.Stores = count(db, &models.Store{}, "remark LIKE ? AND store_code LIKE ?", remarkPattern, storeCodePrefix+"%")
	r.CSLeaders = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"cs_leader_%")
	r.CSUsers = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"cs_user_%")
	r.StoreStaffUsers = count(db, &models.User{}, "remark LIKE ? AND username LIKE ?", remarkPattern, usernamePrefix+"store_staff_%")
	r.AgentTeams = count(db, &models.AgentTeam{}, "remark LIKE ? AND is_default = ?", remarkPattern, false)
	r.RuleAgentTeams = count(db, &models.AgentTeam{}, "tenant_id = ? AND remark LIKE ? AND is_default = ? AND dispatch_mode = ?", tenantID, remarkPattern, false, enums.AgentTeamDispatchModeRule)
	r.AgentTeamSchedules = count(db, &models.AgentTeamSchedule{}, "tenant_id = ? AND remark LIKE ? AND status = ? AND start_at <= ? AND end_at > ?", tenantID, remarkPattern, enums.StatusOk, time.Now(), time.Now())
	r.AgentProfiles = count(db, &models.AgentProfile{}, "remark LIKE ? AND agent_code LIKE ?", remarkPattern, agentCodePrefix+"%")
	r.ActiveDispatchModelSettings = count(db, &models.StoreAIModelSetting{}, "tenant_id = ? AND usage_code = ? AND status = ?", tenantID, retiredDispatchModelUsageCode, enums.StatusOk)
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
	if aiAgent != nil {
		r.SimulationAIAgentBound = count(db, &models.Conversation{}, "id IN (?) AND ai_agent_id = ?", simulationConversationSubquery, aiAgent.ID)
	}
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
	r.ServiceSessions = count(db, &models.ConversationServiceSession{}, "tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.ResponseSpans = count(db, &models.ConversationResponseSpan{}, "tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.WaitingResponseSpans = count(db, &models.ConversationResponseSpan{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.ResponseSpanStatusWaiting)
	r.RepliedResponseSpans = count(db, &models.ConversationResponseSpan{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.ResponseSpanStatusReplied)
	r.PresenceSessions = count(db, &models.AgentPresenceSession{}, "tenant_id = ? AND source = ?", tenantID, simulationPresenceSource)
	r.QualityTemplates = count(db, &models.QualityTemplate{}, "tenant_id = ? AND is_default = ? AND status = ?", tenantID, true, enums.StatusOk)
	qualityTemplateSubquery := db.Model(&models.QualityTemplate{}).Select("id").Where("tenant_id = ? AND is_default = ? AND status = ?", tenantID, true, enums.StatusOk)
	r.QualityTemplateItems = count(db, &models.QualityTemplateItem{}, "tenant_id = ? AND template_id IN (?) AND status = ?", tenantID, qualityTemplateSubquery, enums.StatusOk)
	r.QualityInspections = count(db, &models.QualityInspection{}, "tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.CompletedInspections = count(db, &models.QualityInspection{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.QualityInspectionStatusCompleted)
	qualityInspectionSubquery := db.Model(&models.QualityInspection{}).Select("id").Where("tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.QualityInspectionItems = count(db, &models.QualityInspectionItem{}, "tenant_id = ? AND inspection_id IN (?)", tenantID, qualityInspectionSubquery)
	r.Evaluations = count(db, &models.ConversationEvaluation{}, "tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.SubmittedEvaluations = count(db, &models.ConversationEvaluation{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.ConversationEvaluationStatusSubmitted)
	r.DispatchDecisionLogs = count(db, &models.DispatchDecisionLog{}, "tenant_id = ? AND conversation_id IN (?)", tenantID, simulationConversationSubquery)
	r.SelectedDispatchDecisions = count(db, &models.DispatchDecisionLog{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.DispatchDecisionStatusSelected)
	r.FallbackDispatchDecisions = count(db, &models.DispatchDecisionLog{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.DispatchDecisionStatusFallback)
	r.FailedDispatchDecisions = count(db, &models.DispatchDecisionLog{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.DispatchDecisionStatusFailed)
	r.OverrideDispatchDecisions = count(db, &models.DispatchDecisionLog{}, "tenant_id = ? AND conversation_id IN (?) AND status = ?", tenantID, simulationConversationSubquery, enums.DispatchDecisionStatusOverride)
	r.AnalyticsPolicies = count(db, &models.ServiceAnalyticsPolicy{}, "tenant_id = ?", tenantID)
	r.ExpectedCoreComplete = r.Tenant == 1 &&
		r.TenantSupervisor == 1 &&
		r.TenantInvitation == 1 &&
		r.DefaultAgentTeam == 1 &&
		r.AIAgent == 1 &&
		r.ModelConfigReused &&
		r.ChannelAIAgentBound &&
		r.LegacyCompanyRows == 0 &&
		r.LegacyCompanyReferences == 0 &&
		r.Channel == 1 &&
		r.Stores == 100 &&
		r.CSLeaders == 3 &&
		r.CSUsers == 12 &&
		r.StoreStaffUsers == 100 &&
		r.AgentTeams == 3 &&
		r.RuleAgentTeams == 3 &&
		r.AgentTeamSchedules == 3 &&
		r.AgentProfiles == 12 &&
		r.ActiveDispatchModelSettings == 0 &&
		r.StoreStaffBindings == 100 &&
		r.WxWorkInstances == 100 &&
		r.Customers == 500
	r.ExpectedSimulationComplete = r.SimulatedConversations == expectedSimulationConversationCount &&
		r.SimulatedMessages >= expectedSimulationMessageCount &&
		r.SimulatedAssignments >= expectedSimulationAssignmentCount &&
		r.ServiceSessions == expectedSimulationServiceSessionCount &&
		r.ResponseSpans == expectedSimulationResponseSpanCount &&
		r.PresenceSessions == expectedSimulationPresenceCount &&
		r.QualityInspections == expectedSimulationQualityInspectionCount &&
		r.Evaluations == expectedSimulationEvaluationCount &&
		r.DispatchDecisionLogs == expectedSimulationDispatchDecisionCount
	r.SimulationBaselineIntact = r.SimulatedConversations == expectedSimulationConversationCount &&
		r.SimulatedMessages == expectedSimulationMessageCount &&
		r.SimulatedAssignments == expectedSimulationAssignmentCount &&
		r.SimulationAIAgentBound == expectedSimulationConversationCount &&
		r.SimulatedCurrentlyAssigned == 18 &&
		r.SimulatedAssignedAgents == 12 &&
		r.SimulatedNeedReply == expectedSimulationNeedReplyCount &&
		r.SimulatedAIServing == 6 &&
		r.SimulatedPending == 9 &&
		r.SimulatedActive == 18 &&
		r.SimulatedClosed == 3 &&
		r.ServiceSessions == expectedSimulationServiceSessionCount &&
		r.ResponseSpans == expectedSimulationResponseSpanCount &&
		r.WaitingResponseSpans == expectedSimulationWaitingResponseSpanCount &&
		r.RepliedResponseSpans == expectedSimulationRepliedResponseSpanCount &&
		r.PresenceSessions == expectedSimulationPresenceCount &&
		r.QualityTemplates == 1 &&
		r.QualityTemplateItems == 6 &&
		r.QualityInspections == expectedSimulationQualityInspectionCount &&
		r.CompletedInspections == expectedSimulationCompletedInspectionCount &&
		r.QualityInspectionItems == expectedSimulationQualityItemCount &&
		r.Evaluations == expectedSimulationEvaluationCount &&
		r.SubmittedEvaluations == expectedSimulationSubmittedEvaluationCount &&
		r.DispatchDecisionLogs == expectedSimulationDispatchDecisionCount &&
		r.SelectedDispatchDecisions == 18 &&
		r.FallbackDispatchDecisions == 0 &&
		r.FailedDispatchDecisions == 9 &&
		r.OverrideDispatchDecisions == 3 &&
		r.AnalyticsPolicies == 1
	return r
}

func (ctx *seedContext) loadRoles() error {
	required := []string{
		constants.RoleCodeTenantAdmin,
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

func (ctx *seedContext) loadTenantFoundation() error {
	supervisor := &models.User{}
	if err := ctx.db.Where("tenant_id = ? AND username = ?", ctx.tenant.ID, tenantSupervisorName).Take(supervisor).Error; err != nil {
		return fmt.Errorf("load simulation tenant supervisor failed: %w", err)
	}
	if err := ctx.db.Model(supervisor).Updates(map[string]any{
		"nickname":             "丽斯未来测试公司主管",
		"password":             ctx.passwordHash,
		"must_change_password": false,
		"approval_status":      enums.UserApprovalStatusApproved,
		"status":               enums.StatusOk,
		"deleted_at":           nil,
		"remark":               ctx.seedRemark("仿真测试公司主管账号，不用于生产"),
		"updated_at":           ctx.now,
		"update_user_id":       constants.SystemAuditUserID,
		"update_user_name":     constants.SystemAuditUserName,
	}).Error; err != nil {
		return err
	}
	if err := ctx.replaceUserRole(supervisor.ID, ctx.roles[constants.RoleCodeTenantAdmin].ID); err != nil {
		return err
	}
	ctx.supervisor = supervisor

	defaultTeam := &models.AgentTeam{}
	if err := ctx.db.Where("tenant_id = ? AND is_default = ?", ctx.tenant.ID, true).Take(defaultTeam).Error; err != nil {
		return fmt.Errorf("load simulation tenant default team failed: %w", err)
	}
	if err := ctx.db.Model(defaultTeam).Updates(map[string]any{
		"leader_user_id":   0,
		"dispatch_mode":    enums.AgentTeamDispatchModeRule,
		"status":           enums.StatusOk,
		"description":      "丽斯未来酒店仿真测试租户默认综合客服组",
		"remark":           ctx.seedRemark("仿真测试默认综合客服组，不用于生产"),
		"updated_at":       ctx.now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}).Error; err != nil {
		return err
	}
	ctx.defaultTeam = defaultTeam

	ctx.invitation = repositories.TenantInvitationRepository.FindCurrent(ctx.db, ctx.tenant.ID)
	if ctx.invitation == nil || ctx.invitation.ExpiresAt == nil || !ctx.invitation.ExpiresAt.After(ctx.now) {
		return fmt.Errorf("simulation tenant invitation is missing or expired")
	}
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
	err = ctx.db.Where("tenant_id = ? AND name = ? AND channel_type = ?", ctx.tenant.ID, channelName, enums.ChannelTypeWxWorkProtocol).Take(item).Error
	updates := map[string]any{
		"tenant_id":        ctx.tenant.ID,
		"channel_type":     enums.ChannelTypeWxWorkProtocol,
		"channel_id":       "test_customer_audit_wxwork_protocol",
		"ai_agent_id":      ctx.aiAgent.ID,
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
		TenantID:    ctx.tenant.ID,
		Name:        channelName,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "test_customer_audit_wxwork_protocol",
		AIAgentID:   ctx.aiAgent.ID,
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
		name := fmt.Sprintf("%s测试门店%03d", tenantLegalName, i)
		item := &models.Store{}
		err := ctx.db.Where("tenant_id = ? AND store_code = ?", ctx.tenant.ID, code).Take(item).Error
		if err == gorm.ErrRecordNotFound {
			err = ctx.db.Where("tenant_id = 0 AND store_code = ? AND remark LIKE ?", code, likeMarker(ctx.marker)).Take(item).Error
		}
		updates := map[string]any{
			"tenant_id":        ctx.tenant.ID,
			"name":             name,
			"brand_name":       tenantLegalName,
			"company_id":       0,
			"status":           enums.StatusOk,
			"remark":           ctx.seedRemark("测试分门店"),
			"updated_at":       ctx.now,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.ensureSeedTenantOwnership("store", item.ID, item.TenantID, item.Remark); err != nil {
				return err
			}
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			item.TenantID = ctx.tenant.ID
			ctx.stores = append(ctx.stores, item)
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.Store{
			TenantID:    ctx.tenant.ID,
			StoreCode:   code,
			Name:        name,
			BrandName:   tenantLegalName,
			CompanyID:   0,
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
			"测试用门店员工号角色账号",
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
		"tenant_id":        ctx.tenant.ID,
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
		if item.TenantID != ctx.tenant.ID {
			return nil, fmt.Errorf("test user %s belongs to tenant %d, expected %d", username, item.TenantID, ctx.tenant.ID)
		}
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
	} else if err == gorm.ErrRecordNotFound {
		item = &models.User{
			TenantID:           ctx.tenant.ID,
			Username:           username,
			Nickname:           nickname,
			Password:           ctx.passwordHash,
			RegistrationSource: enums.UserRegistrationSourceLegacyMigration,
			ApprovalStatus:     enums.UserApprovalStatusApproved,
			Status:             enums.StatusOk,
			Remark:             ctx.seedRemark(remark),
			AuditFields:        ctx.audit,
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
		err := ctx.db.Where("tenant_id = ? AND name = ?", ctx.tenant.ID, teamName).Take(item).Error
		updates := map[string]any{
			"tenant_id":         ctx.tenant.ID,
			"leader_user_id":    ctx.leaders[i-1].ID,
			"company_scope_ids": "",
			"store_scope_ids":   joinInt64s(storeIDs),
			"dispatch_mode":     enums.AgentTeamDispatchModeRule,
			"status":            enums.StatusOk,
			"description":       fmt.Sprintf("负责%s测试门店%03d-%03d", tenantLegalName, ranges[i-1][0], ranges[i-1][1]),
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
			TenantID:        ctx.tenant.ID,
			Name:            teamName,
			LeaderUserID:    ctx.leaders[i-1].ID,
			CompanyScopeIDs: "",
			StoreScopeIDs:   joinInt64s(storeIDs),
			DispatchMode:    enums.AgentTeamDispatchModeRule,
			Status:          enums.StatusOk,
			Description:     fmt.Sprintf("负责%s测试门店%03d-%03d", tenantLegalName, ranges[i-1][0], ranges[i-1][1]),
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
		err := ctx.db.Where("tenant_id = ? AND (user_id = ? OR agent_code = ?)", ctx.tenant.ID, user.ID, code).Take(item).Error
		if err == gorm.ErrRecordNotFound {
			err = ctx.db.Where("tenant_id = 0 AND (user_id = ? OR agent_code = ?) AND remark LIKE ?", user.ID, code, likeMarker(ctx.marker)).Take(item).Error
		}
		updates := map[string]any{
			"tenant_id":            ctx.tenant.ID,
			"user_id":              user.ID,
			"team_id":              team.ID,
			"agent_code":           code,
			"display_name":         displayName,
			"max_concurrent_count": 20,
			"priority_level":       10 - (i % 4),
			"auto_assign_enabled":  true,
			"status":               enums.StatusOk,
			"remark":               ctx.seedRemark("测试总部客服档案"),
			"updated_at":           ctx.now,
			"update_user_id":       constants.SystemAuditUserID,
			"update_user_name":     constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.ensureSeedTenantOwnership("agent profile", item.ID, item.TenantID, item.Remark); err != nil {
				return err
			}
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.AgentProfile{
			TenantID:           ctx.tenant.ID,
			UserID:             user.ID,
			TeamID:             team.ID,
			AgentCode:          code,
			DisplayName:        displayName,
			MaxConcurrentCount: 20,
			PriorityLevel:      10 - (i % 4),
			AutoAssignEnabled:  true,
			Status:             enums.StatusOk,
			Remark:             ctx.seedRemark("测试总部客服档案"),
			AuditFields:        ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return err
		}
	}
	return nil
}

func (ctx *seedContext) upsertAgentTeamSchedules() error {
	startAt := ctx.now.Add(-time.Hour)
	endAt := ctx.now.Add(8 * time.Hour)
	remark := ctx.seedRemark("测试规则均衡派单当前班次")
	teamIDs := make([]int64, 0, len(ctx.teams))
	for _, team := range ctx.teams {
		teamIDs = append(teamIDs, team.ID)
		item := &models.AgentTeamSchedule{}
		err := ctx.db.Where("tenant_id = ? AND team_id = ? AND remark = ?", ctx.tenant.ID, team.ID, remark).Order("id ASC").Take(item).Error
		updates := map[string]any{
			"tenant_id":        ctx.tenant.ID,
			"team_id":          team.ID,
			"squad_id":         0,
			"start_at":         startAt,
			"end_at":           endAt,
			"remark":           remark,
			"status":           enums.StatusOk,
			"updated_at":       ctx.now,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}
		if err == nil {
			if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
				return err
			}
			if err := ctx.db.Where("tenant_id = ? AND team_id = ? AND remark = ? AND id <> ?", ctx.tenant.ID, team.ID, remark, item.ID).Delete(&models.AgentTeamSchedule{}).Error; err != nil {
				return err
			}
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		item = &models.AgentTeamSchedule{
			TenantID:    ctx.tenant.ID,
			TeamID:      team.ID,
			SquadID:     0,
			StartAt:     startAt,
			EndAt:       endAt,
			Remark:      remark,
			Status:      enums.StatusOk,
			AuditFields: ctx.audit,
		}
		if err := ctx.db.Create(item).Error; err != nil {
			return err
		}
	}
	if len(teamIDs) == 0 {
		return nil
	}
	return ctx.db.Where("tenant_id = ? AND remark = ? AND team_id NOT IN ?", ctx.tenant.ID, remark, teamIDs).Delete(&models.AgentTeamSchedule{}).Error
}

func (ctx *seedContext) upsertAIAgent() error {
	if ctx.aiConfig == nil {
		return fmt.Errorf("reusable AI configuration is missing")
	}
	teamIDs := make([]int64, 0, len(ctx.teams))
	for _, team := range ctx.teams {
		teamIDs = append(teamIDs, team.ID)
	}
	updates := map[string]any{
		"tenant_id":             ctx.tenant.ID,
		"name":                  aiAgentName,
		"description":           "丽斯未来酒店仿真测试接待策略，不用于生产服务",
		"status":                enums.StatusOk,
		"ai_config_id":          0,
		"service_mode":          enums.IMConversationServiceModeAIFirst,
		"system_prompt":         "你是丽斯未来酒店仿真测试客服。当前数据仅用于测试客户咨询、AI 回复和人工派单链路，不代表真实酒店承诺。",
		"welcome_message":       "您好，这里是丽斯未来酒店仿真测试客服，请问有什么可以帮您？",
		"reply_timeout_seconds": 180,
		"team_ids":              joinInt64s(teamIDs),
		"handoff_mode":          enums.AIAgentHandoffModeWaitPool,
		"fallback_mode":         enums.AIAgentFallbackModeSuggestRetry,
		"fallback_message":      "当前仿真测试知识不足，请补充信息或转人工客服处理。",
		"sort_no":               0,
		"updated_at":            ctx.now,
		"update_user_id":        constants.SystemAuditUserID,
		"update_user_name":      constants.SystemAuditUserName,
	}
	item := repositories.AIAgentRepository.Take(ctx.db, "tenant_id = ? AND name = ?", ctx.tenant.ID, aiAgentName)
	if item == nil {
		item = repositories.AIAgentRepository.FindOne(ctx.db, sqls.NewCnd().Eq("tenant_id", ctx.tenant.ID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	}
	if item != nil {
		if err := repositories.AIAgentRepository.UpdatesInTenant(ctx.db, item.ID, ctx.tenant.ID, updates); err != nil {
			return err
		}
		item.AIConfigID = 0
		item.Name = aiAgentName
		ctx.aiAgent = item
		return ctx.ensureTenantModelAccess()
	}
	item = &models.AIAgent{
		TenantID:            ctx.tenant.ID,
		Name:                aiAgentName,
		Description:         "丽斯未来酒店仿真测试接待策略，不用于生产服务",
		Status:              enums.StatusOk,
		AIConfigID:          0,
		ServiceMode:         enums.IMConversationServiceModeAIFirst,
		SystemPrompt:        "你是丽斯未来酒店仿真测试客服。当前数据仅用于测试客户咨询、AI 回复和人工派单链路，不代表真实酒店承诺。",
		WelcomeMessage:      "您好，这里是丽斯未来酒店仿真测试客服，请问有什么可以帮您？",
		ReplyTimeoutSeconds: 180,
		TeamIDs:             joinInt64s(teamIDs),
		HandoffMode:         enums.AIAgentHandoffModeWaitPool,
		FallbackMode:        enums.AIAgentFallbackModeSuggestRetry,
		FallbackMessage:     "当前仿真测试知识不足，请补充信息或转人工客服处理。",
		AuditFields:         ctx.audit,
	}
	if err := repositories.AIAgentRepository.Create(ctx.db, item); err != nil {
		return err
	}
	ctx.aiAgent = item
	return ctx.ensureTenantModelAccess()
}

func (ctx *seedContext) ensureTenantModelAccess() error {
	grant := repositories.TenantAIModelGrantRepository.Take(ctx.db,
		"tenant_id = ? AND ai_config_id = ?", ctx.tenant.ID, ctx.aiConfig.ID)
	if grant == nil {
		grant = &models.TenantAIModelGrant{
			TenantID: ctx.tenant.ID, AIConfigID: ctx.aiConfig.ID,
			Status: enums.StatusOk, AuditFields: ctx.audit,
		}
		if err := repositories.TenantAIModelGrantRepository.Create(ctx.db, grant); err != nil {
			return err
		}
	} else if err := repositories.TenantAIModelGrantRepository.Updates(ctx.db, grant.ID, map[string]any{
		"status": enums.StatusOk, "updated_at": ctx.now,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	}); err != nil {
		return err
	}

	for _, usageCode := range []string{constants.AIModelUsageReplyLLM, constants.AIModelUsageIntentDetectLLM} {
		setting := repositories.StoreAIModelSettingRepository.Take(ctx.db,
			"tenant_id = ? AND wx_work_instance_id = 0 AND usage_code = ?", ctx.tenant.ID, usageCode)
		if setting == nil {
			setting = &models.StoreAIModelSetting{
				TenantID: ctx.tenant.ID, UsageCode: usageCode, AIConfigID: ctx.aiConfig.ID,
				Status: enums.StatusOk, AuditFields: ctx.audit,
			}
			if err := repositories.StoreAIModelSettingRepository.Create(ctx.db, setting); err != nil {
				return err
			}
			continue
		}
		if err := repositories.StoreAIModelSettingRepository.Updates(ctx.db, setting.ID, map[string]any{
			"company_id": 0, "store_id": 0, "ai_config_id": ctx.aiConfig.ID, "status": enums.StatusOk,
			"updated_at": ctx.now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}); err != nil {
			return err
		}
	}
	return ctx.db.Model(&models.StoreAIModelSetting{}).
		Where("tenant_id = ? AND usage_code = ? AND status <> ?", ctx.tenant.ID, retiredDispatchModelUsageCode, enums.StatusDeleted).
		Updates(map[string]any{
			"status": enums.StatusDeleted, "updated_at": ctx.now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error
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
		"tenant_id":              ctx.tenant.ID,
		"user_id":                staff.ID,
		"agent_team_id":          agentTeamID,
		"company_id":             0,
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
		if err := ctx.ensureSeedTenantOwnership("store staff binding", item.ID, item.TenantID, item.Remark); err != nil {
			return nil, err
		}
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
		item.TenantID = ctx.tenant.ID
		item.AgentTeamID = agentTeamID
		return item, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	item = &models.StoreStaffBinding{
		TenantID:             ctx.tenant.ID,
		UserID:               staff.ID,
		AgentTeamID:          agentTeamID,
		CompanyID:            0,
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
		"tenant_id":                          ctx.tenant.ID,
		"agent_team_id":                      agentTeamID,
		"channel_id":                         ctx.channel.ID,
		"employee_user_id":                   employeeUserID,
		"employee_name":                      "客服",
		"company_id":                         0,
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
		if err := ctx.ensureSeedTenantOwnership("wxwork instance", item.ID, item.TenantID, item.Remark); err != nil {
			return nil, err
		}
		if err := ctx.db.Model(item).Updates(updates).Error; err != nil {
			return nil, err
		}
		item.TenantID = ctx.tenant.ID
		return item, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	item = &models.WxWorkProtocolInstance{
		TenantID:                  ctx.tenant.ID,
		AgentTeamID:               agentTeamID,
		Guid:                      guid,
		ChannelID:                 ctx.channel.ID,
		EmployeeUserID:            employeeUserID,
		EmployeeName:              "客服",
		CompanyID:                 0,
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

func (ctx *seedContext) retireLegacySimulationCompany() error {
	if ctx.tenant == nil || ctx.tenant.ID <= 0 {
		return nil
	}
	return ctx.db.Where("tenant_id = ?", ctx.tenant.ID).Delete(&models.Company{}).Error
}

func (ctx *seedContext) upsertCustomer(index int) (*models.Customer, error) {
	name := fmt.Sprintf("测试顾客%03d", index)
	item := &models.Customer{}
	err := ctx.db.Where("tenant_id = ? AND name = ? AND remark LIKE ?", ctx.tenant.ID, name, likeMarker(ctx.marker)).Take(item).Error
	gender := enums.GenderUnknown
	if index%3 == 1 {
		gender = enums.GenderMale
	} else if index%3 == 2 {
		gender = enums.GenderFemale
	}
	mobile := fmt.Sprintf("199%08d", index)
	email := fmt.Sprintf("test_customer_audit_%03d@example.test", index)
	updates := map[string]any{
		"tenant_id":        ctx.tenant.ID,
		"gender":           gender,
		"company_id":       0,
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
		TenantID:      ctx.tenant.ID,
		Name:          name,
		Gender:        gender,
		CompanyID:     0,
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
	err := ctx.db.Where("tenant_id = ? AND customer_id = ? AND contact_type = ? AND contact_value = ?", ctx.tenant.ID, customer.ID, enums.ContactTypeMobile, value).Take(item).Error
	updates := map[string]any{
		"tenant_id":        ctx.tenant.ID,
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
		TenantID:     ctx.tenant.ID,
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
	err := ctx.db.Where("tenant_id = ? AND customer_id = ? AND external_source = ? AND external_id = ?", ctx.tenant.ID, customer.ID, enums.ExternalSourceWxWorkProtocol, externalID).Take(item).Error
	updates := map[string]any{
		"tenant_id":        ctx.tenant.ID,
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
		TenantID:       ctx.tenant.ID,
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
	err := ctx.db.Where("tenant_id = ? AND customer_id = ? AND store_id = ?", ctx.tenant.ID, customer.ID, store.ID).Take(item).Error
	notes := ctx.seedRemark(fmt.Sprintf("测试顾客%03d关联门店%03d", customerIndex, storeIndex))
	updates := map[string]any{
		"tenant_id":           ctx.tenant.ID,
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
		TenantID:         ctx.tenant.ID,
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

func (ctx *seedContext) ensureSeedTenantOwnership(entity string, id, tenantID int64, remark string) error {
	if tenantID == ctx.tenant.ID {
		return nil
	}
	if tenantID == 0 && strings.Contains(remark, "TEST_SEED:") {
		return nil
	}
	return fmt.Errorf("%s %d belongs to tenant %d, expected %d", entity, id, tenantID, ctx.tenant.ID)
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
