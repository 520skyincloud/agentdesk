package migration

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSwitchUnifiedNewAPIGatewayUpdatesEveryRevisionAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "s75_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.ModelProfileTemplate{}); err != nil {
		t.Fatalf("migrate model profile fixture: %v", err)
	}
	now := time.Now()
	profiles := []models.ModelProfileTemplate{
		{Code: "standard", Name: "r1", Revision: 1, GatewayBaseURL: "https://old.example.com/v1", Status: enums.ModelProfileStatusActive, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{Code: "standard", Name: "r2", Revision: 2, GatewayBaseURL: "", Status: enums.ModelProfileStatusDraft, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{Code: "other", Name: "r1", Revision: 1, GatewayBaseURL: constants.UnifiedNewAPIGatewayBaseURL, Status: enums.ModelProfileStatusCandidate, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create model profiles: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := switchUnifiedNewAPIGateway(db); err != nil {
			t.Fatalf("switch gateway attempt %d: %v", attempt+1, err)
		}
	}

	var stored []models.ModelProfileTemplate
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load model profiles: %v", err)
	}
	if len(stored) != len(profiles) {
		t.Fatalf("profile count=%d want=%d", len(stored), len(profiles))
	}
	for _, profile := range stored {
		if profile.GatewayBaseURL != constants.UnifiedNewAPIGatewayBaseURL {
			t.Fatalf("profile %d gateway=%q", profile.ID, profile.GatewayBaseURL)
		}
	}
}
