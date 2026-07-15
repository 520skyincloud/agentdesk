package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ReplyIntentConfigService = newReplyIntentConfigService()

func newReplyIntentConfigService() *replyIntentConfigService {
	return &replyIntentConfigService{}
}

type replyIntentConfigService struct{}

func (s *replyIntentConfigService) Get(id int64) *models.ReplyIntentConfig {
	return repositories.ReplyIntentConfigRepository.Get(sqls.DB(), id)
}

func (s *replyIntentConfigService) FindPageByParams(params *params.QueryParams) (list []models.ReplyIntentConfig, paging *sqls.Paging) {
	return repositories.ReplyIntentConfigRepository.FindPageByParams(sqls.DB(), params)
}

func (s *replyIntentConfigService) FindPageByCnd(cnd *sqls.Cnd) (list []models.ReplyIntentConfig, paging *sqls.Paging) {
	list, paging = repositories.ReplyIntentConfigRepository.FindPageByCnd(sqls.DB(), cnd)
	return filterHiddenReplyIntentConfigs(list), paging
}

func (s *replyIntentConfigService) CreateReplyIntentConfig(req request.CreateReplyIntentConfigRequest, operator *dto.AuthPrincipal) (*models.ReplyIntentConfig, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return nil, errorsx.InvalidParam("意图编码和名称不能为空")
	}
	scopeType, companyID, storeID, instanceID, err := normalizeIntentScope(req.ScopeType, req.CompanyID, req.StoreID, req.WxWorkInstanceID)
	if err != nil {
		return nil, err
	}
	intentProfileID, err := normalizeIntentConfigProfileID(req.IntentProfileID)
	if err != nil {
		return nil, err
	}
	if existing := repositories.ReplyIntentConfigRepository.Take(sqls.DB(), "code = ? AND intent_profile_id = ? AND scope_type = ? AND company_id = ? AND store_id = ? AND wx_work_instance_id = ?", code, intentProfileID, scopeType, companyID, storeID, instanceID); existing != nil {
		return nil, errorsx.InvalidParam("同一适用范围内意图编码已存在")
	}
	item := &models.ReplyIntentConfig{
		Code:               code,
		Name:               name,
		Description:        strings.TrimSpace(req.Description),
		IntentProfileID:    intentProfileID,
		ScopeType:          scopeType,
		CompanyID:          companyID,
		StoreID:            storeID,
		WxWorkInstanceID:   instanceID,
		Priority:           req.Priority,
		MatchMode:          normalizeIntentMatchMode(req.MatchMode),
		Keywords:           strings.TrimSpace(req.Keywords),
		PositiveExamples:   strings.TrimSpace(req.PositiveExamples),
		NegativeExamples:   strings.TrimSpace(req.NegativeExamples),
		RequiredContext:    strings.TrimSpace(req.RequiredContext),
		NeedsKnowledge:     req.NeedsKnowledge,
		NeedsResource:      req.NeedsResource,
		ResourceType:       strings.TrimSpace(req.ResourceType),
		NeedsTool:          req.NeedsTool,
		ToolCodes:          strings.TrimSpace(req.ToolCodes),
		NeedsHumanRoute:    req.NeedsHumanRoute,
		HumanRoutePolicy:   strings.TrimSpace(req.HumanRoutePolicy),
		PromptPack:         strings.TrimSpace(req.PromptPack),
		ReplyPlanTemplate:  strings.TrimSpace(req.ReplyPlanTemplate),
		ValidationRules:    strings.TrimSpace(req.ValidationRules),
		NoReplyWhenMatched: req.NoReplyWhenMatched,
		Status:             normalizeIntentStatus(req.Status),
		SortNo:             req.SortNo,
		Remark:             strings.TrimSpace(req.Remark),
		AuditFields:        utils.BuildAuditFields(operator),
	}
	if err := repositories.ReplyIntentConfigRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *replyIntentConfigService) UpdateReplyIntentConfig(req request.UpdateReplyIntentConfigRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.Get(req.ID)
	if item == nil {
		return errorsx.InvalidParam("意图配置不存在")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return errorsx.InvalidParam("意图编码和名称不能为空")
	}
	scopeType, companyID, storeID, instanceID, err := normalizeIntentScope(req.ScopeType, req.CompanyID, req.StoreID, req.WxWorkInstanceID)
	if err != nil {
		return err
	}
	intentProfileID, err := normalizeIntentConfigProfileID(req.IntentProfileID)
	if err != nil {
		return err
	}
	if existing := repositories.ReplyIntentConfigRepository.Take(sqls.DB(), "code = ? AND intent_profile_id = ? AND scope_type = ? AND company_id = ? AND store_id = ? AND wx_work_instance_id = ? AND id <> ?", code, intentProfileID, scopeType, companyID, storeID, instanceID, req.ID); existing != nil {
		return errorsx.InvalidParam("同一适用范围内意图编码已存在")
	}
	return repositories.ReplyIntentConfigRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"code":                  code,
		"name":                  name,
		"description":           strings.TrimSpace(req.Description),
		"intent_profile_id":     intentProfileID,
		"scope_type":            scopeType,
		"company_id":            companyID,
		"store_id":              storeID,
		"wx_work_instance_id":   instanceID,
		"priority":              req.Priority,
		"match_mode":            normalizeIntentMatchMode(req.MatchMode),
		"keywords":              strings.TrimSpace(req.Keywords),
		"positive_examples":     strings.TrimSpace(req.PositiveExamples),
		"negative_examples":     strings.TrimSpace(req.NegativeExamples),
		"required_context":      strings.TrimSpace(req.RequiredContext),
		"needs_knowledge":       req.NeedsKnowledge,
		"needs_resource":        req.NeedsResource,
		"resource_type":         strings.TrimSpace(req.ResourceType),
		"needs_tool":            req.NeedsTool,
		"tool_codes":            strings.TrimSpace(req.ToolCodes),
		"needs_human_route":     req.NeedsHumanRoute,
		"human_route_policy":    strings.TrimSpace(req.HumanRoutePolicy),
		"prompt_pack":           strings.TrimSpace(req.PromptPack),
		"reply_plan_template":   strings.TrimSpace(req.ReplyPlanTemplate),
		"validation_rules":      strings.TrimSpace(req.ValidationRules),
		"no_reply_when_matched": req.NoReplyWhenMatched,
		"status":                normalizeIntentStatus(req.Status),
		"sort_no":               req.SortNo,
		"remark":                strings.TrimSpace(req.Remark),
		"update_user_id":        operator.UserID,
		"update_user_name":      operator.Username,
		"updated_at":            time.Now(),
	})
}

func (s *replyIntentConfigService) DeleteReplyIntentConfig(id int64) error {
	if s.Get(id) == nil {
		return errorsx.InvalidParam("意图配置不存在")
	}
	repositories.ReplyIntentConfigRepository.Delete(sqls.DB(), id)
	return nil
}

func normalizeIntentMatchMode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "hybrid"
	}
	return value
}

func normalizeIntentStatus(status enums.Status) enums.Status {
	if status == enums.StatusDisabled || status == enums.StatusDeleted {
		return status
	}
	return enums.StatusOk
}

func normalizeIntentConfigProfileID(id int64) (int64, error) {
	if id > 0 {
		item := ReplyIntentProfileService.Get(id)
		if item == nil || item.Status == enums.StatusDeleted {
			return 0, errorsx.InvalidParam("意图行业配置不存在")
		}
		return id, nil
	}
	if profile := ReplyIntentProfileService.DefaultHotelProfile(); profile != nil {
		return profile.ID, nil
	}
	return 0, nil
}

func filterHiddenReplyIntentConfigs(list []models.ReplyIntentConfig) []models.ReplyIntentConfig {
	results := make([]models.ReplyIntentConfig, 0, len(list))
	for _, item := range list {
		if isHiddenReplyIntentCode(item.Code) && item.Status != enums.StatusOk {
			continue
		}
		results = append(results, item)
	}
	return results
}

func isHiddenReplyIntentCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "account_resource_phone", "account_resource_location", "account_resource_miniprogram", "no_reply_media_only", "media_question", "media_understanding", "complaint_or_risk", "handoff", "thanks_confirm", "social", "social_confirm", "unknown_clarify", "unknown_or_clarify", "invoice", "supplies_self_help", "hotel_knowledge", "store_info_invoice", "store_info_supplies", "store_info_general", "network_wifi":
		return true
	default:
		return false
	}
}

func normalizeIntentScope(scopeType string, companyID int64, storeID int64, instanceID int64) (string, int64, int64, int64, error) {
	scopeType = strings.TrimSpace(scopeType)
	if scopeType == "" {
		scopeType = "global"
	}
	switch scopeType {
	case "global":
		return scopeType, 0, 0, 0, nil
	case "company":
		if companyID <= 0 {
			return "", 0, 0, 0, errorsx.InvalidParam("公司级意图必须选择公司")
		}
		return scopeType, companyID, 0, 0, nil
	case "store":
		if storeID <= 0 {
			return "", 0, 0, 0, errorsx.InvalidParam("门店级意图必须选择门店")
		}
		return scopeType, 0, storeID, 0, nil
	case "instance":
		if instanceID <= 0 {
			return "", 0, 0, 0, errorsx.InvalidParam("账号级意图必须选择企微员工号")
		}
		return scopeType, 0, 0, instanceID, nil
	default:
		return "", 0, 0, 0, errorsx.InvalidParam("适用范围不正确")
	}
}
