package pms

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	memberInfoByPhonePath     = "/admin-api/member/open/members/info/by-phone"
	memberBenefitsByGradePath = "/admin-api/member/open/members/benefits/by-grade"
)

var mainlandMemberPhone = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var memberTenantID = regexp.MustCompile(`^[0-9]+$`)

var memberInfoFieldTypes = map[string]string{
	"tenantId": "number", "memberGuestId": "string", "memberNo": "string",
	"name": "string", "maskedPhone": "string", "status": "number", "statusName": "string",
	"gradeId": "string", "gradeName": "string", "gradeAvailable": "boolean",
	"validStartTime": "string", "validEndTime": "string", "memberCreatedTime": "string",
	"joinChannelCode": "string", "joinChannelName": "string",
}

var memberGradeFieldTypes = map[string]string{
	"tenantId": "number", "gradeId": "string", "gradeCode": "string", "gradeName": "string",
	"ascOrder": "number", "status": "number", "statusName": "string", "gradeAvailable": "boolean",
	"validityText": "string", "paidUpgradeEnabled": "number", "paidUpgradePricingMode": "number",
	"paidUpgradeAmount": "number", "downgradeRule": "number", "downgradeRuleName": "string",
	"upgradeRuleSummary": "string", "keepGradeRuleSummary": "string",
}

var memberBenefitFieldTypes = map[string]string{
	"benefitType": "string", "benefitId": "string", "benefitName": "string", "label": "string",
	"contentMode": "string", "contentValue": "string", "contentText": "string",
	"benefitDescription": "string", "ascOrder": "number",
}

func isMemberQuery(action string) bool {
	return action == "member_info_by_phone" || action == "member_benefits_by_grade"
}

func validateMemberQuery(action string, query url.Values) error {
	switch action {
	case "member_info_by_phone":
		if !mainlandMemberPhone.MatchString(query.Get("phone")) {
			return fmt.Errorf("手机号不能为空或格式不正确")
		}
	case "member_benefits_by_grade":
		if query.Get("gradeId") == "" && query.Get("gradeCode") == "" {
			return fmt.Errorf("会员等级ID或编码不能为空")
		}
	}
	return nil
}

func memberQueryBusinessError(code string) error {
	switch code {
	case "561":
		return fmt.Errorf("会员查询缺少租户上下文")
	case "640":
		return fmt.Errorf("手机号不能为空或格式不正确")
	case "563":
		return fmt.Errorf("会员不存在")
	case "512":
		return fmt.Errorf("当前等级无效")
	case "401", "403":
		return fmt.Errorf("会员查询认证或权限不足")
	default:
		return fmt.Errorf("会员查询暂时不可用，请稍后重试")
	}
}

func validateMemberResponseEnvelope(payload map[string]any) error {
	hasStatus := false
	for _, field := range []string{"code", "status", "success"} {
		value, exists := payload[field]
		if !exists {
			continue
		}
		hasStatus = true
		if field == "success" {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("会员查询成功标识类型不正确")
			}
			continue
		}
		switch typed := value.(type) {
		case json.Number:
		case string:
			if strings.TrimSpace(typed) == "" {
				return fmt.Errorf("会员查询状态标识不能为空")
			}
		default:
			return fmt.Errorf("会员查询状态标识类型不正确")
		}
	}
	if !hasStatus {
		return fmt.Errorf("会员查询返回缺少状态标识")
	}
	return nil
}

func sanitizeMemberQueryData(action string, data any) (any, error) {
	source, ok := data.(map[string]any)
	if !ok || source == nil {
		return nil, fmt.Errorf("会员查询数据结构不符合接口约定")
	}
	if action == "member_info_by_phone" {
		return copyMemberQueryFields(source, memberInfoFieldTypes,
			"tenantId", "memberGuestId", "memberNo", "gradeAvailable",
		)
	}
	result, err := copyMemberQueryFields(source, memberGradeFieldTypes,
		"tenantId", "gradeId", "gradeCode", "gradeName", "ascOrder",
		"status", "statusName", "gradeAvailable", "validityText",
	)
	if err != nil {
		return nil, err
	}
	if value, exists := source["gradeRule"]; exists {
		if value == nil {
			result["gradeRule"] = nil
		} else if rule, ok := value.(map[string]any); ok {
			// GradeRuleVO is not fully specified; only its documented fields are exposed.
			result["gradeRule"], err = copyMemberQueryFields(rule, map[string]string{
				"gradeId": "string", "autoUpgradeEnabled": "number",
				"keepGradeRuleType": "number", "downgradeRule": "number",
			},
			)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("会员等级规则结构不符合接口约定")
		}
	}
	benefits, ok := source["benefits"].([]any)
	if !ok {
		return nil, fmt.Errorf("会员权益列表缺失或类型不正确")
	}
	items := make([]any, 0, len(benefits))
	for _, value := range benefits {
		benefit, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("会员权益结构不符合接口约定")
		}
		item, err := copyMemberQueryFields(benefit, memberBenefitFieldTypes,
			"benefitType", "benefitId", "benefitName", "label", "ascOrder",
		)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	result["benefits"] = items
	return result, nil
}

func copyMemberQueryFields(source map[string]any, fieldTypes map[string]string, required ...string) (map[string]any, error) {
	for _, field := range required {
		value, exists := source[field]
		if !exists || value == nil {
			return nil, fmt.Errorf("会员查询字段 %s 缺失或为空", field)
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("会员查询字段 %s 不能为空", field)
		}
	}
	result := make(map[string]any, len(fieldTypes))
	for field, expectedType := range fieldTypes {
		value, exists := source[field]
		if !exists {
			continue
		}
		if value == nil {
			result[field] = nil
			continue
		}
		if field == "tenantId" {
			var exactID string
			switch typed := value.(type) {
			case string:
				exactID = typed
			case json.Number:
				exactID = typed.String()
			}
			if !memberTenantID.MatchString(exactID) {
				return nil, fmt.Errorf("会员查询字段 tenantId 类型不正确")
			}
			result[field] = value
			continue
		}
		actualType := ""
		switch value.(type) {
		case string:
			actualType = "string"
		case bool:
			actualType = "boolean"
		case json.Number:
			actualType = "number"
		}
		if actualType != expectedType {
			return nil, fmt.Errorf("会员查询字段 %s 类型不正确", field)
		}
		if field == "maskedPhone" && mainlandMemberPhone.MatchString(value.(string)) {
			return nil, fmt.Errorf("会员查询返回的手机号未脱敏")
		}
		result[field] = value
	}
	return result, nil
}
