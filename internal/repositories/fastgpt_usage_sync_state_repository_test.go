package repositories

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestFastGPTUsageCursorConcurrentCASHasOneWinner(t *testing.T) {
	db := openFastGPTUsageStateTestDB(t)
	now := time.Now()
	state := &models.FastGPTUsageSyncState{
		TenantID: 11, StoreID: 21, KnowledgeBaseID: 31, TenantTeamID: "team-21", Cursor: "cursor-0",
		ModelProfileID: 41, ProfileRevision: 1, CredentialRevision: 1, KeyFingerprint: "key-1",
		FastGPTProfileID: "profile-1", FastGPTRevision: "1", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(state).Error; err != nil {
		t.Fatal(err)
	}
	expected := *state
	first := expected
	first.Cursor = "cursor-first"
	first.UpdatedAt = now.Add(time.Second)
	second := expected
	second.Cursor = "cursor-second"
	second.ModelProfileID = 42
	second.ProfileRevision = 2
	second.CredentialRevision = 2
	second.KeyFingerprint = "key-2"
	second.FastGPTProfileID = "profile-2"
	second.FastGPTRevision = "2"
	second.UpdatedAt = now.Add(2 * time.Second)

	start := make(chan struct{})
	results := make(chan bool, 2)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []*models.FastGPTUsageSyncState{&first, &second} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			updated, err := FastGPTUsageSyncStateRepository.CompareAndSwap(db, &expected, candidate)
			results <- updated
			errorsCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)
	winners := 0
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for updated := range results {
		if updated {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("cursor CAS winners=%d, want 1", winners)
	}
	current := FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(db, state.KnowledgeBaseID, state.TenantID)
	if current == nil || (current.Cursor != first.Cursor && current.Cursor != second.Cursor) {
		t.Fatalf("unexpected winning cursor: %#v", current)
	}
}

func TestFastGPTUsageFailureCannotRegressAdvancedCursor(t *testing.T) {
	db := openFastGPTUsageStateTestDB(t)
	now := time.Now()
	state := &models.FastGPTUsageSyncState{
		TenantID: 12, StoreID: 22, KnowledgeBaseID: 32, TenantTeamID: "team-22", Cursor: "cursor-0",
		ModelProfileID: 42, ProfileRevision: 1, CredentialRevision: 1, KeyFingerprint: "key-1",
		FastGPTProfileID: "profile-1", FastGPTRevision: "1", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(state).Error; err != nil {
		t.Fatal(err)
	}
	stale := *state
	next := *state
	next.Cursor = "cursor-2"
	next.ModelProfileID = 43
	next.ProfileRevision = 2
	next.CredentialRevision = 2
	next.KeyFingerprint = "key-2"
	next.FastGPTProfileID = "profile-2"
	next.FastGPTRevision = "2"
	next.UpdatedAt = now.Add(time.Second)
	updated, err := FastGPTUsageSyncStateRepository.CompareAndSwap(db, state, &next)
	if err != nil || !updated {
		t.Fatalf("advance cursor updated=%t err=%v", updated, err)
	}
	recorded, err := FastGPTUsageSyncStateRepository.SaveFailure(db, &stale, "usage_export_failed", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recorded {
		t.Fatal("late failure unexpectedly changed a newer cursor window")
	}
	current := FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(db, state.KnowledgeBaseID, state.TenantID)
	if current == nil || current.Cursor != next.Cursor || current.ModelProfileID != next.ModelProfileID || current.FastGPTRevision != next.FastGPTRevision {
		t.Fatalf("late failure regressed cursor window: %#v", current)
	}
	if current.LastError != "" {
		t.Fatalf("late failure marked a newer successful window unhealthy: %#v", current)
	}
	recorded, err = FastGPTUsageSyncStateRepository.SaveFailure(db, current, "usage_export_failed", now.Add(3*time.Second))
	if err != nil || !recorded {
		t.Fatalf("current failure recorded=%t err=%v", recorded, err)
	}
	current = FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(db, state.KnowledgeBaseID, state.TenantID)
	if current == nil || current.LastError != "usage_export_failed" {
		t.Fatalf("current failure class was not recorded: %#v", current)
	}
}

func openFastGPTUsageStateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.FastGPTUsageSyncState{}); err != nil {
		t.Fatal(err)
	}
	if raw, err := db.DB(); err == nil {
		raw.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = raw.Close() })
	}
	return db
}
