package migration

import (
	"encoding/json"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(15, "seed weather skill definition", func() error {
		now := time.Now()
		tools, _ := json.Marshal([]string{toolx.BuiltinWeather.Code})
		examples, _ := json.Marshal([]string{"今天合肥天气咋样", "明天上海会下雨吗", "北京今天冷不冷"})
		columns := map[string]any{
			"name":             "天气查询",
			"description":      "根据用户描述中的城市、地点和日期查询天气。",
			"instruction":      "当用户询问天气、温度、下雨、冷不冷、热不热、适不适合出门等实时天气问题时使用。先从用户描述中提取城市和日期；缺少城市时结合门店/对话上下文，仍不确定就追问城市。必须调用天气工具获取实时结果后再回答，不要假装无法实时查看。",
			"examples":         string(examples),
			"tool_whitelist":   string(tools),
			"status":           enums.StatusOk,
			"remark":           "系统内置天气 Skill，可在 Agent 能力编排中绑定。",
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
			"updated_at":       now,
		}
		current := repositories.SkillDefinitionRepository.GetByCode(sqls.DB(), "weather_skill")
		if current != nil {
			return repositories.SkillDefinitionRepository.Updates(sqls.DB(), current.ID, columns)
		}
		item := &models.SkillDefinition{
			Code:          "weather_skill",
			Name:          "天气查询",
			Description:   "根据用户描述中的城市、地点和日期查询天气。",
			Instruction:   columns["instruction"].(string),
			Examples:      string(examples),
			ToolWhitelist: string(tools),
			Status:        enums.StatusOk,
			Remark:        "系统内置天气 Skill，可在 Agent 能力编排中绑定。",
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				UpdatedAt:      now,
				CreateUserID:   constants.SystemAuditUserID,
				UpdateUserID:   constants.SystemAuditUserID,
				CreateUserName: constants.SystemAuditUserName,
				UpdateUserName: constants.SystemAuditUserName,
			},
		}
		return repositories.SkillDefinitionRepository.Create(sqls.DB(), item)
	})
}
