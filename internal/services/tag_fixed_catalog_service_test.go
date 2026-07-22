package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestTagServiceOnlyAllowsAliasAndLeafStatusForCurrentIndustryCatalog(t *testing.T) {
	db, platformOperator := setupTenantManagementTestDB(t)
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("fixed-tags", "91350100MA8C1D2E3F"), platformOperator)
	if err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{
		UserID: 9101, Username: "tenant-tag-admin", ActiveTenantID: created.Tenant.ID,
		Roles: []string{constants.RoleCodeTenantAdmin},
	}
	leaf := firstTenantIndustryLeafTag(t, db, created.Tenant.ID, created.Tenant.IntentProfileID)
	original := *leaf
	if err := TagService.UpdateTag(request.UpdateTagRequest{
		ID: leaf.ID,
		CreateTagRequest: request.CreateTagRequest{
			ParentID: 0, Name: "试图修改标准名", DisplayAlias: "住店偏好", Remark: "试图修改语义",
		},
	}, operator); err != nil {
		t.Fatalf("update display alias: %v", err)
	}
	updated := repositories.TagRepository.GetInTenant(db, leaf.ID, created.Tenant.ID)
	if updated == nil || updated.DisplayAlias != "住店偏好" {
		t.Fatalf("updated fixed tag=%#v", updated)
	}
	if updated.Name != original.Name || updated.ParentID != original.ParentID || updated.SemanticKey != original.SemanticKey ||
		updated.ConflictGroup != original.ConflictGroup || updated.AIEnabled != original.AIEnabled || updated.ReplyEnabled != original.ReplyEnabled || updated.Remark != original.Remark {
		t.Fatalf("fixed catalog fields changed: before=%#v after=%#v", original, updated)
	}

	if err := TagService.UpdateStatus(leaf.ID, int(enums.StatusDisabled), operator); err != nil {
		t.Fatalf("disable leaf tag: %v", err)
	}
	if current := repositories.TagRepository.GetInTenant(db, leaf.ID, created.Tenant.ID); current == nil || current.Status != enums.StatusDisabled {
		t.Fatalf("disabled leaf=%#v", current)
	}
	if err := TagService.UpdateStatus(leaf.ID, int(enums.StatusOk), operator); err != nil {
		t.Fatalf("re-enable leaf tag: %v", err)
	}
	parent := repositories.TagRepository.GetInTenant(db, leaf.ParentID, created.Tenant.ID)
	if parent == nil {
		t.Fatal("fixed catalog parent tag missing")
	}
	if err := TagService.UpdateStatus(parent.ID, int(enums.StatusDisabled), operator); err == nil {
		t.Fatal("industry tag category must not be disabled")
	}
	if err := TagService.UpdateTag(request.UpdateTagRequest{
		ID: parent.ID, CreateTagRequest: request.CreateTagRequest{DisplayAlias: "越权分类别名"},
	}, operator); err == nil {
		t.Fatal("industry tag category must not accept a display alias")
	}

	if _, err := TagService.CreateTag(request.CreateTagRequest{Name: "租户自建标签"}, operator); err == nil {
		t.Fatal("tenant must not create custom tags")
	}
	if err := TagService.DeleteTag(leaf.ID, operator); err == nil {
		t.Fatal("tenant must not delete a fixed industry tag")
	}
	if err := TagService.UpdateSort([]int64{leaf.ID}, operator); err == nil {
		t.Fatal("tenant must not reorder the fixed industry catalog")
	}
}

func TestTagServiceCatalogReadsExcludeLegacyAndOtherIndustryTags(t *testing.T) {
	db, platformOperator := setupTenantManagementTestDB(t)
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("tag-scope", "91350100MA8F1A2B3C"), platformOperator)
	if err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{UserID: 9102, ActiveTenantID: created.Tenant.ID, Roles: []string{constants.RoleCodeTenantAdmin}}
	fixed := firstTenantIndustryLeafTag(t, db, created.Tenant.ID, created.Tenant.IntentProfileID)
	templateID := int64(99001)
	otherIndustry := &models.Tag{
		TenantID: created.Tenant.ID, IntentProfileID: created.Tenant.IntentProfileID + 999, TemplateDefinitionID: &templateID,
		ParentID: fixed.ParentID, Name: "其他行业标签", SemanticKey: "other.industry", SystemDefined: true, Status: enums.StatusOk,
	}
	legacyCustom := &models.Tag{
		TenantID: created.Tenant.ID, IntentProfileID: created.Tenant.IntentProfileID,
		ParentID: fixed.ParentID, Name: "历史自定义标签", SemanticKey: "legacy.custom", SystemDefined: true, Status: enums.StatusOk,
	}
	if err := db.Create(otherIndustry).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(legacyCustom).Error; err != nil {
		t.Fatal(err)
	}

	list, err := TagService.FindAllForOperator(operator)
	if err != nil {
		t.Fatal(err)
	}
	for i := range list {
		if list[i].ID == otherIndustry.ID || list[i].ID == legacyCustom.ID || list[i].IntentProfileID != created.Tenant.IntentProfileID || list[i].TemplateDefinitionID == nil {
			t.Fatalf("catalog read returned non-authoritative tag: %#v", list[i])
		}
	}
	if len(list) != 2 {
		t.Fatalf("fixed catalog size=%d, want category plus leaf", len(list))
	}

	otherTenant := &dto.AuthPrincipal{UserID: 9103, ActiveTenantID: created.Tenant.ID + 999, Roles: []string{constants.RoleCodeTenantAdmin}}
	if err := TagService.UpdateTag(request.UpdateTagRequest{ID: fixed.ID, CreateTagRequest: request.CreateTagRequest{DisplayAlias: "越权"}}, otherTenant); err == nil {
		t.Fatal("another tenant scope updated a fixed tag")
	}
}
