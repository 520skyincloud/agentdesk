package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"
	"agent-desk/internal/wxwork"
	"strings"
	"time"

	"github.com/mlogclub/simple/common/jsons"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var WxWorkLoginService = &wxWorkLoginService{}

type wxWorkLoginService struct {
}

func (s *wxWorkLoginService) BuildWxWorkLoginURL(next string) (string, error) {
	if !wxwork.Enabled() {
		return "", errorsx.BusinessError(1, "企业微信登录未启用")
	}
	state, err := wxwork.CreateState(next)
	if err != nil {
		return "", err
	}
	return wxwork.BuildLoginURL(state)
}

func (s *wxWorkLoginService) BuildWxWorkQRCodeLoginURL(next string) (string, error) {
	if !wxwork.Enabled() {
		return "", errorsx.BusinessError(1, "企业微信登录未启用")
	}
	state, err := wxwork.CreateState(next)
	if err != nil {
		return "", err
	}
	return wxwork.BuildQRCodeLoginURL(state)
}

func (s *wxWorkLoginService) LoginByWxWork(code, state string, authCfg config.AuthConfig, clientIP, userAgent string) (string, string, error) {
	next, err := wxwork.ParseState(state)
	if err != nil {
		return "", "", errorsx.Unauthorized("企业微信登录状态无效或已过期")
	}
	profile, err := wxwork.GetUserDetail(code)
	if err != nil {
		return "", "", err
	}
	loginResp, err := s.loginWithWxWorkProfile(profile, authCfg, clientIP, userAgent)
	if err != nil {
		return "", "", err
	}
	ticket, err := wxwork.IssueLoginTicket(loginResp)
	if err != nil {
		return "", "", err
	}
	return ticket, next, nil
}

func (s *wxWorkLoginService) ExchangeWxWorkLoginTicket(ticket string) (*response.LoginResponse, error) {
	return wxwork.ConsumeLoginTicket(ticket)
}

func (s *wxWorkLoginService) loginWithWxWorkProfile(profile *wxwork.LoginUser, authCfg config.AuthConfig, clientIP, userAgent string) (*response.LoginResponse, error) {
	if profile == nil || strings.TrimSpace(profile.UserID) == "" {
		return nil, errorsx.BusinessError(2, "企业微信用户信息不存在")
	}

	var ret *response.LoginResponse
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var (
			identity = repositories.UserIdentityRepository.GetBy(ctx.Tx, enums.ThirdProviderWxWork, profile.CorpID, profile.UserID)
			user     *models.User
			err      error
		)
		if identity == nil {
			user, identity, err = s.bindExistingWxWorkUser(ctx, profile)
			if err != nil {
				return err
			}
		} else {
			if identity.Status != enums.StatusOk {
				return errorsx.BusinessError(3, "当前企业微信绑定已停用")
			}
			user = repositories.UserRepository.Get(ctx.Tx, identity.UserID)
			if user == nil {
				return errorsx.BusinessError(4, "企业微信账号绑定的系统用户不存在")
			}
		}

		if user.Status != enums.StatusOk {
			return errorsx.Unauthorized("当前系统账号已被禁用")
		}

		if err = repositories.UserRepository.Updates(ctx.Tx, user.ID, map[string]any{
			"nickname":         s.resolveWxWorkNickname(user.Nickname, profile),
			"avatar":           s.resolveWxWorkAvatar(user.Avatar, profile),
			"last_login_at":    time.Now(),
			"last_login_ip":    clientIP,
			"update_user_id":   user.ID,
			"update_user_name": user.Username,
			"updated_at":       time.Now(),
		}); err != nil {
			return err
		}

		if err = repositories.UserIdentityRepository.Updates(ctx.Tx, identity.ID, map[string]any{
			"raw_profile":      jsons.ToJsonStr(profile),
			"last_auth_at":     time.Now(),
			"status":           enums.StatusOk,
			"update_user_id":   user.ID,
			"update_user_name": user.Username,
			"updated_at":       time.Now(),
		}); err != nil {
			return err
		}

		ret, err = AuthService.issueTokens(ctx, user, clientIP, userAgent, authCfg)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (s *wxWorkLoginService) bindExistingWxWorkUser(ctx *sqls.TxContext, profile *wxwork.LoginUser) (*models.User, *models.UserIdentity, error) {
	email := strings.ToLower(strings.TrimSpace(s.firstNonEmpty(profile.Email, profile.BizMail)))
	if email == "" {
		return nil, nil, errorsx.Unauthorized("企业微信账号缺少邮箱，无法匹配已注册系统账号")
	}
	now := time.Now()
	user := repositories.UserRepository.GetByEmail(ctx.Tx, email)
	if user == nil || user.EmailVerifiedAt == nil {
		return nil, nil, errorsx.Unauthorized("未找到已验证邮箱的系统账号，请先由公司主管创建账号或邀请注册")
	}
	if user.Status != enums.StatusOk || user.DeletedAt != nil {
		return nil, nil, errorsx.Unauthorized("该邮箱绑定的系统账号已被禁用或尚未通过审核")
	}
	identity, err := s.createWxWorkIdentity(ctx.Tx, user, profile, now)
	if err != nil {
		return nil, nil, err
	}
	return user, identity, nil
}

func (s *wxWorkLoginService) createWxWorkIdentity(tx *gorm.DB, user *models.User, profile *wxwork.LoginUser, now time.Time) (*models.UserIdentity, error) {
	identity := &models.UserIdentity{
		UserID:         user.ID,
		Provider:       enums.ThirdProviderWxWork,
		ProviderUserID: strings.TrimSpace(profile.UserID),
		ProviderCorpID: strings.TrimSpace(profile.CorpID),
		ProviderName:   enums.GetThirdProviderLabel(enums.ThirdProviderWxWork),
		RawProfile:     jsons.ToJsonStr(profile),
		Status:         enums.StatusOk,
		LastAuthAt:     &now,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   user.ID,
			CreateUserName: user.Username,
			UpdatedAt:      now,
			UpdateUserID:   user.ID,
			UpdateUserName: user.Username,
		},
	}
	if unionID := strings.TrimSpace(profile.OpenID); unionID != "" {
		identity.ProviderUnionID = &unionID
	}
	if err := repositories.UserIdentityRepository.Create(tx, identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func (s *wxWorkLoginService) resolveWxWorkNickname(current string, profile *wxwork.LoginUser) string {
	if profile != nil {
		if name := strings.TrimSpace(profile.Name); name != "" {
			return name
		}
	}
	if current = strings.TrimSpace(current); current != "" {
		return current
	}
	if profile != nil {
		return strings.TrimSpace(profile.UserID)
	}
	return ""
}

func (s *wxWorkLoginService) resolveWxWorkAvatar(current string, profile *wxwork.LoginUser) string {
	if profile != nil {
		if avatar := strings.TrimSpace(profile.Avatar); avatar != "" {
			return avatar
		}
	}
	return strings.TrimSpace(current)
}

func (s *wxWorkLoginService) firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
