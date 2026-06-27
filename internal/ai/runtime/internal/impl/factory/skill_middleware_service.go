package factory

import (
	"context"

	runtimetooling "agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

type SkillMiddlewareService struct{}

func NewSkillMiddlewareService() *SkillMiddlewareService {
	return &SkillMiddlewareService{}
}

func (s *SkillMiddlewareService) Build(
	ctx context.Context,
	aiAgent models.AIAgent,
	toolDefinitions []runtimetooling.MCPToolDefinition,
) (adk.ChatModelAgentMiddleware, error) {
	backend, err := newDatabaseSkillBackend(aiAgent, toolDefinitions)
	if err != nil {
		return nil, err
	}
	toolName := toolx.BuiltinSkill.Name
	return einoskill.NewMiddleware(ctx, &einoskill.Config{
		Backend:       backend,
		SkillToolName: &toolName,
		UseChinese:    true,
	})
}
