package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	publicInviteValidationWindow     = 10 * time.Minute
	publicInviteValidationIPLimit    = 30
	publicInviteValidationCodeLimit  = 300
	publicRegistrationWindow         = time.Hour
	publicRegistrationIPLimit        = 20
	publicRegistrationInviteLimit    = 200
	publicRegistrationPrincipalLimit = 5
)

var (
	TenantRegistrationService = &tenantRegistrationService{}
	publicUsernamePattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{3,100}$`)
	publicMobilePattern       = regexp.MustCompile(`^[0-9+ -]{6,32}$`)
	invitationCodePattern     = regexp.MustCompile(`^inv_[0-9a-f]{48}$`)
)

type tenantRegistrationService struct {
	sqliteWriteMu sync.Mutex
}

type PublicRegistrationMeta struct {
	RequestID string
	ClientIP  string
	UserAgent string
}

type TenantInvitationValidationResult struct {
	Valid  bool
	Tenant *models.Tenant
}

type TenantUserRegistrationResult struct {
	User     *models.User
	Tenant   *models.Tenant
	Replayed bool
}

type normalizedPublicRegistration struct {
	Username       string
	Nickname       string
	Mobile         string
	Email          string
	Password       string
	InvitationCode string
	InviteHash     string
	Principal      string
	Fingerprint    string
}

func (s *tenantRegistrationService) ValidateConfiguration() error {
	if !config.Current().TenantRegistration.Enabled {
		return nil
	}
	if _, err := tenantInvitationKey(config.Current().Auth.InvitationEncryptionKey); err != nil {
		return fmt.Errorf("tenant registration configuration: %w", err)
	}
	if !assetaccess.HasIndependentSigningSecret() {
		return fmt.Errorf("tenant registration configuration: storage.assetURLSigningSecret is required")
	}
	return nil
}

func (s *tenantRegistrationService) ValidateInvitation(invitationCode string, meta PublicRegistrationMeta) (*TenantInvitationValidationResult, error) {
	if err := s.requirePublicRegistrationEnabled(); err != nil {
		return nil, err
	}
	meta = normalizePublicRegistrationMeta(meta)
	if meta.RequestID == "" {
		return nil, errorsx.InvalidParam("邀请码校验请求缺少有效的 X-Request-Id")
	}
	code := normalizeTenantInvitationCode(invitationCode)
	inviteHash := hashTenantInvitationCode(code)
	if existing := repositories.TenantRegistrationLogRepository.GetByRequestID(sqls.DB(), meta.RequestID); existing != nil {
		if existing.Action != enums.TenantRegistrationActionValidateInvite || existing.RequestFingerprint != inviteHash {
			return nil, errorsx.InvalidParam("请求标识已被其他操作使用")
		}
		invitation, tenant := s.resolveActiveInvitation(sqls.DB(), code)
		return &TenantInvitationValidationResult{Valid: invitation != nil, Tenant: tenant}, nil
	}
	if err := s.checkPublicRateLimits(enums.TenantRegistrationActionValidateInvite, meta, inviteHash, ""); err != nil {
		if logErr := s.createSecurityLog(sqls.DB(), securityLogInput{
			Meta: meta, Action: enums.TenantRegistrationActionValidateInvite, RequestFingerprint: inviteHash,
			InviteHash: inviteHash, Reason: "rate_limited",
		}); logErr != nil {
			slog.Error("record invitation validation rate limit", "requestId", meta.RequestID, "error", logErr)
		}
		return nil, err
	}

	invitation, tenant := s.resolveActiveInvitation(sqls.DB(), code)
	logInput := securityLogInput{
		Meta: meta, Action: enums.TenantRegistrationActionValidateInvite, RequestFingerprint: inviteHash,
		InviteHash: inviteHash, Success: invitation != nil,
		Reason: "invalid_or_inactive_invitation",
	}
	if invitation != nil {
		logInput.TenantID = tenant.ID
		logInput.InvitationID = invitation.ID
		logInput.Reason = "valid"
	}
	if err := s.createSecurityLog(sqls.DB(), logInput); err != nil {
		return nil, errorsx.BusinessError(30, "邀请码校验暂时不可用")
	}
	return &TenantInvitationValidationResult{Valid: invitation != nil, Tenant: tenant}, nil
}

func (s *tenantRegistrationService) Register(req request.RegisterTenantUserRequest, meta PublicRegistrationMeta) (*TenantUserRegistrationResult, error) {
	if err := s.requirePublicRegistrationEnabled(); err != nil {
		return nil, err
	}
	meta = normalizePublicRegistrationMeta(meta)
	if tracex.NormalizeRequestID(meta.RequestID) == "" {
		return nil, errorsx.InvalidParam("注册请求必须携带有效的 X-Request-Id")
	}
	normalized, err := normalizePublicRegistration(req)
	if err != nil {
		s.recordRegistrationFailure(meta, hashTenantInvitationCode(req.InvitationCode), publicRegistrationPrincipal(req), "", "invalid_input")
		return nil, err
	}
	normalized.Fingerprint, err = registrationRequestFingerprint(normalized)
	if err != nil {
		slog.Error("build tenant registration request fingerprint", "requestId", meta.RequestID, "error", err)
		return nil, errorsx.BusinessError(31, "注册暂时无法完成，请稍后重试")
	}
	if replay, replayed, replayErr := s.replayRegistration(meta.RequestID, normalized.Fingerprint); replayed {
		return replay, replayErr
	}
	if err := s.checkPublicRateLimits(enums.TenantRegistrationActionRegister, meta, normalized.InviteHash, normalized.Principal); err != nil {
		s.recordRegistrationFailure(meta, normalized.InviteHash, normalized.Principal, normalized.Fingerprint, "rate_limited")
		return nil, err
	}

	invitation, tenant := s.resolveActiveInvitation(sqls.DB(), normalized.InvitationCode)
	if invitation == nil || tenant == nil {
		s.recordRegistrationFailure(meta, normalized.InviteHash, normalized.Principal, normalized.Fingerprint, "invalid_or_inactive_invitation")
		return nil, errorsx.InvalidParam("邀请码无效、已失效或公司暂不可用")
	}
	if registrationIdentityExists(sqls.DB(), normalized) {
		s.recordRegistrationFailureWithScope(meta, tenant.ID, invitation.ID, normalized.InviteHash, normalized.Principal, normalized.Fingerprint, "account_identity_unavailable")
		return nil, errorsx.InvalidParam("注册信息不可用或已经提交")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(normalized.Password), bcrypt.DefaultCost)
	if err != nil {
		s.recordRegistrationFailureWithScope(meta, tenant.ID, invitation.ID, normalized.InviteHash, normalized.Principal, normalized.Fingerprint, "password_hash_failed")
		return nil, errorsx.BusinessError(31, "注册暂时无法完成，请稍后重试")
	}

	var user *models.User
	err = s.withWriteTransaction(func(ctx *sqls.TxContext) error {
		currentTenant, err := repositories.TenantRepository.GetForUpdate(ctx.Tx, tenant.ID)
		if err != nil {
			return err
		}
		currentInvitation := repositories.TenantInvitationRepository.Get(ctx.Tx, invitation.ID)
		current := repositories.TenantInvitationRepository.FindCurrent(ctx.Tx, tenant.ID)
		now := time.Now()
		if currentInvitation == nil || current == nil || current.ID != currentInvitation.ID || !tenantInvitationUsableAt(currentInvitation, now) || currentTenant == nil || currentTenant.Status != enums.StatusOk {
			return errorsx.InvalidParam("邀请码无效、已失效或公司暂不可用")
		}
		if registrationIdentityExists(ctx.Tx, normalized) {
			return errorsx.InvalidParam("注册信息不可用或已经提交")
		}
		mobile, email := normalized.Mobile, normalized.Email
		user = &models.User{
			TenantID:           tenant.ID,
			Username:           normalized.Username,
			Nickname:           normalized.Nickname,
			Mobile:             &mobile,
			Email:              &email,
			Password:           string(passwordHash),
			RegistrationSource: enums.UserRegistrationSourceInvitation,
			ApprovalStatus:     enums.UserApprovalStatusPending,
			MustChangePassword: false,
			Status:             enums.StatusDisabled,
			PasswordSalt:       "",
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserID: constants.SystemAuditUserID, CreateUserName: "invitation_registration",
				UpdatedAt: now, UpdateUserID: constants.SystemAuditUserID, UpdateUserName: "invitation_registration",
			},
		}
		if err := repositories.UserRepository.Create(ctx.Tx, user); err != nil {
			return err
		}
		if err := repositories.TenantInvitationRepository.MarkUsed(ctx.Tx, invitation.ID, now); err != nil {
			return err
		}
		return s.createSecurityLog(ctx.Tx, securityLogInput{
			Meta: meta, Action: enums.TenantRegistrationActionRegister, TenantID: tenant.ID, InvitationID: invitation.ID,
			RequestFingerprint: normalized.Fingerprint, InviteHash: normalized.InviteHash,
			UserID: user.ID, Principal: normalized.Principal, Success: true, Reason: "pending_review",
		})
	})
	if err != nil {
		if replay, replayed, replayErr := s.replayRegistration(meta.RequestID, normalized.Fingerprint); replayed {
			return replay, replayErr
		}
		s.recordRegistrationFailureWithScope(meta, tenant.ID, invitation.ID, normalized.InviteHash, normalized.Principal, normalized.Fingerprint, "registration_transaction_failed")
		slog.Error("tenant invitation registration failed", "requestId", meta.RequestID, "tenantId", tenant.ID, "error", err)
		return nil, errorsx.BusinessError(31, "注册暂时无法完成，请稍后重试")
	}
	return &TenantUserRegistrationResult{User: user, Tenant: tenant}, nil
}

func (s *tenantRegistrationService) Review(req request.ReviewTenantRegistrationRequest, meta PublicRegistrationMeta, operator *dto.AuthPrincipal) (*models.User, error) {
	meta = normalizePublicRegistrationMeta(meta)
	if meta.RequestID == "" {
		return nil, errorsx.InvalidParam("审核请求缺少有效的 X-Request-Id")
	}
	if operator == nil || operator.ActiveTenantID <= 0 || !slices.Contains(operator.Permissions, constants.PermissionTenantRegistrationReview.Code) {
		return nil, errorsx.Forbidden("无权限审核邀请注册账号")
	}
	if req.UserID <= 0 {
		return nil, errorsx.InvalidParam("待审核账号不存在")
	}
	decision := req.Decision
	if decision != enums.TenantRegistrationReviewDecisionApprove && decision != enums.TenantRegistrationReviewDecisionReject {
		return nil, errorsx.InvalidParam("审核决定不合法")
	}
	remark := strings.TrimSpace(req.Remark)
	if len([]rune(remark)) > 500 {
		return nil, errorsx.InvalidParam("审核说明长度不能超过 500 个字符")
	}
	roleIDs, err := normalizeRegistrationRoleIDs(req.RoleIDs)
	if err != nil {
		return nil, err
	}
	if decision == enums.TenantRegistrationReviewDecisionApprove {
		if !slices.Contains(operator.Permissions, constants.PermissionUserAssignRole.Code) {
			return nil, errorsx.Forbidden("审核通过并分配角色需要用户角色分配权限")
		}
		if len(roleIDs) == 0 {
			return nil, errorsx.InvalidParam("审核通过时必须选择至少一个角色")
		}
	} else {
		if len(roleIDs) > 0 {
			return nil, errorsx.InvalidParam("拒绝注册时不能分配角色")
		}
		if remark == "" {
			return nil, errorsx.InvalidParam("拒绝注册时必须填写原因")
		}
	}
	fingerprint := reviewRequestFingerprint(req.UserID, decision, roleIDs, remark, operator)
	if existing := repositories.TenantRegistrationLogRepository.GetByRequestID(sqls.DB(), meta.RequestID); existing != nil {
		if existing.Action != enums.TenantRegistrationActionReview || existing.RequestFingerprint != fingerprint {
			return nil, errorsx.InvalidParam("请求标识已被其他操作使用")
		}
		if current := repositories.UserRepository.GetInTenant(sqls.DB(), req.UserID, operator.ActiveTenantID); current != nil {
			return current, nil
		}
	}

	var reviewed *models.User
	err = s.withWriteTransaction(func(ctx *sqls.TxContext) error {
		tenant, err := repositories.TenantRepository.GetForUpdate(ctx.Tx, operator.ActiveTenantID)
		if err != nil {
			return err
		}
		if tenant == nil || tenant.Status != enums.StatusOk {
			return errorsx.Forbidden("当前接入公司不可用")
		}
		current := repositories.UserRepository.GetInTenant(ctx.Tx, req.UserID, operator.ActiveTenantID)
		if current == nil || current.DeletedAt != nil || current.RegistrationSource != enums.UserRegistrationSourceInvitation {
			return errorsx.InvalidParam("待审核账号不存在")
		}
		if current.ApprovalStatus != enums.UserApprovalStatusPending {
			return errorsx.InvalidParam("该注册申请已经审核")
		}
		approvalStatus := enums.UserApprovalStatusRejected
		userStatus := enums.StatusDisabled
		if decision == enums.TenantRegistrationReviewDecisionApprove {
			approvalStatus = enums.UserApprovalStatusApproved
			userStatus = enums.StatusOk
		}
		if err := UserService.replaceUserRolesDB(ctx.Tx, current.ID, roleIDs, operator); err != nil {
			return err
		}
		now := time.Now()
		if err := repositories.UserRepository.Updates(ctx.Tx, current.ID, map[string]any{
			"approval_status":  approvalStatus,
			"approved_at":      now,
			"approved_by":      operator.UserID,
			"approval_remark":  remark,
			"status":           userStatus,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		if err := repositories.LoginSessionRepository.RevokeActiveByUser(ctx.Tx, current.ID, operator.UserID, operator.Username, now); err != nil {
			return err
		}
		if err := s.createSecurityLog(ctx.Tx, securityLogInput{
			Meta: meta, Action: enums.TenantRegistrationActionReview, TenantID: current.TenantID, UserID: current.ID,
			RequestFingerprint: fingerprint, Success: true, Reason: string(decision),
			OperatorID: operator.UserID, OperatorName: operator.Username,
		}); err != nil {
			return err
		}
		reviewed = repositories.UserRepository.Get(ctx.Tx, current.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reviewed, nil
}

type securityLogInput struct {
	Meta               PublicRegistrationMeta
	Action             enums.TenantRegistrationAction
	RequestFingerprint string
	TenantID           int64
	InvitationID       int64
	InviteHash         string
	UserID             int64
	Principal          string
	Success            bool
	Reason             string
	OperatorID         int64
	OperatorName       string
}

func (s *tenantRegistrationService) createSecurityLog(db *gorm.DB, input securityLogInput) error {
	return repositories.TenantRegistrationLogRepository.Create(db, &models.TenantRegistrationLog{
		RequestID: input.Meta.RequestID, RequestFingerprint: input.RequestFingerprint,
		Action: input.Action, TenantID: input.TenantID, InvitationID: input.InvitationID,
		InviteHash: input.InviteHash, UserID: input.UserID, Principal: input.Principal, Success: input.Success,
		Reason: truncateRunes(input.Reason, 255), ClientIP: input.Meta.ClientIP, UserAgent: input.Meta.UserAgent,
		OperatorID: input.OperatorID, OperatorName: truncateRunes(input.OperatorName, 100), CreatedAt: time.Now(),
	})
}

func (s *tenantRegistrationService) withWriteTransaction(fn func(ctx *sqls.TxContext) error) error {
	db := sqls.DB()
	if db != nil && db.Dialector.Name() == "sqlite" {
		s.sqliteWriteMu.Lock()
		defer s.sqliteWriteMu.Unlock()
	}
	return sqls.WithTransaction(fn)
}

func (s *tenantRegistrationService) resolveActiveInvitation(db *gorm.DB, code string) (*models.TenantInvitation, *models.Tenant) {
	code = normalizeTenantInvitationCode(code)
	if !invitationCodePattern.MatchString(code) {
		return nil, nil
	}
	invitation := repositories.TenantInvitationRepository.GetByCodeHash(db, hashTenantInvitationCode(code))
	if !tenantInvitationUsableAt(invitation, time.Now()) {
		return nil, nil
	}
	current := repositories.TenantInvitationRepository.FindCurrent(db, invitation.TenantID)
	if current == nil || current.ID != invitation.ID {
		return nil, nil
	}
	tenant := repositories.TenantRepository.Get(db, invitation.TenantID)
	if tenant == nil || tenant.Status != enums.StatusOk {
		return nil, nil
	}
	return invitation, tenant
}

func (s *tenantRegistrationService) checkPublicRateLimits(action enums.TenantRegistrationAction, meta PublicRegistrationMeta, inviteHash, principal string) error {
	since := time.Now().Add(-publicRegistrationWindow)
	ipLimit, inviteLimit, principalLimit := publicRegistrationIPLimit, publicRegistrationInviteLimit, publicRegistrationPrincipalLimit
	if action == enums.TenantRegistrationActionValidateInvite {
		since = time.Now().Add(-publicInviteValidationWindow)
		ipLimit, inviteLimit, principalLimit = publicInviteValidationIPLimit, publicInviteValidationCodeLimit, 0
	}
	if count, err := repositories.TenantRegistrationLogRepository.CountRecentByClientIP(sqls.DB(), action, meta.ClientIP, since); err != nil {
		slog.Error("query tenant registration IP rate limit", "requestId", meta.RequestID, "action", action, "error", err)
		return errorsx.BusinessError(30, "注册服务暂时不可用")
	} else if count >= int64(ipLimit) {
		return errorsx.BusinessError(32, "操作过于频繁，请稍后重试")
	}
	if count, err := repositories.TenantRegistrationLogRepository.CountRecentByInviteHash(sqls.DB(), action, inviteHash, since); err != nil {
		slog.Error("query tenant registration invitation rate limit", "requestId", meta.RequestID, "action", action, "error", err)
		return errorsx.BusinessError(30, "注册服务暂时不可用")
	} else if count >= int64(inviteLimit) {
		return errorsx.BusinessError(32, "操作过于频繁，请稍后重试")
	}
	if principalLimit > 0 {
		if count, err := repositories.TenantRegistrationLogRepository.CountRecentByPrincipal(sqls.DB(), action, principal, since); err != nil {
			slog.Error("query tenant registration principal rate limit", "requestId", meta.RequestID, "action", action, "error", err)
			return errorsx.BusinessError(30, "注册服务暂时不可用")
		} else if count >= int64(principalLimit) {
			return errorsx.BusinessError(32, "操作过于频繁，请稍后重试")
		}
	}
	return nil
}

func (s *tenantRegistrationService) replayRegistration(requestID, fingerprint string) (*TenantUserRegistrationResult, bool, error) {
	existing := repositories.TenantRegistrationLogRepository.GetByRequestID(sqls.DB(), requestID)
	if existing == nil {
		return nil, false, nil
	}
	if existing.Action != enums.TenantRegistrationActionRegister || existing.RequestFingerprint != fingerprint {
		return nil, true, errorsx.InvalidParam("请求标识已被其他操作使用")
	}
	if !existing.Success || existing.UserID <= 0 {
		return nil, true, errorsx.InvalidParam("注册信息不可用或已经提交")
	}
	user := repositories.UserRepository.Get(sqls.DB(), existing.UserID)
	tenant := repositories.TenantRepository.Get(sqls.DB(), existing.TenantID)
	if user == nil || tenant == nil {
		return nil, true, errorsx.BusinessError(31, "注册结果暂时无法读取")
	}
	return &TenantUserRegistrationResult{User: user, Tenant: tenant, Replayed: true}, true, nil
}

func (s *tenantRegistrationService) recordRegistrationFailure(meta PublicRegistrationMeta, inviteHash, principal, fingerprint, reason string) {
	s.recordRegistrationFailureWithScope(meta, 0, 0, inviteHash, principal, fingerprint, reason)
}

func (s *tenantRegistrationService) recordRegistrationFailureWithScope(meta PublicRegistrationMeta, tenantID, invitationID int64, inviteHash, principal, fingerprint, reason string) {
	if repositories.TenantRegistrationLogRepository.GetByRequestID(sqls.DB(), meta.RequestID) != nil {
		return
	}
	if err := s.createSecurityLog(sqls.DB(), securityLogInput{
		Meta: meta, Action: enums.TenantRegistrationActionRegister, TenantID: tenantID, InvitationID: invitationID,
		RequestFingerprint: fingerprint, InviteHash: inviteHash, Principal: principal, Reason: reason,
	}); err != nil {
		slog.Error("record tenant registration failure", "requestId", meta.RequestID, "error", err)
	}
}

func (s *tenantRegistrationService) requirePublicRegistrationEnabled() error {
	if !config.Current().TenantRegistration.Enabled {
		return errorsx.BusinessError(33, "邀请注册暂未开放")
	}
	return nil
}

func registrationRequestFingerprint(req *normalizedPublicRegistration) (string, error) {
	if req == nil {
		return "", errorsx.InvalidParam("注册信息不能为空")
	}
	payload, err := json.Marshal(struct {
		Username       string `json:"username"`
		Nickname       string `json:"nickname"`
		Mobile         string `json:"mobile"`
		Email          string `json:"email"`
		Password       string `json:"password"`
		InvitationCode string `json:"invitationCode"`
	}{req.Username, req.Nickname, req.Mobile, req.Email, req.Password, req.InvitationCode})
	if err != nil {
		return "", err
	}
	key, err := tenantInvitationKey(config.Current().Auth.InvitationEncryptionKey)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("tenant-registration-request-v1\x00"))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func reviewRequestFingerprint(userID int64, decision enums.TenantRegistrationReviewDecision, roleIDs []int64, remark string, operator *dto.AuthPrincipal) string {
	payload, _ := json.Marshal(struct {
		TenantID   int64                                  `json:"tenantId"`
		UserID     int64                                  `json:"userId"`
		Decision   enums.TenantRegistrationReviewDecision `json:"decision"`
		RoleIDs    []int64                                `json:"roleIds"`
		Remark     string                                 `json:"remark"`
		OperatorID int64                                  `json:"operatorId"`
	}{operator.ActiveTenantID, userID, decision, roleIDs, remark, operator.UserID})
	sum := sha256.Sum256(append([]byte("tenant-registration-review-v1\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func normalizeRegistrationRoleIDs(values []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, errorsx.InvalidParam("角色 ID 必须为正整数")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	slices.Sort(ret)
	return ret, nil
}

func normalizePublicRegistration(req request.RegisterTenantUserRequest) (*normalizedPublicRegistration, error) {
	if req.TenantID != nil || len(req.RoleIDs) > 0 || req.AgentTeamID != nil || req.StoreID != nil {
		return nil, errorsx.InvalidParam("注册请求不能指定公司、角色、客服组或门店")
	}
	ret := &normalizedPublicRegistration{
		Username: strings.ToLower(strings.TrimSpace(req.Username)), Nickname: strings.TrimSpace(req.Nickname),
		Mobile: strings.TrimSpace(req.Mobile), Email: strings.ToLower(strings.TrimSpace(req.Email)),
		Password: req.Password, InvitationCode: normalizeTenantInvitationCode(req.InvitationCode),
	}
	ret.InviteHash = hashTenantInvitationCode(ret.InvitationCode)
	ret.Principal = hashedRegistrationPrincipal(ret.Username, ret.Mobile, ret.Email)
	if !publicUsernamePattern.MatchString(ret.Username) {
		return nil, errorsx.InvalidParam("用户名需为 3-100 位字母、数字、点、下划线或横线")
	}
	if ret.Nickname == "" || len([]rune(ret.Nickname)) > 100 {
		return nil, errorsx.InvalidParam("姓名不能为空且不能超过 100 个字符")
	}
	if !publicMobilePattern.MatchString(ret.Mobile) {
		return nil, errorsx.InvalidParam("手机号格式不合法")
	}
	if !isPlainEmailAddress(ret.Email) {
		return nil, errorsx.InvalidParam("邮箱格式不合法")
	}
	if len([]byte(ret.Password)) < 8 || len([]byte(ret.Password)) > 72 {
		return nil, errorsx.InvalidParam("密码长度必须为 8-72 个字节")
	}
	if ret.Password != req.ConfirmPassword {
		return nil, errorsx.InvalidParam("两次输入的密码不一致")
	}
	if !invitationCodePattern.MatchString(ret.InvitationCode) {
		return nil, errorsx.InvalidParam("邀请码无效、已失效或公司暂不可用")
	}
	return ret, nil
}

func registrationIdentityExists(db *gorm.DB, req *normalizedPublicRegistration) bool {
	return repositories.UserRepository.GetByUsername(db, req.Username) != nil ||
		repositories.UserRepository.GetByMobile(db, req.Mobile) != nil ||
		repositories.UserRepository.GetByEmail(db, req.Email) != nil
}

func normalizePublicRegistrationMeta(meta PublicRegistrationMeta) PublicRegistrationMeta {
	meta.RequestID = tracex.NormalizeRequestID(meta.RequestID)
	meta.ClientIP = truncateRunes(strings.TrimSpace(meta.ClientIP), 64)
	meta.UserAgent = truncateRunes(strings.TrimSpace(meta.UserAgent), 255)
	return meta
}

func publicRegistrationPrincipal(req request.RegisterTenantUserRequest) string {
	return hashedRegistrationPrincipal(strings.ToLower(strings.TrimSpace(req.Username)), strings.TrimSpace(req.Mobile), strings.ToLower(strings.TrimSpace(req.Email)))
}

func hashedRegistrationPrincipal(username, mobile, email string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + mobile + "\x00" + email))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
