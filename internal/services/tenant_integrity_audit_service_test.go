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
	tenantModelCount := 0
	for name, item := range metadata {
		_, hasPolicy := policies[name]
		if item.HasTenantID && !hasPolicy {
			t.Errorf("TenantID model %s has no audit policy", name)
		}
		if item.HasTenantID {
			tenantModelCount++
		}
		if !item.HasTenantID && hasPolicy {
			t.Errorf("non-tenant model %s has a stale audit policy", name)
		}
	}
	if len(policies) != tenantModelCount {
		t.Fatalf("policy count = %d, want one explicit policy for each of %d TenantID models", len(policies), tenantModelCount)
	}
}

func TestTenantIntegrityAuditPassesCleanTwoTenantFixture(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)
	for _, item := range []*models.UserRoleChangeLog{
		{
			TenantID: 0, UserID: fixture.platformUser.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: fmt.Sprintf("[%d]", fixture.platformRole.ID),
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: fmt.Sprintf("[%q]", fixture.platformRole.Code),
			CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: fmt.Sprintf("[%d]", fixture.tenantRole.ID),
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: fmt.Sprintf("[%q]", fixture.tenantRole.Code),
			OperatorID: fixture.platformUser.ID, OperatorName: fixture.platformUser.Username, CreatedAt: now,
		},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create valid role change log: %v", err)
		}
	}
	permission := &models.Permission{
		Name: "Audit tenant permission", Code: "audit.tenant.permission", Type: "api",
		Scope: constants.PermissionScopeTenant, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(permission).Error; err != nil {
		t.Fatalf("create valid role permission: %v", err)
	}
	if err := db.Create(&models.RolePermission{RoleID: fixture.tenantRole.ID, PermissionID: permission.ID, AuditFields: audit}).Error; err != nil {
		t.Fatalf("assign valid role permission: %v", err)
	}
	for _, item := range []*models.RolePermissionChangeLog{
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: fmt.Sprintf("[%d]", permission.ID),
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: fmt.Sprintf("[%q]", permission.Code),
			OperatorID: fixture.platformUser.ID, OperatorName: fixture.platformUser.Username, CreatedAt: now,
		},
		{
			RoleID: 900001, RoleCode: "deleted_role_snapshot",
			BeforePermissionIDsJSON: "[900002]", AfterPermissionIDsJSON: "[]",
			BeforePermissionCodesJSON: "[\"deleted.permission.snapshot\"]", AfterPermissionCodesJSON: "[]",
			CreatedAt: now,
		},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create valid permission change log: %v", err)
		}
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit clean fixture: %v", err)
	}
	if report.Status != "passed" || report.HasViolations() {
		t.Fatalf("clean fixture failed audit: %#v", report.Violations)
	}
	expectedTenantModels := len(tenantIntegrityTablePolicies())
	if report.RegisteredTenantModels != expectedTenantModels || report.PolicyCount != expectedTenantModels {
		t.Fatalf("tenant model coverage = %d/%d, want %d/%d", report.RegisteredTenantModels, report.PolicyCount, expectedTenantModels, expectedTenantModels)
	}
	if report.RequiredTables != 99 || report.ConfiguredRelations != 251 {
		t.Fatalf("audit schema coverage = %d tables/%d relations, want 99/251", report.RequiredTables, report.ConfiguredRelations)
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
	invalidRoleChange := &models.UserRoleChangeLog{
		TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserB.ID,
		BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: fmt.Sprintf("[%d]", fixture.tenantRole.ID),
		BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: fmt.Sprintf("[%q]", fixture.tenantRole.Code),
		OperatorID: 987654, OperatorName: "missing-operator", CreatedAt: now,
	}
	if err := db.Create(invalidRoleChange).Error; err != nil {
		t.Fatalf("create invalid role change log: %v", err)
	}
	invalidPermissionChange := &models.RolePermissionChangeLog{
		RoleID: fixture.platformRole.ID, RoleCode: fixture.platformRole.Code,
		BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: "[]",
		BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: "[]",
		OperatorID: 987655, OperatorName: "missing-permission-operator", CreatedAt: now,
	}
	if err := db.Create(invalidPermissionChange).Error; err != nil {
		t.Fatalf("create invalid permission change log: %v", err)
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
	if violation := tenantIntegrityFindViolation(report, "TENANT_RELATION_MISMATCH", "UserRoleChangeLog.user_id"); violation == nil || violation.Count != 1 {
		t.Fatalf("role change target tenant violation = %#v", violation)
	}
	if violation := tenantIntegrityFindViolation(report, "ORPHAN_PARENT_REFERENCE", "UserRoleChangeLog.operator_id"); violation == nil || violation.Count != 1 {
		t.Fatalf("role change operator violation = %#v", violation)
	}
	if violation := tenantIntegrityFindViolation(report, "ORPHAN_PARENT_REFERENCE", "RolePermissionChangeLog.operator_id"); violation == nil || violation.Count != 1 {
		t.Fatalf("permission change operator violation = %#v", violation)
	}
}

func TestTenantIntegrityAuditReportsInvalidUserRoleChangePayloads(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	validAfterIDs := fmt.Sprintf("[%d]", fixture.tenantRole.ID)
	validAfterCodes := fmt.Sprintf("[%q]", fixture.tenantRole.Code)
	reversedIDs := fmt.Sprintf("[%d,%d]", fixture.tenantRole.ID, fixture.platformRole.ID)
	rows := []*models.UserRoleChangeLog{
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: validAfterIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: validAfterCodes,
			OperatorID: fixture.platformUser.ID, OperatorName: fixture.platformUser.Username, CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "not-json", AfterRoleIDsJSON: validAfterIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: validAfterCodes, CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: reversedIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: fmt.Sprintf("[%q,%q]", fixture.platformRole.Code, fixture.tenantRole.Code), CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: fmt.Sprintf("[%d,%d]", fixture.tenantRole.ID, fixture.tenantRole.ID),
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: fmt.Sprintf("[%q,%q]", fixture.tenantRole.Code, fixture.tenantRole.Code), CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: validAfterIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: "[]", CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: validAfterIDs, AfterRoleIDsJSON: validAfterIDs,
			BeforeRoleCodesJSON: validAfterCodes, AfterRoleCodesJSON: validAfterCodes, CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: validAfterIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: "[\" role_with_spaces \" ]", CreatedAt: now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create role change payload fixtures: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 2})
	if err != nil {
		t.Fatalf("audit role change payloads: %v", err)
	}
	violation := tenantIntegrityFindViolation(report, "USER_ROLE_CHANGE_LOG_PAYLOAD_INVALID", "UserRoleChangeLog.role_snapshots")
	if violation == nil || violation.Count != 6 || len(violation.SampleIDs) != 2 ||
		violation.SampleIDs[0] != rows[1].ID || violation.SampleIDs[1] != rows[2].ID {
		t.Fatalf("role change payload violation = %#v", violation)
	}
}

func TestTenantIntegrityAuditReportsBrokenUserRoleChangeChain(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	platformIDs := fmt.Sprintf("[%d]", fixture.platformRole.ID)
	platformCodes := fmt.Sprintf("[%q]", fixture.platformRole.Code)
	tenantIDs := fmt.Sprintf("[%d]", fixture.tenantRole.ID)
	tenantCodes := fmt.Sprintf("[%q]", fixture.tenantRole.Code)
	rows := []*models.UserRoleChangeLog{
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: platformIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: platformCodes, CreatedAt: now,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID,
			BeforeRoleIDsJSON: platformIDs, AfterRoleIDsJSON: tenantIDs,
			BeforeRoleCodesJSON: platformCodes, AfterRoleCodesJSON: tenantCodes, CreatedAt: now.Add(time.Second),
		},
		{
			TenantID: fixture.tenantB.ID, UserID: fixture.tenantUserB.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: platformIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: platformCodes, CreatedAt: now,
		},
		{
			TenantID: fixture.tenantB.ID, UserID: fixture.tenantUserB.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: tenantIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: tenantCodes, CreatedAt: now.Add(time.Second),
		},
		{
			TenantID: 0, UserID: fixture.platformUser.ID,
			BeforeRoleIDsJSON: "[]", AfterRoleIDsJSON: tenantIDs,
			BeforeRoleCodesJSON: "[]", AfterRoleCodesJSON: tenantCodes, CreatedAt: now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create role change chain fixtures: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit role change chain: %v", err)
	}
	if violation := tenantIntegrityFindViolation(report, "USER_ROLE_CHANGE_LOG_PAYLOAD_INVALID", "UserRoleChangeLog.role_snapshots"); violation != nil {
		t.Fatalf("valid individual payloads were rejected: %#v", violation)
	}
	violation := tenantIntegrityFindViolation(report, "USER_ROLE_CHANGE_LOG_CHAIN_BROKEN", "UserRoleChangeLog.role_snapshots")
	if violation == nil || violation.Count != 2 || len(violation.SampleIDs) != 2 ||
		violation.SampleIDs[0] != rows[3].ID || violation.SampleIDs[1] != rows[4].ID {
		t.Fatalf("role change chain violation = %#v", violation)
	}
}

func TestTenantIntegrityAuditReportsInvalidRolePermissionChangePayloads(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	validAfterIDs := "[11]"
	validAfterCodes := "[\"tenant.permission.view\"]"
	rows := []*models.RolePermissionChangeLog{
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: validAfterCodes, CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "not-json", AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: validAfterCodes, CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: "[22,11]",
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: "[\"tenant.permission.edit\",\"tenant.permission.view\"]", CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: "[11,11]",
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: "[\"tenant.permission.view\",\"tenant.permission.view\"]", CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: "[]", CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: validAfterIDs, AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: validAfterCodes, AfterPermissionCodesJSON: validAfterCodes, CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: "[\" tenant.permission.view \" ]", CreatedAt: now,
		},
		{
			RoleID: 0, RoleCode: "invalid_role_id",
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: validAfterIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: validAfterCodes, CreatedAt: now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create permission change payload fixtures: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 2})
	if err != nil {
		t.Fatalf("audit permission change payloads: %v", err)
	}
	violation := tenantIntegrityFindViolation(report, "ROLE_PERMISSION_CHANGE_LOG_PAYLOAD_INVALID", "RolePermissionChangeLog.permission_snapshots")
	if violation == nil || violation.Count != 7 || len(violation.SampleIDs) != 2 ||
		violation.SampleIDs[0] != rows[1].ID || violation.SampleIDs[1] != rows[2].ID {
		t.Fatalf("permission change payload violation = %#v", violation)
	}
}

func TestTenantIntegrityAuditReportsBrokenRolePermissionChangeChain(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)
	permissionA := &models.Permission{
		Name: "Permission A", Code: "tenant.permission.a", Type: "api",
		Scope: constants.PermissionScopeTenant, Status: enums.StatusOk, AuditFields: audit,
	}
	permissionB := &models.Permission{
		Name: "Permission B", Code: "tenant.permission.b", Type: "api",
		Scope: constants.PermissionScopeTenant, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(permissionA).Error; err != nil {
		t.Fatalf("create permission A: %v", err)
	}
	if err := db.Create(permissionB).Error; err != nil {
		t.Fatalf("create permission B: %v", err)
	}
	if err := db.Create(&models.RolePermission{RoleID: fixture.tenantRole.ID, PermissionID: permissionB.ID, AuditFields: audit}).Error; err != nil {
		t.Fatalf("assign current tenant role permission: %v", err)
	}
	permissionAIDs := fmt.Sprintf("[%d]", permissionA.ID)
	permissionACodes := fmt.Sprintf("[%q]", permissionA.Code)
	permissionBIDs := fmt.Sprintf("[%d]", permissionB.ID)
	permissionBCodes := fmt.Sprintf("[%q]", permissionB.Code)
	rows := []*models.RolePermissionChangeLog{
		{
			RoleID: fixture.platformRole.ID, RoleCode: fixture.platformRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: permissionAIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: permissionACodes, CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: permissionAIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: permissionACodes, CreatedAt: now,
		},
		{
			RoleID: fixture.tenantRole.ID, RoleCode: fixture.tenantRole.Code,
			BeforePermissionIDsJSON: "[]", AfterPermissionIDsJSON: permissionBIDs,
			BeforePermissionCodesJSON: "[]", AfterPermissionCodesJSON: permissionBCodes, CreatedAt: now.Add(time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create permission change chain fixtures: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit permission change chain: %v", err)
	}
	if violation := tenantIntegrityFindViolation(report, "ROLE_PERMISSION_CHANGE_LOG_PAYLOAD_INVALID", "RolePermissionChangeLog.permission_snapshots"); violation != nil {
		t.Fatalf("valid individual payloads were rejected: %#v", violation)
	}
	violation := tenantIntegrityFindViolation(report, "ROLE_PERMISSION_CHANGE_LOG_CHAIN_BROKEN", "RolePermissionChangeLog.permission_snapshots")
	if violation == nil || violation.Count != 2 || len(violation.SampleIDs) != 2 ||
		violation.SampleIDs[0] != rows[0].ID || violation.SampleIDs[1] != rows[2].ID {
		t.Fatalf("permission change chain violation = %#v", violation)
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

func TestTenantIntegrityAuditReportsDutyRoleAndSquadLeaderViolations(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)

	roles := map[string]*models.Role{}
	for _, role := range []*models.Role{
		{Name: "Audit CS User", Code: constants.RoleCodeCsUser, Scope: constants.RoleScopeTenant, Status: enums.StatusOk, AuditFields: audit},
		{Name: "Audit Team Leader", Code: constants.RoleCodeCsTeamLeader, Scope: constants.RoleScopeTenant, Status: enums.StatusOk, AuditFields: audit},
		{Name: "Audit Store Staff", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk, AuditFields: audit},
	} {
		if err := db.Create(role).Error; err != nil {
			t.Fatalf("create duty role %s: %v", role.Code, err)
		}
		roles[role.Code] = role
	}
	for _, code := range []string{constants.RoleCodeCsUser, constants.RoleCodeCsTeamLeader, constants.RoleCodeStoreStaff} {
		if err := db.Create(&models.UserRole{UserID: fixture.tenantUserA.ID, RoleID: roles[code].ID, AuditFields: audit}).Error; err != nil {
			t.Fatalf("assign valid duty role %s: %v", code, err)
		}
	}
	missingRoleUser := &models.User{TenantID: fixture.tenantA.ID, Username: "audit-missing-duty-roles", Password: "x", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(missingRoleUser).Error; err != nil {
		t.Fatalf("create missing role user: %v", err)
	}
	validTeam := &models.AgentTeam{TenantID: fixture.tenantA.ID, Name: "Valid Duty Team", LeaderUserID: fixture.tenantUserA.ID, Status: enums.StatusOk, AuditFields: audit}
	invalidTeam := &models.AgentTeam{TenantID: fixture.tenantA.ID, Name: "Invalid Duty Team", LeaderUserID: missingRoleUser.ID, Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validTeam).Error; err != nil {
		t.Fatalf("create valid duty team: %v", err)
	}
	if err := db.Create(invalidTeam).Error; err != nil {
		t.Fatalf("create invalid duty team: %v", err)
	}
	validProfile := &models.AgentProfile{TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID, TeamID: validTeam.ID, AgentCode: "valid-duty-agent", DisplayName: "Valid Duty Agent", Status: enums.StatusOk, AuditFields: audit}
	invalidProfile := &models.AgentProfile{TenantID: fixture.tenantA.ID, UserID: missingRoleUser.ID, TeamID: invalidTeam.ID, AgentCode: "invalid-duty-agent", DisplayName: "Invalid Duty Agent", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validProfile).Error; err != nil {
		t.Fatalf("create valid duty profile: %v", err)
	}
	if err := db.Create(invalidProfile).Error; err != nil {
		t.Fatalf("create invalid duty profile: %v", err)
	}
	validSquad := &models.AgentTeamSquad{TenantID: fixture.tenantA.ID, TeamID: validTeam.ID, Name: "Valid Leader Squad", LeaderUserID: fixture.tenantUserA.ID, Status: enums.StatusOk, AuditFields: audit}
	invalidSquad := &models.AgentTeamSquad{TenantID: fixture.tenantA.ID, TeamID: validTeam.ID, Name: "Invalid Leader Squad", LeaderUserID: missingRoleUser.ID, Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validSquad).Error; err != nil {
		t.Fatalf("create valid leader squad: %v", err)
	}
	if err := db.Create(invalidSquad).Error; err != nil {
		t.Fatalf("create invalid leader squad: %v", err)
	}
	validStore := &models.Store{TenantID: fixture.tenantA.ID, StoreCode: "valid-duty-store", Name: "Valid Duty Store", Status: enums.StatusOk, AuditFields: audit}
	invalidStore := &models.Store{TenantID: fixture.tenantA.ID, StoreCode: "invalid-duty-store", Name: "Invalid Duty Store", Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validStore).Error; err != nil {
		t.Fatalf("create valid duty store: %v", err)
	}
	if err := db.Create(invalidStore).Error; err != nil {
		t.Fatalf("create invalid duty store: %v", err)
	}
	validBinding := &models.StoreStaffBinding{TenantID: fixture.tenantA.ID, UserID: fixture.tenantUserA.ID, ActiveUserID: positiveInt64Pointer(fixture.tenantUserA.ID), StoreID: validStore.ID, AgentTeamID: validTeam.ID, Status: enums.StatusOk, AuditFields: audit}
	invalidBinding := &models.StoreStaffBinding{TenantID: fixture.tenantA.ID, UserID: missingRoleUser.ID, ActiveUserID: positiveInt64Pointer(missingRoleUser.ID), StoreID: invalidStore.ID, AgentTeamID: invalidTeam.ID, Status: enums.StatusOk, AuditFields: audit}
	if err := db.Create(validBinding).Error; err != nil {
		t.Fatalf("create valid store staff binding: %v", err)
	}
	if err := db.Create(invalidBinding).Error; err != nil {
		t.Fatalf("create invalid store staff binding: %v", err)
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit duty role semantics: %v", err)
	}
	checks := []struct {
		code     string
		entity   string
		sampleID int64
	}{
		{code: "AGENT_PROFILE_MISSING_CS_USER_ROLE", entity: "AgentProfile.user_id", sampleID: invalidProfile.ID},
		{code: "AGENT_TEAM_LEADER_MISSING_ROLE", entity: "AgentTeam.leader_user_id", sampleID: invalidTeam.ID},
		{code: "STORE_STAFF_BINDING_MISSING_ROLE", entity: "StoreStaffBinding.user_id", sampleID: invalidBinding.ID},
		{code: "AGENT_TEAM_SQUAD_LEADER_PROFILE_MISMATCH", entity: "AgentTeamSquad.leader_user_id", sampleID: invalidSquad.ID},
	}
	for _, check := range checks {
		violation := tenantIntegrityFindViolation(report, check.code, check.entity)
		if violation == nil || violation.Count != 1 || len(violation.SampleIDs) != 1 || violation.SampleIDs[0] != check.sampleID {
			t.Errorf("duty semantic violation %s = %#v", check.code, violation)
		}
	}
}

func TestTenantIntegrityAuditReportsStoreStaffOwnershipViolations(t *testing.T) {
	db := openTenantIntegrityTestDB(t, true)
	fixture := createCleanTenantIntegrityFixture(t, db)
	now := time.Now()
	audit := tenantIntegrityTestAuditFields(now)

	storeStaffRole := &models.Role{
		Name: "Audit Store Staff Ownership", Code: constants.RoleCodeStoreStaff,
		Scope: constants.RoleScopeTenant, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(storeStaffRole).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}

	users := make([]models.User, 3)
	stores := make([]models.Store, 4)
	for index := range users {
		users[index] = models.User{
			TenantID: fixture.tenantA.ID, Username: fmt.Sprintf("audit-store-owner-%d", index+1),
			Password: "x", Status: enums.StatusOk, AuditFields: audit,
		}
		if err := db.Create(&users[index]).Error; err != nil {
			t.Fatalf("create store owner %d: %v", index+1, err)
		}
		if err := db.Create(&models.UserRole{UserID: users[index].ID, RoleID: storeStaffRole.ID, AuditFields: audit}).Error; err != nil {
			t.Fatalf("assign store staff role %d: %v", index+1, err)
		}
	}
	for index := range stores {
		stores[index] = models.Store{
			TenantID: fixture.tenantA.ID, StoreCode: fmt.Sprintf("audit-store-ownership-%d", index+1),
			Name: fmt.Sprintf("Audit Store Ownership %d", index+1), Status: enums.StatusOk, AuditFields: audit,
		}
		if err := db.Create(&stores[index]).Error; err != nil {
			t.Fatalf("create ownership store %d: %v", index+1, err)
		}
	}

	activeMismatch := &models.StoreStaffBinding{
		TenantID: fixture.tenantA.ID, UserID: users[0].ID, StoreID: stores[0].ID,
		Status: enums.StatusOk, AuditFields: audit,
	}
	inactiveOccupied := &models.StoreStaffBinding{
		TenantID: fixture.tenantA.ID, UserID: users[1].ID, ActiveUserID: positiveInt64Pointer(users[1].ID),
		StoreID: stores[1].ID, Status: enums.StatusDisabled, AuditFields: audit,
	}
	duplicateBindings := []*models.StoreStaffBinding{
		{
			TenantID: fixture.tenantA.ID, UserID: users[2].ID, ActiveUserID: positiveInt64Pointer(users[2].ID),
			StoreID: stores[2].ID, Status: enums.StatusOk, AuditFields: audit,
		},
		{
			TenantID: fixture.tenantA.ID, UserID: users[2].ID, StoreID: stores[3].ID,
			Status: enums.StatusDisabled, AuditFields: audit,
		},
	}
	for _, binding := range append([]*models.StoreStaffBinding{activeMismatch, inactiveOccupied}, duplicateBindings...) {
		if err := db.Create(binding).Error; err != nil {
			t.Fatalf("create ownership violation binding: %v", err)
		}
	}

	report, err := TenantIntegrityAuditService.Audit(db, TenantIntegrityAuditOptions{SampleLimit: 5})
	if err != nil {
		t.Fatalf("audit store staff ownership semantics: %v", err)
	}
	if violation := tenantIntegrityFindViolation(report, "STORE_STAFF_ACTIVE_OWNER_MISMATCH", "StoreStaffBinding.active_user_id"); violation == nil ||
		violation.Count != 1 || len(violation.SampleIDs) != 1 || violation.SampleIDs[0] != activeMismatch.ID {
		t.Fatalf("active owner mismatch violation = %#v", violation)
	}
	if violation := tenantIntegrityFindViolation(report, "STORE_STAFF_INACTIVE_OWNER_OCCUPIED", "StoreStaffBinding.active_user_id"); violation == nil ||
		violation.Count != 1 || len(violation.SampleIDs) != 1 || violation.SampleIDs[0] != inactiveOccupied.ID {
		t.Fatalf("inactive owner occupied violation = %#v", violation)
	}
	violation := tenantIntegrityFindViolation(report, "STORE_STAFF_ACCOUNT_MULTIPLE_BINDINGS", "StoreStaffBinding.user_id")
	if violation == nil || violation.Count != 2 || len(violation.SampleIDs) != 2 {
		t.Fatalf("multiple bindings violation = %#v", violation)
	}
	if violation.SampleIDs[0] != duplicateBindings[0].ID || violation.SampleIDs[1] != duplicateBindings[1].ID {
		t.Fatalf("multiple bindings samples = %#v, want [%d %d]", violation.SampleIDs, duplicateBindings[0].ID, duplicateBindings[1].ID)
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
	profile := &models.ReplyIntentProfile{
		Code: "audit-industry", Name: "Audit Industry", IndustryCode: "audit",
		IntentDetectPrompt: "audit prompt", IntentJSONSchema: "{}", Revision: 1,
		PublishedAt: &now, Status: enums.StatusOk, AuditFields: audit,
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create audit industry profile: %v", err)
	}
	tenantA := &models.Tenant{
		IntentProfileID: profile.ID,
		TenantCode:      "audit-tenant-a", LegalName: "Audit Tenant A", ShortName: "Tenant A",
		RegistrationType: "credit_code", RegistrationNo: "AUDIT000000000001",
		Status: enums.StatusOk, AuditFields: audit,
	}
	tenantB := &models.Tenant{
		IntentProfileID: profile.ID,
		TenantCode:      "audit-tenant-b", LegalName: "Audit Tenant B", ShortName: "Tenant B",
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
