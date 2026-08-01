package dashboard

import (
	"strings"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func ModelProfileTemplatePostGet(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.GetModelProfileCatalogRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.ModelProfileService.GetCatalog(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildModelProfileCatalogResponse(data))
}

func ModelProfileTemplatePostCreate(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ModelProfileService.Create(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildModelProfileTemplate(&item.Template, item.Slots))
}

func ModelProfileTemplatePostUpdate(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ModelProfileService.Update(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildModelProfileTemplate(&item.Template, item.Slots))
}

func ModelProfileTemplatePostTest(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.TestModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.ModelProfileService.Test(
		ctx.Request.Context(),
		req,
		operator,
		storeCredentialRequestMeta(ctx),
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	issues := make([]response.ModelProfileValidationIssueResponse, 0, len(data.Issues))
	for _, issue := range data.Issues {
		issues = append(issues, response.ModelProfileValidationIssueResponse{UsageCode: string(issue.UsageCode), Message: issue.Message})
	}
	status := "passed"
	if len(issues) > 0 || data.TestRun == nil || data.TestRun.Status != "passed" {
		status = "failed"
	}
	httpx.WriteJSON(ctx, response.ModelProfileValidationResponse{
		TemplateID: data.Template.ID, Revision: data.Template.Revision,
		ConfigDigest: data.ConfigDigest, Status: status, Issues: issues,
		TestRun: builders.BuildModelProfileTestRun(data.TestRun),
	})
}

func ModelProfileTemplatePostPublish(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ModelProfileRevisionActionRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ModelProfileService.Publish(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildModelProfileTemplate(&item.Template, item.Slots))
}

func StoreModelProfilePostGet(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.GetStoreModelProfileAssignmentsRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelProfileAssignmentService.List(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildStoreModelProfileAssignmentsResponse(data))
}

func StoreModelProfilePostAssign(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.AssignStoreModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.StoreModelProfileAssignmentService.Assign(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func StoreModelProfilePostBatchAssign(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BatchAssignStoreModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.StoreModelProfileAssignmentService.BatchAssign(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func StoreModelProfilePostActivatePending(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ActivatePendingStoreModelProfileRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.ActivatePendingProfile(
		ctx.Request.Context(),
		req,
		operator,
		storeCredentialRequestMeta(ctx),
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func buildModelProfileCatalogResponse(data *services.ModelProfileCatalogData) response.ModelProfileCatalogResponse {
	result := response.ModelProfileCatalogResponse{
		Profiles:      make([]response.ModelProfileTemplateResponse, 0, len(data.Profiles)),
		RequiredSlots: make([]response.ModelUsageSlotOptionResponse, 0, len(services.RequiredModelUsageSlotSpecs())),
		TestTargets:   make([]response.ModelProfileTestTargetResponse, 0, len(data.TestTargets)),
		TestRequired:  data.TestRequired,
	}
	for i := range data.Profiles {
		result.Profiles = append(result.Profiles, builders.BuildModelProfileTemplateWithTest(
			&data.Profiles[i].Template,
			data.Profiles[i].Slots,
			data.Profiles[i].ConfigDigest,
			data.Profiles[i].LatestTest,
		))
	}
	for _, spec := range services.RequiredModelUsageSlotSpecs() {
		result.RequiredSlots = append(result.RequiredSlots, response.ModelUsageSlotOptionResponse{
			UsageCode: string(spec.UsageCode), DisplayName: spec.DisplayName, ExpectedModelType: string(spec.ExpectedModelType),
		})
	}
	for i := range data.TestTargets {
		target := &data.TestTargets[i]
		tenantName := strings.TrimSpace(target.Tenant.ShortName)
		if tenantName == "" {
			tenantName = strings.TrimSpace(target.Tenant.LegalName)
		}
		result.TestTargets = append(result.TestTargets, response.ModelProfileTestTargetResponse{
			TenantID:   target.Tenant.ID,
			TenantName: tenantName,
			StoreID:    target.Store.ID, StoreCode: target.Store.StoreCode, StoreName: target.Store.Name,
			StoreStaffBindingID: target.StoreStaffBindingID, StoreStaffAccountName: target.StoreStaffAccountName,
			CredentialRevision: target.CredentialRevision,
			ActiveTemplateID:   target.ActiveTemplateID, ActiveTemplateName: target.ActiveTemplateName,
			ActiveTemplateRevision: target.ActiveTemplateRevision,
		})
	}
	return result
}

func buildStoreModelProfileAssignmentsResponse(data *services.StoreModelProfileAssignmentsData) response.StoreModelProfileAssignmentsResponse {
	result := response.StoreModelProfileAssignmentsResponse{
		TenantID: data.TenantID,
		Profiles: make([]response.StoreModelProfileOptionResponse, 0, len(data.Profiles)),
		Stores:   make([]response.StoreModelProfileAssignmentResponse, 0, len(data.Stores)),
	}
	for i := range data.Profiles {
		result.Profiles = append(result.Profiles, builders.BuildStoreModelProfileOption(&data.Profiles[i].Template, data.Profiles[i].Slots))
	}
	for i := range data.Stores {
		item := builders.BuildStoreModelProfileAssignment(
			&data.Stores[i].Store, data.Stores[i].Assignment, data.Templates,
		)
		item.CredentialBindings = make([]response.StoreModelCredentialBindingResponse, 0, len(data.Stores[i].CredentialBindings))
		for _, binding := range data.Stores[i].CredentialBindings {
			item.CredentialBindings = append(item.CredentialBindings, response.StoreModelCredentialBindingResponse{
				ID: binding.ID, UserID: binding.UserID, AccountName: binding.AccountName,
			})
		}
		result.Stores = append(result.Stores, item)
	}
	return result
}
