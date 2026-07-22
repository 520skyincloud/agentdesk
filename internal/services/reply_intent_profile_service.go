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

var ReplyIntentProfileService = newReplyIntentProfileService()

func newReplyIntentProfileService() *replyIntentProfileService {
	return &replyIntentProfileService{}
}

type replyIntentProfileService struct{}

func (s *replyIntentProfileService) Get(id int64) *models.ReplyIntentProfile {
	if id <= 0 || sqls.DB() == nil {
		return nil
	}
	return repositories.ReplyIntentProfileRepository.Get(sqls.DB(), id)
}

func (s *replyIntentProfileService) Take(where ...any) *models.ReplyIntentProfile {
	if sqls.DB() == nil {
		return nil
	}
	return repositories.ReplyIntentProfileRepository.Take(sqls.DB(), where...)
}

func (s *replyIntentProfileService) FindPageByParams(params *params.QueryParams) (list []models.ReplyIntentProfile, paging *sqls.Paging) {
	return repositories.ReplyIntentProfileRepository.FindPageByParams(sqls.DB(), params)
}

func (s *replyIntentProfileService) FindPageByCnd(cnd *sqls.Cnd) (list []models.ReplyIntentProfile, paging *sqls.Paging) {
	return repositories.ReplyIntentProfileRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *replyIntentProfileService) CreateReplyIntentProfile(req request.CreateReplyIntentProfileRequest, operator *dto.AuthPrincipal) (*models.ReplyIntentProfile, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	industryCode := strings.TrimSpace(req.IndustryCode)
	prompt := strings.TrimSpace(req.IntentDetectPrompt)
	schema := strings.TrimSpace(req.IntentJSONSchema)
	status := normalizeReplyIntentProfileStatus(req.Status)
	if code == "" || name == "" || industryCode == "" {
		return nil, errorsx.InvalidParam("行业配置编码、业务行业编码和名称不能为空")
	}
	if status == enums.StatusOk {
		return nil, errorsx.InvalidParam("新行业必须先保存为停用草稿，完成意图分类和标签目录后再发布")
	}
	if existing := repositories.ReplyIntentProfileRepository.Take(sqls.DB(), "code = ?", code); existing != nil {
		return nil, errorsx.InvalidParam("行业编码已存在")
	}
	item := &models.ReplyIntentProfile{
		Code:               code,
		Name:               name,
		IndustryCode:       industryCode,
		Description:        strings.TrimSpace(req.Description),
		IntentDetectPrompt: prompt,
		IntentJSONSchema:   schema,
		Revision:           1,
		Status:             status,
		SortNo:             req.SortNo,
		Remark:             strings.TrimSpace(req.Remark),
		AuditFields:        utils.BuildAuditFields(operator),
	}
	if err := repositories.ReplyIntentProfileRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *replyIntentProfileService) UpdateReplyIntentProfile(req request.UpdateReplyIntentProfileRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	industryCode := strings.TrimSpace(req.IndustryCode)
	prompt := strings.TrimSpace(req.IntentDetectPrompt)
	schema := strings.TrimSpace(req.IntentJSONSchema)
	status := normalizeReplyIntentProfileStatus(req.Status)
	if code == "" || name == "" || industryCode == "" {
		return errorsx.InvalidParam("行业配置编码、业务行业编码和名称不能为空")
	}
	if status == enums.StatusOk && (prompt == "" || schema == "") {
		return errorsx.InvalidParam("发布行业前必须配置独立的 IntentDetect 提示词和输出 Schema")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ReplyIntentProfileRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("意图行业配置不存在")
		}
		if existing := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ? AND id <> ?", code, req.ID); existing != nil {
			return errorsx.InvalidParam("行业编码已存在")
		}
		tenantCount, err := repositories.TenantRepository.CountByIntentProfile(ctx.Tx, item.ID)
		if err != nil {
			return err
		}
		if tenantCount > 0 && (item.Code != code || item.IndustryCode != industryCode) {
			return errorsx.InvalidParam("已绑定接入公司的行业不能修改稳定编码或业务行业编码")
		}
		if tenantCount > 0 && status != enums.StatusOk {
			return errorsx.InvalidParam("该行业已被接入公司使用，请先切换这些公司的行业后再停用")
		}
		now := time.Now()
		revision := item.Revision
		if revision <= 0 {
			revision = 1
		}
		definitionChanged := item.IndustryCode != industryCode || item.IntentDetectPrompt != prompt || item.IntentJSONSchema != schema
		if definitionChanged {
			revision++
		}
		updates := map[string]any{
			"code":                 code,
			"name":                 name,
			"industry_code":        industryCode,
			"description":          strings.TrimSpace(req.Description),
			"intent_detect_prompt": prompt,
			"intent_json_schema":   schema,
			"revision":             revision,
			"status":               status,
			"sort_no":              req.SortNo,
			"remark":               strings.TrimSpace(req.Remark),
			"update_user_id":       operator.UserID,
			"update_user_name":     operator.Username,
			"updated_at":           now,
		}
		if status == enums.StatusOk {
			if item.PublishedAt == nil || definitionChanged || item.Status != enums.StatusOk {
				updates["published_at"] = now
				updates["published_by"] = operator.UserID
			}
		} else {
			updates["published_at"] = nil
			updates["published_by"] = 0
		}
		if err := repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, req.ID, updates); err != nil {
			return err
		}
		if status == enums.StatusOk {
			if _, err := TenantIndustryService.ValidateBindingProfileDB(ctx.Tx, req.ID); err != nil {
				return err
			}
		}
		if definitionChanged {
			return repositories.IndustryTagDefinitionRepository.UpdateRevisionByProfile(
				ctx.Tx, req.ID, revision, now, operator.UserID, operator.Username,
			)
		}
		return nil
	})
}

func (s *replyIntentProfileService) DeleteReplyIntentProfile(id int64) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ReplyIntentProfileRepository.GetForUpdate(ctx.Tx, id)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("意图行业配置不存在")
		}
		if repositories.ReplyIntentConfigRepository.Count(ctx.Tx, sqls.NewCnd().Eq("intent_profile_id", id)) > 0 {
			return errorsx.InvalidParam("该意图行业已被意图分类使用，不能删除")
		}
		tagDefinitionCount, err := repositories.IndustryTagDefinitionRepository.CountByProfile(ctx.Tx, id)
		if err != nil {
			return err
		}
		if tagDefinitionCount > 0 {
			return errorsx.InvalidParam("该意图行业已配置固定标签目录，不能删除")
		}
		count, err := repositories.TenantRepository.CountByIntentProfile(ctx.Tx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return errorsx.InvalidParam("该行业已被接入公司使用，不能删除")
		}
		return repositories.ReplyIntentProfileRepository.Delete(ctx.Tx, id)
	})
}

func normalizeReplyIntentProfileStatus(status enums.Status) enums.Status {
	if status == enums.StatusDisabled {
		return status
	}
	return enums.StatusOk
}
