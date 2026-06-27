package runtime

import (
	"context"

	"agent-desk/internal/ai/runtime/executor"
	"agent-desk/internal/pkg/utils"
)

type Service struct {
	runtime *executor.Service
	catalog *toolCatalog
	prepare *prepareService
}

func NewService() *Service {
	catalog := newToolCatalog()
	return &Service{
		runtime: executor.NewService(),
		catalog: catalog,
		prepare: newPrepareService(catalog),
	}
}

func (s *Service) Run(ctx context.Context, req Request) (*Summary, error) {
	req.UserMessage.Content = utils.BuildRuntimeMessageText(req.UserMessage.MessageType, req.UserMessage.Content)
	toolSet, err := s.prepare.prepareToolsForRun(req)
	if err != nil {
		return nil, err
	}
	req.ToolSet = toolSet
	summary, err := s.runtime.ExecuteRun(ctx, executor.RunInput{
		Conversation: req.Conversation,
		UserMessage:  req.UserMessage,
		AIAgent:      req.AIAgent,
		AIConfig:     req.AIConfig,
		CheckPointID: req.CheckPointID,
		ToolSet:      req.ToolSet,
	})
	if err != nil {
		return toSummary(summary), err
	}
	return toSummary(summary), nil
}

func (s *Service) Resume(ctx context.Context, req ResumeRequest) (*Summary, error) {
	toolSet, err := s.prepare.prepareToolsForResume(req)
	if err != nil {
		return nil, err
	}
	req.ToolSet = toolSet
	summary, err := s.runtime.ExecuteResume(ctx, executor.ResumeInput{
		Conversation: req.Conversation,
		AIAgent:      req.AIAgent,
		AIConfig:     req.AIConfig,
		CheckPointID: req.CheckPointID,
		ResumeData:   req.ResumeData,
		ToolSet:      req.ToolSet,
	})
	if err != nil {
		return toSummary(summary), err
	}
	return toSummary(summary), nil
}
