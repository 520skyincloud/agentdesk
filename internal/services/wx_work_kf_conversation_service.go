package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var WxWorkKFConversationService = newWxWorkKFConversationService()

func newWxWorkKFConversationService() *wxWorkKFConversationService {
	return &wxWorkKFConversationService{}
}

type wxWorkKFConversationService struct {
}

func (s *wxWorkKFConversationService) Get(id int64) *models.WxWorkKFConversation {
	return repositories.WxWorkKFConversationRepository.Get(sqls.DB(), id)
}

func (s *wxWorkKFConversationService) GetInTenant(id, tenantID int64) *models.WxWorkKFConversation {
	return repositories.WxWorkKFConversationRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *wxWorkKFConversationService) Take(where ...interface{}) *models.WxWorkKFConversation {
	return repositories.WxWorkKFConversationRepository.Take(sqls.DB(), where...)
}

func (s *wxWorkKFConversationService) Find(cnd *sqls.Cnd) []models.WxWorkKFConversation {
	return repositories.WxWorkKFConversationRepository.Find(sqls.DB(), cnd)
}

func (s *wxWorkKFConversationService) FindOne(cnd *sqls.Cnd) *models.WxWorkKFConversation {
	return repositories.WxWorkKFConversationRepository.FindOne(sqls.DB(), cnd)
}

func (s *wxWorkKFConversationService) FindPageByParams(params *params.QueryParams) (list []models.WxWorkKFConversation, paging *sqls.Paging) {
	return repositories.WxWorkKFConversationRepository.FindPageByParams(sqls.DB(), params)
}

func (s *wxWorkKFConversationService) FindPageByCnd(cnd *sqls.Cnd) (list []models.WxWorkKFConversation, paging *sqls.Paging) {
	return repositories.WxWorkKFConversationRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *wxWorkKFConversationService) Count(cnd *sqls.Cnd) int64 {
	return repositories.WxWorkKFConversationRepository.Count(sqls.DB(), cnd)
}

func (s *wxWorkKFConversationService) Create(t *models.WxWorkKFConversation) error {
	if t == nil {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), t.ConversationID)
	if err != nil {
		return err
	}
	channel := repositories.ChannelRepository.GetInTenant(sqls.DB(), t.ChannelID, conversation.TenantID)
	if channel == nil {
		return errorsx.InvalidParam("企业微信客服会话渠道不存在或不属于会话接入公司")
	}
	t.TenantID = conversation.TenantID
	return repositories.WxWorkKFConversationRepository.Create(sqls.DB(), t)
}

func (s *wxWorkKFConversationService) Update(t *models.WxWorkKFConversation) error {
	return repositories.WxWorkKFConversationRepository.Update(sqls.DB(), t)
}

func (s *wxWorkKFConversationService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.WxWorkKFConversationRepository.Updates(sqls.DB(), id, columns)
}

func (s *wxWorkKFConversationService) UpdatesInTenant(id, tenantID int64, columns map[string]any) error {
	return repositories.WxWorkKFConversationRepository.UpdatesInTenant(sqls.DB(), id, tenantID, columns)
}

func (s *wxWorkKFConversationService) GetByConversationIDInTenant(conversationID, tenantID int64) *models.WxWorkKFConversation {
	if conversationID <= 0 || tenantID <= 0 {
		return nil
	}
	return repositories.WxWorkKFConversationRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("conversation_id", conversationID))
}

func (s *wxWorkKFConversationService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.WxWorkKFConversationRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *wxWorkKFConversationService) Delete(id int64) {
	repositories.WxWorkKFConversationRepository.Delete(sqls.DB(), id)
}
