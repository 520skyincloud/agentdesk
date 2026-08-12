package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
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

func (s *replyIntentProfileService) FindOptions() []models.ReplyIntentProfile {
	if sqls.DB() == nil {
		return []models.ReplyIntentProfile{}
	}
	return repositories.ReplyIntentProfileRepository.Find(sqls.DB(), sqls.NewCnd().
		Where("status <> ?", enums.StatusDeleted).
		Asc("sort_no").
		Asc("id"))
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
	if code == "" || name == "" || industryCode == "" {
		return nil, errorsx.InvalidParam("行业配置编码、业务行业编码和名称不能为空")
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
		Status:             enums.StatusDisabled,
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
	if code == "" || name == "" || industryCode == "" {
		return errorsx.InvalidParam("行业配置编码、业务行业编码和名称不能为空")
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
		if item.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("已删除的行业不能编辑")
		}
		if req.Status != item.Status {
			return errorsx.InvalidParam("行业状态不能通过编辑修改，请使用独立发布动作")
		}
		tenantCount, err := repositories.TenantRepository.CountByIntentProfile(ctx.Tx, item.ID)
		if err != nil {
			return err
		}
		now := time.Now()
		revision := item.Revision
		if revision <= 0 {
			revision = 1
		}
		definitionChanged := item.Code != code || item.IndustryCode != industryCode ||
			item.IntentDetectPrompt != prompt || item.IntentJSONSchema != schema
		if tenantCount > 0 && definitionChanged {
			return errorsx.InvalidParam("已绑定接入公司的发布行业不能原地修改运行语义，请新建行业 Profile 后切换")
		}
		if definitionChanged {
			revision++
		}
		status := item.Status
		var publishedAt any = item.PublishedAt
		publishedBy := item.PublishedBy
		if definitionChanged {
			status = enums.StatusDisabled
			publishedAt = nil
			publishedBy = int64(0)
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
			"published_at":         publishedAt,
			"published_by":         publishedBy,
			"sort_no":              req.SortNo,
			"remark":               strings.TrimSpace(req.Remark),
			"update_user_id":       operator.UserID,
			"update_user_name":     operator.Username,
			"updated_at":           now,
		}
		if err := repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, req.ID, updates); err != nil {
			return err
		}
		if definitionChanged {
			return repositories.IndustryTagDefinitionRepository.UpdateRevisionByProfile(
				ctx.Tx, req.ID, revision, now, operator.UserID, operator.Username,
			)
		}
		return nil
	})
}

func (s *replyIntentProfileService) TestReplyIntentProfile(id int64) (*response.ReplyIntentProfileValidationResponse, error) {
	if id <= 0 {
		return nil, errorsx.InvalidParam("意图行业配置不存在")
	}
	item := repositories.ReplyIntentProfileRepository.Get(sqls.DB(), id)
	if item == nil || item.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("意图行业配置不存在")
	}
	return validateReplyIntentProfileDefinitionDB(sqls.DB(), item), nil
}

func (s *replyIntentProfileService) PublishReplyIntentProfile(
	req request.PublishReplyIntentProfileRequest,
	operator *dto.AuthPrincipal,
) (*models.ReplyIntentProfile, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if !req.ConfirmRevision {
		return nil, errorsx.InvalidParam("发布行业需要二次确认当前 revision")
	}
	var published *models.ReplyIntentProfile
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ReplyIntentProfileRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if item == nil || item.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("意图行业配置不存在")
		}
		if item.Revision != req.Revision {
			return errorsx.InvalidParam("行业 revision 已变化，请重新测试后发布")
		}
		result := validateReplyIntentProfileDefinitionDB(ctx.Tx, item)
		if !result.Valid {
			return errorsx.InvalidParam("行业测试未通过：" + strings.Join(result.Errors, "；"))
		}
		now := time.Now()
		if err := repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, item.ID, map[string]any{
			"status":           enums.StatusOk,
			"published_at":     now,
			"published_by":     operator.UserID,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		published = repositories.ReplyIntentProfileRepository.Get(ctx.Tx, item.ID)
		if published == nil {
			return fmt.Errorf("reload published intent profile %d", item.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return published, nil
}

func validateReplyIntentProfileDefinitionDB(db *gorm.DB, profile *models.ReplyIntentProfile) *response.ReplyIntentProfileValidationResponse {
	result := &response.ReplyIntentProfileValidationResponse{
		Errors:   make([]string, 0),
		Warnings: make([]string, 0),
	}
	if profile == nil {
		result.Errors = append(result.Errors, "行业 Profile 不存在")
		return result
	}
	result.ProfileID = profile.ID
	result.Revision = profile.Revision
	if strings.TrimSpace(profile.Code) == "" || strings.TrimSpace(profile.IndustryCode) == "" {
		result.Errors = append(result.Errors, "行业稳定编码和业务行业编码不能为空")
	}
	if strings.TrimSpace(profile.IntentDetectPrompt) == "" {
		result.Errors = append(result.Errors, "IntentDetect 提示词不能为空")
	}
	schemaText := strings.TrimSpace(profile.IntentJSONSchema)
	if schemaText == "" {
		result.Errors = append(result.Errors, "IntentDetect 输出约束不能为空")
	} else {
		for _, field := range []string{"primaryIntent", "confidence", "intentTasks", "reason"} {
			if !strings.Contains(schemaText, field) {
				result.Warnings = append(result.Warnings, "输出约束未明确包含字段 "+field)
			}
		}
	}
	configs := repositories.ReplyIntentConfigRepository.Find(db, sqls.NewCnd().
		Eq("intent_profile_id", profile.ID).Asc("sort_no").Asc("id"))
	for i := range configs {
		if configs[i].Status == enums.StatusOk {
			result.ActiveIntentCount++
		}
	}
	if err := validateIndustryIntentConfigs(profile, configs); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	definitions, err := repositories.IndustryTagDefinitionRepository.FindActiveByProfile(db, profile.ID)
	if err != nil {
		result.Errors = append(result.Errors, "读取行业标签目录失败")
	} else {
		conflicts := make(map[string]struct{})
		for i := range definitions {
			if definitions[i].ParentID == 0 {
				result.TagCategoryCount++
			} else {
				result.TagCount++
			}
			if group := strings.TrimSpace(definitions[i].ConflictGroup); group != "" {
				conflicts[group] = struct{}{}
			}
		}
		result.ConflictGroupCount = len(conflicts)
		if err := validateIndustryTagDefinitions(profile, definitions); err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	result.Valid = len(result.Errors) == 0
	return result
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
