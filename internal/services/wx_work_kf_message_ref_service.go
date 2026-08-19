package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var WxWorkKFMessageRefService = newWxWorkKFMessageRefService()

func newWxWorkKFMessageRefService() *wxWorkKFMessageRefService {
	return &wxWorkKFMessageRefService{}
}

type wxWorkKFMessageRefService struct {
}

func (s *wxWorkKFMessageRefService) Get(id int64) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.Get(sqls.DB(), id)
}

func (s *wxWorkKFMessageRefService) GetInTenant(id, tenantID int64) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *wxWorkKFMessageRefService) Take(where ...interface{}) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), where...)
}

func (s *wxWorkKFMessageRefService) Find(cnd *sqls.Cnd) []models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.Find(sqls.DB(), cnd)
}

func (s *wxWorkKFMessageRefService) FindOne(cnd *sqls.Cnd) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.FindOne(sqls.DB(), cnd)
}

func (s *wxWorkKFMessageRefService) FindPageByParams(params *params.QueryParams) (list []models.WxWorkKFMessageRef, paging *sqls.Paging) {
	return repositories.WxWorkKFMessageRefRepository.FindPageByParams(sqls.DB(), params)
}

func (s *wxWorkKFMessageRefService) FindPageByCnd(cnd *sqls.Cnd) (list []models.WxWorkKFMessageRef, paging *sqls.Paging) {
	return repositories.WxWorkKFMessageRefRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *wxWorkKFMessageRefService) Count(cnd *sqls.Cnd) int64 {
	return repositories.WxWorkKFMessageRefRepository.Count(sqls.DB(), cnd)
}

func (s *wxWorkKFMessageRefService) Create(t *models.WxWorkKFMessageRef) error {
	if t == nil {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), t.ConversationID)
	if err != nil {
		return err
	}
	if t.MessageID > 0 {
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), t.MessageID, conversation.TenantID)
		if message == nil || message.ConversationID != conversation.ID {
			return errorsx.InvalidParam("企业微信客服消息映射不属于当前会话")
		}
	}
	t.TenantID = conversation.TenantID
	return repositories.WxWorkKFMessageRefRepository.Create(sqls.DB(), t)
}

func (s *wxWorkKFMessageRefService) Update(t *models.WxWorkKFMessageRef) error {
	return repositories.WxWorkKFMessageRefRepository.Update(sqls.DB(), t)
}

func (s *wxWorkKFMessageRefService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.WxWorkKFMessageRefRepository.Updates(sqls.DB(), id, columns)
}

func (s *wxWorkKFMessageRefService) UpdatesInTenant(id, tenantID int64, columns map[string]any) error {
	return repositories.WxWorkKFMessageRefRepository.UpdatesInTenant(sqls.DB(), id, tenantID, columns)
}

func (s *wxWorkKFMessageRefService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.WxWorkKFMessageRefRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *wxWorkKFMessageRefService) Delete(id int64) {
	repositories.WxWorkKFMessageRefRepository.Delete(sqls.DB(), id)
}

func (s *wxWorkKFMessageRefService) GetByWxMsgID(wxMsgID string) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.Take(sqls.DB(), "wx_msg_id = ?", wxMsgID)
}

func (s *wxWorkKFMessageRefService) GetByWxMsgIDInTenant(wxMsgID string, tenantID int64) *models.WxWorkKFMessageRef {
	return repositories.WxWorkKFMessageRefRepository.GetByWxMsgIDInTenant(sqls.DB(), wxMsgID, tenantID)
}

func (s *wxWorkKFMessageRefService) ActiveOutboundReconciliationHold(tenantID, conversationID int64, now time.Time) (*time.Time, bool) {
	if tenantID <= 0 || conversationID <= 0 {
		return nil, false
	}
	ref := s.FindOne(sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversationID).
		Eq("direction", string(enums.WxWorkKFMessageDirectionOut)).
		Eq("send_status", string(enums.WxWorkKFMessageSendStatusPendingReconciliation)).
		Desc("id"))
	if ref == nil {
		return nil, false
	}
	deadline := ref.CreatedAt.Add(wxWorkUnknownOutboundReconciliationHold)
	if !now.Before(deadline) {
		_ = s.UpdatesInTenant(ref.ID, tenantID, map[string]any{
			"send_status": string(enums.WxWorkKFMessageSendStatusUnresolvedOutbound),
			"fail_reason": "unknown_outbound_reconciliation_timeout",
			"updated_at":  now,
		})
		return nil, false
	}
	return &deadline, true
}
