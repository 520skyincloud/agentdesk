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
	"agent-desk/internal/pkg/replyintent"
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
	if code == "" || name == "" {
		return nil, errorsx.InvalidParam("行业编码和名称不能为空")
	}
	if existing := repositories.ReplyIntentProfileRepository.Take(sqls.DB(), "code = ?", code); existing != nil {
		return nil, errorsx.InvalidParam("行业编码已存在")
	}
	item := &models.ReplyIntentProfile{
		Code:               code,
		Name:               name,
		IndustryCode:       normalizeReplyIntentIndustryCode(req.IndustryCode),
		Description:        strings.TrimSpace(req.Description),
		IntentDetectPrompt: normalizeIntentDetectPrompt(req.IntentDetectPrompt),
		IntentJSONSchema:   normalizeIntentJSONSchema(req.IntentJSONSchema),
		Status:             normalizeReplyIntentProfileStatus(req.Status),
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
	item := s.Get(req.ID)
	if item == nil {
		return errorsx.InvalidParam("意图行业配置不存在")
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return errorsx.InvalidParam("行业编码和名称不能为空")
	}
	if existing := repositories.ReplyIntentProfileRepository.Take(sqls.DB(), "code = ? AND id <> ?", code, req.ID); existing != nil {
		return errorsx.InvalidParam("行业编码已存在")
	}
	return repositories.ReplyIntentProfileRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"code":                 code,
		"name":                 name,
		"industry_code":        normalizeReplyIntentIndustryCode(req.IndustryCode),
		"description":          strings.TrimSpace(req.Description),
		"intent_detect_prompt": normalizeIntentDetectPrompt(req.IntentDetectPrompt),
		"intent_json_schema":   normalizeIntentJSONSchema(req.IntentJSONSchema),
		"status":               normalizeReplyIntentProfileStatus(req.Status),
		"sort_no":              req.SortNo,
		"remark":               strings.TrimSpace(req.Remark),
		"update_user_id":       operator.UserID,
		"update_user_name":     operator.Username,
		"updated_at":           time.Now(),
	})
}

func (s *replyIntentProfileService) DeleteReplyIntentProfile(id int64) error {
	if s.Get(id) == nil {
		return errorsx.InvalidParam("意图行业配置不存在")
	}
	var count int64
	db := sqls.DB()
	db.Model(&models.ReplyIntentConfig{}).Where("intent_profile_id = ?", id).Count(&count)
	if count > 0 {
		return errorsx.InvalidParam("该意图行业已被意图分类使用，不能删除")
	}
	db.Model(&models.Company{}).Where("intent_profile_id = ?", id).Count(&count)
	if count > 0 {
		return errorsx.InvalidParam("该意图行业已被公司使用，不能删除")
	}
	db.Model(&models.WxWorkProtocolInstance{}).Where("intent_profile_id = ?", id).Count(&count)
	if count > 0 {
		return errorsx.InvalidParam("该意图行业已被企微员工号使用，不能删除")
	}
	return repositories.ReplyIntentProfileRepository.Delete(sqls.DB(), id)
}

func (s *replyIntentProfileService) DefaultHotelProfile() *models.ReplyIntentProfile {
	if sqls.DB() == nil {
		return nil
	}
	if item := repositories.ReplyIntentProfileRepository.Take(sqls.DB(), "code = ?", replyintent.DefaultHotelProfileCode); item != nil && item.Status == enums.StatusOk {
		return item
	}
	return repositories.ReplyIntentProfileRepository.Take(sqls.DB(), "status = ?", enums.StatusOk)
}

func normalizeReplyIntentIndustryCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return replyintent.DefaultHotelIndustryCode
	}
	return value
}

func normalizeIntentDetectPrompt(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return replyintent.DefaultHotelIntentDetectPrompt()
	}
	return value
}

func normalizeIntentJSONSchema(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return replyintent.DefaultHotelIntentJSONSchema()
	}
	return value
}

func normalizeReplyIntentProfileStatus(status enums.Status) enums.Status {
	if status == enums.StatusDisabled || status == enums.StatusDeleted {
		return status
	}
	return enums.StatusOk
}
