package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TagService = newTagService()

func newTagService() *tagService {
	return &tagService{}
}

type tagService struct {
}

var replyTagScenes = map[string]struct{}{
	"room_assignment": {}, "room_selection": {}, "arrival_service": {},
	"stay_service": {}, "checkout_service": {}, "invoice_service": {},
	"parking_service": {}, "pet_service": {}, "room_service": {},
	"customer_profile": {},
}

func (s *tagService) Get(id int64) *models.Tag {
	return repositories.TagRepository.Get(sqls.DB(), id)
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

func (s *tagService) Delete(id int64) error {
	return repositories.TagRepository.Delete(sqls.DB(), id)
}

func (s *tagService) GetChildren(parentID int64) []models.Tag {
	return s.Find(sqls.NewCnd().Eq("parent_id", parentID).Asc("sort_no").Asc("id"))
}

func (s *tagService) HasChildren(parentID int64) bool {
	return s.Count(sqls.NewCnd().Eq("parent_id", parentID)) > 0
}

func (s *tagService) FindByNameAndScope(name string, companyID, parentID int64) *models.Tag {
	return s.FindOne(sqls.NewCnd().Eq("name", name).Eq("company_id", companyID).Eq("parent_id", parentID).Where("status <> ?", enums.StatusDeleted))
}

func (s *tagService) CreateTag(req request.CreateTagRequest, operator *dto.AuthPrincipal) (*models.Tag, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}

	if err := s.requireCreateScope(req.CompanyID, operator); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	isCategory := req.ParentID == 0
	if err := validateTagName(name, isCategory); err != nil {
		return nil, err
	}
	var parent *models.Tag
	if req.ParentID > 0 {
		parent = s.Get(req.ParentID)
		if parent == nil || parent.Status != enums.StatusOk || parent.ParentID != 0 || parent.MergedIntoTagID > 0 {
			return nil, errorsx.InvalidParam("父标签不存在")
		}
		if parent.CompanyID != 0 && parent.CompanyID != req.CompanyID {
			return nil, errorsx.InvalidParam("父标签不属于当前公司")
		}
	}

	existing := s.FindByNameAndScope(name, req.CompanyID, req.ParentID)
	if existing != nil {
		return nil, errorsx.InvalidParam("同级下已存在相同名称的标签")
	}

	applicableScene := strings.TrimSpace(req.ApplicableScene)
	if isCategory {
		applicableScene = ""
	} else if err := validateReplyTagScene(applicableScene, req.ReplyEnabled); err != nil {
		return nil, err
	}
	item := &models.Tag{
		CompanyID:       req.CompanyID,
		ParentID:        req.ParentID,
		Name:            name,
		SemanticKey:     newCustomTagSemanticKey(isCategory),
		Aliases:         strings.TrimSpace(req.Aliases),
		AIEnabled:       !isCategory && req.AIEnabled,
		ReplyEnabled:    !isCategory && req.ReplyEnabled,
		ApplicableScene: applicableScene,
		Remark:          strings.TrimSpace(req.Remark),
		Status:          enums.StatusOk,
		AuditFields:     utils.BuildAuditFields(operator),
	}

	item.SortNo = s.NextSortNo(req.ParentID)
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

func (s *tagService) UpdateTag(req request.UpdateTagRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}

	item := s.Get(req.ID)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}
	if err := s.requireManageTag(item, operator); err != nil {
		return err
	}
	if item.SystemDefined && (req.CompanyID != item.CompanyID || req.ParentID != item.ParentID || strings.TrimSpace(req.Name) != item.Name) {
		return errorsx.Forbidden("系统标准标签的名称、分类和语义标识不可修改")
	}
	if !item.SystemDefined && req.CompanyID != item.CompanyID {
		return errorsx.InvalidParam("标签所属公司不可修改")
	}

	name := strings.TrimSpace(req.Name)
	isCategory := req.ParentID == 0
	if (item.ParentID == 0) != isCategory {
		return errorsx.InvalidParam("分类和叶子标签不能相互转换")
	}
	if err := validateTagName(name, isCategory); err != nil {
		return err
	}

	if req.ParentID > 0 {
		if req.ParentID == req.ID {
			return errorsx.InvalidParam("不能将标签设为自己的子标签")
		}
		parent := s.Get(req.ParentID)
		if parent == nil || parent.Status != enums.StatusOk || parent.ParentID != 0 || parent.MergedIntoTagID > 0 {
			return errorsx.InvalidParam("父标签不存在")
		}
		if parent.CompanyID != 0 && parent.CompanyID != item.CompanyID {
			return errorsx.InvalidParam("父标签不属于当前公司")
		}
	}

	existing := s.FindByNameAndScope(name, item.CompanyID, req.ParentID)
	if existing != nil && existing.ID != req.ID {
		return errorsx.InvalidParam("同级下已存在相同名称的标签")
	}

	applicableScene := strings.TrimSpace(req.ApplicableScene)
	if isCategory {
		applicableScene = ""
	} else if err := validateReplyTagScene(applicableScene, req.ReplyEnabled); err != nil {
		return err
	}
	parentID := req.ParentID
	if item.SystemDefined {
		name = item.Name
		parentID = item.ParentID
	}
	return s.Updates(req.ID, map[string]any{
		"parent_id":        parentID,
		"name":             name,
		"semantic_key":     item.SemanticKey,
		"aliases":          strings.TrimSpace(req.Aliases),
		"ai_enabled":       !isCategory && req.AIEnabled,
		"reply_enabled":    !isCategory && req.ReplyEnabled,
		"applicable_scene": applicableScene,
		"remark":           strings.TrimSpace(req.Remark),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func newCustomTagSemanticKey(category bool) string {
	prefix := "custom."
	if category {
		prefix = "category.custom."
	}
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (s *tagService) UpdateSort(ids []int64, operator *dto.AuthPrincipal) error {
	if len(ids) == 0 {
		return nil
	}
	parentID := int64(-1)
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return errorsx.InvalidParam("标签排序列表不能包含重复项")
		}
		seen[id] = struct{}{}
		item := s.Get(id)
		if item == nil {
			return errorsx.InvalidParam("标签不存在")
		}
		if err := s.requireManageTag(item, operator); err != nil {
			return err
		}
		if parentID == -1 {
			parentID = item.ParentID
		} else if parentID != item.ParentID {
			return errorsx.InvalidParam("只能调整同一分类下的标签排序")
		}
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for i, id := range ids {
			if err := repositories.TagRepository.Updates(ctx.Tx, id, map[string]any{
				"sort_no": i + 1, "update_user_id": operator.UserID,
				"update_user_name": operator.Username, "updated_at": time.Now(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *tagService) DeleteTag(id int64) error {
	return s.deleteTag(id, nil)
}

func (s *tagService) DeleteTagAs(id int64, operator *dto.AuthPrincipal) error {
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}
	if err := s.requireManageTag(item, operator); err != nil {
		return err
	}
	if item.SystemDefined {
		return errorsx.Forbidden("系统标准标签不可删除")
	}
	return s.deleteTag(id, operator)
}

func (s *tagService) deleteTag(id int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item := repositories.TagRepository.Get(ctx.Tx, id)
		if item == nil {
			return errorsx.InvalidParam("标签不存在")
		}
		if item.SystemDefined {
			return errorsx.Forbidden("系统标准标签不可删除")
		}
		if repositories.TagRepository.Count(ctx.Tx, sqls.NewCnd().Eq("parent_id", id)) > 0 {
			return errorsx.InvalidParam("该标签下存在子标签，无法删除")
		}
		if repositories.TicketTagRepository.Take(ctx.Tx, "tag_id = ?", id) != nil {
			return errorsx.InvalidParam("该标签已关联工单，无法删除")
		}
		if repositories.CustomerTagRelationRepository.TakeByTagID(ctx.Tx, id) != nil {
			return errorsx.InvalidParam("该标签已关联客户长期标签，无法删除")
		}
		if err := repositories.TagRepository.Delete(ctx.Tx, id); err != nil {
			return err
		}
		return s.clearSingletonConflictGroup(ctx.Tx, item.ConflictGroup, operator)
	})
}

func (s *tagService) FindAll() []models.Tag {
	return s.Find(sqls.NewCnd().Asc("sort_no").Asc("id"))
}

func (s *tagService) FindAllVisible(operator *dto.AuthPrincipal) []models.Tag {
	cnd := s.ApplyVisibleScope(sqls.NewCnd().Where("status <> ?", enums.StatusDeleted), operator)
	return s.Find(cnd.Asc("sort_no").Asc("id"))
}

func (s *tagService) ApplyVisibleScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	if operator == nil {
		return cnd.Eq("company_id", 0)
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return cnd
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if len(scope.CompanyIDs) == 0 {
		return cnd.Eq("company_id", 0)
	}
	return cnd.Where("company_id = 0 OR company_id IN ?", scope.CompanyIDs)
}

func (s *tagService) GetVisible(id int64, operator *dto.AuthPrincipal) *models.Tag {
	item := s.Get(id)
	if item == nil || !s.canViewCompany(item.CompanyID, operator) {
		return nil
	}
	return item
}

func (s *tagService) UpdateStatus(id int64, status int, operator *dto.AuthPrincipal) error {
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("标签不存在")
	}
	if err := s.requireManageTag(item, operator); err != nil {
		return err
	}
	if item.SystemDefined && item.ParentID == 0 {
		return errorsx.Forbidden("系统标准分类不可停用")
	}
	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return errorsx.InvalidParam("标签状态不合法")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.TagRepository.Updates(ctx.Tx, id, map[string]any{
			"status": status, "update_user_id": operator.UserID,
			"update_user_name": operator.Username, "updated_at": time.Now(),
		}); err != nil {
			return err
		}
		if status == int(enums.StatusDisabled) {
			return s.clearSingletonConflictGroup(ctx.Tx, item.ConflictGroup, operator)
		}
		return nil
	})
}

func (s *tagService) ListConflictGroups(companyID int64, operator *dto.AuthPrincipal) []response.TagConflictGroupResponse {
	list := s.FindAllVisible(operator)
	groups := make(map[string]*response.TagConflictGroupResponse)
	order := make([]string, 0)
	for i := range list {
		item := &list[i]
		key := strings.TrimSpace(item.ConflictGroup)
		if item.ParentID == 0 || item.Status != enums.StatusOk || item.MergedIntoTagID > 0 || key == "" || (companyID > 0 && item.CompanyID != 0 && item.CompanyID != companyID) {
			continue
		}
		group := groups[key]
		if group == nil {
			group = &response.TagConflictGroupResponse{GroupKey: key, CompanyID: item.CompanyID, SystemDefined: item.SystemDefined, Members: make([]response.TagConflictGroupMemberResponse, 0, 2)}
			groups[key] = group
			order = append(order, key)
		}
		if item.CompanyID > 0 {
			group.CompanyID = item.CompanyID
		}
		group.SystemDefined = group.SystemDefined || item.SystemDefined
		if group.SystemDefined {
			group.CompanyID = 0
		}
		group.Members = append(group.Members, response.TagConflictGroupMemberResponse{TagID: item.ID, Name: item.Name, CompanyID: item.CompanyID, SystemDefined: item.SystemDefined})
	}
	ret := make([]response.TagConflictGroupResponse, 0, len(order))
	for _, key := range order {
		if group := groups[key]; group != nil && len(group.Members) >= 2 {
			ret = append(ret, *group)
		}
	}
	return ret
}

func (s *tagService) CreateConflictGroup(req request.CreateTagConflictGroupRequest, operator *dto.AuthPrincipal) (string, error) {
	ids := uniquePositiveTagIDs(req.TagIDs)
	if len(ids) < 2 || len(ids) != len(req.TagIDs) {
		return "", errorsx.InvalidParam("互斥组至少需要两个不重复标签")
	}
	for _, id := range ids {
		item := s.Get(id)
		if item == nil || item.ParentID == 0 || item.Status != enums.StatusOk || item.MergedIntoTagID > 0 || item.SystemDefined || item.CompanyID != req.CompanyID {
			return "", errorsx.InvalidParam("互斥组只能包含同一作用域的有效自定义标签")
		}
		if err := s.requireManageTag(item, operator); err != nil {
			return "", err
		}
	}
	key := "custom." + strings.ReplaceAll(uuid.NewString(), "-", "")
	oldKeys := make(map[string]struct{})
	for _, id := range ids {
		if item := s.Get(id); item != nil && strings.TrimSpace(item.ConflictGroup) != "" {
			oldKeys[item.ConflictGroup] = struct{}{}
		}
	}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, id := range ids {
			if err := repositories.TagRepository.Updates(ctx.Tx, id, map[string]any{
				"conflict_group": key, "update_user_id": operator.UserID,
				"update_user_name": operator.Username, "updated_at": time.Now(),
			}); err != nil {
				return err
			}
		}
		for oldKey := range oldKeys {
			if err := s.clearSingletonConflictGroup(ctx.Tx, oldKey, operator); err != nil {
				return err
			}
		}
		return nil
	})
	return key, err
}

func (s *tagService) AssignConflictGroup(req request.AssignTagConflictGroupRequest, operator *dto.AuthPrincipal) error {
	item := s.Get(req.TagID)
	if item == nil || item.ParentID == 0 || item.Status != enums.StatusOk || item.MergedIntoTagID > 0 {
		return errorsx.InvalidParam("标签不存在或不是有效叶子标签")
	}
	if err := s.requireManageTag(item, operator); err != nil {
		return err
	}
	key := strings.TrimSpace(req.GroupKey)
	if strings.HasPrefix(key, "custom.") {
		members := s.Find(sqls.NewCnd().Eq("conflict_group", key).Eq("status", enums.StatusOk).Eq("merged_into_tag_id", 0).Where("parent_id <> ?", 0))
		if len(members) == 0 {
			return errorsx.InvalidParam("互斥组不存在")
		}
		for i := range members {
			if members[i].SystemDefined || members[i].CompanyID != item.CompanyID {
				return errorsx.Forbidden("不能加入其他公司或系统互斥组")
			}
		}
	} else if key != "" {
		if s.FindOne(sqls.NewCnd().Eq("conflict_group", key).Eq("system_defined", true).Eq("company_id", 0).Eq("status", enums.StatusOk).Eq("merged_into_tag_id", 0).Where("parent_id <> ?", 0)) == nil {
			return errorsx.InvalidParam("标准互斥组不存在")
		}
	}
	oldKey := strings.TrimSpace(item.ConflictGroup)
	if oldKey == key {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.TagRepository.Updates(ctx.Tx, item.ID, map[string]any{
			"conflict_group": key, "update_user_id": operator.UserID,
			"update_user_name": operator.Username, "updated_at": time.Now(),
		}); err != nil {
			return err
		}
		return s.clearSingletonConflictGroup(ctx.Tx, oldKey, operator)
	})
}

func (s *tagService) DeleteConflictGroup(req request.DeleteTagConflictGroupRequest, operator *dto.AuthPrincipal) error {
	key := strings.TrimSpace(req.GroupKey)
	if !strings.HasPrefix(key, "custom.") {
		return errorsx.InvalidParam("只能删除自定义互斥组")
	}
	members := s.Find(sqls.NewCnd().Eq("conflict_group", key))
	if len(members) == 0 {
		return nil
	}
	for i := range members {
		if members[i].CompanyID != req.CompanyID {
			return errorsx.Forbidden("互斥组不属于当前公司")
		}
		if err := s.requireManageTag(&members[i], operator); err != nil {
			return err
		}
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return ctx.Tx.Model(&models.Tag{}).Where("conflict_group = ?", key).Updates(map[string]any{
			"conflict_group": "", "update_user_id": operator.UserID,
			"update_user_name": operator.Username, "updated_at": time.Now(),
		}).Error
	})
}

func (s *tagService) clearSingletonConflictGroup(db *gorm.DB, key string, operator *dto.AuthPrincipal) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	var count int64
	if err := db.Model(&models.Tag{}).Where("conflict_group = ? AND status = ? AND parent_id <> ? AND merged_into_tag_id = ?", key, enums.StatusOk, 0, 0).Count(&count).Error; err != nil {
		return err
	}
	if count >= 2 {
		return nil
	}
	operatorID := constants.SystemAuditUserID
	operatorName := constants.SystemAuditUserName
	if operator != nil {
		operatorID = operator.UserID
		operatorName = operator.Username
	}
	return db.Model(&models.Tag{}).Where("conflict_group = ?", key).Updates(map[string]any{
		"conflict_group": "", "update_user_id": operatorID,
		"update_user_name": operatorName, "updated_at": time.Now(),
	}).Error
}

func (s *tagService) requireCreateScope(companyID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		return nil
	}
	if companyID <= 0 {
		return errorsx.Forbidden("只有超级管理员可以创建全局标签")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return nil
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) && slices.Contains(AgentTeamScopeService.Resolve(operator).CompanyIDs, companyID) {
		return nil
	}
	return errorsx.Forbidden("无权管理该公司标签")
}

func (s *tagService) requireManageTag(item *models.Tag, operator *dto.AuthPrincipal) error {
	if item == nil || operator == nil {
		return errorsx.Forbidden("无权管理该标签")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		return nil
	}
	if item.SystemDefined {
		return errorsx.Forbidden("只有超级管理员可以管理系统标签")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return nil
	}
	if item.CompanyID <= 0 {
		return errorsx.Forbidden("只有管理员可以管理全局自定义标签")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) && slices.Contains(AgentTeamScopeService.Resolve(operator).CompanyIDs, item.CompanyID) {
		return nil
	}
	return errorsx.Forbidden("无权管理该公司标签")
}

func (s *tagService) canViewCompany(companyID int64, operator *dto.AuthPrincipal) bool {
	if companyID == 0 {
		return true
	}
	if operator == nil {
		return false
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return true
	}
	return slices.Contains(AgentTeamScopeService.Resolve(operator).CompanyIDs, companyID)
}

func validateTagName(name string, category bool) error {
	count := len([]rune(strings.TrimSpace(name)))
	max := 5
	label := "标签"
	if category {
		max = 20
		label = "分类"
	}
	if count < 1 || count > max {
		return errorsx.InvalidParam(label + "名称必须为 1～" + strconv.Itoa(max) + " 个字")
	}
	return nil
}

func validateReplyTagScene(scene string, required bool) error {
	if scene == "" && !required {
		return nil
	}
	if _, ok := replyTagScenes[scene]; !ok {
		return errorsx.InvalidParam("标签适用场景不合法")
	}
	return nil
}

func uniquePositiveTagIDs(values []int64) []int64 {
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

func (s *tagService) GetSelfAndDescendantIDs(tagID int64) []int64 {
	if tagID <= 0 {
		return nil
	}

	allTags := s.FindAll()
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
