package services

import (
	"slices"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func (s *tenantInvitationService) Current(tenantID int64, operator *dto.AuthPrincipal) (*models.TenantInvitation, string, error) {
	if operator == nil || tenantID <= 0 || operator.ActiveTenantID != tenantID || !slices.Contains(operator.Permissions, constants.PermissionTenantInviteView.Code) {
		return nil, "", errorsx.Forbidden("只能查看当前接入公司的邀请码")
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), tenantID)
	if tenant == nil || tenant.Status == enums.StatusDeleted {
		return nil, "", errorsx.InvalidParam("接入公司不存在")
	}
	invitation := repositories.TenantInvitationRepository.FindCurrent(sqls.DB(), tenantID)
	if invitation == nil {
		return nil, "", errorsx.InvalidParam("公司邀请码尚未创建")
	}
	code, err := decryptTenantInvitationCode(invitation.CodeCiphertext, config.Current().Auth.InvitationEncryptionKey)
	if err != nil {
		return nil, "", errorsx.BusinessError(20, err.Error())
	}
	return invitation, code, nil
}

func (s *tenantInvitationService) Rotate(tenantID int64, operator *dto.AuthPrincipal) (*models.TenantInvitation, string, error) {
	if operator == nil || operator.ActiveTenantID != tenantID || !slices.Contains(operator.Permissions, constants.PermissionTenantInviteRotate.Code) {
		return nil, "", errorsx.Forbidden("只能重置当前接入公司的邀请码")
	}
	code, err := generateTenantInvitationCode()
	if err != nil {
		return nil, "", err
	}
	code = normalizeTenantInvitationCode(code)
	ciphertext, err := encryptTenantInvitationCode(code, config.Current().Auth.InvitationEncryptionKey)
	if err != nil {
		return nil, "", errorsx.BusinessError(20, err.Error())
	}

	var created *models.TenantInvitation
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		tenant, err := repositories.TenantRepository.GetForUpdate(ctx.Tx, tenantID)
		if err != nil {
			return err
		}
		if tenant == nil || tenant.Status != enums.StatusOk {
			return errorsx.InvalidParam("接入公司不存在或已停用")
		}
		latest := repositories.TenantInvitationRepository.FindLatest(ctx.Tx, tenantID)
		version := 1
		now := time.Now()
		if latest != nil {
			version = latest.Version + 1
		}
		if err := repositories.TenantInvitationRepository.DisableActiveByTenant(ctx.Tx, tenantID, map[string]any{
			"status":           enums.StatusDisabled,
			"rotated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		created = &models.TenantInvitation{
			TenantID:       tenantID,
			CodeHash:       hashTenantInvitationCode(code),
			CodeCiphertext: ciphertext,
			CodeLast4:      code[len(code)-4:],
			Version:        version,
			RotatedAt:      &now,
			Status:         enums.StatusOk,
			AuditFields:    utils.BuildAuditFields(operator),
		}
		return repositories.TenantInvitationRepository.Create(ctx.Tx, created)
	})
	if err != nil {
		return nil, "", err
	}
	return created, code, nil
}
