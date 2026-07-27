package migration

import (
	"path/filepath"
	"strconv"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestInitializeTenantRuntimeAgentsIsCurrentSchemaOnlyAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tenant-runtime-agent.db")), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.AgentTeam{}, &models.AIAgent{}); err != nil {
		t.Fatal(err)
	}

	tenantWithTeam := &models.Tenant{
		TenantCode:       "runtime-team",
		LegalName:        "Runtime Team",
		RegistrationType: "test",
		RegistrationNo:   "runtime-team",
		Status:           enums.StatusOk,
	}
	tenantExisting := &models.Tenant{
		TenantCode:       "runtime-existing",
		LegalName:        "Runtime Existing",
		RegistrationType: "test",
		RegistrationNo:   "runtime-existing",
		Status:           enums.StatusOk,
	}
	tenantDeleted := &models.Tenant{
		TenantCode:       "runtime-deleted",
		LegalName:        "Runtime Deleted",
		RegistrationType: "test",
		RegistrationNo:   "runtime-deleted",
		Status:           enums.StatusDeleted,
	}
	if err := db.Create([]*models.Tenant{tenantWithTeam, tenantExisting, tenantDeleted}).Error; err != nil {
		t.Fatal(err)
	}
	team := &models.AgentTeam{TenantID: tenantWithTeam.ID, Name: "默认客服组", IsDefault: true, Status: enums.StatusOk}
	if err := db.Create(team).Error; err != nil {
		t.Fatal(err)
	}
	existing := &models.AIAgent{TenantID: tenantExisting.ID, Name: "既有接待策略", Status: enums.StatusOk}
	if err := db.Create(existing).Error; err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := initializeTenantRuntimeAgents(db); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}

	var agents []models.AIAgent
	if err := db.Order("tenant_id ASC, id ASC").Find(&agents).Error; err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Fatalf("agent count=%d want=2: %+v", len(agents), agents)
	}
	var created models.AIAgent
	if err := db.Where("tenant_id = ?", tenantWithTeam.ID).Take(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.TeamIDs != strconv.FormatInt(team.ID, 10) || created.Name != "默认接待策略" {
		t.Fatalf("created runtime agent=%+v", created)
	}
	var deletedCount int64
	if err := db.Model(&models.AIAgent{}).Where("tenant_id = ?", tenantDeleted.ID).Count(&deletedCount).Error; err != nil {
		t.Fatal(err)
	}
	if deletedCount != 0 {
		t.Fatalf("deleted tenant agent count=%d want=0", deletedCount)
	}
}
