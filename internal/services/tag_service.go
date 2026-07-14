package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var TagService = newTagService()

func newTagService() *tagService {
	return &tagService{}
}

type tagService struct {
}

func (s *tagService) Get(id int64) *models.Tag {
	return repositories.TagRepository.Get(sqls.DB(), id)
}

func (s *tagService) GetInTenant(id, tenantID int64) *models.Tag {
	return repositories.TagRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *tagService) Take(where ...interface{}) *models.Tag {
	return repositories.TagRepository.Take(sqls.DB(), where...)
}

func (s *tagService) Find(cnd *sqls.Cnd) []models.Tag {
	return repositories.TagRepository.Find(sqls.DB(), cnd)
}

func (s *tagService) FindOne(cnd *sqls.Cnd) *models.Tag {
	return repositories.TagRepository.FindOne(sqls.DB(), cnd)
}

func (s *tagService) FindPageByParams(params *params.QueryParams) (list []models.Tag, paging *sqls.Paging) {
	return repositories.TagRepository.FindPageByParams(sqls.DB(), params)
}

func (s *tagService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Tag, paging *sqls.Paging) {
	return repositories.TagRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *tagService) FindPageForOperator(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.Tag, paging *sqls.Paging, err error) {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return nil, nil, err
	}
	list, paging = repositories.TagRepository.FindPageByCnd(sqls.DB(), cnd.Eq("tenant_id", tenantID))
	return list, paging, nil
}

func (s *tagService) Count(cnd *sqls.Cnd) int64 {
	return repositories.TagRepository.Count(sqls.DB(), cnd)
}

func (s *tagService) Create(t *models.Tag) error {
	return repositories.TagRepository.Create(sqls.DB(), t)
}

func (s *tagService) Update(t *models.Tag) error {
	return repositories.TagRepository.Update(sqls.DB(), t)
}

func (s *tagService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.TagRepository.Updates(sqls.DB(), id, columns)
}

func (s *tagService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.TagRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *tagService) Delete(id int64) {
	repositories.TagRepository.Delete(sqls.DB(), id)
}

func (s *tagService) GetChildren(parentID int64) []models.Tag {
	return s.Find(sqls.NewCnd().Eq("parent_id", parentID).Asc("sort_no").Asc("id"))
}

func (s *tagService) HasChildren(parentID int64) bool {
	return s.Count(sqls.NewCnd().Eq("parent_id", parentID)) > 0
}

func (s *tagService) FindByNameAndParentID(name string, parentID int64) *models.Tag {
	return s.FindOne(sqls.NewCnd().Eq("name", name).Eq("parent_id", parentID))
}

func (s *tagService) CreateTag(req request.CreateTagRequest, operator *dto.AuthPrincipal) (*models.Tag, error) {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("标签名称不能为空")
	}

	if req.ParentID > 0 {
		parent := s.GetInTenant(req.ParentID, tenantID)
		if parent == nil {
			return nil, errorsx.InvalidParam("父标签不存在")
		}
	}

	existing := s.FindOne(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("name", name).Eq("parent_id", req.ParentID))
	if existing != nil {
		return nil, errorsx.InvalidParam("同级下已存在相同名称的标签")
	}

	item := &models.Tag{
		TenantID:    tenantID,
		ParentID:    req.ParentID,
		Name:        name,
		Remark:      strings.TrimSpace(req.Remark),
		Status:      enums.StatusOk,
		AuditFields: utils.BuildAuditFields(operator),
	}

	item.SortNo = s.NextSortNoInTenant(req.ParentID, tenantID)
	if err := s.Create(item); err != nil {
		return nil, err
	}

	return item, nil
}

func (s *tagService) NextSortNo(parentID int64) int {
	if temp := s.FindOne(sqls.NewCnd().Eq("parent_id", parentID).Desc("sort_no").Desc("id")); temp != nil {
		return temp.SortNo + 1
	}
	return 1
}

func (s *tagService) NextSortNoInTenant(parentID, tenantID int64) int {
	if temp := s.FindOne(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("parent_id", parentID).Desc("sort_no").Desc("id")); temp != nil {
		return temp.SortNo + 1
	}
	return 1
}

func (s *tagService) UpdateTag(req request.UpdateTagRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return err
	}

	item := s.GetInTenant(req.ID, tenantID)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errorsx.InvalidParam("标签名称不能为空")
	}

	if req.ParentID > 0 {
		if req.ParentID == req.ID {
			return errorsx.InvalidParam("不能将标签设为自己的子标签")
		}
		parent := s.GetInTenant(req.ParentID, tenantID)
		if parent == nil {
			return errorsx.InvalidParam("父标签不存在")
		}
	}

	existing := s.FindOne(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("name", name).Eq("parent_id", req.ParentID))
	if existing != nil && existing.ID != req.ID {
		return errorsx.InvalidParam("同级下已存在相同名称的标签")
	}

	return repositories.TagRepository.UpdatesInTenant(sqls.DB(), req.ID, tenantID, map[string]any{
		"parent_id":        req.ParentID,
		"name":             name,
		"remark":           strings.TrimSpace(req.Remark),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *tagService) UpdateSort(ids []int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return err
	}
	ids = normalizeTagIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	items := repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("id", ids))
	if len(items) != len(ids) {
		return errorsx.InvalidParam("存在不属于当前接入公司的标签")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for i, id := range ids {
			if err := repositories.TagRepository.UpdateColumnInTenant(ctx.Tx, id, tenantID, "sort_no", i+1); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *tagService) DeleteTag(id int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return err
	}
	item := s.GetInTenant(id, tenantID)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}

	if s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("parent_id", id)) > 0 {
		return errorsx.InvalidParam("该标签下存在子标签，无法删除")
	}
	if ConversationTagService.Take("tenant_id = ? AND tag_id = ?", tenantID, id) != nil {
		return errorsx.InvalidParam("该标签已关联会话，无法删除")
	}
	if TicketTagService.Take("tenant_id = ? AND tag_id = ?", tenantID, id) != nil {
		return errorsx.InvalidParam("该标签已关联工单，无法删除")
	}

	return repositories.TagRepository.DeleteInTenant(sqls.DB(), id, tenantID)
}

func (s *tagService) FindAll() []models.Tag {
	return s.Find(sqls.NewCnd().Asc("sort_no").Asc("id"))
}

func (s *tagService) FindAllInTenant(tenantID int64) []models.Tag {
	if tenantID <= 0 {
		return nil
	}
	return s.Find(sqls.NewCnd().Eq("tenant_id", tenantID).Asc("sort_no").Asc("id"))
}

func (s *tagService) FindAllForOperator(operator *dto.AuthPrincipal) ([]models.Tag, error) {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return nil, err
	}
	return s.FindAllInTenant(tenantID), nil
}

func (s *tagService) GetForOperator(id int64, operator *dto.AuthPrincipal) (*models.Tag, error) {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return nil, err
	}
	item := s.GetInTenant(id, tenantID)
	if item == nil {
		return nil, errorsx.InvalidParam("标签不存在")
	}
	return item, nil
}

func (s *tagService) GetSelfAndDescendantIDs(tagID int64) []int64 {
	return collectTagSelfAndDescendantIDs(tagID, s.FindAll())
}

func (s *tagService) GetSelfAndDescendantIDsInTenant(tagID, tenantID int64) []int64 {
	return collectTagSelfAndDescendantIDs(tagID, s.FindAllInTenant(tenantID))
}

func collectTagSelfAndDescendantIDs(tagID int64, allTags []models.Tag) []int64 {
	if tagID <= 0 {
		return nil
	}
	if len(allTags) == 0 {
		return nil
	}

	exists := false
	childrenMap := make(map[int64][]int64, len(allTags))
	for _, item := range allTags {
		if item.ID == tagID {
			exists = true
		}
		childrenMap[item.ParentID] = append(childrenMap[item.ParentID], item.ID)
	}
	if !exists {
		return nil
	}

	result := make([]int64, 0, 8)
	visited := make(map[int64]bool, len(allTags))
	var walk func(id int64)
	walk = func(id int64) {
		if visited[id] {
			return
		}
		visited[id] = true
		result = append(result, id)
		for _, childID := range childrenMap[id] {
			walk(childID)
		}
	}
	walk(tagID)

	return result
}

func normalizeTagIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *tagService) UpdateStatus(id int64, status int, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "标签")
	if err != nil {
		return err
	}

	item := s.GetInTenant(id, tenantID)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}

	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return errorsx.InvalidParam("状态值不合法")
	}

	now := time.Now()
	return repositories.TagRepository.UpdatesInTenant(sqls.DB(), id, tenantID, map[string]any{
		"status":           status,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       now,
	})
}
