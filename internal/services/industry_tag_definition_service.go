package services

import (
	"regexp"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var IndustryTagDefinitionService = &industryTagDefinitionService{}

var industryTagSemanticKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

type industryTagDefinitionService struct{}

func (s *industryTagDefinitionService) Get(id int64) *models.IndustryTagDefinition {
	if id <= 0 {
		return nil
	}
	return repositories.IndustryTagDefinitionRepository.Get(sqls.DB(), id)
}

func (s *industryTagDefinitionService) FindByProfile(profileID int64) ([]models.IndustryTagDefinition, error) {
	if profileID <= 0 {
		return nil, errorsx.InvalidParam("必须选择行业 Profile")
	}
	if profile := repositories.ReplyIntentProfileRepository.Get(sqls.DB(), profileID); profile == nil || profile.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("行业 Profile 不存在")
	}
	return repositories.IndustryTagDefinitionRepository.FindByProfile(sqls.DB(), profileID)
}

func (s *industryTagDefinitionService) FindPageByCnd(cnd *sqls.Cnd) ([]models.IndustryTagDefinition, *sqls.Paging) {
	return repositories.IndustryTagDefinitionRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *industryTagDefinitionService) Create(
	req request.CreateIndustryTagDefinitionRequest,
	operator *dto.AuthPrincipal,
) (*models.IndustryTagDefinition, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	name := strings.TrimSpace(req.Name)
	semanticKey := strings.TrimSpace(req.SemanticKey)
	if err := validateIndustryTagDefinitionFields(name, semanticKey, req.Status); err != nil {
		return nil, err
	}
	var created *models.IndustryTagDefinition
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		profile := repositories.ReplyIntentProfileRepository.Get(ctx.Tx, req.IntentProfileID)
		if profile == nil || profile.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("行业 Profile 不存在")
		}
		parent, err := validateIndustryTagDefinitionParentDB(ctx.Tx, req.IntentProfileID, req.ParentID)
		if err != nil {
			return err
		}
		if existing := repositories.IndustryTagDefinitionRepository.TakeBySemanticKey(ctx.Tx, req.IntentProfileID, semanticKey); existing != nil {
			return errorsx.InvalidParam("同一行业内 SemanticKey 已存在")
		}
		aiEnabled, replyEnabled, conflictGroup := normalizeIndustryTagDefinitionBehavior(
			parent, req.AIEnabled, req.ReplyEnabled, req.ConflictGroup,
		)
		created = &models.IndustryTagDefinition{
			IntentProfileID:    req.IntentProfileID,
			ParentID:           req.ParentID,
			Name:               name,
			SemanticKey:        semanticKey,
			Aliases:            strings.TrimSpace(req.Aliases),
			ConflictGroup:      conflictGroup,
			ApplicableScene:    strings.TrimSpace(req.ApplicableScene),
			AIEnabled:          aiEnabled,
			ReplyEnabled:       replyEnabled,
			DefinitionRevision: profile.Revision,
			SortNo:             req.SortNo,
			Status:             normalizeIndustryTagDefinitionStatus(req.Status),
			AuditFields:        utils.BuildAuditFields(operator),
		}
		if err := repositories.IndustryTagDefinitionRepository.Create(ctx.Tx, created); err != nil {
			return err
		}
		if err := markIntentProfileDraftForMutationDB(ctx.Tx, profile.ID, operator); err != nil {
			return err
		}
		created = repositories.IndustryTagDefinitionRepository.Get(ctx.Tx, created.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *industryTagDefinitionService) Update(
	req request.UpdateIndustryTagDefinitionRequest,
	operator *dto.AuthPrincipal,
) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errorsx.InvalidParam("标签名称不能为空")
	}
	if req.Status != enums.StatusOk && req.Status != enums.StatusDisabled {
		return errorsx.InvalidParam("标签模板状态不合法")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.IndustryTagDefinitionRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if item == nil || item.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("行业标签模板不存在")
		}
		if req.IntentProfileID != item.IntentProfileID || req.ParentID != item.ParentID ||
			strings.TrimSpace(req.SemanticKey) != item.SemanticKey {
			return errorsx.InvalidParam("行业、所属分类和 SemanticKey 创建后不可修改")
		}
		isCategory := item.ParentID == 0
		if isCategory && req.Status == enums.StatusDisabled {
			childCount, err := repositories.IndustryTagDefinitionRepository.CountChildren(ctx.Tx, item.ID, true)
			if err != nil {
				return err
			}
			if childCount > 0 {
				return errorsx.InvalidParam("请先停用该分类下的全部标签")
			}
		}
		var parent *models.IndustryTagDefinition
		if !isCategory {
			parent = repositories.IndustryTagDefinitionRepository.Get(ctx.Tx, item.ParentID)
			if parent == nil || parent.IntentProfileID != item.IntentProfileID || parent.ParentID != 0 {
				return errorsx.InvalidParam("标签模板父级无效")
			}
		}
		aiEnabled, replyEnabled, conflictGroup := normalizeIndustryTagDefinitionBehavior(
			parent, req.AIEnabled, req.ReplyEnabled, req.ConflictGroup,
		)
		if err := repositories.IndustryTagDefinitionRepository.Updates(ctx.Tx, item.ID, map[string]any{
			"name":             name,
			"aliases":          strings.TrimSpace(req.Aliases),
			"conflict_group":   conflictGroup,
			"applicable_scene": strings.TrimSpace(req.ApplicableScene),
			"ai_enabled":       aiEnabled,
			"reply_enabled":    replyEnabled,
			"sort_no":          req.SortNo,
			"status":           req.Status,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       time.Now(),
		}); err != nil {
			return err
		}
		return markIntentProfileDraftForMutationDB(ctx.Tx, item.IntentProfileID, operator)
	})
}

func validateIndustryTagDefinitionFields(name, semanticKey string, status enums.Status) error {
	if name == "" || semanticKey == "" {
		return errorsx.InvalidParam("标签名称和 SemanticKey 不能为空")
	}
	if len([]rune(name)) > 80 || len(semanticKey) > 128 {
		return errorsx.InvalidParam("标签名称或 SemanticKey 过长")
	}
	if !industryTagSemanticKeyPattern.MatchString(semanticKey) {
		return errorsx.InvalidParam("SemanticKey 只能使用小写字母、数字、点、下划线和连字符")
	}
	if status != enums.StatusOk && status != enums.StatusDisabled {
		return errorsx.InvalidParam("标签模板状态不合法")
	}
	return nil
}

func validateIndustryTagDefinitionParentDB(
	db *gorm.DB,
	profileID, parentID int64,
) (*models.IndustryTagDefinition, error) {
	if parentID <= 0 {
		return nil, nil
	}
	parent := repositories.IndustryTagDefinitionRepository.Get(db, parentID)
	if parent == nil || parent.Status == enums.StatusDeleted || parent.IntentProfileID != profileID || parent.ParentID != 0 {
		return nil, errorsx.InvalidParam("标签只能挂在同一行业的一级分类下")
	}
	return parent, nil
}

func normalizeIndustryTagDefinitionBehavior(
	parent *models.IndustryTagDefinition,
	aiEnabled, replyEnabled bool,
	conflictGroup string,
) (bool, bool, string) {
	if parent == nil {
		return false, false, ""
	}
	return aiEnabled, replyEnabled, strings.TrimSpace(conflictGroup)
}

func normalizeIndustryTagDefinitionStatus(status enums.Status) enums.Status {
	if status == enums.StatusDisabled {
		return enums.StatusDisabled
	}
	return enums.StatusOk
}
