package aiagent

import (
	"agent-desk/cmd/testdata/skill"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"fmt"
	"time"

	"github.com/mlogclub/simple/sqls"
)

type InitResult struct {
	Created int
	Updated int
}

// Init 初始化 AI Agent 测试数据
// 依赖于 AI Config 和 Knowledge Base 已初始化
func Init() (*InitResult, error) {
	result := &InitResult{}

	aiConfigID, err := getDefaultAIConfigID()
	if err != nil {
		return result, fmt.Errorf("get default ai config id failed: %w", err)
	}
	if aiConfigID == 0 {
		return result, fmt.Errorf("no default ai config found, please init ai config first")
	}

	knowledgeIDs, err := getDefaultKnowledgeIDs()
	if err != nil {
		return result, fmt.Errorf("get default knowledge ids failed: %w", err)
	}

	defaultTeamIDs := getDefaultTeamIDs()
	defaultSkillIDs, err := getDefaultSkillIDs()
	if err != nil {
		return result, fmt.Errorf("get default skill ids failed: %w", err)
	}

	seedItems := buildSeedItems(aiConfigID, knowledgeIDs, defaultTeamIDs, defaultSkillIDs)
	for _, item := range seedItems {
		itemCopy := item
		if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			existing := repositories.AIAgentRepository.Take(ctx.Tx, "name = ?", itemCopy.Name)
			if existing != nil {
				// 更新
				if err := ctx.Tx.Model(existing).Updates(&itemCopy).Error; err != nil {
					return err
				}
				result.Updated++
			} else {
				// 创建
				if err := ctx.Tx.Create(&itemCopy).Error; err != nil {
					return err
				}
				result.Created++
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("upsert ai agent failed: %w", err)
		}
	}

	return result, nil
}

func buildSeedItems(aiConfigID int64, knowledgeIDs []int64, defaultTeamIDs string, defaultSkillIDs string) []models.AIAgent {
	now := time.Now()
	return []models.AIAgent{
		{
			Name:        "测试AI客服",
			Description: "本地测试 AI 客服 Agent",
			Status:      enums.StatusOk,
			AIConfigID:  aiConfigID,
			ServiceMode: enums.IMConversationServiceModeAIFirst,
			SystemPrompt: `你正在一个有明确工程约束的客服系统中工作。
执行时必须严格遵守当前注入的 Agent 规则和技能规则。
如果存在工具白名单限制，只能调用当前允许的工具；信息不足时优先追问，不要伪造事实或跳过必要确认。
禁止承诺未经系统确认的处理时效、完成时间、回访时间或联系时间。
禁止代表人工团队、技术团队、售后团队承诺后续动作，除非当前上下文已有明确的工具结果、人工确认或知识库事实支持。
当用户只表示已发送资料、邮件、截图或附件时，只能确认已收到当前消息或建议等待人工确认，不能自行补充内部处理流程、SLA 或跟进安排。`,
			WelcomeMessage:      "您好，有什么可以帮助您的？",
			ReplyTimeoutSeconds: 180,
			TeamIDs:             defaultTeamIDs,
			HandoffMode:         enums.AIAgentHandoffModeWaitPool,
			FallbackMode:        enums.AIAgentFallbackModeSuggestRetry,
			FallbackMessage:     "我暂时没有找到足够准确的信息。你可以补充具体的问题，我再继续帮你查。",
			KnowledgeIDs:        utils.JoinInt64s(knowledgeIDs),
			SkillIDs:            defaultSkillIDs,
			SortNo:              10,
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserID:   0,
				CreateUserName: "System",
				UpdatedAt:      now,
				UpdateUserID:   0,
				UpdateUserName: "System",
			},
		},
	}
}

func getDefaultAIConfigID() (int64, error) {
	aiConfig := repositories.AIConfigRepository.Take(
		sqls.DB(),
		"model_type = ? AND status = ?",
		string(enums.AIModelTypeLLM),
		enums.StatusOk,
	)
	if aiConfig == nil {
		return 0, nil
	}
	return aiConfig.ID, nil
}

func getDefaultKnowledgeIDs() ([]int64, error) {
	knowledges := repositories.KnowledgeBaseRepository.Find(
		sqls.DB(),
		sqls.NewCnd().Where("status = ?", enums.StatusOk),
	)
	ids := make([]int64, 0, len(knowledges))
	for _, knowledge := range knowledges {
		ids = append(ids, knowledge.ID)
	}
	return ids, nil
}

func getDefaultTeamIDs() string {
	teams := repositories.AgentTeamRepository.Find(
		sqls.DB(),
		sqls.NewCnd().Where("status = ?", enums.StatusOk),
	)
	teamIDs := make([]int64, 0, len(teams))
	for _, team := range teams {
		teamIDs = append(teamIDs, team.ID)
	}
	return utils.JoinInt64s(teamIDs)
}

func getDefaultSkillIDs() (string, error) {
	skillItem := repositories.SkillDefinitionRepository.Take(
		sqls.DB(),
		"code = ? AND status = ?",
		skill.AfterSalesEscalationSkillCode,
		enums.StatusOk,
	)
	if skillItem == nil {
		return "", fmt.Errorf("default test skill not found: %s", skill.AfterSalesEscalationSkillCode)
	}
	return utils.JoinInt64s([]int64{skillItem.ID}), nil
}
