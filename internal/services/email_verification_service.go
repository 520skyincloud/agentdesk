package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	EmailVerificationPurposeLogin       = "login"
	EmailVerificationPurposeRemoteSetup = "remote_setup"
	emailVerificationTTL                = 10 * time.Minute
	emailVerificationCooldown           = time.Minute
	emailVerificationMaxAttempts        = 5
	emailVerificationDailyEmailLimit    = 10
	emailVerificationDailyIPLimit       = 30
)

var EmailVerificationService = newEmailVerificationService(smtpEmailSender{})

type emailVerificationService struct {
	sender emailSender
}

type EmailCodeChallenge struct {
	ExpiresAt        time.Time
	RetryAfterSecond int
}

type VerifiedEmailChallenge struct {
	VerificationToken string
	ExpiresAt         time.Time
}

func newEmailVerificationService(sender emailSender) *emailVerificationService {
	return &emailVerificationService{sender: sender}
}

func (s *emailVerificationService) SendCode(ctx context.Context, purpose, email, scopeToken, requestIP, userAgent string) (*EmailCodeChallenge, error) {
	email, err := normalizeVerificationEmail(email)
	if err != nil {
		return nil, err
	}
	if purpose != EmailVerificationPurposeLogin && purpose != EmailVerificationPurposeRemoteSetup {
		return nil, errorsx.InvalidParam("邮箱验证码用途不正确")
	}
	if purpose == EmailVerificationPurposeLogin {
		user := UserService.GetByEmail(email)
		if user == nil || user.Status != enums.StatusOk || user.EmailVerifiedAt == nil {
			return nil, errorsx.InvalidAccount("该邮箱尚未绑定可登录账号")
		}
	}
	scopeHash := verificationHash(strings.TrimSpace(scopeToken))
	now := time.Now()
	if latest := repositories.EmailVerificationCodeRepository.FindLatestOpen(sqls.DB(), email, purpose, scopeHash); latest != nil && now.Sub(latest.CreatedAt) < emailVerificationCooldown {
		return nil, errorsx.InvalidParam(fmt.Sprintf("验证码发送过于频繁，请 %d 秒后重试", int(emailVerificationCooldown.Seconds()-now.Sub(latest.CreatedAt).Seconds())))
	}
	since := now.Add(-24 * time.Hour)
	if repositories.EmailVerificationCodeRepository.CountCreatedSince(sqls.DB(), email, "", since) >= emailVerificationDailyEmailLimit {
		return nil, errorsx.InvalidParam("该邮箱今日验证码发送次数已达上限")
	}
	if requestIP != "" && repositories.EmailVerificationCodeRepository.CountCreatedSince(sqls.DB(), "", requestIP, since) >= emailVerificationDailyIPLimit {
		return nil, errorsx.InvalidParam("当前网络今日验证码发送次数已达上限")
	}
	code, err := randomNumericCode()
	if err != nil {
		return nil, err
	}
	salt, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	item := &models.EmailVerificationCode{
		Email:          email,
		Purpose:        purpose,
		ScopeTokenHash: scopeHash,
		CodeSalt:       salt,
		CodeHash:       verificationHash(salt + ":" + code),
		ExpiresAt:      now.Add(emailVerificationTTL),
		MaxAttempts:    emailVerificationMaxAttempts,
		RequestIP:      strings.TrimSpace(requestIP),
		UserAgent:      truncateText(strings.TrimSpace(userAgent), 500),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := repositories.EmailVerificationCodeRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	if err := s.sender.SendVerificationCode(ctx, email, code, purpose); err != nil {
		failedAt := time.Now()
		_ = repositories.EmailVerificationCodeRepository.Updates(sqls.DB(), item.ID, map[string]any{
			"consumed_at": failedAt,
			"last_error":  truncateText(err.Error(), 500),
			"updated_at":  failedAt,
		})
		return nil, err
	}
	return &EmailCodeChallenge{ExpiresAt: item.ExpiresAt, RetryAfterSecond: int(emailVerificationCooldown.Seconds())}, nil
}

func (s *emailVerificationService) VerifyCode(purpose, email, scopeToken, code string) (*VerifiedEmailChallenge, error) {
	email, err := normalizeVerificationEmail(email)
	if err != nil {
		return nil, err
	}
	item := repositories.EmailVerificationCodeRepository.FindLatestOpen(sqls.DB(), email, purpose, verificationHash(strings.TrimSpace(scopeToken)))
	if item == nil || time.Now().After(item.ExpiresAt) || item.VerifiedAt != nil || item.AttemptCount >= item.MaxAttempts {
		return nil, errorsx.InvalidParam("验证码无效或已过期")
	}
	expected := verificationHash(item.CodeSalt + ":" + strings.TrimSpace(code))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(item.CodeHash)) != 1 {
		attempts := item.AttemptCount + 1
		_ = repositories.EmailVerificationCodeRepository.Updates(sqls.DB(), item.ID, map[string]any{"attempt_count": attempts, "last_error": "验证码不匹配", "updated_at": time.Now()})
		if attempts >= item.MaxAttempts {
			return nil, errorsx.InvalidParam("验证码错误次数过多，请重新获取")
		}
		return nil, errorsx.InvalidParam("验证码不正确")
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := repositories.EmailVerificationCodeRepository.Updates(sqls.DB(), item.ID, map[string]any{
		"verification_token_hash": verificationHash(token),
		"verified_at":             now,
		"last_error":              "",
		"updated_at":              now,
	}); err != nil {
		return nil, err
	}
	return &VerifiedEmailChallenge{VerificationToken: token, ExpiresAt: item.ExpiresAt}, nil
}

func (s *emailVerificationService) ConsumeVerifiedToken(db *gorm.DB, purpose, email, scopeToken, verificationToken string) error {
	email, err := normalizeVerificationEmail(email)
	if err != nil {
		return err
	}
	item := repositories.EmailVerificationCodeRepository.FindVerifiedByTokenForUpdate(db, verificationHash(strings.TrimSpace(verificationToken)))
	if item == nil || item.Purpose != purpose || item.Email != email || item.ScopeTokenHash != verificationHash(strings.TrimSpace(scopeToken)) || time.Now().After(item.ExpiresAt) {
		return errorsx.InvalidParam("邮箱验证状态无效或已过期")
	}
	now := time.Now()
	return repositories.EmailVerificationCodeRepository.Updates(db, item.ID, map[string]any{"consumed_at": now, "updated_at": now})
}

func (s *emailVerificationService) LoginWithCode(ctx context.Context, email, code, clientIP, userAgent string, authCfg config.AuthConfig) (*response.LoginResponse, error) {
	verified, err := s.VerifyCode(EmailVerificationPurposeLogin, email, "", code)
	if err != nil {
		return nil, err
	}
	email, _ = normalizeVerificationEmail(email)
	var ret *response.LoginResponse
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := s.ConsumeVerifiedToken(tx.Tx, EmailVerificationPurposeLogin, email, "", verified.VerificationToken); err != nil {
			return err
		}
		user := repositories.UserRepository.GetByEmail(tx.Tx, email)
		if user == nil || user.Status != enums.StatusOk || user.EmailVerifiedAt == nil {
			return errorsx.InvalidAccount("该邮箱尚未绑定可登录账号")
		}
		var issueErr error
		ret, issueErr = AuthService.issueTokens(tx, user, clientIP, userAgent, authCfg)
		if issueErr != nil {
			return issueErr
		}
		now := time.Now()
		return repositories.UserRepository.Updates(tx.Tx, user.ID, map[string]any{"last_login_at": now, "last_login_ip": clientIP, "updated_at": now, "update_user_id": user.ID, "update_user_name": user.Username})
	})
	if err != nil {
		return nil, err
	}
	_ = AuthService.createLoginCredentialLog(email, ret.User.ID, true, clientIP, userAgent, "email_code")
	return ret, nil
}

func normalizeVerificationEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || strings.ToLower(parsed.Address) != value || len(value) > 100 {
		return "", errorsx.InvalidParam("邮箱格式不正确")
	}
	return value, nil
}

func randomNumericCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func randomHex(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func verificationHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
