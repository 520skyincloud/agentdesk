package oidcclient

import (
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/errorsx"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	StateTTL             = 5 * time.Minute
	LoginTicketTTL       = 1 * time.Minute
	defaultLoginNextPath = "/dashboard"
)

var (
	oidcCfg          config.OIDCConfig
	provider         *gooidc.Provider
	oauthConfig      *oauth2.Config
	idTokenVerifier  *gooidc.IDTokenVerifier
	loginTicketStore sync.Map
)

type Profile struct {
	Subject           string `json:"sub"`
	Email             string `json:"email,omitempty"`
	PreferredUsername string `json:"preferred_username,omitempty"`
	Name              string `json:"name,omitempty"`
	Picture           string `json:"picture,omitempty"`
	RawProfile        string `json:"-"`
}

type statePayload struct {
	Next      string `json:"next"`
	Nonce     string `json:"nonce"`
	ExpiredAt int64  `json:"expiredAt"`
}

type loginTicket struct {
	Response  *response.LoginResponse
	ExpiredAt time.Time
}

func Init(ctx context.Context) error {
	provider = nil
	oauthConfig = nil
	idTokenVerifier = nil
	oidcCfg = config.OIDCConfig{}

	cfg := config.Current().OIDC
	if !cfg.Enabled {
		return nil
	}
	oidcCfg = cfg
	if strings.TrimSpace(cfg.Issuer) == "" {
		return fmt.Errorf("OIDC issuer 未配置")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return fmt.Errorf("OIDC clientId 未配置")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return fmt.Errorf("OIDC clientSecret 未配置")
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		return fmt.Errorf("OIDC redirectUrl 未配置")
	}

	p, err := gooidc.NewProvider(ctx, strings.TrimSpace(cfg.Issuer))
	if err != nil {
		return err
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	provider = p
	oauthConfig = &oauth2.Config{
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Endpoint:     p.Endpoint(),
		RedirectURL:  strings.TrimSpace(cfg.RedirectURL),
		Scopes:       scopes,
	}
	idTokenVerifier = p.Verifier(&gooidc.Config{ClientID: strings.TrimSpace(cfg.ClientID)})
	return nil
}

func Enabled() bool {
	return oidcCfg.Enabled && provider != nil && oauthConfig != nil && idTokenVerifier != nil
}

func BuildAuthCodeURL(next string) (string, error) {
	if !Enabled() {
		return "", errorsx.BusinessError(1, "OIDC 登录未启用")
	}
	state, err := CreateState(next)
	if err != nil {
		return "", err
	}
	return oauthConfig.AuthCodeURL(state), nil
}

func ExchangeCode(ctx context.Context, code string) (*Profile, error) {
	if !Enabled() {
		return nil, errorsx.BusinessError(1, "OIDC 登录未启用")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errorsx.InvalidParam("OIDC 授权 code 不能为空")
	}
	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		return nil, errorsx.Unauthorized("OIDC id_token 不存在")
	}
	idToken, err := idTokenVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, err
	}
	profile, err := profileFromIDToken(idToken)
	if err != nil {
		return nil, err
	}
	userInfo, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err == nil && userInfo != nil && strings.TrimSpace(userInfo.Subject) == profile.Subject {
		if mergedProfile, mergeErr := profileFromUserInfo(userInfo, profile); mergeErr == nil {
			profile = mergedProfile
		}
	}
	return profile, nil
}

func CreateState(next string) (string, error) {
	secret := stateSecret()
	if secret == "" {
		return "", errorsx.BusinessError(2, "OIDC 登录密钥未配置")
	}
	nonce, err := randomToken("os_")
	if err != nil {
		return "", err
	}
	payload := statePayload{
		Next:      sanitizeNextPath(next),
		Nonce:     nonce,
		ExpiredAt: time.Now().Add(StateTTL).Unix(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + signState(encoded, secret), nil
}

func ParseState(state string) (string, error) {
	secret := stateSecret()
	if secret == "" {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	parts := strings.Split(strings.TrimSpace(state), ".")
	if len(parts) != 2 {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	if !hmac.Equal([]byte(parts[1]), []byte(signState(parts[0], secret))) {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	payload := statePayload{}
	if err = json.Unmarshal(body, &payload); err != nil {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	if payload.ExpiredAt <= time.Now().Unix() {
		return "", errorsx.Unauthorized("OIDC 登录状态无效或已过期")
	}
	return sanitizeNextPath(payload.Next), nil
}

func IssueLoginTicket(loginResp *response.LoginResponse) (string, error) {
	if loginResp == nil {
		return "", fmt.Errorf("登录结果不能为空")
	}
	ticket, err := randomToken("olt_")
	if err != nil {
		return "", err
	}
	cleanupExpiredLoginTickets()
	loginTicketStore.Store(ticket, loginTicket{
		Response:  loginResp,
		ExpiredAt: time.Now().Add(LoginTicketTTL),
	})
	return ticket, nil
}

func ConsumeLoginTicket(ticket string) (*response.LoginResponse, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, errorsx.InvalidParam("ticket 不能为空")
	}
	value, ok := loginTicketStore.LoadAndDelete(ticket)
	if !ok {
		return nil, errorsx.Unauthorized("登录票据无效或已过期")
	}
	record, ok := value.(loginTicket)
	if !ok || record.Response == nil || time.Now().After(record.ExpiredAt) {
		return nil, errorsx.Unauthorized("登录票据无效或已过期")
	}
	return record.Response, nil
}

func profileFromIDToken(idToken *gooidc.IDToken) (*Profile, error) {
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(claims)
	profile := &Profile{
		Subject:           claimString(claims, "sub"),
		Email:             claimString(claims, "email"),
		PreferredUsername: claimString(claims, "preferred_username"),
		Name:              claimString(claims, "name"),
		Picture:           claimString(claims, "picture"),
		RawProfile:        string(raw),
	}
	if strings.TrimSpace(profile.Subject) == "" {
		return nil, errorsx.Unauthorized("OIDC 用户标识不存在")
	}
	return profile, nil
}

func profileFromUserInfo(userInfo *gooidc.UserInfo, fallback *Profile) (*Profile, error) {
	var claims map[string]any
	if err := userInfo.Claims(&claims); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(claims)
	profile := &Profile{
		Subject:           strings.TrimSpace(userInfo.Subject),
		Email:             claimString(claims, "email"),
		PreferredUsername: claimString(claims, "preferred_username"),
		Name:              claimString(claims, "name"),
		Picture:           claimString(claims, "picture"),
		RawProfile:        string(raw),
	}
	if fallback != nil {
		profile.Email = firstNonEmpty(profile.Email, fallback.Email)
		profile.PreferredUsername = firstNonEmpty(profile.PreferredUsername, fallback.PreferredUsername)
		profile.Name = firstNonEmpty(profile.Name, fallback.Name)
		profile.Picture = firstNonEmpty(profile.Picture, fallback.Picture)
	}
	if profile.RawProfile == "" {
		if fallback != nil {
			profile.RawProfile = fallback.RawProfile
		}
	}
	if strings.TrimSpace(profile.Subject) == "" {
		return nil, errorsx.Unauthorized("OIDC 用户标识不存在")
	}
	return profile, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func claimString(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func stateSecret() string {
	if strings.TrimSpace(oidcCfg.StateSecret) != "" {
		return strings.TrimSpace(oidcCfg.StateSecret)
	}
	return strings.TrimSpace(oidcCfg.ClientSecret)
}

func signState(content, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(content))
	return hex.EncodeToString(mac.Sum(nil))
}

func cleanupExpiredLoginTickets() {
	now := time.Now()
	loginTicketStore.Range(func(key, value any) bool {
		record, ok := value.(loginTicket)
		if !ok || now.After(record.ExpiredAt) {
			loginTicketStore.Delete(key)
		}
		return true
	})
}

func sanitizeNextPath(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return defaultLoginNextPath
	}
	return next
}

func randomToken(prefix string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
