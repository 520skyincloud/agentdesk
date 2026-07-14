package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"
	"strings"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var AgentRunLogService = newAgentRunLogService()

func newAgentRunLogService() *agentRunLogService {
	return &agentRunLogService{}
}

type agentRunLogService struct{}

func (s *agentRunLogService) Get(id int64) *models.AgentRunLog {
	return repositories.AgentRunLogRepository.Get(sqls.DB(), id)
}

func (s *agentRunLogService) GetInTenant(id, tenantID int64) *models.AgentRunLog {
	return repositories.AgentRunLogRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *agentRunLogService) Take(where ...interface{}) *models.AgentRunLog {
	return repositories.AgentRunLogRepository.Take(sqls.DB(), where...)
}

func (s *agentRunLogService) Find(cnd *sqls.Cnd) []models.AgentRunLog {
	return repositories.AgentRunLogRepository.Find(sqls.DB(), cnd)
}

func (s *agentRunLogService) FindOne(cnd *sqls.Cnd) *models.AgentRunLog {
	return repositories.AgentRunLogRepository.FindOne(sqls.DB(), cnd)
}

func (s *agentRunLogService) FindPageByParams(params *params.QueryParams) (list []models.AgentRunLog, paging *sqls.Paging) {
	return repositories.AgentRunLogRepository.FindPageByParams(sqls.DB(), params)
}

func (s *agentRunLogService) FindPageInTenant(queryParams *params.QueryParams, tenantID int64) (list []models.AgentRunLog, paging *sqls.Paging) {
	if queryParams == nil || tenantID <= 0 {
		return nil, &sqls.Paging{}
	}
	queryParams.Cnd.Eq("tenant_id", tenantID)
	return repositories.AgentRunLogRepository.FindPageByParams(sqls.DB(), queryParams)
}

func (s *agentRunLogService) FindPageByCnd(cnd *sqls.Cnd) (list []models.AgentRunLog, paging *sqls.Paging) {
	return repositories.AgentRunLogRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *agentRunLogService) Count(cnd *sqls.Cnd) int64 {
	return repositories.AgentRunLogRepository.Count(sqls.DB(), cnd)
}

func (s *agentRunLogService) Create(t *models.AgentRunLog) error {
	if err := s.validateCreate(t); err != nil {
		return err
	}
	return repositories.AgentRunLogRepository.Create(sqls.DB(), t)
}

func (s *agentRunLogService) validateCreate(t *models.AgentRunLog) error {
	if t == nil || t.TenantID <= 0 {
		return errorsx.InvalidParam("Agent 运行日志缺少租户归属")
	}
	db := sqls.DB()
	if t.ConversationID <= 0 {
		return errorsx.InvalidParam("Agent 运行日志缺少会话")
	}
	if repositories.ConversationRepository.GetInTenant(db, t.ConversationID, t.TenantID) == nil {
		return errorsx.InvalidParam("Agent 运行日志会话不属于当前租户")
	}
	if t.MessageID > 0 {
		message := repositories.MessageRepository.GetInTenant(db, t.MessageID, t.TenantID)
		if message == nil || message.ConversationID != t.ConversationID {
			return errorsx.InvalidParam("Agent 运行日志消息与会话不匹配")
		}
	}
	if t.AIAgentID > 0 && repositories.AIAgentRepository.GetInTenant(db, t.AIAgentID, t.TenantID) == nil {
		return errorsx.InvalidParam("Agent 运行日志 AI Agent 不属于当前租户")
	}
	return nil
}

func (s *agentRunLogService) Update(t *models.AgentRunLog) error {
	return repositories.AgentRunLogRepository.Update(sqls.DB(), t)
}

func (s *agentRunLogService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.AgentRunLogRepository.Updates(sqls.DB(), id, columns)
}

func (s *agentRunLogService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.AgentRunLogRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *agentRunLogService) Delete(id int64) {
	repositories.AgentRunLogRepository.Delete(sqls.DB(), id)
}

func (s *agentRunLogService) ApplyHITLStatusFilter(cnd *sqls.Cnd, hitlStatus string) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	switch strings.TrimSpace(hitlStatus) {
	case "pending":
		cnd.Eq("final_status", "interrupted")
	case "expired":
		cnd.Eq("final_status", "expired")
	case "cancelled":
		cnd.Where("(reply_text LIKE ? OR reply_text LIKE ?)", "%已取消本次工单创建%", "%已取消本次转人工%")
	case "confirmed":
		cnd.Where("resume_source <> ''")
	case "triggered":
		cnd.Where("interrupt_type <> ''")
	}
	return cnd
}
