package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const wxWorkLoginVerificationMaxAttempts = 5

var wxWorkLoginVerificationAttempts = struct {
	sync.Mutex
	values map[int64]int
}{values: make(map[int64]int)}

// ResetLoginVerificationAttempts starts a fresh confirmation-code window for a new QR code.
func (s *wxWorkProtocolService) ResetLoginVerificationAttempts(instanceID int64) {
	wxWorkLoginVerificationAttempts.Lock()
	delete(wxWorkLoginVerificationAttempts.values, instanceID)
	wxWorkLoginVerificationAttempts.Unlock()
}

func (s *wxWorkProtocolService) CheckLoginQRCodeStatus(instanceID int64) (*response.WxWorkProtocolLoginStatusResponse, error) {
	raw, err := s.callInstanceAPI(instanceID, "/login/check_login_qrcode", nil, nil)
	if err != nil {
		return nil, err
	}
	status := parseWxWorkProtocolLoginStatus(raw)
	if status.Status == "success" {
		if err := s.persistSuccessfulLogin(instanceID, raw); err != nil {
			return nil, err
		}
		s.ResetLoginVerificationAttempts(instanceID)
	}
	return status, nil
}

func (s *wxWorkProtocolService) VerifyLoginQRCodeStatus(instanceID int64, code string) (*response.WxWorkProtocolLoginStatusResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errorsx.InvalidParam("确认码不能为空")
	}
	if !claimWxWorkLoginVerificationAttempt(instanceID) {
		return nil, errorsx.InvalidParam("确认码尝试次数已达上限，请重新生成二维码")
	}
	raw, err := s.callInstanceAPI(instanceID, "/login/verify_login_qrcode", map[string]any{"code": code}, nil)
	if err != nil {
		return nil, err
	}
	status := parseWxWorkProtocolLoginStatus(raw)
	if status.Status == "success" {
		if err := s.persistSuccessfulLogin(instanceID, raw); err != nil {
			return nil, err
		}
		s.ResetLoginVerificationAttempts(instanceID)
	}
	return status, nil
}

func (s *wxWorkProtocolService) persistSuccessfulLogin(instanceID int64, raw string) error {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	updates := s.profileUpdatesFromResponse(raw)
	updates["status"] = enums.StatusOk
	if _, ok := updates["health_status"]; !ok {
		updates["health_status"] = "online"
	}
	updates["updated_at"] = time.Now()
	updates["update_user_name"] = wxWorkProtocolSystemOperatorName
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instanceID, updates)
}

func claimWxWorkLoginVerificationAttempt(instanceID int64) bool {
	wxWorkLoginVerificationAttempts.Lock()
	defer wxWorkLoginVerificationAttempts.Unlock()
	count := wxWorkLoginVerificationAttempts.values[instanceID]
	if count >= wxWorkLoginVerificationMaxAttempts {
		return false
	}
	wxWorkLoginVerificationAttempts.values[instanceID] = count + 1
	return true
}

func parseWxWorkProtocolLoginStatus(raw string) *response.WxWorkProtocolLoginStatusResponse {
	result := &response.WxWorkProtocolLoginStatusResponse{
		Status:      "pending",
		Message:     "等待扫码",
		RawResponse: raw,
	}
	value, ok := decodeWxWorkProtocolLoginResponse(raw)
	if !ok {
		result.Status = "failed"
		result.Message = "协议返回的扫码状态无法解析"
		return result
	}
	data := findWxWorkProtocolLoginStatusMap(value)
	statusCode, hasStatusCode := wxWorkProtocolLoginStatusCode(data)
	if hasStatusCode {
		result.StatusCode = statusCode
	}
	message := firstStringFromMap(data, "message", "msg", "detail")

	if hasStatusCode {
		// Status values follow the official QRCODE_LOGIN_* enum documented by the protocol.
		switch statusCode {
		case 0:
			result.Status, result.Message = "pending", "等待扫码"
		case 1, 5:
			result.Status, result.Message = "scanned", "已扫码，等待员工号确认"
		case 2, 6, 9:
			result.Status, result.Message = "success", "登录成功"
		case 3, 7:
			result.Status, result.Message = "failed", "登录失败，请重新生成二维码"
		case 4, 8:
			result.Status, result.Message = "refused", "本次登录已拒绝"
		case 10:
			result.Status, result.RequiresCode, result.Message = "verification_required", true, "需要在新设备输入确认码"
		}
	} else {
		if status := exactWxWorkProtocolLoginStatus(firstStringFromMap(data, "status", "state")); status != "" {
			result.Status = status
			result.RequiresCode = status == "verification_required"
			result.Message = wxWorkProtocolLoginStatusMessage(status)
		} else if wxWorkProtocolLoginResponseHasIdentity(data) {
			result.Status, result.Message = "success", "登录成功"
		}
	}
	if message != "" && result.Status != "pending" {
		result.Message = message
	}
	return result
}

func decodeWxWorkProtocolLoginResponse(raw string) (any, bool) {
	var value any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &value); err != nil {
		return nil, false
	}
	for i := 0; i < 3; i++ {
		text, ok := value.(string)
		if !ok {
			break
		}
		text = strings.TrimSpace(text)
		if text == "" || (text[0] != '{' && text[0] != '[' && text[0] != '"') {
			break
		}
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			break
		}
	}
	return value, true
}

func findWxWorkProtocolLoginStatusMap(value any) map[string]any {
	root, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	for _, key := range []string{"data", "result"} {
		if nested, ok := root[key].(map[string]any); ok {
			root = nested
			continue
		}
		if text, ok := root[key].(string); ok {
			if decoded, decodedOK := decodeWxWorkProtocolLoginResponse(text); decodedOK {
				if nested, nestedOK := decoded.(map[string]any); nestedOK {
					root = nested
				}
			}
		}
	}
	return root
}

func wxWorkProtocolLoginStatusCode(data map[string]any) (int, bool) {
	for _, key := range []string{"status", "status_code", "statusCode"} {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case json.Number:
			parsed, err := strconv.Atoi(typed.String())
			return parsed, err == nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func exactWxWorkProtocolLoginStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "QRCODE_LOGIN_NEVER", "PENDING", "WAITING", "UNSCANNED":
		return "pending"
	case "QRCODE_LOGIN_ING", "QRCODE_LOGIN_ING_WX", "SCANNED":
		return "scanned"
	case "QRCODE_LOGIN_SUCC", "QRCODE_LOGIN_SUCC_WX", "QRCODE_WX_AUTH_OK", "SUCCESS", "LOGIN_SUCCESS", "LOGGED_IN":
		return "success"
	case "QRCODE_LOGIN_FAIL", "QRCODE_LOGIN_FAIL_WX", "FAILED", "ERROR":
		return "failed"
	case "QRCODE_LOGIN_REFUSE", "QRCODE_LOGIN_REFUSE_WX", "REFUSED", "CANCELLED", "CANCELED":
		return "refused"
	case "QRCODE_REQUIRE_VERIFY", "VERIFICATION_REQUIRED":
		return "verification_required"
	case "EXPIRED", "QRCODE_EXPIRED":
		return "expired"
	default:
		return ""
	}
}

func wxWorkProtocolLoginStatusMessage(status string) string {
	switch status {
	case "scanned":
		return "已扫码，等待员工号确认"
	case "verification_required":
		return "需要在新设备输入确认码"
	case "success":
		return "登录成功"
	case "refused":
		return "本次登录已拒绝"
	case "expired":
		return "二维码已过期，请重新生成"
	case "failed":
		return "登录失败，请重新生成二维码"
	default:
		return "等待扫码"
	}
}

func wxWorkProtocolLoginResponseHasIdentity(data map[string]any) bool {
	for _, key := range []string{"username", "user_name", "userName", "user_id", "userId", "wxid"} {
		value := strings.TrimSpace(fmt.Sprint(data[key]))
		if value != "" && value != "<nil>" {
			return true
		}
	}
	return false
}
