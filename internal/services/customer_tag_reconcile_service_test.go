package services

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCustomerTagReconcilePreserveSourceCopiesExactProtectedSet(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	sourceOne := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "source.one", "")
	sourceTwo := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "source.two", "")
	targetOnly := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "target.only", "")
	addCustomerTagForTest(t, fixture, fixture.conversationA.ID, sourceOne.ID)
	addCustomerTagForTest(t, fixture, fixture.conversationA.ID, sourceTwo.ID)
	addCustomerTagForTest(t, fixture, fixture.conversationB.ID, targetOnly.ID)

	decision, err := CustomerTagService.ReconcileStoreRelationTags(
		reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveSource),
		fixture.adminA,
	)
	if err != nil {
		t.Fatalf("preserve source: %v", err)
	}
	if decision == nil || decision.SourceStoreRelationID != fixture.relationA.ID ||
		decision.TargetStoreRelationID != fixture.relationB.ID ||
		decision.Strategy != enums.StoreCustomerTagReconcileStrategyPreserveSource {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	assertDecisionTagIDs(t, decision.SourceTagIDsJSON, sourceOne.ID, sourceTwo.ID)
	assertDecisionTagIDs(t, decision.TargetBeforeTagIDsJSON, targetOnly.ID)
	assertDecisionTagIDs(t, decision.TargetAfterTagIDsJSON, sourceOne.ID, sourceTwo.ID)
	assertActiveTagIDsForRelation(t, fixture, fixture.relationA, sourceOne.ID, sourceTwo.ID)
	assertActiveTagIDsForRelation(t, fixture, fixture.relationB, sourceOne.ID, sourceTwo.ID)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, sourceOne.ID, customerTagRelationActive, true)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, sourceTwo.ID, customerTagRelationActive, true)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, targetOnly.ID, customerTagRelationInactive, true)
	assertReconcileDecisionCount(t, fixture, 1)
	assertReconcileSummaryCount(t, fixture, fixture.relationB, "reconcile_preserve_source", 1)
}

func TestCustomerTagReconcilePreserveTargetIsAppendOnly(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	sourceTag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "source.kept-out", "")
	targetOne := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "target.one", "")
	targetTwo := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "target.two", "")
	addCustomerTagForTest(t, fixture, fixture.conversationA.ID, sourceTag.ID)
	addCustomerTagForTest(t, fixture, fixture.conversationB.ID, targetOne.ID)

	first, err := CustomerTagService.ReconcileStoreRelationTags(
		reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveTarget),
		fixture.adminA,
	)
	if err != nil {
		t.Fatalf("first preserve target: %v", err)
	}
	addCustomerTagForTest(t, fixture, fixture.conversationB.ID, targetTwo.ID)
	second, err := CustomerTagService.ReconcileStoreRelationTags(
		reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveTarget),
		fixture.adminA,
	)
	if err != nil {
		t.Fatalf("second preserve target: %v", err)
	}
	if first.ID <= 0 || second.ID <= first.ID {
		t.Fatalf("decisions are not append-only: first=%#v second=%#v", first, second)
	}
	assertDecisionTagIDs(t, first.TargetBeforeTagIDsJSON, targetOne.ID)
	assertDecisionTagIDs(t, first.TargetAfterTagIDsJSON, targetOne.ID)
	assertDecisionTagIDs(t, second.TargetBeforeTagIDsJSON, targetOne.ID, targetTwo.ID)
	assertDecisionTagIDs(t, second.TargetAfterTagIDsJSON, targetOne.ID, targetTwo.ID)

	var persistedFirst models.StoreCustomerTagDecision
	if err := fixture.db.First(&persistedFirst, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertDecisionTagIDs(t, persistedFirst.TargetAfterTagIDsJSON, targetOne.ID)
	assertActiveTagIDsForRelation(t, fixture, fixture.relationB, targetOne.ID, targetTwo.ID)
	assertReconcileDecisionCount(t, fixture, 2)
	assertReconcileSummaryCount(t, fixture, fixture.relationB, "reconcile_preserve_target", 2)
}

func TestCustomerTagReconcileClearRebuildRemovesProtectionAndAllowsAI(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	activeTag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "target.active", "")
	removedTag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "target.removed", "")
	addCustomerTagForTest(t, fixture, fixture.conversationB.ID, activeTag.ID)
	addCustomerTagForTest(t, fixture, fixture.conversationB.ID, removedTag.ID)
	if err := CustomerTagService.ManualRemove(request.RemoveCustomerTagRequest{
		ConversationID: fixture.conversationB.ID,
		TagID:          removedTag.ID,
	}, fixture.adminA); err != nil {
		t.Fatal(err)
	}

	decision, err := CustomerTagService.ReconcileStoreRelationTags(
		reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyClearRebuild),
		fixture.adminA,
	)
	if err != nil {
		t.Fatalf("clear and rebuild: %v", err)
	}
	assertDecisionTagIDs(t, decision.TargetBeforeTagIDsJSON, activeTag.ID)
	assertDecisionTagIDs(t, decision.TargetAfterTagIDsJSON)
	assertActiveTagIDsForRelation(t, fixture, fixture.relationB)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, activeTag.ID, customerTagRelationInactive, false)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, removedTag.ID, customerTagRelationInactive, false)

	changed, err := CustomerTagService.ApplyAI(fixture.conversationB.ID, 7701, []CustomerTagOperation{{
		Op: "add", TagID: removedTag.ID, Confidence: 0.95, EvidenceMessageIDs: []int64{8801},
	}})
	if err != nil || !changed {
		t.Fatalf("AI rebuild after clear changed=%t err=%v", changed, err)
	}
	assertCustomerTagRelationStatus(t, fixture, fixture.relationB, removedTag.ID, customerTagRelationActive, false)
	assertReconcileSummaryCount(t, fixture, fixture.relationB, "reconcile_clear_rebuild", 1)
}

func TestCustomerTagReconcileRequiresSupervisorAndStrictScope(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	valid := reconcileCustomerTagsRequest(
		fixture.relationA.ID,
		fixture.relationB.ID,
		enums.StoreCustomerTagReconcileStrategyPreserveTarget,
	)
	unconfirmed := valid
	unconfirmed.Confirmed = false
	if _, err := CustomerTagService.ReconcileStoreRelationTags(unconfirmed, fixture.adminA); err == nil {
		t.Fatal("missing confirmation must be rejected")
	}
	if _, err := CustomerTagService.ReconcileStoreRelationTags(valid, fixture.storeAStaff); err == nil {
		t.Fatal("Store staff must not reconcile Store relations")
	}
	teamLeader := &dto.AuthPrincipal{
		UserID: 6101, Username: "team-leader", TenantID: 101, ActiveTenantID: 101,
		Roles: []string{constants.RoleCodeCsTeamLeader},
	}
	if _, err := CustomerTagService.ReconcileStoreRelationTags(valid, teamLeader); err == nil {
		t.Fatal("team leader must not reconcile Store relations")
	}
	sameRelation := valid
	sameRelation.TargetStoreRelationID = sameRelation.SourceStoreRelationID
	if _, err := CustomerTagService.ReconcileStoreRelationTags(sameRelation, fixture.adminA); err == nil {
		t.Fatal("same Store relation must be rejected")
	}
	foreignRelation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		fixture.db, 202, fixture.conversationOtherTenant.CustomerID, 21,
	)
	crossTenant := valid
	crossTenant.TargetStoreRelationID = foreignRelation.ID
	if _, err := CustomerTagService.ReconcileStoreRelationTags(crossTenant, fixture.adminA); err == nil {
		t.Fatal("cross-Tenant relation must be rejected")
	}

	otherCustomer := &models.Customer{TenantID: 101, Name: "另一个客户", Status: enums.StatusOk}
	if err := fixture.db.Create(otherCustomer).Error; err != nil {
		t.Fatal(err)
	}
	otherRelation := &models.StoreCustomerRelation{
		TenantID: 101, CustomerID: otherCustomer.ID, StoreID: fixture.relationB.StoreID, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(otherRelation).Error; err != nil {
		t.Fatal(err)
	}
	differentCustomer := valid
	differentCustomer.TargetStoreRelationID = otherRelation.ID
	if _, err := CustomerTagService.ReconcileStoreRelationTags(differentCustomer, fixture.adminA); err == nil {
		t.Fatal("different customer relations must be rejected")
	}

	platformAdmin := &dto.AuthPrincipal{
		UserID: 6201, Username: "platform-admin", ActiveTenantID: 101,
		Roles: []string{constants.RoleCodeAdmin}, IsPlatformAccount: true,
	}
	if _, err := CustomerTagService.ReconcileStoreRelationTags(valid, platformAdmin); err != nil {
		t.Fatalf("platform administrator in an active Tenant context: %v", err)
	}
	assertReconcileDecisionCount(t, fixture, 1)
}

func TestCustomerTagReconcileRejectsCorruptSourceSet(t *testing.T) {
	t.Run("more than six active tags", func(t *testing.T) {
		fixture := setupCustomerTagMutationFixture(t)
		for i := 0; i < maxActiveCustomerTags+1; i++ {
			tag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, fmt.Sprintf("corrupt.limit.%d", i), "")
			createRawCustomerTagRelation(t, fixture, fixture.relationA, tag.ID)
		}
		_, err := CustomerTagService.ReconcileStoreRelationTags(
			reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveSource),
			fixture.adminA,
		)
		if err == nil {
			t.Fatal("source relation above the six-tag ceiling must be rejected")
		}
		assertReconcileDecisionCount(t, fixture, 0)
	})

	t.Run("conflicting active tags", func(t *testing.T) {
		fixture := setupCustomerTagMutationFixture(t)
		first := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "corrupt.conflict.one", "same_group")
		second := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "corrupt.conflict.two", "same_group")
		createRawCustomerTagRelation(t, fixture, fixture.relationA, first.ID)
		createRawCustomerTagRelation(t, fixture, fixture.relationA, second.ID)
		_, err := CustomerTagService.ReconcileStoreRelationTags(
			reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveSource),
			fixture.adminA,
		)
		if err == nil {
			t.Fatal("source relation with mutually exclusive tags must be rejected")
		}
		assertReconcileDecisionCount(t, fixture, 0)
	})
}

func TestCustomerTagReconcileConcurrentRequestsKeepLimitAndAudit(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	for i := 0; i < maxActiveCustomerTags; i++ {
		tag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, fmt.Sprintf("concurrent.source.%d", i), "")
		addCustomerTagForTest(t, fixture, fixture.conversationA.ID, tag.ID)
	}
	const workers = 4
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := CustomerTagService.ReconcileStoreRelationTags(
				reconcileCustomerTagsRequest(fixture.relationA.ID, fixture.relationB.ID, enums.StoreCustomerTagReconcileStrategyPreserveSource),
				fixture.adminA,
			)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent reconciliation: %v", err)
		}
	}
	assertActiveCustomerTagCount(t, fixture, fixture.relationB, maxActiveCustomerTags)
	assertReconcileDecisionCount(t, fixture, workers)
	assertReconcileSummaryCount(t, fixture, fixture.relationB, "reconcile_preserve_source", workers)
}

func TestCustomerTagReconcileMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), customerTagMutationGORMConfig())
	if err != nil {
		t.Fatalf("open MySQL customer tag database: %v", err)
	}
	modelsForTest := customerTagMutationModels()
	dropModels := func() {
		for i := len(modelsForTest) - 1; i >= 0; i-- {
			if dropErr := db.Migrator().DropTable(modelsForTest[i]); dropErr != nil {
				t.Errorf("drop MySQL customer tag fixture %T: %v", modelsForTest[i], dropErr)
			}
		}
	}
	dropModels()
	t.Cleanup(func() {
		dropModels()
		sqls.SetDB(nil)
		if raw, dbErr := db.DB(); dbErr == nil {
			_ = raw.Close()
		}
	})

	fixture := setupCustomerTagMutationFixtureWithDB(t, db)
	for i := 0; i < maxActiveCustomerTags; i++ {
		tag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, fmt.Sprintf("mysql.source.%d", i), "")
		addCustomerTagForTest(t, fixture, fixture.conversationA.ID, tag.ID)
	}
	const workers = 4
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reconcileErr := CustomerTagService.ReconcileStoreRelationTags(
				reconcileCustomerTagsRequest(
					fixture.relationA.ID,
					fixture.relationB.ID,
					enums.StoreCustomerTagReconcileStrategyPreserveSource,
				),
				fixture.adminA,
			)
			errCh <- reconcileErr
		}()
	}
	wg.Wait()
	close(errCh)
	for reconcileErr := range errCh {
		if reconcileErr != nil {
			t.Fatalf("concurrent MySQL reconciliation: %v", reconcileErr)
		}
	}
	assertActiveCustomerTagCount(t, fixture, fixture.relationB, maxActiveCustomerTags)
	assertReconcileDecisionCount(t, fixture, workers)
	assertReconcileSummaryCount(t, fixture, fixture.relationB, "reconcile_preserve_source", workers)
}

func reconcileCustomerTagsRequest(
	sourceRelationID, targetRelationID int64,
	strategy enums.StoreCustomerTagReconcileStrategy,
) request.ReconcileStoreCustomerRelationTagsRequest {
	return request.ReconcileStoreCustomerRelationTagsRequest{
		SourceStoreRelationID: sourceRelationID,
		TargetStoreRelationID: targetRelationID,
		Strategy:              strategy,
		Confirmed:             true,
	}
}

func addCustomerTagForTest(t *testing.T, fixture *customerTagMutationFixture, conversationID, tagID int64) {
	t.Helper()
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{
		ConversationID: conversationID,
		TagID:          tagID,
	}, fixture.adminA); err != nil {
		t.Fatal(err)
	}
}

func createRawCustomerTagRelation(
	t *testing.T,
	fixture *customerTagMutationFixture,
	relation models.StoreCustomerRelation,
	tagID int64,
) {
	t.Helper()
	now := time.Now()
	item := &models.CustomerTagRelation{
		TenantID: relation.TenantID, StoreID: relation.StoreID, CustomerID: relation.CustomerID,
		StoreCustomerRelationID: relation.ID, TagID: tagID,
		Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
		Confidence: 1, FirstMatchedAt: &now, LastMatchedAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
}

func assertDecisionTagIDs(t *testing.T, raw string, want ...int64) {
	t.Helper()
	var got []int64
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode tag IDs %q: %v", raw, err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tag IDs=%v, want %v", got, want)
	}
}

func assertActiveTagIDsForRelation(
	t *testing.T,
	fixture *customerTagMutationFixture,
	relation models.StoreCustomerRelation,
	want ...int64,
) {
	t.Helper()
	items, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelation(
		fixture.db, relation.TenantID, relation.StoreID, relation.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	got := activeCustomerTagIDs(items)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("active tag IDs=%v, want %v", got, want)
	}
}

func assertReconcileDecisionCount(t *testing.T, fixture *customerTagMutationFixture, want int64) {
	t.Helper()
	var got int64
	if err := fixture.db.Model(&models.StoreCustomerTagDecision{}).Count(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decision count=%d, want %d", got, want)
	}
}

func assertReconcileSummaryCount(
	t *testing.T,
	fixture *customerTagMutationFixture,
	relation models.StoreCustomerRelation,
	action string,
	want int64,
) {
	t.Helper()
	var got int64
	if err := fixture.db.Model(&models.CustomerTagChangeLog{}).Where(
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND action = ?",
		relation.TenantID, relation.StoreID, relation.ID, action,
	).Count(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("summary log count=%d, want %d", got, want)
	}
}
