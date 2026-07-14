package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var ConversationTagService = newConversationTagService()

func newConversationTagService() *conversationTagService {
	return &conversationTagService{}
}

type conversationTagService struct {
}

func (s *conversationTagService) Get(id int64) *models.ConversationTag {
	return repositories.ConversationTagRepository.Get(sqls.DB(), id)
}

func (s *conversationTagService) Take(where ...interface{}) *models.ConversationTag {
	return repositories.ConversationTagRepository.Take(sqls.DB(), where...)
}

func (s *conversationTagService) Find(cnd *sqls.Cnd) []models.ConversationTag {
	return repositories.ConversationTagRepository.Find(sqls.DB(), cnd)
}

func (s *conversationTagService) FindOne(cnd *sqls.Cnd) *models.ConversationTag {
	return repositories.ConversationTagRepository.FindOne(sqls.DB(), cnd)
}

func (s *conversationTagService) FindPageByParams(params *params.QueryParams) (list []models.ConversationTag, paging *sqls.Paging) {
	return repositories.ConversationTagRepository.FindPageByParams(sqls.DB(), params)
}

func (s *conversationTagService) FindPageByCnd(cnd *sqls.Cnd) (list []models.ConversationTag, paging *sqls.Paging) {
	return repositories.ConversationTagRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *conversationTagService) Count(cnd *sqls.Cnd) int64 {
	return repositories.ConversationTagRepository.Count(sqls.DB(), cnd)
}

func (s *conversationTagService) Create(t *models.ConversationTag) error {
	return repositories.ConversationTagRepository.Create(sqls.DB(), t)
}

func (s *conversationTagService) Update(t *models.ConversationTag) error {
	return repositories.ConversationTagRepository.Update(sqls.DB(), t)
}

func (s *conversationTagService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.ConversationTagRepository.Updates(sqls.DB(), id, columns)
}

func (s *conversationTagService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.ConversationTagRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *conversationTagService) Delete(id int64) {
	repositories.ConversationTagRepository.Delete(sqls.DB(), id)
}

func (s *conversationTagService) IsExistsInTenant(conversationID, tagID, tenantID int64) bool {
	return repositories.ConversationTagRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversationID).
		Eq("tag_id", tagID)) != nil
}

func (s *conversationTagService) AddTag(req request.AddConversationTagRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "会话标签")
	if err != nil {
		return err
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), req.ConversationID, tenantID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	tag := repositories.TagRepository.GetInTenant(sqls.DB(), req.TagID, tenantID)
	if tag == nil || tag.Status != enums.StatusOk {
		return errorsx.InvalidParam("标签不存在")
	}
	if s.IsExistsInTenant(req.ConversationID, req.TagID, tenantID) {
		return nil
	}
	return repositories.ConversationTagRepository.Create(sqls.DB(), &models.ConversationTag{
		TenantID:       tenantID,
		ConversationID: req.ConversationID,
		TagID:          req.TagID,
		AuditFields:    utils.BuildAuditFields(operator),
	})
}

func (s *conversationTagService) RemoveTag(req request.RemoveConversationTagRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "会话标签")
	if err != nil {
		return err
	}
	if repositories.ConversationRepository.GetInTenant(sqls.DB(), req.ConversationID, tenantID) == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	return repositories.ConversationTagRepository.DeleteRelationInTenant(sqls.DB(), req.ConversationID, req.TagID, tenantID)
}
