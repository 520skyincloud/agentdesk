package services

import (
	"context"
	"fmt"
	"strings"

	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/errorsx"
)

var SkillRuntimeService = newSkillRuntimeService()
var SkillDebugRunHook func(ctx context.Context, req request.SkillDebugRunRequest) (*response.SkillDebugRunResponse, error)
var SkillDebugResumeHook func(ctx context.Context, req request.SkillDebugResumeRequest) (*response.SkillDebugRunResponse, error)

func newSkillRuntimeService() *skillRuntimeService {
	return &skillRuntimeService{}
}

type skillRuntimeService struct{}

func (s *skillRuntimeService) DebugRun(ctx context.Context, req request.SkillDebugRunRequest, operator *dto.AuthPrincipal) (*response.SkillDebugRunResponse, error) {
	if req.AIAgentID <= 0 {
		return nil, errorsx.InvalidParam("接待策略不能为空")
	}
	if req.ConversationID <= 0 {
		return nil, errorsx.InvalidParam("conversationId不能为空，模型调试必须绑定真实门店会话")
	}
	if strings.TrimSpace(req.SkillCode) == "" {
		return nil, errorsx.InvalidParam("skillCode不能为空")
	}
	if strings.TrimSpace(req.UserMessage) == "" {
		return nil, errorsx.InvalidParam("userMessage不能为空")
	}
	tenantID, err := requireActiveTenantID(operator, "Skill 调试")
	if err != nil {
		return nil, err
	}
	if AIAgentService.GetByTenantID(req.AIAgentID, tenantID) == nil {
		return nil, errorsx.InvalidParam("接待策略不存在或不属于当前公司")
	}
	conversation := ConversationService.GetByTenantID(req.ConversationID, tenantID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在或不属于当前公司")
	}
	if conversation.AIAgentID > 0 && conversation.AIAgentID != req.AIAgentID {
		return nil, errorsx.InvalidParam("会话与接待策略不匹配")
	}
	if SkillDebugRunHook == nil {
		return nil, fmt.Errorf("skill debug runner is not initialized")
	}
	return SkillDebugRunHook(ctx, req)
}

func (s *skillRuntimeService) DebugResume(ctx context.Context, req request.SkillDebugResumeRequest, operator *dto.AuthPrincipal) (*response.SkillDebugRunResponse, error) {
	if req.AIAgentID <= 0 {
		return nil, errorsx.InvalidParam("接待策略不能为空")
	}
	if strings.TrimSpace(req.CheckPointID) == "" {
		return nil, errorsx.InvalidParam("checkPointId不能为空")
	}
	if strings.TrimSpace(req.UserMessage) == "" {
		return nil, errorsx.InvalidParam("userMessage不能为空")
	}
	tenantID, err := requireActiveTenantID(operator, "Skill 调试")
	if err != nil {
		return nil, err
	}
	if AIAgentService.GetByTenantID(req.AIAgentID, tenantID) == nil {
		return nil, errorsx.InvalidParam("接待策略不存在或不属于当前公司")
	}
	interrupt := ConversationInterruptService.GetByCheckPointIDInTenant(strings.TrimSpace(req.CheckPointID), tenantID)
	if interrupt == nil {
		return nil, errorsx.InvalidParam("CheckPoint 不存在或不属于当前公司")
	}
	conversationID := req.ConversationID
	if conversationID <= 0 {
		conversationID = interrupt.ConversationID
	}
	conversation := ConversationService.GetByTenantID(conversationID, tenantID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在或不属于当前公司")
	}
	if conversation.AIAgentID > 0 && conversation.AIAgentID != req.AIAgentID {
		return nil, errorsx.InvalidParam("会话与接待策略不匹配")
	}
	if SkillDebugResumeHook == nil {
		return nil, fmt.Errorf("skill debug resume runner is not initialized")
	}
	return SkillDebugResumeHook(ctx, req)
}
