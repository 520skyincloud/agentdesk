package services

import (
	"fmt"
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

func TestTenantIntegrityPoliciesCoverEveryRegisteredTenantModel(t *testing.T) {
	db := openTenantIntegrityTestDB(t, false)
	metadata, err := tenantIntegrityModelMetadataMap(db)
	if err != nil {
		t.Fatalf("build model metadata: %v", err)
	}
	policies := tenantIntegrityTablePolicies()
	for name, item := range metadata {
		_, hasPolicy := policies[name]
		if item.HasTenantID && !hasPolicy {
			t.Errorf("TenantID model %s has no audit policy", name)
		}
		if !item.HasTenantID && hasPolicy {
			t.Errorf("non-tenant model %s has a stale audit policy", name)
		}
	}
	if len(policies) != 51 {
		t.Fatalf("policy count = %d, want 51 explicit TenantID policies", len(policies))
	}
}

func TestTenantIntegrityAuditPassesCleanTwoTenantFixture(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	createCleanTenantIntegrityFixture(t, db)

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit clean fixture: %v", err)
	}
	if report.Status != "passed" || report.HasViolations() {
		t.Fatalf("clean fixture failed audit: %#v", report.Violations)
	}
	if report.RegisteredTenantModels != 51 || report.PolicyCount != 51 {
		t.Fatalf("tenant model coverage = %d/%d, want 51/51", report.RegisteredTenantModels, report.PolicyCount)
	}
	if report.CheckedTables != report.RequiredTables {
		t.Fatalf("checked tables = %d, required = %d", report.CheckedTables, report.RequiredTables)
	}
	if report.CheckedRelations != report.ConfiguredRelations {
		t.Fatalf("checked relations = %d, configured = %d", report.CheckedRelations, report.ConfiguredRelations)
	}
}

func TestTenantIntegrityAuditReportsTenantRelationAndRoleViolations(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)

	for i := 0; i < 3; i++ {
		user := &models.User{
			TenantID: -1, Username: fmt.Sprintf("negative-tenant-%d", i), Password: "x",
			Status: enums.StatusOk, AuditFields: audit,
		}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create negative tenant user: %v", err)
		}
	}
	if err := db.Create(&models.Company{TenantID: 0, Name: "zero tenant company", Status: enums.StatusOk, AuditFields: audit}).Error; err != nil {
		t.Fatalf("create zero tenant company: %v", err)
	}
	if err := db.Create(&models.Company{TenantID: 999999, Name: "unknown tenant company", Status: enums.StatusOk, AuditFields: audit}).Error; err != nil {
		t.Fatalf("create unknown tenant company: %v", err)
	}
	companyA := &models.Company{TenantID: fixture.tenantA.ID, Name: "tenant A company", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(companyA).Error; err != nil {
		t.Fatalf("create tenant A company: %v", err)
	}
	mismatchCustomer := &models.Customer{TenantID: fixture.tenantB.ID, CompanyID: companyA.ID, Name: "mismatch customer", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(mismatchCustomer).Error; err != nil {
		t.Fatalf("create mismatched customer: %v", err)
	}
	orphanIdentity := &models.CustomerIdentity{
		TenantID: fixture.tenantA.ID, CustomerID: 987654, ExternalSource: enums.ExternalSourceGuest,
		ExternalID: "orphan-external", Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(orphanIdentity).Error; err != nil {
		t.Fatalf("create orphan identity: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: fixture.tenantUserA.ID, RoleID: fixture.platformRole.ID, AuditFields: audit}).Error; err != nil {
		t.Fatalf("assign platform role to tenant user: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: fixture.platformUser.ID, RoleID: fixture.tenantRole.ID, AuditFields: audit}).Error; err != nil {
		t.Fatalf("assign tenant role to platform user: %v", err)
	}
	platformPermission := &models.Permission{
		Name: "Platform only", Code: "test.platform.only", Type: "api", Scope: constants.PermissionScopePlatform,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(platformPermission).Error; err != nil {
		t.Fatalf("create platform permission: %v", err)
	}
	if err := db.Create(&models.RolePermission{RoleID: fixture.tenantRole.ID, PermissionID: platformPermission.ID, AuditFields: audit}).Error; err != nil {
		t.Fatalf("assign platform permission to tenant role: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 2})
	if err != nil {
		t.Fatalf("audit invalid fixture: %v", err)
	}
	for _, code := range []string{
		"INVALID_TENANT_ID",
		"UNKNOWN_TENANT_ID",
		"ORPHAN_PARENT_REFERENCE",
		"TENANT_RELATION_MISMATCH",
		"TENANT_USER_PLATFORM_ROLE",
		"PLATFORM_USER_TENANT_ROLE",
		"TENANT_ROLE_PLATFORM_PERMISSION",
	} {
		if !tenantIntegrityReportHasCode(report, code) {
			t.Errorf("audit report does not contain %s: %#v", code, report.Violations)
		}
	}
	userViolation := tenantIntegrityFindViolation(report, "INVALID_TENANT_ID", "User")
	if userViolation == nil {
		t.Fatal("missing invalid User tenant violation")
	}
	if userViolation.Count != 3 || len(userViolation.SampleIDs) != 2 {
		t.Fatalf("User violation count/samples = %d/%d, want 3/2", userViolation.Count, len(userViolation.SampleIDs))
	}
}

func TestTenantIntegrityAuditReportsMissingRequiredTable(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	if err := db.Migrator().DropTable(&models.Notification{}); err != nil {
		t.Fatalf("drop notification table: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit missing table fixture: %v", err)
	}
	violation := tenantIntegrityFindViolation(report, "MISSING_REQUIRED_TABLE", "Notification")
	if violation == nil {
		t.Fatalf("missing table was not reported: %#v", report.Violations)
	}
}

func TestTenantIntegrityAuditReportsDuplicateTenantBusinessKeys(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	if err := db.Migrator().DropIndex(&models.Company{}, "uk_company_tenant_name"); err != nil {
		t.Fatalf("drop company tenant unique index: %v", err)
	}
	audit := tenantIntegrityTestAuditFields(time.Now())
	for i := 0; i < 2; i++ {
		if err := db.Create(&models.Company{
			TenantID: fixture.tenantA.ID, Name: "duplicate tenant company", Status: enums.StatusOk, AuditFields: audit,
		}).Error; err != nil {
			t.Fatalf("create duplicate company %d: %v", i, err)
		}
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 1})
	if err != nil {
		t.Fatalf("audit duplicate business keys: %v", err)
	}
	violation := tenantIntegrityFindViolation(report, "DUPLICATE_TENANT_COMPANY_NAME", "Company.name")
	if violation == nil {
		t.Fatalf("duplicate company names were not reported: %#v", report.Violations)
	}
	if violation.Count != 2 || len(violation.SampleIDs) != 1 {
		t.Fatalf("duplicate violation count/samples = %d/%d, want 2/1", violation.Count, len(violation.SampleIDs))
	}
}

func TestTenantIntegrityAuditReportsDynamicReferenceViolations(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)
	conversationA := &models.Conversation{TenantID: fixture.tenantA.ID, CustomerName: "tenant A customer", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now, AuditFields: audit}
	conversationAOther := &models.Conversation{TenantID: fixture.tenantA.ID, CustomerName: "tenant A other customer", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now, AuditFields: audit}
	conversationB := &models.Conversation{TenantID: fixture.tenantB.ID, CustomerName: "tenant B customer", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now, AuditFields: audit}
	for label, conversation := range map[string]*models.Conversation{"tenant A": conversationA, "tenant A other": conversationAOther, "tenant B": conversationB} {
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create %s conversation: %v", label, err)
		}
	}
	messageA := &models.Message{TenantID: fixture.tenantA.ID, ConversationID: conversationA.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "tenant A evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now, AuditFields: audit}
	messageAOther := &models.Message{TenantID: fixture.tenantA.ID, ConversationID: conversationAOther.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "tenant A other evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now, AuditFields: audit}
	messageB := &models.Message{TenantID: fixture.tenantB.ID, ConversationID: conversationB.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "tenant B evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now, AuditFields: audit}
	for label, message := range map[string]*models.Message{"tenant A": messageA, "tenant A other": messageAOther, "tenant B": messageB} {
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create %s message: %v", label, err)
		}
	}
	ticketA := &models.Ticket{TenantID: fixture.tenantA.ID, TicketNo: "AUDIT-DYNAMIC-TICKET-A", Title: "tenant A ticket", Status: enums.TicketStatusPending, AuditFields: audit}
	if err := db.Create(ticketA).Error; err != nil {
		t.Fatalf("create tenant A ticket: %v", err)
	}
	for _, notification := range []*models.Notification{
		{TenantID: fixture.tenantB.ID, RecipientUserID: fixture.tenantUserB.ID, Title: "cross conversation", BizType: "conversation", BizID: conversationA.ID, Status: enums.StatusOk, CreatedAt: now},
		{TenantID: fixture.tenantB.ID, RecipientUserID: fixture.tenantUserB.ID, Title: "cross ticket", BizType: "ticket", BizID: ticketA.ID, Status: enums.StatusOk, CreatedAt: now},
		{TenantID: fixture.tenantA.ID, RecipientUserID: fixture.tenantUserA.ID, Title: "valid conversation", BizType: "conversation", BizID: conversationA.ID, Status: enums.StatusOk, CreatedAt: now},
	} {
		if err := db.Create(notification).Error; err != nil {
			t.Fatalf("create notification %q: %v", notification.Title, err)
		}
	}
	for _, candidate := range []*models.KnowledgeCandidate{
		{TenantID: fixture.tenantA.ID, ConversationID: conversationA.ID, MessageIDs: fmt.Sprint(messageB.ID), Question: "cross tenant evidence", Status: enums.KnowledgeCandidateStatusPending, AuditFields: audit},
		{TenantID: fixture.tenantA.ID, ConversationID: conversationA.ID, MessageIDs: fmt.Sprint(messageAOther.ID), Question: "cross conversation evidence", Status: enums.KnowledgeCandidateStatusPending, AuditFields: audit},
		{TenantID: fixture.tenantA.ID, ConversationID: conversationA.ID, MessageIDs: "invalid,0", Question: "invalid evidence ids", Status: enums.KnowledgeCandidateStatusPending, AuditFields: audit},
		{TenantID: fixture.tenantA.ID, ConversationID: conversationA.ID, MessageIDs: fmt.Sprint(messageA.ID), Question: "valid evidence", Status: enums.KnowledgeCandidateStatusPending, AuditFields: audit},
	} {
		if err := db.Create(candidate).Error; err != nil {
			t.Fatalf("create candidate %q: %v", candidate.Question, err)
		}
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 1})
	if err != nil {
		t.Fatalf("audit dynamic references: %v", err)
	}
	for _, entity := range []string{"Notification.conversation", "Notification.ticket"} {
		violation := tenantIntegrityFindViolation(report, "DYNAMIC_TENANT_RELATION_MISMATCH", entity)
		if violation == nil || violation.Count != 1 || len(violation.SampleIDs) != 1 {
			t.Errorf("dynamic notification violation %s = %#v", entity, violation)
		}
	}
	evidenceViolation := tenantIntegrityFindViolation(report, "KNOWLEDGE_CANDIDATE_MESSAGE_EVIDENCE_MISMATCH", "KnowledgeCandidate.message_ids")
	if evidenceViolation == nil || evidenceViolation.Count != 3 || len(evidenceViolation.SampleIDs) != 1 {
		t.Fatalf("candidate evidence violation = %#v", evidenceViolation)
	}
}

func TestTenantIntegrityAuditReportsAgentOrganizationSemanticViolations(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)

	teamA := &models.AgentTeam{TenantID: fixture.tenantA.ID, Name: "Audit Team A", Status: enums.StatusOk, AuditFields: audit}
	teamB := &models.AgentTeam{TenantID: fixture.tenantA.ID, Name: "Audit Team B", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	otherUser := &models.User{TenantID: fixture.tenantA.ID, Username: "audit-team-b-user", Password: "x", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(otherUser).Error; err != nil {
		t.Fatalf("create team B user: %v", err)
	}
	profileA := &models.AgentProfile{TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID, TeamID: teamA.ID, AgentCode: "audit-agent-a", DisplayName: "Audit Agent A", Status: enums.StatusOk, AuditFields: audit}
	profileB := &models.AgentProfile{TenantID: fixture.tenantA.ID, UserID: otherUser.ID, TeamID: teamB.ID, AgentCode: "audit-agent-b", DisplayName: "Audit Agent B", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(profileA).Error; err != nil {
		t.Fatalf("create profile A: %v", err)
	}
	if err := db.Create(profileB).Error; err != nil {
		t.Fatalf("create profile B: %v", err)
	}
	squadA := &models.AgentTeamSquad{TenantID: fixture.tenantA.ID, TeamID: teamA.ID, Name: "Audit Squad A", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(squadA).Error; err != nil {
		t.Fatalf("create squad A: %v", err)
	}
	validMember := &models.AgentTeamSquadMember{TenantID: fixture.tenantA.ID, SquadID: squadA.ID, AgentProfileID: profileA.ID, Status: enums.StatusOk, AuditFields: audit}
	mismatchMember := &models.AgentTeamSquadMember{TenantID: fixture.tenantA.ID, SquadID: squadA.ID, AgentProfileID: profileB.ID, Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validMember).Error; err != nil {
		t.Fatalf("create valid squad member: %v", err)
	}
	if err := db.Create(mismatchMember).Error; err != nil {
		t.Fatalf("create mismatched squad member: %v", err)
	}
	validSchedule := &models.AgentTeamSchedule{TenantID: fixture.tenantA.ID, TeamID: teamA.ID, SquadID: squadA.ID, StartAt: now, EndAt: now.Add(time.Hour), Status: enums.StatusOk, AuditFields: audit}
	mismatchSchedule := &models.AgentTeamSchedule{TenantID: fixture.tenantA.ID, TeamID: teamB.ID, SquadID: squadA.ID, StartAt: now.Add(time.Hour), EndAt: now.Add(2 * time.Hour), Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validSchedule).Error; err != nil {
		t.Fatalf("create valid squad schedule: %v", err)
	}
	if err := db.Create(mismatchSchedule).Error; err != nil {
		t.Fatalf("create mismatched squad schedule: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit agent organization semantics: %v", err)
	}
	memberViolation := tenantIntegrityFindViolation(report, "AGENT_TEAM_SQUAD_MEMBER_TEAM_MISMATCH", "AgentTeamSquadMember.agent_profile_id")
	if memberViolation == nil || memberViolation.Count != 1 || len(memberViolation.SampleIDs) != 1 || memberViolation.SampleIDs[0] != mismatchMember.ID {
		t.Fatalf("squad member semantic violation = %#v", memberViolation)
	}
	scheduleViolation := tenantIntegrityFindViolation(report, "AGENT_TEAM_SCHEDULE_SQUAD_TEAM_MISMATCH", "AgentTeamSchedule.squad_id")
	if scheduleViolation == nil || scheduleViolation.Count != 1 || len(scheduleViolation.SampleIDs) != 1 || scheduleViolation.SampleIDs[0] != mismatchSchedule.ID {
		t.Fatalf("squad schedule semantic violation = %#v", scheduleViolation)
	}
}

type tenantIntegrityFixture struct {
	tenantA      *models.Tenant
	tenantB      *models.Tenant
	platformUser *models.User
	tenantUserA  *models.User
	tenantUserB  *models.User
	platformRole *models.Role
	tenantRole   *models.Role
}

func createCleanTenantIntegrityFixture(t *testing.T, db *gorm.DB) tenantIntegrityFixture {
	t.Helper()
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)
	tenantA := &models.Tenant{
		TenantCode: "audit-tenant-a", LegalName: "Audit Tenant A", ShortName: "Tenant A",
		RegistrationType: "credit_code", RegistrationNo: "AUDIT000000000001",
		Status: enums.StatusOk, AuditFields: audit,
	}
	tenantB := &models.Tenant{
		TenantCode: "audit-tenant-b", LegalName: "Audit Tenant B", ShortName: "Tenant B",
		RegistrationType: "credit_code", RegistrationNo: "AUDIT000000000002",
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(tenantA).Error; err != nil {
		t.Fatalf("create tenant A: %v", err)
	}
	if err := db.Create(tenantB).Error; err != nil {
		t.Fatalf("create tenant B: %v", err)
	}
	platformRole := &models.Role{
		Name: "Audit platform role", Code: "audit_platform", Scope: constants.RoleScopePlatform,
		Status: enums.StatusOk, AuditFields: audit,
	}
	tenantRole := &models.Role{
		Name: "Audit tenant role", Code: "audit_tenant", Scope: constants.RoleScopeTenant,
		Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(platformRole).Error; err != nil {
		t.Fatalf("create platform role: %v", err)
	}
	if err := db.Create(tenantRole).Error; err != nil {
		t.Fatalf("create tenant role: %v", err)
	}
	platformUser := &models.User{Username: "audit-platform", Password: "x", Status: enums.StatusOk, AuditFields: audit}
	tenantUserA := &models.User{TenantID: tenantA.ID, Username: "audit-tenant-a", Password: "x", Status: enums.StatusOk, AuditFields: audit}
	tenantUserB := &models.User{TenantID: tenantB.ID, Username: "audit-tenant-b", Password: "x", Status: enums.StatusOk, AuditFields: audit}
	for _, user := range []*models.User{platformUser, tenantUserA, tenantUserB} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user %s: %v", user.Username, err)
		}
	}
	for _, userRole := range []*models.UserRole{
		{UserID: platformUser.ID, RoleID: platformRole.ID, AuditFields: audit},
		{UserID: tenantUserA.ID, RoleID: tenantRole.ID, AuditFields: audit},
		{UserID: tenantUserB.ID, RoleID: tenantRole.ID, AuditFields: audit},
	} {
		if err := db.Create(userRole).Error; err != nil {
			t.Fatalf("create user role: %v", err)
		}
	}
	return tenantIntegrityFixture{
		tenantA: tenantA, tenantB: tenantB, platformUser: platformUser,
		tenantUserA: tenantUserA, tenantUserB: tenantUserB, platformRole: platformRole, tenantRole: tenantRole,
	}
}

func openTenantIntegrityTestDB(t *testing.T, migrate bool) *gorm.DB {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open tenant integrity test db: %v", err)
	}
	if migrate {
		if err := db.AutoMigrate(models.Models...); err != nil {
			t.Fatalf("migrate tenant integrity test db: %v", err)
		}
	}
	return db
}

func tenantIntegrityTestAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test",
	}
}

func tenantIntegrityReportHasCode(report *TenantIntegrityAuditReport, code string) bool {
	for _, violation := range report.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

func tenantIntegrityFindViolation(report *TenantIntegrityAuditReport, code, entity string) *TenantIntegrityAuditViolation {
	for i := range report.Violations {
		if report.Violations[i].Code == code && report.Violations[i].Entity == entity {
			return &report.Violations[i]
		}
	}
	return nil
}
