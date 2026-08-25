package tools

import (
	"context"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestHandoffGraphToolDescribesDirectDispatch(t *testing.T) {
	tool := NewHandoffGraphTool()
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if !strings.Contains(info.Desc, "直接转接") || !strings.Contains(info.Desc, "自行追问房号") || !strings.Contains(info.Desc, "auto_handoff_disabled") {
		t.Fatalf("expected direct handoff behavior in tool description, got %q", info.Desc)
	}

	spec := tool.Spec()
	if spec.Title != "直接转人工流程" {
		t.Fatalf("unexpected tool title %q", spec.Title)
	}
	if !strings.Contains(spec.Appendix, "直接执行转人工") || !strings.Contains(spec.Appendix, "awaiting_room_number") || !strings.Contains(spec.Appendix, "auto_handoff_disabled") {
		t.Fatalf("expected direct dispatch contract in appendix, got %q", spec.Appendix)
	}
	for _, forbidden := range []string{"回复“确认”", "用户确认后才会真正转人工", "用户取消则结束本次转人工流程"} {
		if strings.Contains(spec.Appendix, forbidden) {
			t.Fatalf("handoff appendix still requires confirmation %q: %q", forbidden, spec.Appendix)
		}
	}
}

func TestHandoffGraphToolEnabledHonorsConversationSetting(t *testing.T) {
	db := setupHandoffGraphToolTestDB(t)
	conversation := models.Conversation{CustomerID: 301}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   conversation.ID,
		WxWorkInstanceID: 401,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}

	tool := NewHandoffGraphTool()
	ctx := registry.Context{Conversation: conversation}
	if !tool.Enabled(ctx) {
		t.Fatal("missing setting should keep automatic handoff enabled")
	}
	if err := repositories.WxWorkCustomerHandoffSettingRepository.Create(db, &models.WxWorkCustomerHandoffSetting{
		CustomerID:         conversation.CustomerID,
		WxWorkInstanceID:   401,
		AutoHandoffEnabled: false,
	}); err != nil {
		t.Fatalf("create disabled setting: %v", err)
	}
	if tool.Enabled(ctx) {
		t.Fatal("tool must be disabled for a real conversation with automatic handoff disabled")
	}
	built, err := tool.Build(ctx)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built != nil {
		t.Fatal("disabled handoff tool must not be built")
	}
}

func TestHandoffGraphToolEnabledWithoutConversationMetadata(t *testing.T) {
	tool := NewHandoffGraphTool()
	ctx := registry.Context{}
	if !tool.Enabled(ctx) {
		t.Fatal("tool metadata context without a conversation must remain enabled")
	}
	built, err := tool.Build(ctx)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built == nil {
		t.Fatal("tool metadata context must remain buildable")
	}
}

func setupHandoffGraphToolTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.WxWorkCustomerHandoffSetting{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
