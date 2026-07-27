package services

import (
	"sort"
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
	"gorm.io/gorm"
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
	var item *models.ReplyIntentConfig
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		intentProfileID, err := requireIntentConfigProfileDB(ctx.Tx, req.IntentProfileID)
		if err != nil {
			return err
		}
		if existing := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND intent_profile_id = ?", code, intentProfileID); existing != nil {
			return errorsx.InvalidParam("同一行业内意图编码已存在")
		}
		item = &models.ReplyIntentConfig{
			Code:               code,
			Name:               name,
			Description:        strings.TrimSpace(req.Description),
			IntentProfileID:    intentProfileID,
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
		if err := repositories.ReplyIntentConfigRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		return markIntentProfileDraftForMutationDB(ctx.Tx, intentProfileID, operator)
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (s *replyIntentConfigService) UpdateReplyIntentConfig(req request.UpdateReplyIntentConfigRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return errorsx.InvalidParam("意图编码和名称不能为空")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ReplyIntentConfigRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("意图配置不存在")
		}
		intentProfileID, err := requireIntentConfigProfileDB(ctx.Tx, req.IntentProfileID)
		if err != nil {
			return err
		}
		if existing := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND intent_profile_id = ? AND id <> ?", code, intentProfileID, req.ID); existing != nil {
			return errorsx.InvalidParam("同一行业内意图编码已存在")
		}
		if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, req.ID, map[string]any{
			"code":                  code,
			"name":                  name,
			"description":           strings.TrimSpace(req.Description),
			"intent_profile_id":     intentProfileID,
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
		}); err != nil {
			return err
		}
		for _, profileID := range sortedIntentProfileIDs(item.IntentProfileID, intentProfileID) {
			if err := markIntentProfileDraftForMutationDB(ctx.Tx, profileID, operator); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *replyIntentConfigService) DeleteReplyIntentConfig(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ReplyIntentConfigRepository.GetForUpdate(ctx.Tx, id)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("意图配置不存在")
		}
		if err := repositories.ReplyIntentConfigRepository.Delete(ctx.Tx, id); err != nil {
			return err
		}
		return markIntentProfileDraftForMutationDB(ctx.Tx, item.IntentProfileID, operator)
	})
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

func requireIntentConfigProfileDB(db *gorm.DB, id int64) (int64, error) {
	if id <= 0 {
		return 0, errorsx.InvalidParam("意图分类必须选择所属行业")
	}
	item := repositories.ReplyIntentProfileRepository.Get(db, id)
	if item == nil || item.Status == enums.StatusDeleted {
		return 0, errorsx.InvalidParam("意图行业配置不存在")
	}
	return id, nil
}

func markIntentProfileDraftForMutationDB(db *gorm.DB, profileID int64, operator *dto.AuthPrincipal) error {
	profile, err := repositories.ReplyIntentProfileRepository.GetForUpdate(db, profileID)
	if err != nil {
		return err
	}
	if profile == nil || profile.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("意图行业配置不存在")
	}
	if profile.Status == enums.StatusOk {
		tenantCount, err := repositories.TenantRepository.CountByIntentProfile(db, profile.ID)
		if err != nil {
			return err
		}
		if tenantCount > 0 {
			return errorsx.InvalidParam("已绑定接入公司的发布行业不能原地修改分类，请新建行业 Profile 后切换")
		}
	}
	revision := profile.Revision + 1
	if revision <= 0 {
		revision = 1
	}
	now := time.Now()
	updates := map[string]any{
		"revision":         revision,
		"status":           enums.StatusDisabled,
		"published_at":     nil,
		"published_by":     0,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       now,
	}
	if err := repositories.ReplyIntentProfileRepository.Updates(db, profile.ID, updates); err != nil {
		return err
	}
	return repositories.IndustryTagDefinitionRepository.UpdateRevisionByProfile(
		db, profile.ID, revision, now, operator.UserID, operator.Username,
	)
}

func sortedIntentProfileIDs(values ...int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i] < ret[j] })
	return ret
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
