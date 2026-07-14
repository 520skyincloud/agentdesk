package services

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var TenantIntegrityAuditService = newTenantIntegrityAuditService()

func newTenantIntegrityAuditService() *tenantIntegrityAuditService {
	return &tenantIntegrityAuditService{}
}

type tenantIntegrityAuditService struct{}

type TenantIntegrityAuditOptions struct {
	SampleLimit int
	Now         time.Time
}

type TenantIntegrityAuditReport struct {
	Status                 string                          `json:"status"`
	GeneratedAt            time.Time                       `json:"generatedAt"`
	DatabaseDriver         string                          `json:"databaseDriver"`
	SampleLimit            int                             `json:"sampleLimit"`
	RegisteredTenantModels int                             `json:"registeredTenantModels"`
	PolicyCount            int                             `json:"policyCount"`
	RequiredTables         int                             `json:"requiredTables"`
	CheckedTables          int                             `json:"checkedTables"`
	ConfiguredRelations    int                             `json:"configuredRelations"`
	CheckedRelations       int                             `json:"checkedRelations"`
	Violations             []TenantIntegrityAuditViolation `json:"violations"`
}

type TenantIntegrityAuditViolation struct {
	Code      string  `json:"code"`
	Entity    string  `json:"entity"`
	Count     int64   `json:"count"`
	SampleIDs []int64 `json:"sampleIds"`
	Message   string  `json:"message"`
}

func (r *TenantIntegrityAuditReport) HasViolations() bool {
	return r != nil && len(r.Violations) > 0
}

type tenantIntegrityModelMetadata struct {
	Name        string
	Table       string
	HasTenantID bool
}

type tenantIntegrityTablePolicy struct {
	AllowAnyZero       bool
	AllowZeroCondition string
}

type tenantIntegrityRelation struct {
	ChildModel  string
	FKColumn    string
	ParentModel string
	Required    bool
	TenantMatch bool
}

func tenantIntegrityTablePolicies() map[string]tenantIntegrityTablePolicy {
	positive := tenantIntegrityTablePolicy{}
	allowZero := tenantIntegrityTablePolicy{AllowAnyZero: true}
	return map[string]tenantIntegrityTablePolicy{
		"TicketView":                 allowZero,
		"Notification":               allowZero,
		"User":                       allowZero,
		"UserRoleChangeLog":          allowZero,
		"TenantInvitation":           positive,
		"TenantRegistrationLog":      {AllowZeroCondition: "c.success = false"},
		"Company":                    positive,
		"Customer":                   positive,
		"CustomerIdentity":           positive,
		"StoreCustomerRelation":      positive,
		"CustomerContact":            positive,
		"Asset":                      allowZero,
		"Tag":                        positive,
		"Conversation":               positive,
		"Store":                      positive,
		"StoreStaffBinding":          positive,
		"WxWorkProtocolInstance":     {AllowZeroCondition: "c.agent_team_id = 0 AND c.channel_id = 0 AND c.company_id = 0 AND c.store_id = 0 AND c.store_staff_binding_id = 0 AND c.knowledge_base_id = 0"},
		"ConversationRouteState":     positive,
		"ConversationSessionSummary": positive,
		"MessageSyncLog":             {AllowZeroCondition: "c.conversation_id = 0 AND c.message_id = 0"},
		"ConversationParticipant":    positive,
		"ConversationReadState":      positive,
		"Message":                    positive,
		"WxWorkKFSyncState":          positive,
		"WxWorkKFConversation":       positive,
		"WxWorkKFMessageRef":         positive,
		"ChannelMessageOutbox":       positive,
		"ConversationAssignment":     positive,
		"ConversationTag":            positive,
		"QuickReply":                 positive,
		"AIAgent":                    positive,
		"Channel":                    positive,
		"ConversationEventLog":       positive,
		"Ticket":                     positive,
		"TicketTag":                  positive,
		"TicketProgress":             positive,
		"AgentProfile":               positive,
		"AgentTeam":                  positive,
		"AgentTeamSquad":             positive,
		"AgentTeamSquadMember":       positive,
		"AgentTeamSchedule":          positive,
		"KnowledgeBase":              positive,
		"KnowledgeDocument":          positive,
		"KnowledgeFAQ":               positive,
		"KnowledgeCandidate":         positive,
		"KnowledgeChunk":             positive,
		"KnowledgeRetrieveLog":       positive,
		"KnowledgeRetrieveHit":       positive,
		"KnowledgeFeedback":          positive,
		"SkillRunLog":                positive,
		"AgentRunLog":                positive,
		"ConversationInterrupt":      {AllowZeroCondition: "c.conversation_id = 0 AND c.ai_agent_id = 0 AND c.source_message_id = 0 AND c.last_resume_message_id = 0"},
	}
}

func tenantIntegrityRelations() []tenantIntegrityRelation {
	tenant := func(child, fk, parent string, required bool) tenantIntegrityRelation {
		return tenantIntegrityRelation{ChildModel: child, FKColumn: fk, ParentModel: parent, Required: required, TenantMatch: true}
	}
	global := func(child, fk, parent string, required bool) tenantIntegrityRelation {
		return tenantIntegrityRelation{ChildModel: child, FKColumn: fk, ParentModel: parent, Required: required}
	}

	return []tenantIntegrityRelation{
		global("UserIdentity", "user_id", "User", true),
		global("UserRole", "user_id", "User", true),
		global("UserRole", "role_id", "Role", true),
		tenant("UserRoleChangeLog", "user_id", "User", true),
		global("UserRoleChangeLog", "operator_id", "User", false),
		global("RolePermission", "role_id", "Role", true),
		global("RolePermission", "permission_id", "Permission", true),
		global("LoginSession", "user_id", "User", true),
		global("LoginCredentialLog", "user_id", "User", false),
		global("WxWorkProtocolDevicePoolInstance", "bound_wx_work_protocol_instance_id", "WxWorkProtocolInstance", false),

		tenant("TenantRegistrationLog", "invitation_id", "TenantInvitation", false),
		tenant("TenantRegistrationLog", "user_id", "User", false),
		global("TenantRegistrationLog", "operator_id", "User", false),
		tenant("Customer", "company_id", "Company", false),
		tenant("CustomerIdentity", "customer_id", "Customer", true),
		tenant("StoreCustomerRelation", "customer_id", "Customer", true),
		tenant("StoreCustomerRelation", "store_id", "Store", true),
		tenant("StoreCustomerRelation", "wx_work_instance_id", "WxWorkProtocolInstance", false),
		tenant("StoreCustomerRelation", "last_conversation_id", "Conversation", false),
		tenant("CustomerContact", "customer_id", "Customer", true),
		tenant("TicketView", "user_id", "User", true),
		tenant("Notification", "recipient_user_id", "User", true),
		tenant("Tag", "parent_id", "Tag", false),

		tenant("Conversation", "ai_agent_id", "AIAgent", false),
		tenant("Conversation", "channel_id", "Channel", false),
		tenant("Conversation", "customer_id", "Customer", false),
		tenant("Conversation", "current_assignee_id", "User", false),
		tenant("Conversation", "current_team_id", "AgentTeam", false),
		tenant("Conversation", "last_message_id", "Message", false),
		global("Conversation", "closed_by", "User", false),
		tenant("Store", "company_id", "Company", false),
		tenant("Store", "knowledge_base_id", "KnowledgeBase", false),
		tenant("StoreStaffBinding", "user_id", "User", true),
		tenant("StoreStaffBinding", "agent_team_id", "AgentTeam", false),
		tenant("StoreStaffBinding", "company_id", "Company", false),
		tenant("StoreStaffBinding", "store_id", "Store", true),
		tenant("WxWorkProtocolInstance", "agent_team_id", "AgentTeam", false),
		tenant("WxWorkProtocolInstance", "channel_id", "Channel", false),
		tenant("WxWorkProtocolInstance", "company_id", "Company", false),
		tenant("WxWorkProtocolInstance", "store_id", "Store", false),
		tenant("WxWorkProtocolInstance", "store_staff_binding_id", "StoreStaffBinding", false),
		tenant("WxWorkProtocolInstance", "knowledge_base_id", "KnowledgeBase", false),

		tenant("ConversationRouteState", "conversation_id", "Conversation", true),
		tenant("ConversationRouteState", "store_id", "Store", false),
		tenant("ConversationRouteState", "knowledge_base_id", "KnowledgeBase", false),
		tenant("ConversationRouteState", "wx_work_instance_id", "WxWorkProtocolInstance", false),
		tenant("ConversationSessionSummary", "conversation_id", "Conversation", true),
		tenant("ConversationSessionSummary", "wx_work_instance_id", "WxWorkProtocolInstance", false),
		tenant("ConversationSessionSummary", "store_id", "Store", false),
		tenant("ConversationSessionSummary", "customer_id", "Customer", false),
		tenant("ConversationSessionSummary", "last_message_id", "Message", false),
		tenant("MessageSyncLog", "conversation_id", "Conversation", false),
		tenant("MessageSyncLog", "message_id", "Message", false),
		tenant("ConversationParticipant", "conversation_id", "Conversation", true),
		tenant("ConversationReadState", "conversation_id", "Conversation", true),
		tenant("ConversationReadState", "last_read_message_id", "Message", false),
		tenant("Message", "conversation_id", "Conversation", true),
		tenant("Message", "quoted_message_id", "Message", false),
		tenant("WxWorkKFConversation", "conversation_id", "Conversation", true),
		tenant("WxWorkKFConversation", "channel_id", "Channel", false),
		tenant("WxWorkKFMessageRef", "conversation_id", "Conversation", false),
		tenant("WxWorkKFMessageRef", "message_id", "Message", false),
		tenant("ChannelMessageOutbox", "conversation_id", "Conversation", true),
		tenant("ChannelMessageOutbox", "message_id", "Message", true),
		tenant("ConversationAssignment", "conversation_id", "Conversation", true),
		tenant("ConversationAssignment", "squad_id", "AgentTeamSquad", false),
		tenant("ConversationAssignment", "from_user_id", "User", false),
		tenant("ConversationAssignment", "to_user_id", "User", false),
		global("ConversationAssignment", "operator_id", "User", false),
		tenant("ConversationTag", "conversation_id", "Conversation", true),
		tenant("ConversationTag", "tag_id", "Tag", true),
		tenant("ConversationEventLog", "conversation_id", "Conversation", true),

		global("AIAgent", "ai_config_id", "AIConfig", false),
		tenant("Channel", "ai_agent_id", "AIAgent", false),
		tenant("Ticket", "customer_id", "Customer", false),
		tenant("Ticket", "conversation_id", "Conversation", false),
		tenant("Ticket", "current_assignee_id", "User", false),
		tenant("TicketTag", "ticket_id", "Ticket", true),
		tenant("TicketTag", "tag_id", "Tag", true),
		tenant("TicketProgress", "ticket_id", "Ticket", true),
		global("TicketProgress", "author_id", "User", false),

		tenant("AgentProfile", "user_id", "User", true),
		tenant("AgentProfile", "team_id", "AgentTeam", false),
		tenant("AgentTeam", "leader_user_id", "User", false),
		tenant("AgentTeamSquad", "team_id", "AgentTeam", true),
		tenant("AgentTeamSquad", "leader_user_id", "User", false),
		tenant("AgentTeamSquadMember", "squad_id", "AgentTeamSquad", true),
		tenant("AgentTeamSquadMember", "agent_profile_id", "AgentProfile", true),
		tenant("AgentTeamSchedule", "team_id", "AgentTeam", true),
		tenant("AgentTeamSchedule", "squad_id", "AgentTeamSquad", false),

		tenant("KnowledgeDocument", "knowledge_base_id", "KnowledgeBase", true),
		tenant("KnowledgeFAQ", "knowledge_base_id", "KnowledgeBase", true),
		tenant("KnowledgeCandidate", "store_id", "Store", false),
		tenant("KnowledgeCandidate", "knowledge_base_id", "KnowledgeBase", false),
		tenant("KnowledgeCandidate", "conversation_id", "Conversation", false),
		global("KnowledgeCandidate", "review_user_id", "User", false),
		tenant("KnowledgeChunk", "knowledge_base_id", "KnowledgeBase", true),
		tenant("KnowledgeChunk", "document_id", "KnowledgeDocument", false),
		tenant("KnowledgeChunk", "faq_id", "KnowledgeFAQ", false),
		tenant("KnowledgeRetrieveLog", "knowledge_base_id", "KnowledgeBase", true),
		tenant("KnowledgeRetrieveLog", "conversation_id", "Conversation", false),
		tenant("KnowledgeRetrieveHit", "retrieve_log_id", "KnowledgeRetrieveLog", true),
		tenant("KnowledgeRetrieveHit", "knowledge_base_id", "KnowledgeBase", false),
		tenant("KnowledgeRetrieveHit", "chunk_id", "KnowledgeChunk", true),
		tenant("KnowledgeRetrieveHit", "document_id", "KnowledgeDocument", false),
		tenant("KnowledgeRetrieveHit", "faq_id", "KnowledgeFAQ", false),
		tenant("KnowledgeFeedback", "retrieve_log_id", "KnowledgeRetrieveLog", true),
		global("KnowledgeFeedback", "user_id", "User", false),

		tenant("SkillRunLog", "conversation_id", "Conversation", false),
		tenant("SkillRunLog", "ai_agent_id", "AIAgent", false),
		global("SkillRunLog", "ai_config_id", "AIConfig", false),
		global("SkillRunLog", "skill_definition_id", "SkillDefinition", false),
		tenant("AgentRunLog", "conversation_id", "Conversation", false),
		tenant("AgentRunLog", "message_id", "Message", false),
		tenant("AgentRunLog", "ai_agent_id", "AIAgent", false),
		global("AgentRunLog", "ai_config_id", "AIConfig", false),
		tenant("ConversationInterrupt", "conversation_id", "Conversation", false),
		tenant("ConversationInterrupt", "ai_agent_id", "AIAgent", false),
		tenant("ConversationInterrupt", "source_message_id", "Message", false),
		tenant("ConversationInterrupt", "last_resume_message_id", "Message", false),

		global("StoreAIModelSetting", "company_id", "Company", false),
		global("StoreAIModelSetting", "store_id", "Store", false),
		global("StoreAIModelSetting", "wx_work_instance_id", "WxWorkProtocolInstance", false),
		global("StoreAIModelSetting", "ai_config_id", "AIConfig", false),
		global("ReplyIntentConfig", "company_id", "Company", false),
		global("ReplyIntentConfig", "store_id", "Store", false),
		global("ReplyIntentConfig", "wx_work_instance_id", "WxWorkProtocolInstance", false),
	}
}

func (s *tenantIntegrityAuditService) Audit(
	db *gorm.DB,
	options TenantIntegrityAuditOptions,
) (*TenantIntegrityAuditReport, error) {
	if db == nil {
		return nil, fmt.Errorf("tenant integrity audit requires a database")
	}
	sampleLimit := options.SampleLimit
	if sampleLimit <= 0 {
		sampleLimit = 20
	}
	if sampleLimit > 1000 {
		sampleLimit = 1000
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}

	metadata, err := tenantIntegrityModelMetadataMap(db)
	if err != nil {
		return nil, err
	}
	policies := tenantIntegrityTablePolicies()
	relations := tenantIntegrityRelations()
	report := &TenantIntegrityAuditReport{
		Status:              "passed",
		GeneratedAt:         now.UTC(),
		DatabaseDriver:      db.Dialector.Name(),
		SampleLimit:         sampleLimit,
		PolicyCount:         len(policies),
		ConfiguredRelations: len(relations),
		Violations:          []TenantIntegrityAuditViolation{},
	}

	tenantModels := make([]string, 0)
	for name, item := range metadata {
		if item.HasTenantID {
			tenantModels = append(tenantModels, name)
		}
	}
	sort.Strings(tenantModels)
	report.RegisteredTenantModels = len(tenantModels)
	for _, name := range tenantModels {
		if _, ok := policies[name]; !ok {
			report.addViolation("UNHANDLED_TENANT_MODEL", name, 1, nil, "注册模型包含 TenantID，但未配置显式审计策略")
		}
	}
	policyNames := sortedTenantIntegrityPolicyNames(policies)
	for _, name := range policyNames {
		if item, ok := metadata[name]; !ok || !item.HasTenantID {
			report.addViolation("STALE_TENANT_POLICY", name, 1, nil, "审计策略未对应当前已注册的 TenantID 模型")
		}
	}

	requiredModels := map[string]struct{}{"Tenant": {}, "Role": {}, "Permission": {}, "UserRole": {}, "RolePermission": {}}
	for name := range policies {
		requiredModels[name] = struct{}{}
	}
	for _, relation := range relations {
		requiredModels[relation.ChildModel] = struct{}{}
		requiredModels[relation.ParentModel] = struct{}{}
	}
	requiredNames := sortedTenantIntegritySet(requiredModels)
	report.RequiredTables = len(requiredNames)
	available := make(map[string]bool, len(requiredNames))
	for _, name := range requiredNames {
		item, ok := metadata[name]
		if !ok {
			report.addViolation("UNREGISTERED_REQUIRED_MODEL", name, 1, nil, "审计关系依赖的模型未注册到 models.Models")
			continue
		}
		if !repositories.TenantIntegrityAuditRepository.HasTable(db, item.Table) {
			report.addViolation("MISSING_REQUIRED_TABLE", name, 1, nil, "缺少租户一致性审计所需数据表 "+item.Table)
			continue
		}
		report.CheckedTables++
		available[name] = true
		if !repositories.TenantIntegrityAuditRepository.HasColumn(db, item.Table, "id") {
			report.addViolation("MISSING_REQUIRED_COLUMN", name+".id", 1, nil, "数据表缺少审计所需主键列")
			available[name] = false
		}
		if item.HasTenantID && !repositories.TenantIntegrityAuditRepository.HasColumn(db, item.Table, "tenant_id") {
			report.addViolation("MISSING_REQUIRED_COLUMN", name+".tenant_id", 1, nil, "租户模型数据表缺少 tenant_id 列")
			available[name] = false
		}
	}

	if available["Tenant"] {
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table: metadata["Tenant"].Table, Alias: "c", Where: "c.id <= 0", IDExpr: "c.id",
		}, sampleLimit, "INVALID_TENANT_ROOT_ID", "Tenant", "租户根记录必须使用正数主键"); err != nil {
			return nil, err
		}
	}
	for _, name := range policyNames {
		if !available[name] {
			continue
		}
		item := metadata[name]
		policy := policies[name]
		invalidWhere := "c.tenant_id <= 0"
		switch {
		case policy.AllowAnyZero:
			invalidWhere = "c.tenant_id < 0"
		case policy.AllowZeroCondition != "":
			invalidWhere = "c.tenant_id < 0 OR (c.tenant_id = 0 AND NOT (" + policy.AllowZeroCondition + "))"
		}
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table: item.Table, Alias: "c", Where: invalidWhere, IDExpr: "c.id",
		}, sampleLimit, "INVALID_TENANT_ID", name, "记录使用了该模型策略不允许的零值或负数 tenant_id"); err != nil {
			return nil, err
		}
		if !available["Tenant"] {
			continue
		}
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table:  item.Table,
			Alias:  "c",
			Joins:  []string{"LEFT JOIN " + metadata["Tenant"].Table + " AS tenant_root ON tenant_root.id = c.tenant_id"},
			Where:  "c.tenant_id > 0 AND tenant_root.id IS NULL",
			IDExpr: "c.id",
		}, sampleLimit, "UNKNOWN_TENANT_ID", name, "记录引用了不存在的租户"); err != nil {
			return nil, err
		}
	}

	for _, relation := range relations {
		if !available[relation.ChildModel] || !available[relation.ParentModel] {
			continue
		}
		child := metadata[relation.ChildModel]
		parent := metadata[relation.ParentModel]
		if !repositories.TenantIntegrityAuditRepository.HasColumn(db, child.Table, relation.FKColumn) {
			report.addViolation("MISSING_REQUIRED_COLUMN", relation.ChildModel+"."+relation.FKColumn, 1, nil, "关系审计所需外键列不存在")
			continue
		}
		report.CheckedRelations++
		entity := relation.ChildModel + "." + relation.FKColumn
		if relation.Required {
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: child.Table, Alias: "c", Where: "c." + relation.FKColumn + " <= 0", IDExpr: "c.id",
			}, sampleLimit, "MISSING_PARENT_REFERENCE", entity, "必填父级引用为空或为负数"); err != nil {
				return nil, err
			}
		}
		join := "LEFT JOIN " + parent.Table + " AS p ON p.id = c." + relation.FKColumn
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table: child.Table, Alias: "c", Joins: []string{join},
			Where: "c." + relation.FKColumn + " > 0 AND p.id IS NULL", IDExpr: "c.id",
		}, sampleLimit, "ORPHAN_PARENT_REFERENCE", entity, "外键引用的父记录不存在"); err != nil {
			return nil, err
		}
		if relation.TenantMatch && child.HasTenantID && parent.HasTenantID {
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: child.Table, Alias: "c", Joins: []string{join},
				Where: "c." + relation.FKColumn + " > 0 AND p.id IS NOT NULL AND c.tenant_id <> p.tenant_id", IDExpr: "c.id",
			}, sampleLimit, "TENANT_RELATION_MISMATCH", entity, "子记录与父记录 tenant_id 不一致"); err != nil {
				return nil, err
			}
		}
	}

	if err := s.auditTenantBusinessKeyDuplicates(db, metadata, available, report, sampleLimit); err != nil {
		return nil, err
	}
	if err := s.auditDynamicTenantReferences(db, metadata, available, report, sampleLimit); err != nil {
		return nil, err
	}
	if err := s.auditAgentOrganizationSemantics(db, metadata, available, report, sampleLimit); err != nil {
		return nil, err
	}
	if err := s.auditRoleScopes(db, metadata, available, report, sampleLimit); err != nil {
		return nil, err
	}
	sort.SliceStable(report.Violations, func(i, j int) bool {
		if report.Violations[i].Code == report.Violations[j].Code {
			return report.Violations[i].Entity < report.Violations[j].Entity
		}
		return report.Violations[i].Code < report.Violations[j].Code
	})
	if len(report.Violations) > 0 {
		report.Status = "failed"
	}
	return report, nil
}

func (s *tenantIntegrityAuditService) auditAgentOrganizationSemantics(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	columnAvailability := make(map[string]bool)
	requireColumn := func(model, table, column, message string) bool {
		key := model + "." + column
		if available, checked := columnAvailability[key]; checked {
			return available
		}
		if repositories.TenantIntegrityAuditRepository.HasColumn(db, table, column) {
			columnAvailability[key] = true
			return true
		}
		columnAvailability[key] = false
		report.addViolation("MISSING_REQUIRED_COLUMN", key, 1, nil, message)
		return false
	}

	if available["AgentTeamSquadMember"] && available["AgentTeamSquad"] && available["AgentProfile"] {
		memberTable := metadata["AgentTeamSquadMember"].Table
		squadTable := metadata["AgentTeamSquad"].Table
		profileTable := metadata["AgentProfile"].Table
		ready := true
		for _, column := range []struct {
			model string
			table string
			name  string
		}{
			{model: "AgentTeamSquadMember", table: memberTable, name: "status"},
			{model: "AgentTeamSquad", table: squadTable, name: "team_id"},
			{model: "AgentProfile", table: profileTable, name: "team_id"},
		} {
			ready = requireColumn(column.model, column.table, column.name, "客服小组组织语义审计所需列不存在") && ready
		}
		if ready {
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: memberTable, Alias: "c",
				Joins: []string{
					"LEFT JOIN " + squadTable + " AS squad ON squad.id = c.squad_id",
					"LEFT JOIN " + profileTable + " AS profile ON profile.id = c.agent_profile_id",
				},
				Where: "c.status = ? AND squad.id IS NOT NULL AND profile.id IS NOT NULL AND squad.team_id <> profile.team_id",
				Args:  []any{enums.StatusOk}, IDExpr: "c.id",
			}, sampleLimit, "AGENT_TEAM_SQUAD_MEMBER_TEAM_MISMATCH", "AgentTeamSquadMember.agent_profile_id", "启用客服小组成员不属于该小组的综合客服组"); err != nil {
				return err
			}
		}
	}

	if available["AgentTeamSchedule"] && available["AgentTeamSquad"] {
		scheduleTable := metadata["AgentTeamSchedule"].Table
		squadTable := metadata["AgentTeamSquad"].Table
		ready := true
		for _, column := range []struct {
			model string
			table string
			name  string
		}{
			{model: "AgentTeamSchedule", table: scheduleTable, name: "team_id"},
			{model: "AgentTeamSchedule", table: scheduleTable, name: "squad_id"},
			{model: "AgentTeamSquad", table: squadTable, name: "team_id"},
		} {
			ready = requireColumn(column.model, column.table, column.name, "客服小组排班语义审计所需列不存在") && ready
		}
		if ready {
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: scheduleTable, Alias: "c",
				Joins:  []string{"LEFT JOIN " + squadTable + " AS squad ON squad.id = c.squad_id"},
				Where:  "c.squad_id > 0 AND squad.id IS NOT NULL AND c.team_id <> squad.team_id",
				IDExpr: "c.id",
			}, sampleLimit, "AGENT_TEAM_SCHEDULE_SQUAD_TEAM_MISMATCH", "AgentTeamSchedule.squad_id", "客服小组排班引用了其他综合客服组的小组"); err != nil {
				return err
			}
		}
	}

	if available["UserRole"] && available["Role"] {
		userRoleTable := metadata["UserRole"].Table
		roleTable := metadata["Role"].Table
		baseReady := requireColumn("UserRole", userRoleTable, "user_id", "职责角色语义审计所需列不存在")
		baseReady = requireColumn("UserRole", userRoleTable, "role_id", "职责角色语义审计所需列不存在") && baseReady
		baseReady = requireColumn("Role", roleTable, "code", "职责角色语义审计所需列不存在") && baseReady
		baseReady = requireColumn("Role", roleTable, "status", "职责角色语义审计所需列不存在") && baseReady
		checks := []struct {
			model      string
			userColumn string
			roleCode   string
			code       string
			entity     string
			message    string
		}{
			{model: "AgentProfile", userColumn: "user_id", roleCode: constants.RoleCodeCsUser, code: "AGENT_PROFILE_MISSING_CS_USER_ROLE", entity: "AgentProfile.user_id", message: "未删除客服档案关联账号缺少启用的客服角色"},
			{model: "AgentTeam", userColumn: "leader_user_id", roleCode: constants.RoleCodeCsTeamLeader, code: "AGENT_TEAM_LEADER_MISSING_ROLE", entity: "AgentTeam.leader_user_id", message: "未删除综合客服组负责人缺少启用的客服组长角色"},
			{model: "StoreStaffBinding", userColumn: "user_id", roleCode: constants.RoleCodeStoreStaff, code: "STORE_STAFF_BINDING_MISSING_ROLE", entity: "StoreStaffBinding.user_id", message: "未删除门店员工绑定账号缺少启用的门店员工角色"},
		}
		for _, check := range checks {
			if !baseReady || !available[check.model] {
				continue
			}
			childTable := metadata[check.model].Table
			ready := requireColumn(check.model, childTable, "status", "职责角色语义审计所需列不存在")
			ready = requireColumn(check.model, childTable, check.userColumn, "职责角色语义审计所需列不存在") && ready
			if !ready {
				continue
			}
			where := fmt.Sprintf(
				"c.status <> ? AND c.%s > 0 AND NOT EXISTS (SELECT 1 FROM %s AS ur JOIN %s AS role ON role.id = ur.role_id WHERE ur.user_id = c.%s AND role.code = ? AND role.status = ?)",
				check.userColumn, userRoleTable, roleTable, check.userColumn,
			)
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: childTable, Alias: "c", Where: where,
				Args: []any{enums.StatusDeleted, check.roleCode, enums.StatusOk}, IDExpr: "c.id",
			}, sampleLimit, check.code, check.entity, check.message); err != nil {
				return err
			}
		}
	}

	if available["AgentTeamSquad"] && available["AgentProfile"] {
		squadTable := metadata["AgentTeamSquad"].Table
		profileTable := metadata["AgentProfile"].Table
		ready := true
		for _, column := range []struct {
			model string
			table string
			name  string
		}{
			{model: "AgentTeamSquad", table: squadTable, name: "status"},
			{model: "AgentTeamSquad", table: squadTable, name: "tenant_id"},
			{model: "AgentTeamSquad", table: squadTable, name: "team_id"},
			{model: "AgentTeamSquad", table: squadTable, name: "leader_user_id"},
			{model: "AgentProfile", table: profileTable, name: "status"},
			{model: "AgentProfile", table: profileTable, name: "tenant_id"},
			{model: "AgentProfile", table: profileTable, name: "team_id"},
			{model: "AgentProfile", table: profileTable, name: "user_id"},
		} {
			ready = requireColumn(column.model, column.table, column.name, "客服小组负责人语义审计所需列不存在") && ready
		}
		if ready {
			if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
				Table: squadTable, Alias: "c",
				Where: "c.status <> ? AND c.leader_user_id > 0 AND NOT EXISTS (SELECT 1 FROM " + profileTable + " AS profile WHERE profile.tenant_id = c.tenant_id AND profile.team_id = c.team_id AND profile.user_id = c.leader_user_id AND profile.status <> ?)",
				Args:  []any{enums.StatusDeleted, enums.StatusDeleted}, IDExpr: "c.id",
			}, sampleLimit, "AGENT_TEAM_SQUAD_LEADER_PROFILE_MISMATCH", "AgentTeamSquad.leader_user_id", "未删除客服小组负责人缺少本综合客服组内客服档案"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *tenantIntegrityAuditService) auditDynamicTenantReferences(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	if err := s.auditNotificationBusinessReferences(db, metadata, available, report, sampleLimit); err != nil {
		return err
	}
	return s.auditKnowledgeCandidateMessageEvidence(db, metadata, available, report, sampleLimit)
}

func (s *tenantIntegrityAuditService) auditNotificationBusinessReferences(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	if !available["Notification"] {
		return nil
	}
	table := metadata["Notification"].Table
	for _, column := range []string{"biz_type", "biz_id"} {
		if !repositories.TenantIntegrityAuditRepository.HasColumn(db, table, column) {
			report.addViolation("MISSING_REQUIRED_COLUMN", "Notification."+column, 1, nil, "动态业务引用审计所需列不存在")
			return nil
		}
	}
	checks := []struct {
		bizType     string
		parentModel string
		entity      string
	}{
		{bizType: "conversation", parentModel: "Conversation", entity: "Notification.conversation"},
		{bizType: "ticket", parentModel: "Ticket", entity: "Notification.ticket"},
	}
	for _, check := range checks {
		if !available[check.parentModel] {
			continue
		}
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table: table, Alias: "c",
			Joins: []string{"LEFT JOIN " + metadata[check.parentModel].Table + " AS p ON p.id = c.biz_id"},
			Where: "c.biz_type = ? AND c.biz_id > 0 AND (p.id IS NULL OR c.tenant_id <> p.tenant_id)",
			Args:  []any{check.bizType}, IDExpr: "c.id",
		}, sampleLimit, "DYNAMIC_TENANT_RELATION_MISMATCH", check.entity, "通知与动态业务对象不存在或 tenant_id 不一致"); err != nil {
			return err
		}
	}
	return nil
}

func (s *tenantIntegrityAuditService) auditKnowledgeCandidateMessageEvidence(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	if !available["KnowledgeCandidate"] || !available["Message"] {
		return nil
	}
	candidateTable := metadata["KnowledgeCandidate"].Table
	for _, column := range []string{"conversation_id", "message_ids"} {
		if !repositories.TenantIntegrityAuditRepository.HasColumn(db, candidateTable, column) {
			report.addViolation("MISSING_REQUIRED_COLUMN", "KnowledgeCandidate."+column, 1, nil, "候选证据审计所需列不存在")
			return nil
		}
	}
	messageTable := metadata["Message"].Table
	if !repositories.TenantIntegrityAuditRepository.HasColumn(db, messageTable, "conversation_id") {
		return nil
	}
	rows, err := repositories.TenantIntegrityAuditRepository.FindCandidateEvidenceRows(db, candidateTable)
	if err != nil {
		return fmt.Errorf("read knowledge candidate message evidence failed: %w", err)
	}
	messageIDs := make([]int64, 0)
	seenMessageIDs := make(map[int64]struct{})
	parsedByCandidateID := make(map[int64][]int64, len(rows))
	invalidCandidateIDs := make(map[int64]struct{})
	for _, row := range rows {
		ids, valid := parseTenantIntegrityMessageIDs(row.MessageIDs)
		if !valid {
			invalidCandidateIDs[row.ID] = struct{}{}
			continue
		}
		parsedByCandidateID[row.ID] = ids
		for _, id := range ids {
			if _, exists := seenMessageIDs[id]; exists {
				continue
			}
			seenMessageIDs[id] = struct{}{}
			messageIDs = append(messageIDs, id)
		}
	}
	sort.Slice(messageIDs, func(i, j int) bool { return messageIDs[i] < messageIDs[j] })
	messagesByID := make(map[int64]repositories.TenantIntegrityMessageEvidenceRow, len(messageIDs))
	const batchSize = 500
	for start := 0; start < len(messageIDs); start += batchSize {
		end := start + batchSize
		if end > len(messageIDs) {
			end = len(messageIDs)
		}
		messageRows, queryErr := repositories.TenantIntegrityAuditRepository.FindMessageEvidenceRows(db, messageTable, messageIDs[start:end])
		if queryErr != nil {
			return fmt.Errorf("read knowledge candidate evidence messages failed: %w", queryErr)
		}
		for _, message := range messageRows {
			messagesByID[message.ID] = message
		}
	}
	for _, row := range rows {
		if _, invalid := invalidCandidateIDs[row.ID]; invalid {
			continue
		}
		for _, messageID := range parsedByCandidateID[row.ID] {
			message, exists := messagesByID[messageID]
			if !exists || message.TenantID != row.TenantID || (row.ConversationID > 0 && message.ConversationID != row.ConversationID) {
				invalidCandidateIDs[row.ID] = struct{}{}
				break
			}
		}
	}
	if len(invalidCandidateIDs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(invalidCandidateIDs))
	for id := range invalidCandidateIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	samples := ids
	if len(samples) > sampleLimit {
		samples = samples[:sampleLimit]
	}
	report.addViolation(
		"KNOWLEDGE_CANDIDATE_MESSAGE_EVIDENCE_MISMATCH",
		"KnowledgeCandidate.message_ids",
		int64(len(ids)),
		samples,
		"知识候选消息证据无效、跨租户或不属于候选会话",
	)
	return nil
}

func parseTenantIntegrityMessageIDs(raw string) ([]int64, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) == 0 {
		return nil, false
	}
	ret := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, false
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ret = append(ret, id)
	}
	return ret, len(ret) > 0
}

func (s *tenantIntegrityAuditService) auditTenantBusinessKeyDuplicates(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	checks := []struct {
		model   string
		column  string
		code    string
		message string
	}{
		{model: "Company", column: "name", code: "DUPLICATE_TENANT_COMPANY_NAME", message: "同一租户存在重复客户企业名称"},
		{model: "Store", column: "store_code", code: "DUPLICATE_TENANT_STORE_CODE", message: "同一租户存在重复门店编码"},
		{model: "AgentProfile", column: "agent_code", code: "DUPLICATE_TENANT_AGENT_CODE", message: "同一租户存在重复客服工号"},
	}
	for _, check := range checks {
		if !available[check.model] {
			continue
		}
		table := metadata[check.model].Table
		where := fmt.Sprintf(
			"c.tenant_id > 0 AND EXISTS (SELECT 1 FROM %s AS duplicate WHERE duplicate.tenant_id = c.tenant_id AND duplicate.%s = c.%s AND duplicate.id <> c.id)",
			table, check.column, check.column,
		)
		if err := s.runCheck(db, report, repositories.TenantIntegrityQuery{
			Table: table, Alias: "c", Where: where, IDExpr: "c.id",
		}, sampleLimit, check.code, check.model+"."+check.column, check.message); err != nil {
			return err
		}
	}
	return nil
}

func (s *tenantIntegrityAuditService) auditRoleScopes(
	db *gorm.DB,
	metadata map[string]tenantIntegrityModelMetadata,
	available map[string]bool,
	report *TenantIntegrityAuditReport,
	sampleLimit int,
) error {
	for _, name := range []string{"User", "UserRole", "Role", "RolePermission", "Permission"} {
		if !available[name] {
			return nil
		}
	}
	checks := []struct {
		query   repositories.TenantIntegrityQuery
		code    string
		entity  string
		message string
	}{
		{
			query: repositories.TenantIntegrityQuery{Table: metadata["Role"].Table, Alias: "c", Where: "c.scope NOT IN (?, ?)", Args: []any{constants.RoleScopePlatform, constants.RoleScopeTenant}, IDExpr: "c.id"},
			code:  "INVALID_ROLE_SCOPE", entity: "Role.scope", message: "角色 scope 不是 platform 或 tenant",
		},
		{
			query: repositories.TenantIntegrityQuery{Table: metadata["Permission"].Table, Alias: "c", Where: "c.scope NOT IN (?, ?)", Args: []any{constants.PermissionScopePlatform, constants.PermissionScopeTenant}, IDExpr: "c.id"},
			code:  "INVALID_PERMISSION_SCOPE", entity: "Permission.scope", message: "权限 scope 不是 platform 或 tenant",
		},
		{
			query: repositories.TenantIntegrityQuery{
				Table: metadata["UserRole"].Table, Alias: "c",
				Joins: []string{"JOIN " + metadata["User"].Table + " AS u ON u.id = c.user_id", "JOIN " + metadata["Role"].Table + " AS r ON r.id = c.role_id"},
				Where: "u.tenant_id > 0 AND r.scope = ?", Args: []any{constants.RoleScopePlatform}, IDExpr: "c.id",
			},
			code: "TENANT_USER_PLATFORM_ROLE", entity: "UserRole", message: "租户账号被赋予平台角色",
		},
		{
			query: repositories.TenantIntegrityQuery{
				Table: metadata["UserRole"].Table, Alias: "c",
				Joins: []string{"JOIN " + metadata["User"].Table + " AS u ON u.id = c.user_id", "JOIN " + metadata["Role"].Table + " AS r ON r.id = c.role_id"},
				Where: "u.tenant_id = 0 AND r.scope <> ?", Args: []any{constants.RoleScopePlatform}, IDExpr: "c.id",
			},
			code: "PLATFORM_USER_TENANT_ROLE", entity: "UserRole", message: "平台账号被赋予租户角色",
		},
		{
			query: repositories.TenantIntegrityQuery{
				Table: metadata["RolePermission"].Table, Alias: "c",
				Joins: []string{"JOIN " + metadata["Role"].Table + " AS r ON r.id = c.role_id", "JOIN " + metadata["Permission"].Table + " AS p ON p.id = c.permission_id"},
				Where: "r.scope <> ? AND p.scope = ?", Args: []any{constants.RoleScopePlatform, constants.PermissionScopePlatform}, IDExpr: "c.id",
			},
			code: "TENANT_ROLE_PLATFORM_PERMISSION", entity: "RolePermission", message: "租户角色被赋予平台权限",
		},
	}
	for _, check := range checks {
		if err := s.runCheck(db, report, check.query, sampleLimit, check.code, check.entity, check.message); err != nil {
			return err
		}
	}
	return nil
}

func (s *tenantIntegrityAuditService) runCheck(
	db *gorm.DB,
	report *TenantIntegrityAuditReport,
	query repositories.TenantIntegrityQuery,
	sampleLimit int,
	code, entity, message string,
) error {
	result, err := repositories.TenantIntegrityAuditRepository.Query(db, query, sampleLimit)
	if err != nil {
		return fmt.Errorf("tenant integrity check %s for %s failed: %w", code, entity, err)
	}
	if result.Count > 0 {
		report.addViolation(code, entity, result.Count, result.SampleIDs, message)
	}
	return nil
}

func (r *TenantIntegrityAuditReport) addViolation(code, entity string, count int64, sampleIDs []int64, message string) {
	if sampleIDs == nil {
		sampleIDs = []int64{}
	}
	r.Violations = append(r.Violations, TenantIntegrityAuditViolation{
		Code: code, Entity: entity, Count: count, SampleIDs: sampleIDs, Message: message,
	})
}

func tenantIntegrityModelMetadataMap(db *gorm.DB) (map[string]tenantIntegrityModelMetadata, error) {
	ret := make(map[string]tenantIntegrityModelMetadata, len(models.Models))
	cache := &sync.Map{}
	for _, model := range models.Models {
		modelType := reflect.TypeOf(model)
		if modelType == nil {
			return nil, fmt.Errorf("models.Models contains a nil model")
		}
		for modelType.Kind() == reflect.Pointer {
			modelType = modelType.Elem()
		}
		if modelType.Kind() != reflect.Struct {
			return nil, fmt.Errorf("registered model %T is not a struct", model)
		}
		parsed, err := schema.Parse(model, cache, db.NamingStrategy)
		if err != nil {
			return nil, fmt.Errorf("parse registered model %s failed: %w", modelType.Name(), err)
		}
		_, hasTenantID := modelType.FieldByName("TenantID")
		ret[modelType.Name()] = tenantIntegrityModelMetadata{
			Name: modelType.Name(), Table: parsed.Table, HasTenantID: hasTenantID,
		}
	}
	return ret, nil
}

func sortedTenantIntegrityPolicyNames(policies map[string]tenantIntegrityTablePolicy) []string {
	ret := make([]string, 0, len(policies))
	for name := range policies {
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret
}

func sortedTenantIntegritySet(items map[string]struct{}) []string {
	ret := make([]string, 0, len(items))
	for item := range items {
		ret = append(ret, item)
	}
	sort.Strings(ret)
	return ret
}
