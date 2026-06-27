package factory

import (
	"context"
	"strings"
	"testing"
	"time"

	runtimetooling "agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestDatabaseSkillBackendListAndGet(t *testing.T) {
	setupSkillBackendTestDB(t)
	createSkillDefinitionForTest(t, models.SkillDefinition{
		ID:            1,
		Code:          "after_sales_escalation_skill",
		Name:          "售后升级",
		Description:   "处理转人工和升级诉求",
		Instruction:   "请优先判断是否需要转人工。",
		ToolWhitelist: `["graph/handoff_to_human"]`,
		Status:        enums.StatusOk,
	})
	createSkillDefinitionForTest(t, models.SkillDefinition{
		ID:          2,
		Code:        "disabled_skill",
		Name:        "禁用技能",
		Description: "不会被暴露",
		Instruction: "noop",
		Status:      enums.StatusDeleted,
	})

	backend, err := newDatabaseSkillBackend(models.AIAgent{SkillIDs: "1,2"}, []runtimetooling.MCPToolDefinition{
		{ToolCode: "graph/handoff_to_human", Title: "转人工确认流程"},
		{ToolCode: "graph/prepare_ticket_draft", Title: "整理工单草稿"},
	})
	if err != nil {
		t.Fatalf("newDatabaseSkillBackend returned error: %v", err)
	}

	matters, err := backend.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(matters) != 1 || matters[0].Name != "after_sales_escalation_skill" {
		t.Fatalf("unexpected matters: %#v", matters)
	}

	skill, err := backend.Get(context.Background(), "after_sales_escalation_skill")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if skill.Name != "after_sales_escalation_skill" {
		t.Fatalf("unexpected skill name: %#v", skill)
	}
	if skill.Content == "" || !containsAll(skill.Content, "处理转人工和升级诉求", "graph/handoff_to_human") {
		t.Fatalf("unexpected skill content: %q", skill.Content)
	}
}

func TestHasVisibleSkills(t *testing.T) {
	setupSkillBackendTestDB(t)
	createSkillDefinitionForTest(t, models.SkillDefinition{
		ID:          3,
		Code:        "enabled_skill",
		Name:        "启用技能",
		Description: "可见",
		Instruction: "noop",
		Status:      enums.StatusOk,
	})
	createSkillDefinitionForTest(t, models.SkillDefinition{
		ID:          4,
		Code:        "deleted_skill",
		Name:        "删除技能",
		Description: "不可见",
		Instruction: "noop",
		Status:      enums.StatusDeleted,
	})
	if !HasVisibleSkills(models.AIAgent{SkillIDs: "3,4"}) {
		t.Fatalf("expected visible skills")
	}
	if HasVisibleSkills(models.AIAgent{SkillIDs: "4"}) {
		t.Fatalf("expected no visible skills")
	}
}

func setupSkillBackendTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:skill_backend_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&models.SkillDefinition{}); err != nil {
		t.Fatalf("auto migrate skill definition failed: %v", err)
	}
	if err := db.Exec("DELETE FROM skill_definitions").Error; err != nil {
		t.Fatalf("cleanup skill definitions failed: %v", err)
	}
	sqls.SetDB(db)
}

func createSkillDefinitionForTest(t *testing.T, item models.SkillDefinition) {
	t.Helper()
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	if err := sqls.DB().Create(&item).Error; err != nil {
		t.Fatalf("create skill definition failed: %v", err)
	}
}

func containsAll(text string, items ...string) bool {
	for _, item := range items {
		if item != "" && !strings.Contains(text, item) {
			return false
		}
	}
	return true
}
