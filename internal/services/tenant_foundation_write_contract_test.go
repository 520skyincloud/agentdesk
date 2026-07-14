package services

import (
	"go/ast"
	"regexp"
	"testing"
)

var (
	tenantTablePattern             = regexp.MustCompile(`\bt_tenant\b`)
	tenantInvitationTablePattern   = regexp.MustCompile(`\bt_tenant_invitation\b`)
	tenantRegistrationTablePattern = regexp.MustCompile(`\bt_tenant_registration_log\b`)
)

func TestTenantRuntimeWritesStayBehindManagementService(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"tenant_management_service.go": {
			"CreateTenant":       {},
			"UpdateTenant":       {},
			"UpdateTenantStatus": {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "Tenant", isTenantMutationCall, allowed)
}

func TestTenantInvitationRuntimeWritesStayBehindLifecycleServices(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"tenant_management_service.go": {
			"CreateTenant": {},
		},
		"tenant_invitation_business_service.go": {
			"Rotate": {},
		},
		"tenant_registration_business_service.go": {
			"Register": {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "TenantInvitation", isTenantInvitationMutationCall, allowed)
}

func TestTenantRegistrationLogsRemainAppendOnly(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"tenant_registration_business_service.go": {
			"createSecurityLog": {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "TenantRegistrationLog", isTenantRegistrationLogMutationCall, allowed)
}

func TestIsTenantFoundationMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "tenant repository update", expression: "repositories.TenantRepository.Updates(db, id, values)", want: true},
		{name: "tenant service delete", expression: "TenantService.Delete(id)", want: true},
		{name: "tenant gorm create", expression: "db.Create(&models.Tenant{})", want: true},
		{name: "tenant raw SQL", expression: "db.Exec(\"UPDATE t_tenant SET status = ?\", status)", want: true},
		{name: "tenant repository read", expression: "repositories.TenantRepository.Get(db, id)", want: false},
		{name: "invitation write", expression: "db.Create(&models.TenantInvitation{})", want: false},
		{name: "invitation SQL", expression: "db.Exec(\"UPDATE t_tenant_invitation SET status = ?\", status)", want: false},
	}
	assertMutationDetectorCases(t, tests, isTenantMutationCall)
}

func TestIsTenantInvitationMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.TenantInvitationRepository.Create(db, item)", want: true},
		{name: "repository disable active", expression: "repositories.TenantInvitationRepository.DisableActiveByTenant(db, tenantID, values)", want: true},
		{name: "repository mark used", expression: "repositories.TenantInvitationRepository.MarkUsed(db, id, now)", want: true},
		{name: "service update", expression: "TenantInvitationService.Update(item)", want: true},
		{name: "gorm update", expression: "db.Model(&models.TenantInvitation{}).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"DELETE FROM t_tenant_invitation WHERE id = ?\", id)", want: true},
		{name: "repository read", expression: "repositories.TenantInvitationRepository.FindCurrent(db, tenantID)", want: false},
		{name: "tenant write", expression: "db.Create(&models.Tenant{})", want: false},
	}
	assertMutationDetectorCases(t, tests, isTenantInvitationMutationCall)
}

func TestIsTenantRegistrationLogMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.TenantRegistrationLogRepository.Create(db, item)", want: true},
		{name: "service create", expression: "TenantRegistrationLogService.Create(item)", want: true},
		{name: "gorm delete", expression: "db.Delete(&models.TenantRegistrationLog{}, id)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"UPDATE t_tenant_registration_log SET reason = ?\", reason)", want: true},
		{name: "repository read", expression: "repositories.TenantRegistrationLogRepository.GetByRequestID(db, requestID)", want: false},
		{name: "invitation write", expression: "db.Create(&models.TenantInvitation{})", want: false},
	}
	assertMutationDetectorCases(t, tests, isTenantRegistrationLogMutationCall)
}

func isTenantMutationCall(call *ast.CallExpr) bool {
	return isDefinitionMutationCall(call, "TenantRepository", "TenantService", "Tenant", tenantTablePattern)
}

func isTenantInvitationMutationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if receiver, receiverOK := selector.X.(*ast.SelectorExpr); receiverOK && receiver.Sel.Name == "TenantInvitationRepository" {
			if selector.Sel.Name == "DisableActiveByTenant" || selector.Sel.Name == "MarkUsed" {
				return true
			}
		}
	}
	return isDefinitionMutationCall(call, "TenantInvitationRepository", "TenantInvitationService", "TenantInvitation", tenantInvitationTablePattern)
}

func isTenantRegistrationLogMutationCall(call *ast.CallExpr) bool {
	return isDefinitionMutationCall(call, "TenantRegistrationLogRepository", "TenantRegistrationLogService", "TenantRegistrationLog", tenantRegistrationTablePattern)
}
