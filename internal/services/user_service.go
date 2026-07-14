package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var UserService = newUserService()

func newUserService() *userService {
	return &userService{}
}

type userService struct {
}

func (s *userService) Get(id int64) *models.User {
	return repositories.UserRepository.Get(sqls.DB(), id)
}

func (s *userService) GetInTenant(id, tenantID int64) *models.User {
	return repositories.UserRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *userService) GetInScope(id int64, operator *dto.AuthPrincipal) *models.User {
	if id <= 0 {
		return nil
	}
	cnd := s.ApplyTenantScope(sqls.NewCnd().Eq("id", id), operator)
	return repositories.UserRepository.FindOne(sqls.DB(), cnd)
}

func (s *userService) Take(where ...interface{}) *models.User {
	return repositories.UserRepository.Take(sqls.DB(), where...)
}

func (s *userService) Find(cnd *sqls.Cnd) []models.User {
	return repositories.UserRepository.Find(sqls.DB(), cnd)
}

func (s *userService) FindOne(cnd *sqls.Cnd) *models.User {
	return repositories.UserRepository.FindOne(sqls.DB(), cnd)
}

func (s *userService) FindPageByParams(params *params.QueryParams) (list []models.User, paging *sqls.Paging) {
	return repositories.UserRepository.FindPageByParams(sqls.DB(), params)
}

func (s *userService) FindPageByCnd(cnd *sqls.Cnd) (list []models.User, paging *sqls.Paging) {
	return repositories.UserRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *userService) Count(cnd *sqls.Cnd) int64 {
	return repositories.UserRepository.Count(sqls.DB(), cnd)
}

func (s *userService) FindByIds(ids []int64) []models.User {
	return repositories.UserRepository.FindByIds(sqls.DB(), ids)
}

func (s *userService) FindByIdsInTenant(ids []int64, tenantID int64) []models.User {
	return repositories.UserRepository.FindByIdsInTenant(sqls.DB(), ids, tenantID)
}

func (s *userService) Create(t *models.User) error {
	return repositories.UserRepository.Create(sqls.DB(), t)
}

func (s *userService) Update(t *models.User) error {
	return repositories.UserRepository.Update(sqls.DB(), t)
}

func (s *userService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.UserRepository.Updates(sqls.DB(), id, columns)
}

func (s *userService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.UserRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *userService) GetByUsername(username string) *models.User {
	return repositories.UserRepository.GetByUsername(sqls.DB(), username)
}

func (s *userService) GetByMobile(mobile string) *models.User {
	return repositories.UserRepository.GetByMobile(sqls.DB(), mobile)
}

func (s *userService) GetByEmail(email string) *models.User {
	return repositories.UserRepository.GetByEmail(sqls.DB(), email)
}

func (s *userService) HasRole(userID int64, roleCode string) bool {
	if userID <= 0 || strings.TrimSpace(roleCode) == "" {
		return false
	}
	role := repositories.RoleRepository.GetByCode(sqls.DB(), strings.TrimSpace(roleCode))
	if role == nil || role.Status != enums.StatusOk {
		return false
	}
	return repositories.UserRoleRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("user_id", userID).Eq("role_id", role.ID)) != nil
}

func (s *userService) CreateUser(req request.CreateUserRequest, operator *dto.AuthPrincipal) (*models.User, string, error) {
	tenantID, registrationSource, err := s.resolveNewUserTenant(operator)
	if err != nil {
		return nil, "", err
	}
	var user *models.User
	var plain string
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		user, plain, err = s.createManagedUserDB(ctx.Tx, req, tenantID, registrationSource, req.RoleIDs, operator)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return user, plain, nil
}

func (s *userService) createManagedUserDB(db *gorm.DB, req request.CreateUserRequest, tenantID int64, registrationSource enums.UserRegistrationSource, roleIDs []int64, operator *dto.AuthPrincipal) (*models.User, string, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, "", errorsx.InvalidParam("用户名不能为空")
	}
	if repositories.UserRepository.GetByUsername(db, username) != nil {
		return nil, "", errorsx.InvalidParam("用户名已存在")
	}

	mobile := utils.NormalizeNullableString(req.Mobile)
	email := utils.NormalizeNullableString(req.Email)
	if email != nil {
		normalizedEmail := strings.ToLower(*email)
		email = &normalizedEmail
	}
	if mobile != nil && repositories.UserRepository.GetByMobile(db, *mobile) != nil {
		return nil, "", errorsx.InvalidParam("手机号已存在")
	}
	if email != nil && repositories.UserRepository.GetByEmail(db, *email) != nil {
		return nil, "", errorsx.InvalidParam("邮箱已存在")
	}

	plain, err := utils.GenerateRandomPassword(12)
	if err != nil {
		return nil, "", err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	user := &models.User{
		TenantID:           tenantID,
		Username:           username,
		Nickname:           strings.TrimSpace(req.Nickname),
		Password:           string(passwordHash),
		Avatar:             strings.TrimSpace(req.Avatar),
		Mobile:             mobile,
		Email:              email,
		RegistrationSource: registrationSource,
		ApprovalStatus:     enums.UserApprovalStatusApproved,
		ApprovedAt:         &now,
		ApprovedBy:         operator.UserID,
		MustChangePassword: true,
		Status:             enums.StatusOk,
		Remark:             strings.TrimSpace(req.Remark),
		PasswordSalt:       "",
		AuditFields:        utils.BuildAuditFields(operator),
	}
	if user.Nickname == "" {
		user.Nickname = username
	}

	if err = repositories.UserRepository.Create(db, user); err != nil {
		return nil, "", err
	}
	if err = s.replaceUserRolesDB(db, user.ID, roleIDs, operator); err != nil {
		return nil, "", err
	}
	return user, plain, nil
}

func (s *userService) UpdateUser(req request.UpdateUserRequest, operator *dto.AuthPrincipal) error {
	var user *models.User
	if operator != nil && req.ID == operator.UserID {
		user = s.Get(req.ID)
	} else {
		user = s.GetInScope(req.ID, operator)
	}
	if user == nil || user.DeletedAt != nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if req.ID != operator.UserID {
		if err := s.EnsureCanManageUser(operator, user); err != nil {
			return err
		}
	}

	mobile := utils.NormalizeNullableString(req.Mobile)
	email := utils.NormalizeNullableString(req.Email)
	if mobile != nil {
		if existed := s.GetByMobile(*mobile); existed != nil && existed.ID != req.ID {
			return errorsx.InvalidParam("手机号已存在")
		}
	}
	if email != nil {
		if existed := s.GetByEmail(*email); existed != nil && existed.ID != req.ID {
			return errorsx.InvalidParam("邮箱已存在")
		}
	}

	return s.Updates(req.ID, map[string]any{
		"nickname":         strings.TrimSpace(req.Nickname),
		"avatar":           strings.TrimSpace(req.Avatar),
		"mobile":           mobile,
		"email":            email,
		"remark":           strings.TrimSpace(req.Remark),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *userService) DeleteUser(id int64, operator *dto.AuthPrincipal) error {
	user := s.GetInScope(id, operator)
	if user == nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if err := s.EnsureCanManageUser(operator, user); err != nil {
		return err
	}
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.UserRepository.Take(ctx.Tx, "id = ? AND tenant_id = ?", id, user.TenantID)
		if current == nil || current.DeletedAt != nil {
			return errorsx.InvalidParam("用户不存在")
		}
		if err := s.ensureDeleteDependenciesCleared(ctx.Tx, current); err != nil {
			return err
		}
		return repositories.UserRepository.Updates(ctx.Tx, id, map[string]any{
			"status":           enums.StatusDisabled,
			"deleted_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		})
	}); err != nil {
		return err
	}
	return LoginSessionService.RevokeByUser(id, operator.UserID, operator.Username)
}

func (s *userService) ensureDeleteDependenciesCleared(db *gorm.DB, user *models.User) error {
	if user == nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if repositories.ConversationRepository.Take(
		db,
		"tenant_id = ? AND current_assignee_id = ? AND status <> ?",
		user.TenantID,
		user.ID,
		enums.IMConversationStatusClosed,
	) != nil {
		return errorsx.InvalidParam("用户仍有未完成会话，请先完成转派或关闭")
	}
	if repositories.AgentTeamRepository.Take(
		db,
		"tenant_id = ? AND leader_user_id = ? AND status <> ?",
		user.TenantID,
		user.ID,
		enums.StatusDeleted,
	) != nil {
		return errorsx.InvalidParam("用户仍是综合客服组组长，请先更换组长")
	}
	if repositories.AgentTeamSquadRepository.Take(
		db,
		"tenant_id = ? AND leader_user_id = ? AND status <> ?",
		user.TenantID,
		user.ID,
		enums.StatusDeleted,
	) != nil {
		return errorsx.InvalidParam("用户仍是客服小组组长，请先更换组长")
	}
	if repositories.AgentProfileRepository.Take(
		db,
		"tenant_id = ? AND user_id = ? AND status <> ?",
		user.TenantID,
		user.ID,
		enums.StatusDeleted,
	) != nil {
		return errorsx.InvalidParam("用户仍有关联客服档案，请先删除客服档案")
	}
	if repositories.StoreStaffBindingRepository.Take(
		db,
		"tenant_id = ? AND user_id = ? AND status <> ?",
		user.TenantID,
		user.ID,
		enums.StatusDeleted,
	) != nil {
		return errorsx.InvalidParam("用户仍有关联门店员工身份，请先解除绑定")
	}
	return nil
}

func (s *userService) UpdateStatus(id int64, status int, operator *dto.AuthPrincipal) error {
	user := s.GetInScope(id, operator)
	if user == nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if err := s.EnsureCanManageUser(operator, user); err != nil {
		return err
	}
	if !slices.Contains(enums.StatusValues, enums.Status(status)) {
		return errorsx.InvalidParam("状态值不合法")
	}
	if err := s.Updates(id, map[string]any{
		"status":           status,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	}); err != nil {
		return err
	}
	if status == int(enums.StatusDisabled) || status == int(enums.StatusDeleted) {
		return LoginSessionService.RevokeByUser(id, operator.UserID, operator.Username)
	}
	return nil
}

func (s *userService) ResetPassword(userID int64, operator *dto.AuthPrincipal) (string, error) {
	user := s.GetInScope(userID, operator)
	if user == nil || user.DeletedAt != nil {
		return "", errorsx.InvalidParam("用户不存在")
	}
	if err := s.EnsureCanManageUser(operator, user); err != nil {
		return "", err
	}
	password, err := utils.GenerateRandomPassword(12)
	if err != nil {
		return "", err
	}
	if err = s.changePassword(userID, password, operator); err != nil {
		return "", err
	}
	return password, nil
}

func (s *userService) ChangeOwnPassword(password string, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return s.changePassword(operator.UserID, password, operator)
}

func (s *userService) AssignRoles(userID int64, roleIDs []int64, operator *dto.AuthPrincipal) error {
	user := s.GetInScope(userID, operator)
	if user == nil || user.DeletedAt != nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if err := s.EnsureCanManageUser(operator, user); err != nil {
		return err
	}
	if err := s.replaceUserRoles(userID, roleIDs, operator); err != nil {
		return err
	}
	return LoginSessionService.RevokeByUser(userID, operator.UserID, operator.Username)
}

func (s *userService) replaceUserRoles(userID int64, roleIDs []int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.replaceUserRolesDB(ctx.Tx, userID, roleIDs, operator)
	})
}

func (s *userService) replaceUserRolesDB(db *gorm.DB, userID int64, roleIDs []int64, operator *dto.AuthPrincipal) error {
	user := repositories.UserRepository.Get(db, userID)
	if user == nil || user.DeletedAt != nil {
		return errorsx.InvalidParam("用户不存在")
	}
	seen := make(map[int64]struct{}, len(roleIDs))
	roles := make([]*models.Role, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		if _, exists := seen[roleID]; exists {
			continue
		}
		seen[roleID] = struct{}{}
		role := repositories.RoleRepository.Get(db, roleID)
		if role == nil {
			return errorsx.InvalidParam("角色不存在")
		}
		if role.Status != enums.StatusOk {
			return errorsx.InvalidParam("禁用角色不允许分配")
		}
		if !RoleService.CanAssignRole(operator, role) {
			return errorsx.Forbidden("不能分配同级或更高等级角色")
		}
		if user.TenantID > 0 && role.Scope != constants.RoleScopeTenant {
			return errorsx.Forbidden("租户账号只能分配公司角色")
		}
		if user.TenantID == 0 && role.Scope != constants.RoleScopePlatform {
			return errorsx.Forbidden("平台账号只能分配平台角色")
		}
		roles = append(roles, role)
	}
	if err := s.ensureRetainedRoleDependenciesDB(db, user, roles); err != nil {
		return err
	}
	if err := db.Where("user_id = ?", userID).Delete(&models.UserRole{}).Error; err != nil {
		return err
	}
	for _, role := range roles {
		relation := &models.UserRole{
			UserID:      userID,
			RoleID:      role.ID,
			AuditFields: utils.BuildAuditFields(operator),
		}
		if err := db.Create(relation).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *userService) ensureRetainedRoleDependenciesDB(db *gorm.DB, user *models.User, roles []*models.Role) error {
	if user == nil || user.TenantID <= 0 {
		return nil
	}
	assigned := make(map[string]struct{})
	relations := repositories.UserRoleRepository.Find(db, sqls.NewCnd().Eq("user_id", user.ID))
	for i := range relations {
		role := repositories.RoleRepository.Get(db, relations[i].RoleID)
		if role != nil {
			assigned[role.Code] = struct{}{}
		}
	}
	retained := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if role != nil {
			retained[role.Code] = struct{}{}
		}
	}
	removesRole := func(code string) bool {
		_, hadRole := assigned[code]
		_, keepsRole := retained[code]
		return hadRole && !keepsRole
	}
	if removesRole(constants.RoleCodeCsUser) {
		if repositories.ConversationRepository.Take(db,
			"tenant_id = ? AND current_assignee_id = ? AND status <> ?",
			user.TenantID, user.ID, enums.IMConversationStatusClosed,
		) != nil {
			return errorsx.InvalidParam("用户仍有未完成会话，不能移除客服角色")
		}
		if repositories.AgentProfileRepository.Take(db,
			"tenant_id = ? AND user_id = ? AND status <> ?",
			user.TenantID, user.ID, enums.StatusDeleted,
		) != nil {
			return errorsx.InvalidParam("用户仍有关联客服档案，请先删除客服档案再移除客服角色")
		}
	}
	if removesRole(constants.RoleCodeCsTeamLeader) {
		if repositories.AgentTeamRepository.Take(db,
			"tenant_id = ? AND leader_user_id = ? AND status <> ?",
			user.TenantID, user.ID, enums.StatusDeleted,
		) != nil {
			return errorsx.InvalidParam("用户仍是综合客服组组长，请先更换组长再移除客服组长角色")
		}
	}
	if removesRole(constants.RoleCodeStoreStaff) {
		if repositories.StoreStaffBindingRepository.Take(db,
			"tenant_id = ? AND user_id = ? AND status <> ?",
			user.TenantID, user.ID, enums.StatusDeleted,
		) != nil {
			return errorsx.InvalidParam("用户仍有关联门店员工身份，请先解除绑定再移除门店员工角色")
		}
	}
	return nil
}

func (s *userService) CanManageUser(operator *dto.AuthPrincipal, user *models.User) bool {
	if operator == nil || user == nil || operator.UserID <= 0 || user.ID == operator.UserID {
		return false
	}
	if RoleService.HighestAuthorityLevel(operator) <= 0 {
		return false
	}
	if !s.CanViewUser(operator, user) {
		return false
	}
	relations := UserRoleService.Find(sqls.NewCnd().Eq("user_id", user.ID))
	for i := range relations {
		role := RoleService.Get(relations[i].RoleID)
		if role != nil && !RoleService.CanManageRole(operator, role) {
			return false
		}
	}
	return true
}

func (s *userService) CanViewUser(operator *dto.AuthPrincipal, user *models.User) bool {
	if operator == nil || user == nil {
		return false
	}
	if operator.IsPlatformAccount {
		if operator.ActiveTenantID > 0 {
			return user.TenantID == operator.ActiveTenantID
		}
		return user.TenantID == 0
	}
	return operator.TenantID > 0 && user.TenantID == operator.TenantID
}

func (s *userService) ApplyTenantScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	if operator == nil {
		return cnd.Where("1 = 0")
	}
	if operator.IsPlatformAccount {
		if operator.ActiveTenantID > 0 {
			return cnd.Eq("tenant_id", operator.ActiveTenantID)
		}
		return cnd.Eq("tenant_id", 0)
	}
	if operator.TenantID <= 0 {
		return cnd.Where("1 = 0")
	}
	return cnd.Eq("tenant_id", operator.TenantID)
}

func (s *userService) resolveNewUserTenant(operator *dto.AuthPrincipal) (int64, enums.UserRegistrationSource, error) {
	if operator == nil {
		return 0, "", errorsx.Unauthorized("未登录或登录已过期")
	}
	if operator.IsPlatformAccount {
		if operator.ActiveTenantID > 0 {
			return operator.ActiveTenantID, enums.UserRegistrationSourceTenant, nil
		}
		return 0, enums.UserRegistrationSourcePlatform, nil
	}
	if operator.TenantID <= 0 {
		return 0, "", errorsx.Forbidden("账号尚未归属接入公司")
	}
	return operator.TenantID, enums.UserRegistrationSourceTenant, nil
}

func (s *userService) EnsureCanManageUser(operator *dto.AuthPrincipal, user *models.User) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if user != nil && user.ID == operator.UserID {
		return errorsx.Forbidden("不能通过用户管理修改自己的账号")
	}
	if !s.CanManageUser(operator, user) {
		return errorsx.Forbidden("目标账号包含无权管理的角色")
	}
	return nil
}

func (s *userService) changePassword(userID int64, password string, operator *dto.AuthPrincipal) error {
	user := s.Get(userID)
	if user == nil || user.DeletedAt != nil {
		return errorsx.InvalidParam("用户不存在")
	}
	if strings.TrimSpace(password) == "" {
		return errorsx.InvalidParam("新密码不能为空")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now()
	if err = s.Updates(userID, map[string]any{
		"password":         string(passwordHash),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       now,
	}); err != nil {
		return err
	}
	return LoginSessionService.RevokeByUser(userID, operator.UserID, operator.Username)
}
