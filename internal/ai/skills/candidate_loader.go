package skills

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var newCandidateLoader = func() *candidateLoader {
	return &candidateLoader{}
}

type candidateLoader struct {
}

func (l *candidateLoader) findManualSkillDefinition(skillCode string) *models.SkillDefinition {
	return repositories.SkillDefinitionRepository.GetByCode(sqls.DB(), skillCode)
}

func (l *candidateLoader) loadCandidateSkills(aiAgent models.AIAgent) []models.SkillDefinition {
	skillIDs := utils.SplitInt64s(aiAgent.SkillIDs)
	skills := repositories.SkillDefinitionRepository.GetByIDs(sqls.DB(), skillIDs)
	ret := make([]models.SkillDefinition, 0, len(skillIDs))
	for _, id := range skillIDs {
		if skill, ok := skills[id]; ok && skill.Status == enums.StatusOk {
			ret = append(ret, skill)
		}
	}
	return ret
}
