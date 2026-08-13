package services

import (
	"time"

	"agent-desk/internal/ai/runtime/actions"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ReplyActionCatalogService = newReplyActionCatalogService()

func newReplyActionCatalogService() *replyActionCatalogService {
	return &replyActionCatalogService{}
}

type replyActionCatalogService struct{}

// Seed 把代码注册表同步到数据库：以 code 为键补齐缺失动作，
// 并刷新名称、类型、描述与执行器引用；不覆盖运营已修改的开关与排序。
func (s *replyActionCatalogService) Seed() error {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.ReplyActionDefinition{}) {
		return nil
	}
	now := time.Now()
	for _, def := range actions.List() {
		existing := repositories.ReplyActionDefinitionRepository.Take(db, "code = ?", def.Code)
		if existing == nil {
			item := &models.ReplyActionDefinition{
				Code: def.Code, Name: def.Name, Kind: string(def.Kind),
				Description: def.Description, InputSchema: def.InputSchema,
				RequireConfirmation: def.RequireConfirmation, ExecutorRef: def.ExecutorRef,
				Enabled: def.DefaultEnabled, SortNo: 0,
				AuditFields: models.AuditFields{
					CreatedAt: now, CreateUserName: "system",
					UpdatedAt: now, UpdateUserName: "system",
				},
			}
			if err := repositories.ReplyActionDefinitionRepository.UpsertByCode(db, item); err != nil {
				return err
			}
			continue
		}
		if err := repositories.ReplyActionDefinitionRepository.Updates(db, existing.ID, map[string]any{
			"name":                 def.Name,
			"kind":                 string(def.Kind),
			"description":          def.Description,
			"input_schema":         def.InputSchema,
			"require_confirmation": def.RequireConfirmation,
			"executor_ref":         def.ExecutorRef,
			"updated_at":           now,
			"update_user_name":     "system",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *replyActionCatalogService) List() []models.ReplyActionDefinition {
	return repositories.ReplyActionDefinitionRepository.Find(sqls.DB(), sqls.NewCnd().Asc("sort_no").Asc("id"))
}

func (s *replyActionCatalogService) Get(id int64) *models.ReplyActionDefinition {
	return repositories.ReplyActionDefinitionRepository.Get(sqls.DB(), id)
}

// SetEnabled 开关一个动作。外部未接入的动作禁止打开。
func (s *replyActionCatalogService) SetEnabled(id int64, enabled bool, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := repositories.ReplyActionDefinitionRepository.Get(sqls.DB(), id)
	if item == nil {
		return errorsx.InvalidParam("动作不存在")
	}
	if enabled {
		if item.Kind == string(actions.KindExternal) && !actions.Provisioned(item.Code) {
			return errorsx.InvalidParam("该动作依赖的外部系统尚未接入，暂不能启用")
		}
	}
	return repositories.ReplyActionDefinitionRepository.Updates(sqls.DB(), id, map[string]any{
		"enabled":          enabled,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

// EnabledActionMap 返回当前启用的动作 code 集合，供运行时快速过滤。
func (s *replyActionCatalogService) EnabledActionMap() map[string]bool {
	items := repositories.ReplyActionDefinitionRepository.Find(sqls.DB(), sqls.NewCnd().Eq("enabled", true))
	ret := make(map[string]bool, len(items))
	for _, item := range items {
		ret[item.Code] = true
	}
	return ret
}

// ResolveProvisioned 判断动作是否已接入可执行。
func (s *replyActionCatalogService) ResolveProvisioned(code string) bool {
	return actions.Provisioned(code)
}

var _ = utils.BuildAuditFields
