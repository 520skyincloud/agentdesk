package services

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	weComStageSuiteToken            = "suite_token"
	weComStageCorpToken             = "corp_token"
	weComStageAuthorizationValidate = "authorization_validate"
	weComStageContactMemberValidate = "contact_member_validate"
	weComStageAddContactWay         = "add_contact_way"
	weComStageAddContactWayResponse = "add_contact_way_response"
	weComStageQRCodeArtifact        = "qr_code_artifact"
	weComStageContactWayPersist     = "contact_way_persist"
)

var (
	weComSensitiveAssignmentPattern = regexp.MustCompile(
		`(?i)\b(access_token|suite_access_token|permanent_code|suite_secret|encodingaeskey|token|openid|unionid|external_userid|userid|user_id|corpid|corp_id|suiteid|suite_id|guid|conversation_id)\b\s*[:=]\s*["']?[^,;\s&"']+`,
	)
	weComProviderHintPattern = regexp.MustCompile(
		`(?i)(?:,\s*)?\bhint\s*:\s*(?:\[[^\]\r\n]{0,256}\]|[^,;\s]{1,256})`,
	)
	weComProviderSourceIPPattern = regexp.MustCompile(
		`(?i)(?:,\s*)?\bfrom\s+ip\s*:\s*(?:\[[0-9a-f:.]{2,64}\]|[0-9a-f:.]{2,64})`,
	)
	weComProviderMoreInfoPattern = regexp.MustCompile(
		`(?i)(?:,\s*)?\bmore\s+info\s+at\s+(?:\[[^\]\r\n]{0,512}\]|https?://[^,;\s]+)`,
	)
	weComIPv4Pattern            = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	weComLongOpaqueValuePattern = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)
	weComURLPattern             = regexp.MustCompile(`https?://[^\s]+`)
)

type weComProviderError struct {
	Stage      string
	HTTPStatus int
	ErrCode    int
	ErrMsg     string
	Retryable  bool
}

func (e *weComProviderError) Error() string {
	if e == nil {
		return "企业微信调用失败"
	}
	message := strings.TrimSpace(e.ErrMsg)
	if message == "" {
		message = "企业微信调用失败"
	}
	if e.ErrCode != 0 {
		return fmt.Sprintf("%s（阶段 %s，错误码 %d）", message, e.Stage, e.ErrCode)
	}
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s（阶段 %s，HTTP %d）", message, e.Stage, e.HTTPStatus)
	}
	return fmt.Sprintf("%s（阶段 %s）", message, e.Stage)
}

func newWeComProviderError(stage string, httpStatus, errCode int, message string, retryable bool) error {
	message = sanitizeWeComProviderMessage(message)
	if message == "" {
		message = "企业微信操作失败"
	}
	return &weComProviderError{
		Stage:      normalizeWeComProviderStage(stage),
		HTTPStatus: httpStatus,
		ErrCode:    errCode,
		ErrMsg:     message,
		Retryable:  retryable,
	}
}

func asWeComProviderError(err error) *weComProviderError {
	if err == nil {
		return nil
	}
	var providerErr *weComProviderError
	if errors.As(err, &providerErr) {
		return providerErr
	}
	return nil
}

func isWeComCorpAccessTokenError(err error) bool {
	providerErr := asWeComProviderError(err)
	if providerErr == nil {
		return false
	}
	return providerErr.ErrCode == 40014 || providerErr.ErrCode == 42001
}

func isRetryableWeComError(errCode int, httpStatus int) bool {
	if httpStatus == 429 || httpStatus >= 500 {
		return true
	}
	switch errCode {
	case -1, 40014, 42001, 45009:
		return true
	default:
		return false
	}
}

func sanitizeWeComProviderMessage(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(message))
	message = strings.Join(strings.Fields(message), " ")
	message = weComProviderHintPattern.ReplaceAllString(message, "")
	message = weComProviderSourceIPPattern.ReplaceAllString(message, "")
	message = weComProviderMoreInfoPattern.ReplaceAllString(message, "")
	message = weComURLPattern.ReplaceAllString(message, "[url-redacted]")
	message = weComIPv4Pattern.ReplaceAllString(message, "[ip-redacted]")
	message = weComSensitiveAssignmentPattern.ReplaceAllString(message, "${1}=[redacted]")
	message = weComLongOpaqueValuePattern.ReplaceAllString(message, "[redacted]")
	message = strings.Trim(strings.Join(strings.Fields(message), " "), " ,;")
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[:240])
	}
	return strings.TrimSpace(message)
}

func normalizeWeComProviderStage(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return "unknown"
	}
	if len(stage) > 80 {
		stage = stage[:80]
	}
	return stage
}

func weComRequestStage(path string) string {
	switch strings.TrimSpace(path) {
	case "/cgi-bin/service/get_suite_token":
		return weComStageSuiteToken
	case "/cgi-bin/service/get_corp_token":
		return weComStageCorpToken
	case "/cgi-bin/externalcontact/add_contact_way":
		return weComStageAddContactWay
	default:
		trimmed := strings.Trim(strings.TrimSpace(path), "/")
		if trimmed == "" {
			return "wecom_api"
		}
		parts := strings.Split(trimmed, "/")
		return normalizeWeComProviderStage(parts[len(parts)-1])
	}
}
