package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	storeCredentialKeyMask              = "************"
	storeBindingCredentialCipherVersion = securex.AESGCMCipherVersion + "-binding-v1"
)

var StoreModelCredentialService = newStoreModelCredentialService()

type StoreCredentialRequestMeta struct {
	RequestID string
	ClientIP  string
}

type StoreModelCredentialData struct {
	Store              models.Store
	Binding            *models.StoreStaffBinding
	BindingAccountName string
	Credential         *models.StoreModelCredential
	Policy             *models.StoreCredentialPolicy
	Assignment         *models.StoreModelProfileAssignment
	ActiveTemplate     *models.ModelProfileTemplate
	ActiveSlots        []models.ModelProfileSlot
	PendingTemplate    *models.ModelProfileTemplate
	PendingSlots       []models.ModelProfileSlot
	CanSelfService     bool
}

type StoreModelCredentialAuditData struct {
	Items []models.StoreModelCredentialAuditLog
}

type resolvedStoreModelCredential struct {
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	APIKey              string
	Revision            int64
	Fingerprint         string
}

type storeProfileActivationCredential struct {
	Binding    models.StoreStaffBinding
	Credential models.StoreModelCredential
	Resolved   *resolvedStoreModelCredential
	TestRun    *models.ModelProfileTestRun
}

type storeModelCredentialService struct {
	validator storeCredentialSlotValidator
	fastGPT   storeCredentialFastGPTSynchronizer
}

func newStoreModelCredentialService() *storeModelCredentialService {
	return &storeModelCredentialService{
		validator: &newAPIStoreCredentialValidator{},
		fastGPT:   &managedStoreCredentialFastGPTSynchronizer{},
	}
}

func (s *storeModelCredentialService) GetManager(req request.GetStoreModelCredentialRequest, operator *dto.AuthPrincipal) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigView.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	return s.getData(tenantID, req.StoreID, req.StoreStaffBindingID, false)
}

func (s *storeModelCredentialService) GetSelf(operator *dto.AuthPrincipal) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionStoreWorkbenchView.Code); err != nil {
		return nil, err
	}
	snapshot, err := StoreWorkbenchService.Current(operator)
	if err != nil {
		return nil, err
	}
	if snapshot.Binding == nil || snapshot.Store == nil {
		return nil, errorsx.InvalidParam("当前账号尚未绑定门店")
	}
	data, err := s.getData(snapshot.TenantID, snapshot.Store.ID, snapshot.Binding.ID, false)
	if err != nil {
		return nil, err
	}
	data.CanSelfService = snapshot.Binding.Status == enums.StatusOk &&
		snapshot.Binding.ActiveUserID != nil &&
		*snapshot.Binding.ActiveUserID == operator.UserID &&
		slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) &&
		slices.Contains(operator.Permissions, constants.PermissionStoreWorkbenchUpdate.Code) &&
		data.Policy != nil &&
		data.Policy.Status == enums.StatusOk &&
		data.Policy.AllowCredentialSelfService
	return data, nil
}

func (s *storeModelCredentialService) GetAudit(req request.GetStoreModelCredentialAuditRequest, operator *dto.AuthPrincipal) (*StoreModelCredentialAuditData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigView.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, false); err != nil {
		return nil, err
	}
	if store := repositories.StoreRepository.GetInTenant(sqls.DB(), req.StoreID, tenantID); store == nil {
		return nil, errorsx.InvalidParam("门店不存在或不属于当前接入公司")
	}
	return &StoreModelCredentialAuditData{Items: repositories.StoreModelCredentialAuditLogRepository.FindLatestByBinding(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, req.Limit)}, nil
}

func (s *storeModelCredentialService) UpdatePolicy(req request.UpdateStoreCredentialPolicyRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) error {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return err
	}
	storeIDs := normalizePositiveIDs(req.StoreIDs)
	if len(storeIDs) == 0 {
		return errorsx.InvalidParam("至少选择一个门店")
	}
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		for _, storeID := range storeIDs {
			s.recordFailedSensitiveStorePolicyAction(tenantID, storeID, operator, meta, "password_verification_failed")
		}
		return err
	}
	requireSupervisorApproval := req.AllowCredentialSelfService && req.RequireSupervisorApproval
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, storeID := range storeIDs {
			store, err := repositories.StoreRepository.GetForUpdateInTenant(ctx.Tx, storeID, tenantID)
			if err != nil {
				return err
			}
			if store == nil || store.Status == enums.StatusDeleted {
				return errorsx.InvalidParam(fmt.Sprintf("门店 %d 不存在或不属于当前接入公司", storeID))
			}
			_, policy, err := s.ensureStoreRecordsDB(ctx.Tx, store, operator, now)
			if err != nil {
				return err
			}
			if err := repositories.StoreCredentialPolicyRepository.Updates(ctx.Tx, policy.ID, map[string]any{
				"allow_credential_self_service": req.AllowCredentialSelfService,
				"require_supervisor_approval":   requireSupervisorApproval,
				"status":                        enums.StatusOk, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
			if err := s.appendStorePolicyAuditDB(ctx.Tx, store.TenantID, store.ID, operator, meta, enums.CredentialAuditResultSuccess, ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, storeID := range storeIDs {
		s.publishConfigurationState(tenantID, storeID, 0, now)
	}
	return nil
}

func (s *storeModelCredentialService) SubmitManager(ctx context.Context, req request.SubmitStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	return s.submit(ctx, tenantID, req.StoreID, req.StoreStaffBindingID, req, operator, meta, false)
}

func (s *storeModelCredentialService) SubmitSelf(ctx context.Context, req request.SubmitStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionStoreWorkbenchUpdate.Code); err != nil {
		return nil, err
	}
	snapshot, err := StoreWorkbenchService.Current(operator)
	if err != nil {
		return nil, err
	}
	if snapshot.Binding == nil || snapshot.Store == nil || snapshot.Binding.Status != enums.StatusOk {
		return nil, errorsx.Forbidden("当前门店员工绑定不可用")
	}
	if snapshot.Binding.UserID != operator.UserID {
		return nil, errorsx.Forbidden("当前账号不是该门店员工号的绑定账号")
	}
	if snapshot.Binding.ActiveUserID == nil || *snapshot.Binding.ActiveUserID != operator.UserID {
		return nil, errorsx.Forbidden("当前账号没有有效的门店员工号绑定")
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		return nil, errorsx.Forbidden("当前账号未持有门店员工角色")
	}
	return s.submit(ctx, snapshot.TenantID, snapshot.Store.ID, snapshot.Binding.ID, req, operator, meta, true)
}

func (s *storeModelCredentialService) Approve(ctx context.Context, req request.DecideStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, true)
	if err != nil {
		return nil, err
	}
	bindingID := binding.ID
	if err := requireCredentialSupervisorApproval(operator, tenantID); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionApprove, req.CandidateRevision, "supervisor_role_required")
		return nil, err
	}
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionApprove, req.CandidateRevision, "password_verification_failed")
		return nil, err
	}
	now := time.Now()
	selfApprovalAttempt := false
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		credential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, req.StoreID, bindingID)
		if err != nil {
			return err
		}
		if credential == nil || credential.CandidateRevision != req.CandidateRevision || credential.CandidateApprovalStatus != enums.CredentialApprovalStatusPending || credential.CandidateStatus != enums.StoreCredentialStatusPendingApproval {
			return errorsx.InvalidParam("待审批凭据已变化，请刷新后重试")
		}
		if credential.CandidateRequestedBy == operator.UserID {
			selfApprovalAttempt = true
			return errorsx.Forbidden("凭据提交人不能审批自己的申请")
		}
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"candidate_approval_status": enums.CredentialApprovalStatusApproved,
			"candidate_status":          enums.StoreCredentialStatusTesting,
			"candidate_approved_by":     operator.UserID, "candidate_approved_at": now,
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.appendAuditDB(tx.Tx, credential, operator, operator, meta, enums.CredentialAuditActionApprove, enums.CredentialAuditResultSuccess, credential.CredentialRevision, credential.CandidateRevision, credential.CandidateProfileID, credential.CandidateProfileRevision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), "")
	})
	if err != nil {
		if selfApprovalAttempt {
			s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionApprove, req.CandidateRevision, "self_approval_forbidden")
		}
		return nil, err
	}
	s.publishConfigurationState(tenantID, req.StoreID, bindingID, now)
	if err := s.processCandidate(ctx, tenantID, req.StoreID, bindingID, req.CandidateRevision, operator, meta); err != nil {
		return nil, err
	}
	return s.getData(tenantID, req.StoreID, bindingID, false)
}

func (s *storeModelCredentialService) Reject(req request.DecideStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, false)
	if err != nil {
		return nil, err
	}
	bindingID := binding.ID
	if err := requireCredentialSupervisorApproval(operator, tenantID); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionReject, req.CandidateRevision, "supervisor_role_required")
		return nil, err
	}
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionReject, req.CandidateRevision, "password_verification_failed")
		return nil, err
	}
	now := time.Now()
	selfApprovalAttempt := false
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		credential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, req.StoreID, bindingID)
		if err != nil {
			return err
		}
		if credential == nil || credential.CandidateRevision != req.CandidateRevision || credential.CandidateApprovalStatus != enums.CredentialApprovalStatusPending || credential.CandidateStatus != enums.StoreCredentialStatusPendingApproval {
			return errorsx.InvalidParam("待审批凭据已变化，请刷新后重试")
		}
		if credential.CandidateRequestedBy == operator.UserID {
			selfApprovalAttempt = true
			return errorsx.Forbidden("凭据提交人不能审批自己的申请")
		}
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"candidate_approval_status": enums.CredentialApprovalStatusRejected,
			"candidate_status":          enums.StoreCredentialStatusFailed,
			"candidate_approved_by":     operator.UserID, "candidate_approved_at": now,
			"candidate_encrypted_key": "", "candidate_key_nonce": "", "candidate_key_fingerprint": "",
			"candidate_cipher_version": "", "candidate_master_key_id": "",
			"last_error_class": "approval_rejected", "last_error_message": "公司主管已拒绝本次凭据更新",
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := s.preserveAssignmentAfterFailureDB(tx.Tx, tenantID, req.StoreID, "approval_rejected", "公司主管已拒绝本次凭据更新", operator, now); err != nil {
			return err
		}
		return s.appendAuditDB(tx.Tx, credential, operator, operator, meta, enums.CredentialAuditActionReject, enums.CredentialAuditResultSuccess, credential.CredentialRevision, credential.CandidateRevision, credential.CandidateProfileID, credential.CandidateProfileRevision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), "approval_rejected")
	})
	if err != nil {
		if selfApprovalAttempt {
			s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionReject, req.CandidateRevision, "self_approval_forbidden")
		}
		return nil, err
	}
	s.publishConfigurationState(tenantID, req.StoreID, bindingID, now)
	return s.getData(tenantID, req.StoreID, bindingID, false)
}

func (s *storeModelCredentialService) Disable(req request.DecideStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, false)
	if err != nil {
		return nil, err
	}
	bindingID := binding.ID
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, bindingID, operator, meta, enums.CredentialAuditActionDisable, 0, "password_verification_failed")
		return nil, err
	}
	now := time.Now()
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		credential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, req.StoreID, bindingID)
		if err != nil {
			return err
		}
		if credential == nil || credential.CredentialRevision <= 0 || credential.Status != enums.StoreCredentialStatusActive {
			return errorsx.InvalidParam("当前门店没有可停用的 active 凭据")
		}
		if liveCredentialCandidate(credential) {
			return errorsx.InvalidParam("当前存在进行中的凭据更新，请先完成或拒绝该申请")
		}
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"status": enums.StoreCredentialStatusDisabled, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.appendAuditDB(tx.Tx, credential, operator, nil, meta, enums.CredentialAuditActionDisable, enums.CredentialAuditResultSuccess, credential.CredentialRevision, credential.CredentialRevision, 0, 0, securex.FingerprintLast6(credential.KeyFingerprint), "")
	})
	if err != nil {
		return nil, err
	}
	s.publishConfigurationState(tenantID, req.StoreID, bindingID, now)
	return s.getData(tenantID, req.StoreID, bindingID, false)
}

func (s *storeModelCredentialService) ActivatePendingProfile(
	ctx context.Context,
	req request.ActivatePendingStoreModelProfileRequest,
	operator *dto.AuthPrincipal,
	meta StoreCredentialRequestMeta,
) (*StoreModelCredentialData, error) {
	if err := requireCredentialPermission(operator, constants.PermissionAIConfigUpdate.Code); err != nil {
		return nil, err
	}
	tenantID, err := resolveCredentialManagerTenant(operator, req.TenantID)
	if err != nil {
		return nil, err
	}
	ownerBinding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, req.StoreID, req.StoreStaffBindingID, true)
	if err != nil {
		return nil, err
	}
	ownerBindingID := ownerBinding.ID
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		s.recordFailedSensitiveAction(tenantID, req.StoreID, ownerBindingID, operator, meta, enums.CredentialAuditActionSwitchProfile, 0, "password_verification_failed")
		return nil, err
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), tenantID, req.StoreID)
	if assignment == nil ||
		assignment.Status != enums.StoreModelAssignmentStatusReady ||
		assignment.TemplateID <= 0 ||
		assignment.TemplateRevision <= 0 ||
		assignment.PendingTemplateID != req.TemplateID ||
		assignment.PendingTemplateRevision != req.ConfirmRevision {
		return nil, errorsx.InvalidParam("待切换模型方案已变化，请刷新后重试")
	}
	target, err := s.loadActivationTargetDB(sqls.DB(), tenantID, req.StoreID, req.TemplateID, req.ConfirmRevision)
	if err != nil {
		return nil, err
	}
	target.StoreStaffBindingID = ownerBindingID
	oldTarget, err := s.loadActiveTargetDB(sqls.DB(), tenantID, req.StoreID)
	if err != nil {
		return nil, errorsx.InvalidParam("当前 active 模型方案不可用")
	}
	if normalizeGatewayBaseURL(oldTarget.Template.GatewayBaseURL) != normalizeGatewayBaseURL(target.Template.GatewayBaseURL) {
		return nil, errorsx.InvalidParam("待切换方案不使用当前统一 NewAPI 网关，禁止复用门店凭据")
	}
	credentials, err := s.loadProfileActivationCredentials(tenantID, req.StoreID, ownerBindingID)
	if err != nil {
		return nil, err
	}
	ownerCredential := profileActivationCredentialByBinding(credentials, ownerBindingID)
	if ownerCredential == nil {
		return nil, errorsx.InvalidParam("FastGPT 凭据 owner 已变化，请刷新后重试")
	}
	oldOwnerBindingID := ownerBindingID
	if target.Store.KnowledgeBaseID > 0 {
		if state := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), req.StoreID, tenantID); state != nil {
			oldOwnerBindingID = firstPositiveInt64(state.AppliedStoreStaffBindingID, state.TargetStoreStaffBindingID, ownerBindingID)
		}
	}
	oldCredential := profileActivationCredentialByBinding(credentials, oldOwnerBindingID)
	if oldCredential == nil {
		return nil, errorsx.InvalidParam("当前 FastGPT 凭据 owner 已停用，无法安全切换模型方案")
	}
	oldTarget.StoreStaffBindingID = oldOwnerBindingID
	for i := range credentials {
		credential := &credentials[i].Credential
		if err := s.appendAudit(
			credential, operator, nil, meta,
			enums.CredentialAuditActionSwitchProfile, enums.CredentialAuditResultPending,
			credential.CredentialRevision, credential.CredentialRevision,
			target.Template.ID, target.Template.Revision,
			securex.FingerprintLast6(credential.KeyFingerprint), "",
		); err != nil {
			return nil, errorsx.BusinessError(3001, "模型方案切换审计写入失败")
		}
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), tenantID)
	if tenant == nil {
		s.appendProfileSwitchFailureAudits(credentials, operator, meta, target, "tenant_unavailable")
		return nil, errorsx.BusinessError(3001, "模型方案测试接入公司上下文不可用")
	}
	validationFailed := false
	validationMessage := "门店员工号模型凭据验证未通过"
	for i := range credentials {
		testStarted := time.Now()
		testErr := s.validator.Validate(ctx, &target.Template, target.Slots, credentials[i].Resolved.APIKey)
		run, recordErr := recordModelProfileTestRun(
			&target.Template, target.Slots, tenant, &target.Store,
			credentials[i].Binding.ID, credentials[i].Credential.CredentialRevision,
			enums.ModelProfileTestCredentialSourceActive, testErr, testStarted, operator, meta,
		)
		if recordErr != nil {
			s.appendProfileSwitchFailureAudits(credentials, operator, meta, target, "test_evidence_write_failed")
			return nil, recordErr
		}
		credentials[i].TestRun = run
		if run.Status != enums.ModelProfileTestStatusPassed {
			validationFailed = true
			if strings.TrimSpace(run.ErrorMessage) != "" {
				validationMessage = run.ErrorMessage
			}
		}
	}
	if validationFailed {
		for i := range credentials {
			class := "peer_binding_validation_failed"
			if credentials[i].TestRun != nil && credentials[i].TestRun.Status != enums.ModelProfileTestStatusPassed {
				class = credentials[i].TestRun.ErrorClass
			}
			credential := &credentials[i].Credential
			_ = s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionSwitchProfile, enums.CredentialAuditResultFailure, credential.CredentialRevision, credential.CredentialRevision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.KeyFingerprint), class)
		}
		return nil, errorsx.BusinessError(5, validationMessage)
	}
	fastGPTStatus, err := s.fastGPT.SyncOwner(
		ctx,
		*target,
		ownerCredential.Resolved.APIKey,
		ownerCredential.Resolved.Revision,
		ownerCredential.Resolved.Fingerprint,
	)
	if err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential.Resolved, tenantID, req.StoreID, ownerBindingID, ownerCredential.Credential.CredentialRevision)
		s.appendProfileSwitchFailureAudits(credentials, operator, meta, target, "fastgpt_sync_failed")
		return nil, errorsx.BusinessError(5, "FastGPT 模型配置同步失败，当前方案继续使用")
	}
	now := time.Now()
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		lockedCredentials := make([]*models.StoreModelCredential, len(credentials))
		for i := range credentials {
			lockedCredential, lockErr := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, req.StoreID, credentials[i].Binding.ID)
			if lockErr != nil {
				return lockErr
			}
			if lockedCredential == nil || lockedCredential.ID != credentials[i].Credential.ID ||
				lockedCredential.Status != enums.StoreCredentialStatusActive ||
				lockedCredential.CredentialRevision != credentials[i].Credential.CredentialRevision ||
				lockedCredential.KeyFingerprint != credentials[i].Credential.KeyFingerprint ||
				liveCredentialCandidate(lockedCredential) {
				return errors.New("active credential changed during profile switch")
			}
			lockedCredentials[i] = lockedCredential
		}
		lockedAssignment, err := repositories.StoreModelProfileAssignmentRepository.GetForUpdateByStore(tx.Tx, tenantID, req.StoreID)
		if err != nil {
			return err
		}
		if lockedAssignment == nil ||
			lockedAssignment.TemplateID != assignment.TemplateID ||
			lockedAssignment.TemplateRevision != assignment.TemplateRevision ||
			lockedAssignment.PendingTemplateID != target.Template.ID ||
			lockedAssignment.PendingTemplateRevision != target.Template.Revision {
			return errors.New("model profile assignment changed during switch")
		}
		lockedTemplate, err := repositories.ModelProfileTemplateRepository.GetForUpdate(tx.Tx, target.Template.ID)
		if err != nil {
			return err
		}
		if lockedTemplate == nil ||
			lockedTemplate.Revision != target.Template.Revision ||
			!slices.Contains([]enums.ModelProfileStatus{enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive}, lockedTemplate.Status) {
			return errors.New("target model profile changed during switch")
		}
		if lockedTemplate.Status == enums.ModelProfileStatusCandidate {
			if err := repositories.ModelProfileTemplateRepository.Updates(tx.Tx, lockedTemplate.ID, map[string]any{
				"status": enums.ModelProfileStatusActive, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		if err := repositories.StoreModelProfileAssignmentRepository.Updates(tx.Tx, lockedAssignment.ID, map[string]any{
			"template_id": lockedTemplate.ID, "template_revision": lockedTemplate.Revision,
			"pending_template_id": 0, "pending_template_revision": 0, "pending_requested_at": nil,
			"pending_requested_by": 0, "pending_requested_by_name": "",
			"status": enums.StoreModelAssignmentStatusReady, "readiness_status": "ready",
			"last_validated_at": now, "last_ready_at": now, "last_error_class": "", "last_error_message": "",
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		for i := range lockedCredentials {
			columns := map[string]any{
				"last_test_status": "passed", "last_tested_at": credentials[i].TestRun.CreatedAt,
				"last_test_latency_ms": credentials[i].TestRun.LatencyMS,
				"last_error_class":     "", "last_error_message": "",
				"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
			}
			if lockedCredentials[i].StoreStaffBindingID == ownerBindingID {
				columns["last_fast_gpt_sync_status"] = fastGPTStatus
				columns["last_fast_gpt_synced_at"] = now
			}
			if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, lockedCredentials[i].ID, columns); err != nil {
				return err
			}
			if err := s.appendAuditDB(
				tx.Tx, lockedCredentials[i], operator, nil, meta,
				enums.CredentialAuditActionSwitchProfile, enums.CredentialAuditResultSuccess,
				lockedCredentials[i].CredentialRevision, lockedCredentials[i].CredentialRevision,
				lockedTemplate.ID, lockedTemplate.Revision,
				securex.FingerprintLast6(lockedCredentials[i].KeyFingerprint), "",
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential.Resolved, tenantID, req.StoreID, ownerBindingID, ownerCredential.Credential.CredentialRevision)
		s.appendProfileSwitchFailureAudits(credentials, operator, meta, target, "profile_switch_conflict")
		return nil, errorsx.BusinessError(5, "模型方案切换冲突，当前方案继续使用")
	}
	for i := range credentials {
		s.publishConfigurationState(tenantID, req.StoreID, credentials[i].Binding.ID, now)
	}
	return s.getData(tenantID, req.StoreID, ownerBindingID, false)
}

func (s *storeModelCredentialService) loadProfileActivationCredentials(tenantID, storeID, ownerBindingID int64) ([]storeProfileActivationCredential, error) {
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("store_id", storeID).
		Eq("status", enums.StatusOk).
		Where("active_user_id IS NOT NULL").
		Asc("id"))
	if len(bindings) == 0 {
		return nil, errorsx.InvalidParam("当前门店没有有效的门店员工绑定")
	}
	ret := make([]storeProfileActivationCredential, 0, len(bindings))
	ownerFound := false
	for i := range bindings {
		binding := bindings[i]
		if binding.ID == ownerBindingID {
			ownerFound = true
		}
		credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, binding.ID)
		if credential == nil || credential.Status != enums.StoreCredentialStatusActive || credential.CredentialRevision <= 0 ||
			strings.TrimSpace(credential.EncryptedKey) == "" || strings.TrimSpace(credential.KeyNonce) == "" {
			return nil, errorsx.InvalidParam("门店存在尚未配置 active NewAPI 凭据的员工号")
		}
		if liveCredentialCandidate(credential) {
			return nil, errorsx.InvalidParam("门店员工号存在进行中的凭据更新，请先完成或拒绝该申请")
		}
		resolved, err := s.ResolveActiveForBinding(tenantID, storeID, binding.ID)
		if err != nil {
			return nil, err
		}
		if resolved.Revision != credential.CredentialRevision || resolved.Fingerprint != credential.KeyFingerprint {
			return nil, errorsx.InvalidParam("门店员工号 active 凭据 revision 已变化，请刷新后重试")
		}
		ret = append(ret, storeProfileActivationCredential{Binding: binding, Credential: *credential, Resolved: resolved})
	}
	if !ownerFound {
		return nil, errorsx.InvalidParam("所选 FastGPT 凭据 owner 已停用或归属已变化")
	}
	return ret, nil
}

func profileActivationCredentialByBinding(credentials []storeProfileActivationCredential, bindingID int64) *storeProfileActivationCredential {
	for i := range credentials {
		if credentials[i].Binding.ID == bindingID {
			return &credentials[i]
		}
	}
	return nil
}

func (s *storeModelCredentialService) appendProfileSwitchFailureAudits(credentials []storeProfileActivationCredential, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, target *storeCredentialActivationTarget, class string) {
	if target == nil {
		return
	}
	for i := range credentials {
		credential := &credentials[i].Credential
		_ = s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionSwitchProfile, enums.CredentialAuditResultFailure, credential.CredentialRevision, credential.CredentialRevision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.KeyFingerprint), class)
	}
}

func (s *storeModelCredentialService) submit(ctx context.Context, tenantID, storeID, bindingID int64, req request.SubmitStoreModelCredentialRequest, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, selfService bool) (*StoreModelCredentialData, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return nil, errorsx.InvalidParam("请输入新的 NewAPI API Key")
	}
	if len(apiKey) > 4096 {
		return nil, errorsx.InvalidParam("NewAPI API Key 长度不合法")
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, storeID, bindingID, true)
	if err != nil {
		return nil, err
	}
	bindingID = binding.ID
	if err := verifyCredentialSensitiveAction(operator, req.CurrentPassword, req.Confirmed); err != nil {
		s.recordFailedSensitiveAction(tenantID, storeID, bindingID, operator, meta, enums.CredentialAuditActionConfigure, 0, "password_verification_failed")
		return nil, err
	}
	cipher, masterKeyID, err := s.cipher()
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型凭据加密配置不可用")
	}
	now := time.Now()
	var revision int64
	var approvalStatus enums.CredentialApprovalStatus
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		store, err := repositories.StoreRepository.GetForUpdateInTenant(tx.Tx, storeID, tenantID)
		if err != nil {
			return err
		}
		if store == nil || store.Status != enums.StatusOk {
			return errorsx.InvalidParam("门店不存在、已停用或不属于当前接入公司")
		}
		lockedBinding, err := repositories.StoreStaffBindingRepository.GetForUpdateInTenant(tx.Tx, bindingID, tenantID)
		if err != nil {
			return err
		}
		if lockedBinding == nil || lockedBinding.StoreID != storeID || lockedBinding.Status != enums.StatusOk || lockedBinding.ActiveUserID == nil {
			return errorsx.InvalidParam("门店员工绑定不存在、已停用或归属已变化")
		}
		credential, err := s.ensureBindingCredentialDB(tx.Tx, store, lockedBinding, operator, now)
		if err != nil {
			return err
		}
		credential, err = repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, storeID, bindingID)
		if err != nil {
			return err
		}
		if liveCredentialCandidate(credential) {
			return errorsx.InvalidParam("当前门店已有进行中的凭据更新")
		}
		var policy *models.StoreCredentialPolicy
		if selfService {
			policy, err = repositories.StoreCredentialPolicyRepository.GetForUpdateByStore(tx.Tx, tenantID, storeID)
			if err != nil {
				return err
			}
			if policy == nil || policy.Status != enums.StatusOk || !policy.AllowCredentialSelfService {
				return errorsx.Forbidden("公司主管尚未允许门店自行维护模型凭据")
			}
		}
		target, err := s.loadActivationTargetDB(tx.Tx, tenantID, storeID, 0, 0)
		if err != nil {
			return err
		}
		target.StoreStaffBindingID = bindingID
		revision = credential.CredentialRevision + 1
		if credential.CandidateRevision >= revision {
			revision = credential.CandidateRevision + 1
		}
		ciphertext, nonce, err := cipher.Encrypt(apiKey, storeBindingCredentialAAD(tenantID, storeID, bindingID, revision))
		if err != nil {
			return errorsx.BusinessError(5001, "门店模型凭据加密失败")
		}
		approvalStatus = enums.CredentialApprovalStatusNotRequired
		candidateStatus := enums.StoreCredentialStatusTesting
		if selfService && policy.RequireSupervisorApproval {
			approvalStatus = enums.CredentialApprovalStatusPending
			candidateStatus = enums.StoreCredentialStatusPendingApproval
		}
		fingerprint := securex.Fingerprint(apiKey)
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"candidate_encrypted_key": ciphertext, "candidate_key_nonce": nonce,
			"candidate_key_fingerprint": fingerprint, "candidate_cipher_version": storeBindingCredentialCipherVersion,
			"candidate_master_key_id": masterKeyID, "candidate_revision": revision,
			"candidate_profile_id": target.Template.ID, "candidate_profile_revision": target.Template.Revision,
			"candidate_status": candidateStatus, "candidate_approval_status": approvalStatus,
			"candidate_requested_by": operator.UserID, "candidate_requested_at": now,
			"candidate_approved_by": 0, "candidate_approved_at": nil,
			"last_test_status": "", "last_fast_gpt_sync_status": "", "last_error_class": "", "last_error_message": "",
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		credential.CandidateKeyFingerprint = fingerprint
		credential.CandidateProfileID = target.Template.ID
		credential.CandidateProfileRevision = target.Template.Revision
		if err := s.appendAuditDB(tx.Tx, credential, operator, nil, meta, enums.CredentialAuditActionConfigure, enums.CredentialAuditResultSuccess, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(fingerprint), ""); err != nil {
			return err
		}
		result := enums.CredentialAuditResultSuccess
		if approvalStatus == enums.CredentialApprovalStatusPending {
			result = enums.CredentialAuditResultPending
		}
		return s.appendAuditDB(tx.Tx, credential, operator, nil, meta, enums.CredentialAuditActionSubmit, result, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(fingerprint), "")
	})
	if err != nil {
		return nil, err
	}
	s.publishConfigurationState(tenantID, storeID, bindingID, now)
	if approvalStatus != enums.CredentialApprovalStatusPending {
		if err := s.processCandidate(ctx, tenantID, storeID, bindingID, revision, operator, meta); err != nil {
			return nil, err
		}
	}
	data, err := s.getData(tenantID, storeID, bindingID, false)
	if err == nil && selfService {
		data.CanSelfService = true
	}
	return data, err
}

func (s *storeModelCredentialService) processCandidate(ctx context.Context, tenantID, storeID, bindingID, revision int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) error {
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, bindingID)
	if credential == nil || credential.CandidateRevision != revision || credential.CandidateStatus != enums.StoreCredentialStatusTesting || credential.CandidateApprovalStatus == enums.CredentialApprovalStatusPending {
		return errorsx.InvalidParam("待验证凭据已变化，请刷新后重试")
	}
	apiKey, err := s.decryptCandidate(credential)
	if err != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, "candidate_decrypt_failed", "候选凭据无法解密，旧版本继续使用")
		return errorsx.BusinessError(5001, "候选凭据无法解密，旧版本继续使用")
	}
	target, err := s.loadActivationTargetDB(sqls.DB(), tenantID, storeID, credential.CandidateProfileID, credential.CandidateProfileRevision)
	if err != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, "profile_assignment_changed", "门店模型指派已变化，旧版本继续使用")
		return errorsx.BusinessError(2005, "门店模型指派已变化，旧版本继续使用")
	}
	target.StoreStaffBindingID = bindingID
	oldCredential, _ := s.ResolveActiveForBinding(tenantID, storeID, bindingID)
	oldTarget, _ := s.loadActiveTargetDB(sqls.DB(), tenantID, storeID)
	if oldTarget != nil {
		oldTarget.StoreStaffBindingID = bindingID
	}
	testStarted := time.Now()
	if err := s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionTest, enums.CredentialAuditResultPending, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), ""); err != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, "audit_write_failed", "凭据验证审计写入失败，旧版本继续使用")
		return errorsx.BusinessError(5001, "凭据验证审计写入失败，旧版本继续使用")
	}
	testErr := s.validator.Validate(ctx, &target.Template, target.Slots, apiKey)
	tenant := repositories.TenantRepository.Get(sqls.DB(), tenantID)
	testRun, evidenceErr := recordModelProfileTestRun(
		&target.Template,
		target.Slots,
		tenant,
		&target.Store,
		bindingID,
		revision,
		enums.ModelProfileTestCredentialSourceCandidate,
		testErr,
		testStarted,
		operator,
		meta,
	)
	if evidenceErr != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, "test_evidence_write_failed", "模型方案测试证据写入失败，旧版本继续使用")
		return evidenceErr
	}
	if testErr != nil {
		class, message := publicCredentialValidationFailure(testErr, target.Slots)
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, class, message)
		return errorsx.BusinessError(2005, message)
	}
	testedAt := testRun.CreatedAt
	if err := s.updateCandidateState(tenantID, storeID, bindingID, revision, enums.StoreCredentialStatusTesting, map[string]any{
		"candidate_status": enums.StoreCredentialStatusSyncing,
		"last_test_status": "passed", "last_tested_at": testedAt,
		"last_test_latency_ms": testRun.LatencyMS,
		"last_error_class":     "", "last_error_message": "",
	}); err != nil {
		return err
	}
	if err := s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionTest, enums.CredentialAuditResultSuccess, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), ""); err != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionTest, "audit_write_failed", "凭据验证结果审计写入失败，旧版本继续使用")
		return errorsx.BusinessError(5001, "凭据验证结果审计写入失败，旧版本继续使用")
	}
	if err := s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionSyncFastGPT, enums.CredentialAuditResultPending, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), ""); err != nil {
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionSyncFastGPT, "audit_write_failed", "FastGPT 同步审计写入失败，旧版本继续使用")
		return errorsx.BusinessError(5001, "FastGPT 同步审计写入失败，旧版本继续使用")
	}
	fastGPTStatus, err := s.fastGPT.Sync(ctx, *target, apiKey, revision, credential.CandidateKeyFingerprint)
	if err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential, tenantID, storeID, bindingID, revision)
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionSyncFastGPT, "fastgpt_sync_failed", "FastGPT 模型配置同步失败，旧版本继续使用")
		return errorsx.BusinessError(2005, "FastGPT 模型配置同步失败，旧版本继续使用")
	}
	syncedAt := time.Now()
	if err := s.updateCandidateState(tenantID, storeID, bindingID, revision, enums.StoreCredentialStatusSyncing, map[string]any{
		"candidate_status":          enums.StoreCredentialStatusReady,
		"last_fast_gpt_sync_status": fastGPTStatus, "last_fast_gpt_synced_at": syncedAt,
		"last_error_class": "", "last_error_message": "",
	}); err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential, tenantID, storeID, bindingID, revision)
		return err
	}
	if err := s.appendAudit(credential, operator, nil, meta, enums.CredentialAuditActionSyncFastGPT, enums.CredentialAuditResultSuccess, credential.CredentialRevision, revision, target.Template.ID, target.Template.Revision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), ""); err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential, tenantID, storeID, bindingID, revision)
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionSyncFastGPT, "audit_write_failed", "FastGPT 同步结果审计写入失败，旧版本继续使用")
		return errorsx.BusinessError(5001, "FastGPT 同步结果审计写入失败，旧版本继续使用")
	}
	if err := s.activateCandidate(tenantID, storeID, bindingID, revision, operator, meta); err != nil {
		s.restoreOldFastGPT(oldTarget, oldCredential, tenantID, storeID, bindingID, revision)
		s.failCandidate(tenantID, storeID, bindingID, revision, operator, meta, enums.CredentialAuditActionActivate, "activation_conflict", "凭据切换冲突，旧版本继续使用")
		return errorsx.BusinessError(2005, "凭据切换冲突，旧版本继续使用")
	}
	return nil
}

func (s *storeModelCredentialService) restoreOldFastGPT(oldTarget *storeCredentialActivationTarget, oldCredential *resolvedStoreModelCredential, tenantID, storeID, bindingID, candidateRevision int64) {
	restored := false
	if oldCredential != nil && oldTarget != nil {
		_, err := s.fastGPT.SyncOwner(context.Background(), *oldTarget, oldCredential.APIKey, oldCredential.Revision, oldCredential.Fingerprint)
		restored = err == nil
	}
	if !restored {
		s.markFastGPTActivationFailed(tenantID, storeID, bindingID, candidateRevision)
	}
}

func (s *storeModelCredentialService) markFastGPTActivationFailed(tenantID, storeID, bindingID, revision int64) {
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.MarkActivationFailedIfAppliedRevision(sqls.DB(), tenantID, storeID, bindingID, revision, map[string]any{
		"readiness_status": "failed", "last_error": "credential_activation_conflict",
		"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	})
	publishFastGPTConfigurationState(tenantID, storeID, now)
}

func (s *storeModelCredentialService) activateCandidate(tenantID, storeID, bindingID, revision int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta) error {
	now := time.Now()
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		credential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, storeID, bindingID)
		if err != nil {
			return err
		}
		if credential == nil || credential.CandidateRevision != revision || credential.CandidateStatus != enums.StoreCredentialStatusReady || credential.CandidateProfileID <= 0 {
			return errors.New("candidate changed during activation")
		}
		assignment, err := repositories.StoreModelProfileAssignmentRepository.GetForUpdateByStore(tx.Tx, tenantID, storeID)
		if err != nil {
			return err
		}
		if assignment == nil || !assignmentMatchesCandidate(assignment, credential.CandidateProfileID, credential.CandidateProfileRevision) {
			return errors.New("model profile assignment changed during activation")
		}
		template, err := repositories.ModelProfileTemplateRepository.GetForUpdate(tx.Tx, credential.CandidateProfileID)
		if err != nil {
			return err
		}
		if template == nil || template.Revision != credential.CandidateProfileRevision || !slices.Contains([]enums.ModelProfileStatus{enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive}, template.Status) {
			return errors.New("model profile revision changed during activation")
		}
		if template.Status == enums.ModelProfileStatusCandidate {
			if err := repositories.ModelProfileTemplateRepository.Updates(tx.Tx, template.ID, map[string]any{
				"status": enums.ModelProfileStatusActive, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		if err := repositories.StoreModelProfileAssignmentRepository.Updates(tx.Tx, assignment.ID, map[string]any{
			"template_id": template.ID, "template_revision": template.Revision,
			"pending_template_id": 0, "pending_template_revision": 0, "pending_requested_at": nil,
			"pending_requested_by": 0, "pending_requested_by_name": "",
			"status": enums.StoreModelAssignmentStatusReady, "readiness_status": "ready",
			"last_validated_at": now, "last_ready_at": now, "last_error_class": "", "last_error_message": "",
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"encrypted_key": credential.CandidateEncryptedKey, "key_nonce": credential.CandidateKeyNonce,
			"key_fingerprint": credential.CandidateKeyFingerprint, "cipher_version": credential.CandidateCipherVersion,
			"master_key_id": credential.CandidateMasterKeyID, "credential_revision": credential.CandidateRevision,
			"status":                  enums.StoreCredentialStatusActive,
			"candidate_encrypted_key": "", "candidate_key_nonce": "", "candidate_key_fingerprint": "",
			"candidate_cipher_version": "", "candidate_master_key_id": "", "candidate_revision": 0,
			"candidate_profile_id": 0, "candidate_profile_revision": 0, "candidate_status": "",
			"candidate_approval_status": enums.CredentialApprovalStatusNotRequired,
			"candidate_requested_by":    0, "candidate_requested_at": nil,
			"candidate_approved_by": 0, "candidate_approved_at": nil,
			"last_error_class": "", "last_error_message": "",
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.appendAuditDB(tx.Tx, credential, operator, nil, meta, enums.CredentialAuditActionActivate, enums.CredentialAuditResultSuccess, credential.CredentialRevision, revision, template.ID, template.Revision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), "")
	}); err != nil {
		return err
	}
	s.publishConfigurationState(tenantID, storeID, bindingID, now)
	return nil
}

func (s *storeModelCredentialService) ResolveActiveForBinding(tenantID, storeID, bindingID int64) (*resolvedStoreModelCredential, error) {
	return s.resolveActiveCredential(tenantID, storeID, bindingID, true)
}

func (s *storeModelCredentialService) ResolveForBillingBinding(tenantID, storeID, bindingID int64) (*resolvedStoreModelCredential, error) {
	return s.resolveActiveCredential(tenantID, storeID, bindingID, false)
}

func (s *storeModelCredentialService) resolveActiveCredential(tenantID, storeID, bindingID int64, requireActiveStore bool) (*resolvedStoreModelCredential, error) {
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted || (requireActiveStore && store.Status != enums.StatusOk) {
		return nil, errorsx.BusinessError(2005, "当前门店已停用或不属于接入公司")
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, storeID, bindingID, requireActiveStore)
	if err != nil {
		return nil, err
	}
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, binding.ID)
	credentialStatusAllowed := credential != nil && credential.Status == enums.StoreCredentialStatusActive
	if !requireActiveStore && credential != nil && credential.Status == enums.StoreCredentialStatusDisabled {
		credentialStatusAllowed = true
	}
	if credential == nil || !credentialStatusAllowed || credential.CredentialRevision <= 0 || strings.TrimSpace(credential.EncryptedKey) == "" {
		return nil, errorsx.BusinessError(2005, "当前门店员工号尚未配置可用的 NewAPI API Key")
	}
	cipher, masterKeyID, err := s.cipher()
	if err != nil || !supportedStoreCredentialCipherVersion(credential.CipherVersion) || credential.MasterKeyID != masterKeyID {
		return nil, errorsx.BusinessError(5001, "门店模型凭据加密配置不匹配")
	}
	apiKey, err := cipher.Decrypt(credential.EncryptedKey, credential.KeyNonce, storeCredentialAADForVersion(credential.CipherVersion, tenantID, storeID, binding.ID, credential.CredentialRevision))
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型凭据解密失败")
	}
	return &resolvedStoreModelCredential{TenantID: tenantID, StoreID: storeID, StoreStaffBindingID: binding.ID, APIKey: apiKey, Revision: credential.CredentialRevision, Fingerprint: credential.KeyFingerprint}, nil
}

func (s *storeModelCredentialService) decryptCandidate(credential *models.StoreModelCredential) (string, error) {
	if credential == nil || credential.CandidateRevision <= 0 || strings.TrimSpace(credential.CandidateEncryptedKey) == "" {
		return "", errors.New("candidate is unavailable")
	}
	cipher, masterKeyID, err := s.cipher()
	if err != nil || !supportedStoreCredentialCipherVersion(credential.CandidateCipherVersion) || credential.CandidateMasterKeyID != masterKeyID {
		return "", errors.New("candidate cipher does not match deployment key")
	}
	return cipher.Decrypt(
		credential.CandidateEncryptedKey,
		credential.CandidateKeyNonce,
		storeCredentialAADForVersion(credential.CandidateCipherVersion, credential.TenantID, credential.StoreID, credential.StoreStaffBindingID, credential.CandidateRevision),
	)
}

func (s *storeModelCredentialService) cipher() (*securex.AESGCM, string, error) {
	cfg := config.Current().StoreCredential
	masterKeyID := strings.TrimSpace(cfg.MasterKeyID)
	if masterKeyID == "" {
		return nil, "", errors.New("store credential master key id is not configured")
	}
	cipher, err := securex.NewAESGCM(cfg.MasterKey)
	return cipher, masterKeyID, err
}

func (s *storeModelCredentialService) EnsureStoreRecordsDB(db *gorm.DB, store *models.Store, operator *dto.AuthPrincipal) error {
	if store == nil {
		return errors.New("store is required")
	}
	_, _, err := s.ensureStoreRecordsDB(db, store, operator, time.Now())
	return err
}

func (s *storeModelCredentialService) EnsureBindingRecordDB(db *gorm.DB, binding *models.StoreStaffBinding, operator *dto.AuthPrincipal) error {
	if binding == nil {
		return errors.New("store staff binding is required")
	}
	store := repositories.StoreRepository.GetInTenant(db, binding.StoreID, binding.TenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return errors.New("store staff binding store is unavailable")
	}
	_, err := s.ensureBindingCredentialDB(db, store, binding, operator, time.Now())
	return err
}

func (s *storeModelCredentialService) ensureStoreRecordsDB(db *gorm.DB, store *models.Store, operator *dto.AuthPrincipal, now time.Time) ([]models.StoreModelCredential, *models.StoreCredentialPolicy, error) {
	policy := repositories.StoreCredentialPolicyRepository.GetByStore(db, store.TenantID, store.ID)
	if policy == nil {
		policy = &models.StoreCredentialPolicy{
			TenantID: store.TenantID, StoreID: store.ID, Status: enums.StatusOk,
			AuditFields: modelProfileAuditFields(operatorOrSystem(operator), now),
		}
		if err := repositories.StoreCredentialPolicyRepository.Create(db, policy); err != nil {
			return nil, nil, err
		}
	}
	bindings := repositories.StoreStaffBindingRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", store.TenantID).
		Eq("store_id", store.ID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	for i := range bindings {
		if _, err := s.ensureBindingCredentialDB(db, store, &bindings[i], operator, now); err != nil {
			return nil, nil, err
		}
	}
	return repositories.StoreModelCredentialRepository.FindByStore(db, store.TenantID, store.ID), policy, nil
}

func (s *storeModelCredentialService) ensureBindingCredentialDB(db *gorm.DB, store *models.Store, binding *models.StoreStaffBinding, operator *dto.AuthPrincipal, now time.Time) (*models.StoreModelCredential, error) {
	if db == nil || store == nil || binding == nil || binding.TenantID != store.TenantID || binding.StoreID != store.ID || binding.ID <= 0 {
		return nil, errors.New("store staff credential scope is invalid")
	}
	credential := repositories.StoreModelCredentialRepository.GetByBinding(db, store.TenantID, store.ID, binding.ID)
	if credential != nil {
		return credential, nil
	}
	credential = &models.StoreModelCredential{
		TenantID: store.TenantID, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		Status: enums.StoreCredentialStatusUnconfigured, CandidateApprovalStatus: enums.CredentialApprovalStatusNotRequired,
		AuditFields: modelProfileAuditFields(operatorOrSystem(operator), now),
	}
	if err := repositories.StoreModelCredentialRepository.Create(db, credential); err != nil {
		return nil, err
	}
	return credential, nil
}

func (s *storeModelCredentialService) getData(tenantID, storeID, bindingID int64, canSelfService bool) (*StoreModelCredentialData, error) {
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在或不属于当前接入公司")
	}
	binding, err := s.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, storeID, bindingID, false)
	if err != nil {
		return nil, err
	}
	data := &StoreModelCredentialData{
		Store: *store, Binding: binding, Credential: repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, binding.ID),
		Policy:         repositories.StoreCredentialPolicyRepository.GetByStore(sqls.DB(), tenantID, storeID),
		Assignment:     repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), tenantID, storeID),
		CanSelfService: canSelfService,
	}
	if user := repositories.UserRepository.Get(sqls.DB(), binding.UserID); user != nil {
		data.BindingAccountName = firstNonBlank(strings.TrimSpace(user.Nickname), strings.TrimSpace(user.Username))
	}
	if data.Assignment != nil {
		if data.Assignment.TemplateID > 0 {
			data.ActiveTemplate = repositories.ModelProfileTemplateRepository.Get(sqls.DB(), data.Assignment.TemplateID)
			data.ActiveSlots = repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), data.Assignment.TemplateID)
		}
		if data.Assignment.PendingTemplateID > 0 {
			data.PendingTemplate = repositories.ModelProfileTemplateRepository.Get(sqls.DB(), data.Assignment.PendingTemplateID)
			data.PendingSlots = repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), data.Assignment.PendingTemplateID)
		}
	}
	return data, nil
}

func (s *storeModelCredentialService) publishConfigurationState(tenantID, storeID, bindingID int64, updatedAt time.Time) {
	if tenantID <= 0 || storeID <= 0 {
		return
	}
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, bindingID)
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), tenantID, storeID)

	if credential != nil {
		profileID := int64(0)
		revision := credential.CredentialRevision
		status := string(credential.Status)
		if credential.CandidateRevision > 0 && credential.CandidateStatus != "" {
			profileID = credential.CandidateProfileID
			revision = credential.CandidateRevision
			status = string(credential.CandidateStatus)
		} else if assignment != nil {
			profileID = assignment.TemplateID
		}
		WsService.PublishStoreModelCredentialChanged(
			tenantID,
			storeID,
			profileID,
			revision,
			status,
			updatedAt,
		)
	}

	if assignment == nil {
		return
	}
	profileID := assignment.TemplateID
	revision := assignment.TemplateRevision
	status := firstNonBlank(assignment.ReadinessStatus, string(assignment.Status))
	if assignment.PendingTemplateID > 0 {
		profileID = assignment.PendingTemplateID
		revision = assignment.PendingTemplateRevision
		if status == "" || status == "ready" {
			status = "pending"
		}
	}
	WsService.PublishStoreModelProfileChanged(
		tenantID,
		storeID,
		profileID,
		revision,
		status,
		updatedAt,
	)
}

func (s *storeModelCredentialService) loadActivationTargetDB(db *gorm.DB, tenantID, storeID, expectedProfileID, expectedProfileRevision int64) (*storeCredentialActivationTarget, error) {
	store := repositories.StoreRepository.GetInTenant(db, storeID, tenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("门店不存在或已停用")
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(db, tenantID, storeID)
	if assignment == nil {
		return nil, errorsx.InvalidParam("请先为门店指派模型方案")
	}
	profileID, profileRevision := assignment.PendingTemplateID, assignment.PendingTemplateRevision
	if profileID <= 0 {
		profileID, profileRevision = assignment.TemplateID, assignment.TemplateRevision
	}
	if expectedProfileID > 0 && (profileID != expectedProfileID || profileRevision != expectedProfileRevision) {
		return nil, errorsx.InvalidParam("门店模型指派已变化")
	}
	template := repositories.ModelProfileTemplateRepository.Get(db, profileID)
	if template == nil || template.Revision != profileRevision || !slices.Contains([]enums.ModelProfileStatus{enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive}, template.Status) {
		return nil, errorsx.InvalidParam("门店模型方案 revision 不可用")
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(db, template.ID)
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) > 0 {
		return nil, errorsx.InvalidParam(issues[0].Message)
	}
	return &storeCredentialActivationTarget{Store: *store, Assignment: *assignment, Template: *template, Slots: slots}, nil
}

func (s *storeModelCredentialService) loadActiveTargetDB(db *gorm.DB, tenantID, storeID int64) (*storeCredentialActivationTarget, error) {
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(db, tenantID, storeID)
	if assignment == nil || assignment.TemplateID <= 0 || assignment.TemplateRevision <= 0 {
		return nil, errors.New("active model assignment is unavailable")
	}
	store := repositories.StoreRepository.GetInTenant(db, storeID, tenantID)
	template := repositories.ModelProfileTemplateRepository.Get(db, assignment.TemplateID)
	if store == nil || template == nil || template.Revision != assignment.TemplateRevision {
		return nil, errors.New("active model assignment is invalid")
	}
	return &storeCredentialActivationTarget{Store: *store, Assignment: *assignment, Template: *template, Slots: repositories.ModelProfileSlotRepository.FindByTemplateID(db, template.ID)}, nil
}

func (s *storeModelCredentialService) updateCandidateState(tenantID, storeID, bindingID, revision int64, expectedStatus enums.StoreCredentialStatus, updates map[string]any) error {
	updates["updated_at"] = time.Now()
	result := sqls.DB().Model(&models.StoreModelCredential{}).
		Where("tenant_id = ? AND store_id = ? AND store_staff_binding_id = ? AND candidate_revision = ? AND candidate_status = ?", tenantID, storeID, bindingID, revision, expectedStatus).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("candidate changed during processing")
	}
	s.publishConfigurationState(tenantID, storeID, bindingID, time.Now())
	return nil
}

func (s *storeModelCredentialService) failCandidate(tenantID, storeID, bindingID, revision int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, action enums.CredentialAuditAction, class, message string) {
	now := time.Now()
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		credential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(tx.Tx, tenantID, storeID, bindingID)
		if err != nil || credential == nil || credential.CandidateRevision != revision {
			return err
		}
		if err := repositories.StoreModelCredentialRepository.Updates(tx.Tx, credential.ID, map[string]any{
			"candidate_status":        enums.StoreCredentialStatusFailed,
			"candidate_encrypted_key": "", "candidate_key_nonce": "", "candidate_key_fingerprint": "",
			"candidate_cipher_version": "", "candidate_master_key_id": "",
			"last_test_status": func() string {
				if action == enums.CredentialAuditActionTest {
					return "failed"
				}
				return credential.LastTestStatus
			}(),
			"last_fast_gpt_sync_status": func() string {
				if action == enums.CredentialAuditActionSyncFastGPT {
					return storeCredentialFastGPTStatusFailed
				}
				return credential.LastFastGPTSyncStatus
			}(),
			"last_error_class": class, "last_error_message": message,
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := s.preserveAssignmentAfterFailureDB(tx.Tx, tenantID, storeID, class, message, operator, now); err != nil {
			return err
		}
		return s.appendAuditDB(tx.Tx, credential, operator, nil, meta, action, enums.CredentialAuditResultFailure, credential.CredentialRevision, revision, credential.CandidateProfileID, credential.CandidateProfileRevision, securex.FingerprintLast6(credential.CandidateKeyFingerprint), class)
	}); err == nil {
		s.publishConfigurationState(tenantID, storeID, bindingID, now)
	}
}

func (s *storeModelCredentialService) preserveAssignmentAfterFailureDB(db *gorm.DB, tenantID, storeID int64, class, message string, operator *dto.AuthPrincipal, now time.Time) error {
	assignment, err := repositories.StoreModelProfileAssignmentRepository.GetForUpdateByStore(db, tenantID, storeID)
	if err != nil || assignment == nil {
		return err
	}
	status := enums.StoreModelAssignmentStatusBlocked
	readiness := "blocked"
	if assignment.TemplateID > 0 && assignment.TemplateRevision > 0 && assignment.Status == enums.StoreModelAssignmentStatusReady {
		status = enums.StoreModelAssignmentStatusReady
		readiness = "ready"
	}
	return repositories.StoreModelProfileAssignmentRepository.Updates(db, assignment.ID, map[string]any{
		"status": status, "readiness_status": readiness,
		"last_error_class": class, "last_error_message": message,
		"last_validated_at": now, "updated_at": now,
		"update_user_id": operator.UserID, "update_user_name": operator.Username,
	})
}

func (s *storeModelCredentialService) appendAudit(credential *models.StoreModelCredential, operator, approver *dto.AuthPrincipal, meta StoreCredentialRequestMeta, action enums.CredentialAuditAction, result enums.CredentialAuditResult, fromRevision, toRevision, profileID, profileRevision int64, fingerprintLast6, errorClass string) error {
	return s.appendAuditDB(sqls.DB(), credential, operator, approver, meta, action, result, fromRevision, toRevision, profileID, profileRevision, fingerprintLast6, errorClass)
}

func (s *storeModelCredentialService) appendAuditDB(db *gorm.DB, credential *models.StoreModelCredential, operator, approver *dto.AuthPrincipal, meta StoreCredentialRequestMeta, action enums.CredentialAuditAction, result enums.CredentialAuditResult, fromRevision, toRevision, profileID, profileRevision int64, fingerprintLast6, errorClass string) error {
	if credential == nil || operator == nil {
		return errors.New("credential audit context is required")
	}
	item := &models.StoreModelCredentialAuditLog{
		TenantID: credential.TenantID, StoreID: credential.StoreID, StoreStaffBindingID: credential.StoreStaffBindingID, CredentialID: credential.ID,
		RequestID: trimCredentialAuditValue(meta.RequestID, 128), Action: action, Result: result,
		FromRevision: fromRevision, ToRevision: toRevision, ProfileID: profileID, ProfileRevision: profileRevision,
		FingerprintLast6: trimCredentialAuditValue(fingerprintLast6, 6),
		OperatorID:       operator.UserID, OperatorName: trimCredentialAuditValue(operator.Username, 100),
		OperatorRole: trimCredentialAuditValue(strings.Join(operator.Roles, ","), 100),
		ErrorClass:   trimCredentialAuditValue(errorClass, 80), ClientIP: trimCredentialAuditValue(meta.ClientIP, 64),
		CreatedAt: time.Now(),
	}
	if approver != nil {
		item.ApproverID = approver.UserID
		item.ApproverName = trimCredentialAuditValue(approver.Username, 100)
	}
	return repositories.StoreModelCredentialAuditLogRepository.Create(db, item)
}

func (s *storeModelCredentialService) appendStorePolicyAuditDB(db *gorm.DB, tenantID, storeID int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, result enums.CredentialAuditResult, errorClass string) error {
	if db == nil || tenantID <= 0 || storeID <= 0 || operator == nil {
		return errors.New("store credential policy audit context is required")
	}
	item := &models.StoreModelCredentialAuditLog{
		TenantID: tenantID, StoreID: storeID,
		RequestID: trimCredentialAuditValue(meta.RequestID, 128),
		Action:    enums.CredentialAuditActionPolicyUpdate, Result: result,
		OperatorID: operator.UserID, OperatorName: trimCredentialAuditValue(operator.Username, 100),
		OperatorRole: trimCredentialAuditValue(strings.Join(operator.Roles, ","), 100),
		ErrorClass:   trimCredentialAuditValue(errorClass, 80), ClientIP: trimCredentialAuditValue(meta.ClientIP, 64),
		CreatedAt: time.Now(),
	}
	return repositories.StoreModelCredentialAuditLogRepository.Create(db, item)
}

func (s *storeModelCredentialService) recordFailedSensitiveAction(tenantID, storeID, bindingID int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, action enums.CredentialAuditAction, revision int64, class string) {
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, bindingID)
	if credential == nil || operator == nil {
		return
	}
	_ = s.appendAudit(credential, operator, nil, meta, action, enums.CredentialAuditResultFailure, credential.CredentialRevision, revision, credential.CandidateProfileID, credential.CandidateProfileRevision, "", class)
}

func (s *storeModelCredentialService) recordFailedSensitiveStorePolicyAction(tenantID, storeID int64, operator *dto.AuthPrincipal, meta StoreCredentialRequestMeta, class string) {
	if operator == nil {
		return
	}
	_ = s.appendStorePolicyAuditDB(sqls.DB(), tenantID, storeID, operator, meta, enums.CredentialAuditResultFailure, class)
}

func (s *storeModelCredentialService) requireStoreStaffCredentialScopeDB(db *gorm.DB, tenantID, storeID, requestedBindingID int64, requireActive bool) (*models.StoreStaffBinding, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil, errorsx.InvalidParam("门店员工凭据范围不完整")
	}
	if requestedBindingID > 0 {
		binding := repositories.StoreStaffBindingRepository.GetInTenant(db, requestedBindingID, tenantID)
		if binding == nil || binding.StoreID != storeID || binding.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("门店员工绑定不存在或不属于所选门店")
		}
		if requireActive && (binding.Status != enums.StatusOk || binding.ActiveUserID == nil || *binding.ActiveUserID <= 0) {
			return nil, errorsx.InvalidParam("门店员工绑定已停用或未分配活动账号")
		}
		return binding, nil
	}
	bindings := repositories.StoreStaffBindingRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("store_id", storeID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	if requireActive {
		active := bindings[:0]
		for i := range bindings {
			if bindings[i].Status == enums.StatusOk && bindings[i].ActiveUserID != nil && *bindings[i].ActiveUserID > 0 {
				active = append(active, bindings[i])
			}
		}
		bindings = active
	}
	if len(bindings) == 0 {
		return nil, errorsx.InvalidParam("所选门店尚未绑定可用的门店员工号")
	}
	if len(bindings) > 1 {
		return nil, errorsx.InvalidParam("所选门店存在多个门店员工号，请明确选择需要使用的账号")
	}
	return &bindings[0], nil
}

func requireCredentialPermission(operator *dto.AuthPrincipal, permissionCode string) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !slices.Contains(operator.Permissions, permissionCode) {
		return errorsx.Forbidden("无权限执行该操作")
	}
	return nil
}

func requireCredentialSupervisorApproval(operator *dto.AuthPrincipal, tenantID int64) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	operatorTenantID := operator.ActiveTenantID
	if operatorTenantID <= 0 {
		operatorTenantID = operator.TenantID
	}
	if operator.IsPlatformAccount ||
		operatorTenantID != tenantID ||
		!slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin) {
		return errorsx.Forbidden("门店员工自助凭据必须由当前接入公司的公司主管审批")
	}
	return nil
}

func resolveCredentialManagerTenant(operator *dto.AuthPrincipal, requestedTenantID int64) (int64, error) {
	if operator == nil {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	if operator.IsPlatformAccount {
		if requestedTenantID <= 0 || repositories.TenantRepository.Get(sqls.DB(), requestedTenantID) == nil {
			return 0, errorsx.InvalidParam("接入公司不存在")
		}
		return requestedTenantID, nil
	}
	tenantID := operator.ActiveTenantID
	if tenantID <= 0 {
		tenantID = operator.TenantID
	}
	if tenantID <= 0 || (requestedTenantID > 0 && requestedTenantID != tenantID) {
		return 0, errorsx.Forbidden("只能管理当前接入公司的门店凭据")
	}
	return tenantID, nil
}

func verifyCredentialSensitiveAction(operator *dto.AuthPrincipal, currentPassword string, confirmed bool) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !confirmed {
		return errorsx.InvalidParam("请完成本次敏感操作的二次确认")
	}
	return AuthService.VerifyCurrentPassword(operator.UserID, currentPassword)
}

func liveCredentialCandidate(credential *models.StoreModelCredential) bool {
	if credential == nil || credential.CandidateRevision <= 0 {
		return false
	}
	return slices.Contains([]enums.StoreCredentialStatus{
		enums.StoreCredentialStatusPendingApproval,
		enums.StoreCredentialStatusTesting,
		enums.StoreCredentialStatusSyncing,
		enums.StoreCredentialStatusReady,
	}, credential.CandidateStatus)
}

func assignmentMatchesCandidate(assignment *models.StoreModelProfileAssignment, profileID, revision int64) bool {
	if assignment == nil {
		return false
	}
	if assignment.PendingTemplateID > 0 {
		return assignment.PendingTemplateID == profileID && assignment.PendingTemplateRevision == revision
	}
	return assignment.TemplateID == profileID && assignment.TemplateRevision == revision
}

func storeCredentialAAD(tenantID, storeID, revision int64) []byte {
	return []byte(fmt.Sprintf("tenant:%d:store:%d:revision:%d", tenantID, storeID, revision))
}

func storeBindingCredentialAAD(tenantID, storeID, bindingID, revision int64) []byte {
	return []byte(fmt.Sprintf("tenant:%d:store:%d:binding:%d:revision:%d", tenantID, storeID, bindingID, revision))
}

func supportedStoreCredentialCipherVersion(version string) bool {
	return version == securex.AESGCMCipherVersion || version == storeBindingCredentialCipherVersion
}

func storeCredentialAADForVersion(version string, tenantID, storeID, bindingID, revision int64) []byte {
	if version == storeBindingCredentialCipherVersion {
		return storeBindingCredentialAAD(tenantID, storeID, bindingID, revision)
	}
	return storeCredentialAAD(tenantID, storeID, revision)
}

func publicCredentialValidationFailure(err error, slots []models.ModelProfileSlot) (string, string) {
	class := "model_validation_failed"
	usage := enums.ModelUsageSlot("")
	var validationErr *storeCredentialValidationError
	if errors.As(err, &validationErr) {
		usage = validationErr.UsageCode
		if strings.TrimSpace(validationErr.Class) != "" {
			class = validationErr.Class
		}
	}
	name := "模型"
	for _, slot := range slots {
		if slot.UsageCode == usage && strings.TrimSpace(slot.DisplayName) != "" {
			name = slot.DisplayName
			break
		}
	}
	return class, name + "连接验证未通过，旧版本继续使用"
}

func operatorOrSystem(operator *dto.AuthPrincipal) *dto.AuthPrincipal {
	if operator != nil {
		return operator
	}
	return &dto.AuthPrincipal{UserID: constants.SystemAuditUserID, Username: constants.SystemAuditUserName}
}

func trimCredentialAuditValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func MaskedStoreCredentialKey(hasKey bool) string {
	if !hasKey {
		return ""
	}
	return storeCredentialKeyMask
}
