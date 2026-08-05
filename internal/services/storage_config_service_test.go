package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestWxProtocolPublicAssetBaseURLFallsBackToChannelConfig(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system config: %v", err)
	}

	previousConfig, hadPreviousConfig := config.LookupCurrent()
	config.SetCurrent(&config.Config{})
	sqls.SetDB(db)
	t.Cleanup(func() {
		if hadPreviousConfig {
			config.SetCurrent(&previousConfig)
		} else {
			config.SetCurrent(nil)
		}
		sqls.SetDB(nil)
	})

	if got := DefaultStorageSetting().PublicAssetBaseURL; got != "" {
		t.Fatalf("default publicAssetBaseUrl=%q, want empty", got)
	}

	const channelBaseURL = "https://agentdesk.example.test"
	got := wxProtocolPublicAssetBaseURL(&dto.WxWorkProtocolChannelConfig{
		PublicAssetBaseURL: channelBaseURL,
	})
	if got != channelBaseURL {
		t.Fatalf("effective publicAssetBaseUrl=%q, want channel config %q", got, channelBaseURL)
	}
}
