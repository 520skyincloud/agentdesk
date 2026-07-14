package services

import (
	"net/mail"
	"regexp"
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
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type CreateTenantResult struct {
	Tenant             *models.Tenant
	Supervisor         *models.User
	SupervisorPassword string
	DefaultAgentTeam   *models.AgentTeam
	Invitation         *models.TenantInvitation
	InvitationCode     string
}

func (s *tenantService) FindSupervisors(tenantIDs []int64) (map[int64]*models.User, error) {
	return repositories.UserRepository.FindTenantSupervisors(sqls.DB(), tenantIDs, constants.RoleCodeTenantAdmin)
}

func (s *tenantService) CreateTenant(req request.CreateTenantRequest, operator *dto.AuthPrincipal) (*CreateTenantResult, error) {
	if !canManagePlatformTenant(operator, constants.PermissionTenantCreate.Code) {
		return nil, errorsx.Forbidden("无权限创建接入公司")
	}
	normalized, err := normalizeCreateTenantRequest(req)
	if err != nil {
		return nil, err
	}
	tenantCode, err := generateTenantCode()
	if err != nil {
		return nil, err
	}
	invitationCode, err := generateTenantInvitationCode()
	if err != nil {
		return nil, err
	}
	invitationCiphertext, err := encryptTenantInvitationCode(invitationCode, config.Current().Auth.InvitationEncryptionKey)
	if err != nil {
		return nil, errorsx.BusinessError(20, err.Error())
	}

	result := &CreateTenantResult{InvitationCode: normalizeTenantInvitationCode(invitationCode)}
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if existing := repositories.TenantRepository.GetByRegistration(ctx.Tx, normalized.RegistrationType, normalized.RegistrationNo); existing != nil {
			return errorsx.InvalidParam("公司法定识别号已存在")
		}
		now := time.Now()
		tenant := &models.Tenant{
			TenantCode:         tenantCode,
			LegalName:          normalized.LegalName,
			ShortName:          normalized.ShortName,
			RegistrationType:   normalized.RegistrationType,
			RegistrationNo:     normalized.RegistrationNo,
			ContactName:        normalized.ContactName,
			ContactMobile:      normalized.ContactMobile,
			ContactEmail:       normalized.ContactEmail,
			Address:            normalized.Address,
			VerificationStatus: enums.TenantVerificationStatusVerified,
			VerifiedAt:         &now,
			VerifiedBy:         operator.UserID,
			Status:             enums.StatusOk,
			Remark:             normalized.Remark,
			AuditFields:        utils.BuildAuditFields(operator),
		}
		if err := repositories.TenantRepository.Create(ctx.Tx, tenant); err != nil {
			return err
		}

		role := repositories.RoleRepository.GetByCode(ctx.Tx, constants.RoleCodeTenantAdmin)
		if role == nil || role.Status != enums.StatusOk || role.Scope != constants.RoleScopeTenant {
			return errorsx.BusinessError(21, "公司主管角色未正确初始化")
		}
		mobile := normalized.Supervisor.Mobile
		email := normalized.Supervisor.Email
		supervisor, password, err := UserService.createManagedUserDB(ctx.Tx, request.CreateUserRequest{
			Username: normalized.Supervisor.Username,
			Nickname: normalized.Supervisor.Nickname,
			Mobile:   &mobile,
			Email:    &email,
		}, tenant.ID, enums.UserRegistrationSourceTenant, []int64{role.ID}, operator)
		if err != nil {
			return err
		}

		team := &models.AgentTeam{
			TenantID:    tenant.ID,
			Name:        "综合客服组",
			IsDefault:   true,
			Status:      enums.StatusOk,
			Description: "接入公司创建时生成的默认综合客服组",
			AuditFields: utils.BuildAuditFields(operator),
		}
		if err := repositories.AgentTeamRepository.Create(ctx.Tx, team); err != nil {
			return err
		}

		invitation := &models.TenantInvitation{
			TenantID:       tenant.ID,
			CodeHash:       hashTenantInvitationCode(invitationCode),
			CodeCiphertext: invitationCiphertext,
			CodeLast4:      invitationCode[len(invitationCode)-4:],
			Version:        1,
			Status:         enums.StatusOk,
			AuditFields:    utils.BuildAuditFields(operator),
		}
		if err := repositories.TenantInvitationRepository.Create(ctx.Tx, invitation); err != nil {
			return err
		}

		result.Tenant = tenant
		result.Supervisor = supervisor
		result.SupervisorPassword = password
		result.DefaultAgentTeam = team
		result.Invitation = invitation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *tenantService) UpdateTenant(req request.UpdateTenantRequest, operator *dto.AuthPrincipal) error {
	if !canManagePlatformTenant(operator, constants.PermissionTenantUpdate.Code) {
		return errorsx.Forbidden("无权限更新接入公司")
	}
	if req.ID <= 0 {
		return errorsx.InvalidParam("接入公司不存在")
	}
	normalized, err := normalizeTenantFields(req.LegalName, req.ShortName, req.RegistrationType, req.RegistrationNo, req.ContactName, req.ContactMobile, req.ContactEmail, req.Address, req.Remark)
	if err != nil {
		return err
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.TenantRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if current == nil || current.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("接入公司不存在")
		}
		if existing := repositories.TenantRepository.GetByRegistration(ctx.Tx, normalized.RegistrationType, normalized.RegistrationNo); existing != nil && existing.ID != req.ID {
			return errorsx.InvalidParam("公司法定识别号已存在")
		}
		now := time.Now()
		return repositories.TenantRepository.Updates(ctx.Tx, req.ID, map[string]any{
			"legal_name":          normalized.LegalName,
			"short_name":          normalized.ShortName,
			"registration_type":   normalized.RegistrationType,
			"registration_no":     normalized.RegistrationNo,
			"contact_name":        normalized.ContactName,
			"contact_mobile":      normalized.ContactMobile,
			"contact_email":       normalized.ContactEmail,
			"address":             normalized.Address,
			"remark":              normalized.Remark,
			"verification_status": enums.TenantVerificationStatusVerified,
			"verified_at":         now,
			"verified_by":         operator.UserID,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		})
	})
}

func (s *tenantService) UpdateTenantStatus(req request.UpdateTenantStatusRequest, operator *dto.AuthPrincipal) error {
	if !canManagePlatformTenant(operator, constants.PermissionTenantUpdateStatus.Code) {
		return errorsx.Forbidden("无权限启停接入公司")
	}
	if req.Status != int(enums.StatusOk) && req.Status != int(enums.StatusDisabled) {
		return errorsx.InvalidParam("接入公司状态不合法")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.TenantRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if current == nil || current.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("接入公司不存在")
		}
		return repositories.TenantRepository.Updates(ctx.Tx, req.ID, map[string]any{
			"status":           req.Status,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       time.Now(),
		})
	})
}

func canManagePlatformTenant(operator *dto.AuthPrincipal, permissionCode string) bool {
	return operator != nil && operator.IsPlatformAccount && slices.Contains(operator.Permissions, permissionCode)
}

type normalizedTenantFields struct {
	LegalName        string
	ShortName        string
	RegistrationType string
	RegistrationNo   string
	ContactName      string
	ContactMobile    string
	ContactEmail     string
	Address          string
	Remark           string
}

type normalizedCreateTenantRequest struct {
	normalizedTenantFields
	Supervisor request.CreateTenantSupervisorRequest
}

var unifiedSocialCreditCodePattern = regexp.MustCompile(`^[0-9ABCDEFGHJKLMNPQRTUWXY]{18}$`)

func normalizeCreateTenantRequest(req request.CreateTenantRequest) (*normalizedCreateTenantRequest, error) {
	fields, err := normalizeTenantFields(req.LegalName, req.ShortName, req.RegistrationType, req.RegistrationNo, req.ContactName, req.ContactMobile, req.ContactEmail, req.Address, req.Remark)
	if err != nil {
		return nil, err
	}
	supervisor := request.CreateTenantSupervisorRequest{
		Username: strings.TrimSpace(req.Supervisor.Username),
		Nickname: strings.TrimSpace(req.Supervisor.Nickname),
		Mobile:   strings.TrimSpace(req.Supervisor.Mobile),
		Email:    strings.ToLower(strings.TrimSpace(req.Supervisor.Email)),
	}
	if supervisor.Username == "" || supervisor.Nickname == "" || supervisor.Mobile == "" || supervisor.Email == "" {
		return nil, errorsx.InvalidParam("公司主管用户名、姓名、手机号和邮箱不能为空")
	}
	if len(supervisor.Username) > 100 || len(supervisor.Nickname) > 100 || len(supervisor.Mobile) > 32 || len(supervisor.Email) > 100 {
		return nil, errorsx.InvalidParam("公司主管账号信息长度不合法")
	}
	if !isPlainEmailAddress(supervisor.Email) {
		return nil, errorsx.InvalidParam("公司主管邮箱格式不合法")
	}
	return &normalizedCreateTenantRequest{normalizedTenantFields: *fields, Supervisor: supervisor}, nil
}

func normalizeTenantFields(legalName, shortName, registrationType, registrationNo, contactName, contactMobile, contactEmail, address, remark string) (*normalizedTenantFields, error) {
	ret := &normalizedTenantFields{
		LegalName:        strings.TrimSpace(legalName),
		ShortName:        strings.TrimSpace(shortName),
		RegistrationType: strings.ToLower(strings.TrimSpace(registrationType)),
		RegistrationNo:   strings.ToUpper(strings.TrimSpace(registrationNo)),
		ContactName:      strings.TrimSpace(contactName),
		ContactMobile:    strings.TrimSpace(contactMobile),
		ContactEmail:     strings.ToLower(strings.TrimSpace(contactEmail)),
		Address:          strings.TrimSpace(address),
		Remark:           strings.TrimSpace(remark),
	}
	if ret.LegalName == "" || ret.ShortName == "" || ret.RegistrationType == "" || ret.RegistrationNo == "" {
		return nil, errorsx.InvalidParam("公司法定名称、简称、证件类型和法定识别号不能为空")
	}
	if len(ret.LegalName) > 200 || len(ret.ShortName) > 100 || len(ret.RegistrationType) > 30 || len(ret.RegistrationNo) > 64 || len(ret.ContactName) > 100 || len(ret.ContactMobile) > 32 || len(ret.ContactEmail) > 100 || len(ret.Address) > 500 {
		return nil, errorsx.InvalidParam("公司信息长度不合法")
	}
	if ret.RegistrationType == "unified_social_credit_code" && !unifiedSocialCreditCodePattern.MatchString(ret.RegistrationNo) {
		return nil, errorsx.InvalidParam("统一社会信用代码必须为 18 位合法字符")
	}
	if ret.ContactEmail != "" && !isPlainEmailAddress(ret.ContactEmail) {
		return nil, errorsx.InvalidParam("公司联系邮箱格式不合法")
	}
	return ret, nil
}

func isPlainEmailAddress(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}
