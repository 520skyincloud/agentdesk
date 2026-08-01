package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestStoreStaffRuntimeNeverFallsBackToProtocolInstanceConfiguration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	if err := db.AutoMigrate(&models.StoreStaffBinding{}); err != nil {
		t.Fatalf("migrate Store staff binding: %v", err)
	}
	sqls.SetDB(db)

	legacyOnly := &models.WxWorkProtocolInstance{
		TenantID: 101, StoreID: 31, AgentTeamID: 7,
		ServiceHours: "08:00-22:00", StoreRoomConversationID: "R:legacy-room",
		StoreRoomNotifyEnabled: true, StoreRoomAtList: "legacy-user",
		FallbackToHQ: false, ManualTimeoutMinutes: 90,
	}
	runtime := StoreStaffBindingService.ResolveForInstance(legacyOnly)
	if !runtime.NoWxWorkInstance || runtime.BindingID != 0 || runtime.StoreID != 0 || runtime.AgentTeamID != 0 {
		t.Fatalf("legacy-only instance unexpectedly became a runtime fact source: %#v", runtime)
	}
	if runtime.ServiceHours != "" || runtime.StoreRoomConversationID != "" || runtime.StoreRoomNotifyEnabled || runtime.StoreRoomAtList != "" {
		t.Fatalf("legacy Store configuration leaked into runtime: %#v", runtime)
	}
	if runtime.ManagedMode != constants.StoreManagedModeSemi || !runtime.FallbackToHQ || runtime.ManualTimeoutMinutes != DefaultManualTimeoutMinutes {
		t.Fatalf("unavailable runtime did not use the safe human fallback: %#v", runtime)
	}

	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: 9, StoreID: 31, AgentTeamID: 8,
		ManagedMode: constants.StoreManagedModeFull, ServiceHours: "09:00-18:00",
		StoreRoomConversationID: "R:binding-room", StoreRoomNotifyEnabled: true,
		StoreRoomAtList: "binding-user", FallbackToHQ: true, ManualTimeoutMinutes: 18,
		Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create Store staff binding: %v", err)
	}
	legacyOnly.StoreStaffBindingID = binding.ID
	runtime = StoreStaffBindingService.ResolveForInstance(legacyOnly)
	if runtime.NoWxWorkInstance || runtime.BindingID != binding.ID || runtime.StoreID != binding.StoreID || runtime.AgentTeamID != binding.AgentTeamID {
		t.Fatalf("binding did not become the runtime fact source: %#v", runtime)
	}
	if runtime.ServiceHours != binding.ServiceHours || runtime.StoreRoomConversationID != binding.StoreRoomConversationID ||
		!runtime.StoreRoomNotifyEnabled || runtime.StoreRoomAtList != binding.StoreRoomAtList || !runtime.FallbackToHQ ||
		runtime.ManualTimeoutMinutes != binding.ManualTimeoutMinutes {
		t.Fatalf("runtime did not use Store staff binding configuration: %#v", runtime)
	}
}
