package repositories

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestPMSSandboxMySQLRoomLocksAreScopedAndOrdered(t *testing.T) {
	var output bytes.Buffer
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "test:test@tcp(127.0.0.1:1)/sandbox_test?parseTime=true",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
	if err != nil {
		t.Fatal(err)
	}
	if err := PMSSandboxRepository.LockRooms(db, 7, 12, []int64{4, 9}); err != nil {
		t.Fatal(err)
	}
	sql := output.String()
	if !strings.Contains(sql, "store_id = 7") || !strings.Contains(sql, "dataset_id = 12") || !strings.Contains(sql, "ORDER BY id ASC") || !strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("MySQL room locks must have scope and stable row order: %s", sql)
	}
}

func TestPMSSandboxCustomerInStoreUsesConfiguredNaming(t *testing.T) {
	for _, tc := range []struct {
		name   string
		naming schema.NamingStrategy
	}{
		{name: "default", naming: schema.NamingStrategy{}},
		{name: "production", naming: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{NamingStrategy: tc.naming, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := db.DB()
			t.Cleanup(func() { _ = raw.Close() })
			if err := db.AutoMigrate(&models.Conversation{}, &models.ConversationRouteState{}); err != nil {
				t.Fatal(err)
			}
			for _, item := range []any{
				&models.Conversation{ID: 11, CustomerID: 7},
				&models.Conversation{ID: 12, CustomerID: 8},
				&models.ConversationRouteState{ConversationID: 11, StoreID: 3},
				&models.ConversationRouteState{ConversationID: 12, StoreID: 4},
			} {
				if err := db.Create(item).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, check := range []struct {
				customerID int64
				storeID    int64
				want       bool
			}{{7, 3, true}, {7, 4, false}, {8, 3, false}, {99, 3, false}} {
				got, err := PMSSandboxRepository.CustomerInStore(db, check.customerID, check.storeID)
				if err != nil || got != check.want {
					t.Fatalf("customer=%d store=%d: got=%v want=%v err=%v", check.customerID, check.storeID, got, check.want, err)
				}
			}
		})
	}
}

func TestPMSSandboxPendingAndSupersedeOnlyCustomerChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	t.Cleanup(func() { _ = raw.Close() })
	if err := db.AutoMigrate(&models.PMSOperation{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	items := []models.PMSOperation{
		{ID: 1, StoreID: 3, DatasetID: 5, ConversationID: 7, Provider: "sandbox", OperationType: "sandbox_change", Status: "pending"},
		{ID: 2, StoreID: 3, DatasetID: 5, ConversationID: 7, Provider: "sandbox", OperationType: "renew", Status: "pending"},
		{ID: 3, StoreID: 3, DatasetID: 5, ConversationID: 7, Provider: "hpms", OperationType: "renew", Status: "pending"},
		{ID: 4, StoreID: 3, DatasetID: 5, ConversationID: 7, Provider: "sandbox", OperationType: "admin_update", Status: "pending"},
		{ID: 5, StoreID: 4, DatasetID: 5, ConversationID: 7, Provider: "sandbox", OperationType: "sandbox_change", Status: "pending"},
		{ID: 6, StoreID: 3, DatasetID: 5, ConversationID: 8, Provider: "sandbox", OperationType: "sandbox_change", Status: "pending"},
	}
	for i := range items {
		items[i].IdempotencyKey = fmt.Sprintf("pending-scope-%d", items[i].ID)
		items[i].CreatedAt, items[i].UpdatedAt = now, now
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	pending, err := PMSSandboxRepository.Pending(db, 3, 7)
	if err != nil || pending.ID != 1 {
		t.Fatalf("wrong pending operation: %+v %v", pending, err)
	}
	if err := PMSSandboxRepository.Supersede(db, 3, 7); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		var actual models.PMSOperation
		if err := db.First(&actual, item.ID).Error; err != nil {
			t.Fatal(err)
		}
		want := "pending"
		if item.ID == 1 {
			want = "superseded"
		}
		if actual.Status != want {
			t.Fatalf("operation %d status=%s want=%s", actual.ID, actual.Status, want)
		}
	}
}
