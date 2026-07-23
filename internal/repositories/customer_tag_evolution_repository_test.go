package repositories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestCustomerTagEvolutionRepositorySQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "customer-tag-evolution.db")), evolutionRepositoryGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	testCustomerTagEvolutionRepository(t, db)
}

func TestCustomerTagEvolutionRepositoryMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), evolutionRepositoryGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []any{
		&models.ConversationEvolutionState{},
		&models.StoreCustomerTagRuntimePolicy{},
		&models.TenantCustomerTagPolicy{},
	} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatalf("drop MySQL evolution fixture %T: %v", table, err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []any{
			&models.ConversationEvolutionState{},
			&models.StoreCustomerTagRuntimePolicy{},
			&models.TenantCustomerTagPolicy{},
		} {
			if err := db.Migrator().DropTable(table); err != nil {
				t.Errorf("cleanup MySQL evolution fixture %T: %v", table, err)
			}
		}
	})
	testCustomerTagEvolutionRepository(t, db)
}

func testCustomerTagEvolutionRepository(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&models.TenantCustomerTagPolicy{},
		&models.StoreCustomerTagRuntimePolicy{},
		&models.ConversationEvolutionState{},
	); err != nil {
		t.Fatal(err)
	}
	const tenantID int64 = 701
	const storeID int64 = 801
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&models.TenantCustomerTagPolicy{
		TenantID: tenantID, IntentProfileID: 1, QuietPeriodMinutes: 60,
		MinimumConfidence: 0.8, MaxOperationsPerRun: 6, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreCustomerTagRuntimePolicy{
		TenantID: tenantID, StoreID: storeID, CustomerTagEvolutionEnabled: true,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	newerDeadline := now.Add(-time.Hour)
	newer := evolutionRepositoryState(tenantID, storeID, 200, newerDeadline, now)
	if err := ConversationEvolutionStateRepository.Observe(db, newer); err != nil {
		t.Fatal(err)
	}
	older := evolutionRepositoryState(tenantID, storeID, 100, now.Add(-2*time.Hour), now)
	if err := ConversationEvolutionStateRepository.Observe(db, older); err != nil {
		t.Fatal(err)
	}
	state, err := ConversationEvolutionStateRepository.GetByConversationSession(db, tenantID, newer.ConversationID, newer.SessionNo)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.LastObservedMessageID != newer.LastObservedMessageID ||
		state.NextEvolutionAt == nil || !state.NextEvolutionAt.Equal(newerDeadline) {
		t.Fatalf("out-of-order observation moved state backwards: %#v", state)
	}

	due, err := ConversationEvolutionStateRepository.FindDue(db, now, 20)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	claimed, err := ConversationEvolutionStateRepository.Claim(db, state.ID, tenantID, "worker-a", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	claimedAgain, err := ConversationEvolutionStateRepository.Claim(db, state.ID, tenantID, "worker-b", now, now.Add(time.Minute))
	if err != nil || claimedAgain {
		t.Fatalf("second claim=%v err=%v", claimedAgain, err)
	}

	latestDeadline := now.Add(time.Hour)
	latest := evolutionRepositoryState(tenantID, storeID, 300, latestDeadline, now)
	if err := ConversationEvolutionStateRepository.Observe(db, latest); err != nil {
		t.Fatal(err)
	}
	renewed, err := ConversationEvolutionStateRepository.RenewLease(
		db, state.ID, tenantID, "worker-a", state.LastObservedMessageID, now.Add(2*time.Minute), now,
	)
	if err != nil || renewed {
		t.Fatalf("stale checkpoint renewed lease=%v err=%v", renewed, err)
	}
	released, err := ConversationEvolutionStateRepository.ReleaseOwned(db, state.ID, tenantID, "worker-a", now)
	if err != nil || !released {
		t.Fatalf("release=%v err=%v", released, err)
	}
	current, err := ConversationEvolutionStateRepository.GetByConversationSession(db, tenantID, latest.ConversationID, latest.SessionNo)
	if err != nil {
		t.Fatal(err)
	}
	if current.LastObservedMessageID != latest.LastObservedMessageID || current.LeaseOwner != "" ||
		current.NextEvolutionAt == nil || !current.NextEvolutionAt.Equal(latestDeadline) {
		t.Fatalf("stale worker overwrote latest observation: %#v", current)
	}
}

func evolutionRepositoryState(tenantID, storeID, messageID int64, deadline, now time.Time) *models.ConversationEvolutionState {
	return &models.ConversationEvolutionState{
		TenantID: tenantID, ConversationID: 901, SessionNo: 1,
		StoreID: storeID, CustomerID: 1001, StoreCustomerRelationID: 1101,
		LastObservedMessageID: messageID, NextEvolutionAt: &deadline,
		LastStatus: conversationEvolutionStatusWaitingRepositoryTest, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
}

func evolutionRepositoryGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: "t_", SingularTable: true,
		},
	}
}

const conversationEvolutionStatusWaitingRepositoryTest = "waiting"
