package services

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type customerTagMutationFixture struct {
	db                      *gorm.DB
	adminA                  *dto.AuthPrincipal
	adminB                  *dto.AuthPrincipal
	storeAStaff             *dto.AuthPrincipal
	conversationA           models.Conversation
	conversationB           models.Conversation
	conversationOtherTenant models.Conversation
	relationA               models.StoreCustomerRelation
	relationB               models.StoreCustomerRelation
	parentA                 models.Tag
	parentB                 models.Tag
	nextTagID               int64
}

func TestCustomerTagManualMutationsEnforceTenantAndStoreScope(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	quiet := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "room.quiet", "room_noise")
	highFloor := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "floor.high", "")
	otherTenantTag := fixture.createLeafTag(t, 202, 2001, fixture.parentB.ID, "room.other-tenant", "")

	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: quiet.ID}, fixture.adminB); err == nil {
		t.Fatal("other tenant administrator must not mutate customer tags")
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationB.ID, TagID: highFloor.ID}, fixture.storeAStaff); err == nil {
		t.Fatal("Store A account must not mutate Store B customer tags")
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: otherTenantTag.ID}, fixture.adminA); err == nil {
		t.Fatal("a tag from another tenant must not enter the active Store relation")
	}

	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: quiet.ID}, fixture.storeAStaff); err != nil {
		t.Fatalf("Store A account adds its customer tag: %v", err)
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationB.ID, TagID: highFloor.ID}, fixture.adminA); err != nil {
		t.Fatalf("tenant administrator adds Store B customer tag: %v", err)
	}

	byConversation := CustomerTagService.ListForConversations([]models.Conversation{fixture.conversationA, fixture.conversationB, fixture.conversationOtherTenant})
	assertCustomerTagIDs(t, byConversation[fixture.conversationA.ID], quiet.ID)
	assertCustomerTagIDs(t, byConversation[fixture.conversationB.ID], highFloor.ID)
	if len(byConversation[fixture.conversationOtherTenant.ID]) != 0 {
		t.Fatalf("unrelated tenant conversation received tags: %#v", byConversation[fixture.conversationOtherTenant.ID])
	}

	var logCount int64
	if err := fixture.db.Model(&models.CustomerTagChangeLog{}).
		Where("tenant_id = ? AND store_id = ? AND store_customer_relation_id = ?", 101, fixture.relationA.StoreID, fixture.relationA.ID).
		Count(&logCount).Error; err != nil {
		t.Fatal(err)
	}
	if logCount != 1 {
		t.Fatalf("Store A change log count=%d, want 1", logCount)
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: quiet.ID}, fixture.storeAStaff); err != nil {
		t.Fatalf("idempotent manual add: %v", err)
	}
	var repeatedLogCount int64
	if err := fixture.db.Model(&models.CustomerTagChangeLog{}).
		Where("tenant_id = ? AND store_id = ? AND store_customer_relation_id = ?", 101, fixture.relationA.StoreID, fixture.relationA.ID).
		Count(&repeatedLogCount).Error; err != nil {
		t.Fatal(err)
	}
	if repeatedLogCount != logCount {
		t.Fatalf("idempotent add wrote another audit record: before=%d after=%d", logCount, repeatedLogCount)
	}
}

func TestCustomerTagConversationFilterUsesStoreScopedRelation(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	storeATag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "store-a.preference", "")
	storeBTag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "store-b.preference", "")
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{
		ConversationID: fixture.conversationA.ID,
		TagID:          storeATag.ID,
	}, fixture.adminA); err != nil {
		t.Fatal(err)
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{
		ConversationID: fixture.conversationB.ID,
		TagID:          storeBTag.ID,
	}, fixture.adminA); err != nil {
		t.Fatal(err)
	}

	list := repositories.ConversationRepository.Find(
		fixture.db,
		CustomerTagService.ApplyConversationFilter(
			sqls.NewCnd().Eq("tenant_id", int64(101)).Asc("id"),
			101,
			[]int64{storeATag.ID},
		),
	)
	if len(list) != 1 || list[0].ID != fixture.conversationA.ID {
		t.Fatalf("Store A tag filter returned conversations %#v", list)
	}

	list = repositories.ConversationRepository.Find(
		fixture.db,
		CustomerTagService.ApplyConversationFilter(
			sqls.NewCnd().Eq("tenant_id", int64(101)).Asc("id"),
			101,
			[]int64{storeATag.ID, storeBTag.ID},
		),
	)
	if len(list) != 2 || list[0].ID != fixture.conversationA.ID || list[1].ID != fixture.conversationB.ID {
		t.Fatalf("combined Store tag filter returned conversations %#v", list)
	}
}

func TestCustomerTagSixLimitAndConflictReplacement(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	tags := make([]models.Tag, 0, 8)
	for i := 0; i < 6; i++ {
		conflictGroup := ""
		if i == 0 {
			conflictGroup = "bed_preference"
		}
		tags = append(tags, fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, fmt.Sprintf("preference.%d", i+1), conflictGroup))
	}
	overflow := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "preference.overflow", "")
	replacement := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "preference.replacement", "bed_preference")

	for i := range tags {
		if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: tags[i].ID}, fixture.adminA); err != nil {
			t.Fatalf("add tag %d: %v", i+1, err)
		}
	}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: overflow.ID}, fixture.adminA); err == nil {
		t.Fatal("seventh unrelated tag must be rejected")
	}
	assertActiveCustomerTagCount(t, fixture, fixture.relationA, 6)

	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: replacement.ID}, fixture.adminA); err != nil {
		t.Fatalf("replace one conflicting tag at the six-tag ceiling: %v", err)
	}
	assertActiveCustomerTagCount(t, fixture, fixture.relationA, 6)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationA, tags[0].ID, customerTagRelationInactive, true)
	assertCustomerTagRelationStatus(t, fixture, fixture.relationA, replacement.ID, customerTagRelationActive, true)

	var replaceLogs int64
	if err := fixture.db.Model(&models.CustomerTagChangeLog{}).
		Where("tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND action = ? AND old_tag_id = ? AND new_tag_id = ?",
			101, fixture.relationA.StoreID, fixture.relationA.ID, "replace", tags[0].ID, replacement.ID).
		Count(&replaceLogs).Error; err != nil {
		t.Fatal(err)
	}
	if replaceLogs != 1 {
		t.Fatalf("conflict replacement audit records=%d, want 1", replaceLogs)
	}
}

func TestCustomerTagManualProtectionBlocksAIReversal(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	quiet := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "room.quiet", "room_noise")
	nearElevator := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "room.near-elevator", "room_noise")
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: quiet.ID}, fixture.adminA); err != nil {
		t.Fatal(err)
	}

	changed, err := CustomerTagService.ApplyAI(fixture.conversationA.ID, 901, []CustomerTagOperation{{
		Op: "remove", TagID: quiet.ID, Confidence: 0.99, EvidenceMessageIDs: []int64{101},
	}})
	if err != nil || changed {
		t.Fatalf("AI removal of a manually protected tag changed=%t err=%v", changed, err)
	}
	changed, err = CustomerTagService.ApplyAI(fixture.conversationA.ID, 902, []CustomerTagOperation{{
		Op: "replace", TagID: nearElevator.ID, Replaces: []int64{quiet.ID}, Confidence: 0.99, EvidenceMessageIDs: []int64{102},
	}})
	if err != nil || changed {
		t.Fatalf("AI replacement of a manually protected conflict changed=%t err=%v", changed, err)
	}
	assertCustomerTagRelationStatus(t, fixture, fixture.relationA, quiet.ID, customerTagRelationActive, true)

	if err := CustomerTagService.ManualRemove(request.RemoveCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: quiet.ID}, fixture.adminA); err != nil {
		t.Fatal(err)
	}
	changed, err = CustomerTagService.ApplyAI(fixture.conversationA.ID, 903, []CustomerTagOperation{{
		Op: "add", TagID: quiet.ID, Confidence: 0.99, EvidenceMessageIDs: []int64{103},
	}})
	if err != nil || changed {
		t.Fatalf("AI re-add after a manual removal changed=%t err=%v", changed, err)
	}
	assertCustomerTagRelationStatus(t, fixture, fixture.relationA, quiet.ID, customerTagRelationInactive, true)
}

func TestCustomerTagOptionsKeepDisabledAssignedTagsRemovable(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	tag := fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, "legacy.disabled", "")
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{
		ConversationID: fixture.conversationA.ID, TagID: tag.ID,
	}, fixture.adminA); err != nil {
		t.Fatal(err)
	}
	if err := repositories.TagRepository.UpdatesInTenant(fixture.db, tag.ID, 101, map[string]any{
		"status": enums.StatusDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	options, err := CustomerTagService.ListOptionsForConversation(fixture.conversationA.ID, fixture.adminA)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range options {
		if options[i].ID == tag.ID && options[i].Status == enums.StatusDisabled {
			found = true
		}
	}
	if !found {
		t.Fatal("disabled fixed tag must remain in options so an existing assignment can be removed")
	}
	if err := CustomerTagService.ManualRemove(request.RemoveCustomerTagRequest{
		ConversationID: fixture.conversationA.ID, TagID: tag.ID,
	}, fixture.adminA); err != nil {
		t.Fatalf("remove disabled assigned tag: %v", err)
	}
	assertCustomerTagRelationStatus(t, fixture, fixture.relationA, tag.ID, customerTagRelationInactive, true)
}

func TestCustomerTagConcurrentWritesNeverExceedSix(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	tags := make([]models.Tag, 12)
	for i := range tags {
		tags[i] = fixture.createLeafTag(t, 101, 1001, fixture.parentA.ID, fmt.Sprintf("concurrent.%02d", i+1), "")
	}

	errCh := make(chan error, len(tags))
	var wg sync.WaitGroup
	for i := range tags {
		wg.Add(1)
		go func(tagID int64) {
			defer wg.Done()
			errCh <- CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: fixture.conversationA.ID, TagID: tagID}, fixture.adminA)
		}(tags[i].ID)
	}
	wg.Wait()
	close(errCh)
	failures := 0
	for err := range errCh {
		if err != nil {
			failures++
		}
	}
	if failures != len(tags)-maxActiveCustomerTags {
		t.Fatalf("concurrent rejected writes=%d, want %d", failures, len(tags)-maxActiveCustomerTags)
	}
	assertActiveCustomerTagCount(t, fixture, fixture.relationA, maxActiveCustomerTags)
}

func TestCustomerAndContactsHonorStoreDataScope(t *testing.T) {
	fixture := setupCustomerTagMutationFixture(t)
	storeBOnly := &models.Customer{TenantID: 101, Name: "仅 B 门店客户", Status: enums.StatusOk}
	if err := fixture.db.Create(storeBOnly).Error; err != nil {
		t.Fatal(err)
	}
	relation := &models.StoreCustomerRelation{TenantID: 101, CustomerID: storeBOnly.ID, StoreID: fixture.relationB.StoreID, Status: enums.StatusOk}
	if err := fixture.db.Create(relation).Error; err != nil {
		t.Fatal(err)
	}

	adminList, adminPaging := CustomerService.ListCustomers(request.CustomerListRequest{Page: 1, Limit: 20}, fixture.adminA)
	if len(adminList) != 2 || adminPaging.Total != 2 {
		t.Fatalf("tenant admin customer scope list=%#v paging=%#v", adminList, adminPaging)
	}
	storeList, storePaging := CustomerService.ListCustomers(request.CustomerListRequest{Page: 1, Limit: 20}, fixture.storeAStaff)
	if len(storeList) != 1 || storeList[0].ID != fixture.conversationA.CustomerID || storePaging.Total != 1 {
		t.Fatalf("Store A customer scope list=%#v paging=%#v", storeList, storePaging)
	}
	if CustomerService.CanAccessCustomer(fixture.storeAStaff, storeBOnly.ID) {
		t.Fatal("Store A account can access a Store B-only customer")
	}

	sharedCustomer := repositories.CustomerRepository.GetInTenant(fixture.db, fixture.conversationA.CustomerID, 101)
	adminPresentation := CustomerService.LoadPresentationDataForOperator([]models.Customer{*sharedCustomer}, true, fixture.adminA)
	storePresentation := CustomerService.LoadPresentationDataForOperator([]models.Customer{*sharedCustomer}, true, fixture.storeAStaff)
	if len(adminPresentation.StoreRelationsByCustomerID[sharedCustomer.ID]) != 2 {
		t.Fatalf("tenant admin Store relation count=%d, want 2", len(adminPresentation.StoreRelationsByCustomerID[sharedCustomer.ID]))
	}
	if got := storePresentation.StoreRelationsByCustomerID[sharedCustomer.ID]; len(got) != 1 || got[0].StoreID != fixture.relationA.StoreID {
		t.Fatalf("Store A presentation leaked another Store relation: %#v", got)
	}

	contact, err := CustomerContactService.CreateCustomerContact(request.CreateCustomerContactRequest{
		CustomerID: storeBOnly.ID, ContactType: string(enums.ContactTypeMobile), ContactValue: "13800000000", Status: int(enums.StatusOk),
	}, fixture.adminA)
	if err != nil {
		t.Fatalf("tenant admin creates Store B customer contact: %v", err)
	}
	if contacts := CustomerContactService.FindActiveByCustomerID(storeBOnly.ID, fixture.storeAStaff); len(contacts) != 0 {
		t.Fatalf("Store A account listed Store B-only contacts: %#v", contacts)
	}
	if err := CustomerContactService.UpdateCustomerContact(request.UpdateCustomerContactRequest{
		ID: contact.ID, ContactType: string(enums.ContactTypeMobile), ContactValue: "13900000000", Status: int(enums.StatusOk),
	}, fixture.storeAStaff); err == nil {
		t.Fatal("Store A account updated Store B-only contact")
	}
}

func setupCustomerTagMutationFixture(t *testing.T) *customerTagMutationFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "customer-tag.db")), customerTagMutationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, dbErr := db.DB(); dbErr == nil {
			_ = raw.Close()
		}
	})
	return setupCustomerTagMutationFixtureWithDB(t, db)
}

func customerTagMutationGORMConfig() *gorm.Config {
	return &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	}
}

func customerTagMutationModels() []any {
	return []any{
		&models.Tenant{}, &models.Store{}, &models.StoreStaffBinding{},
		&models.Customer{}, &models.CustomerContact{}, &models.Conversation{}, &models.ConversationRouteState{},
		&models.StoreCustomerRelation{}, &models.Tag{}, &models.CustomerTagRelation{}, &models.CustomerTagChangeLog{},
		&models.StoreCustomerTagDecision{},
	}
}

func setupCustomerTagMutationFixtureWithDB(t *testing.T, db *gorm.DB) *customerTagMutationFixture {
	t.Helper()
	if err := db.AutoMigrate(customerTagMutationModels()...); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)

	tenants := []models.Tenant{
		{ID: 101, IntentProfileID: 1001, TenantCode: "tag-tenant-a", LegalName: "标签租户 A", RegistrationType: "test", RegistrationNo: "tag-a", Status: enums.StatusOk},
		{ID: 202, IntentProfileID: 2001, TenantCode: "tag-tenant-b", LegalName: "标签租户 B", RegistrationType: "test", RegistrationNo: "tag-b", Status: enums.StatusOk},
	}
	if err := db.Create(&tenants).Error; err != nil {
		t.Fatal(err)
	}
	stores := []models.Store{
		{ID: 11, TenantID: 101, StoreCode: "store-a", Name: "A 门店", Status: enums.StatusOk},
		{ID: 12, TenantID: 101, StoreCode: "store-b", Name: "B 门店", Status: enums.StatusOk},
		{ID: 21, TenantID: 202, StoreCode: "store-c", Name: "C 门店", Status: enums.StatusOk},
	}
	if err := db.Create(&stores).Error; err != nil {
		t.Fatal(err)
	}
	customers := []models.Customer{
		{ID: 31, TenantID: 101, Name: "同一自然客户", Status: enums.StatusOk},
		{ID: 32, TenantID: 202, Name: "其他租户客户", Status: enums.StatusOk},
	}
	if err := db.Create(&customers).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	conversations := []models.Conversation{
		{ID: 10001, TenantID: 101, CustomerID: 31, CustomerName: customers[0].Name, Status: enums.IMConversationStatusAIServing, LastMessageAt: now, LastActiveAt: now},
		{ID: 10002, TenantID: 101, CustomerID: 31, CustomerName: customers[0].Name, Status: enums.IMConversationStatusAIServing, LastMessageAt: now, LastActiveAt: now},
		{ID: 20001, TenantID: 202, CustomerID: 32, CustomerName: customers[1].Name, Status: enums.IMConversationStatusAIServing, LastMessageAt: now, LastActiveAt: now},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	routes := []models.ConversationRouteState{
		{TenantID: 101, ConversationID: conversations[0].ID, StoreID: stores[0].ID},
		{TenantID: 101, ConversationID: conversations[1].ID, StoreID: stores[1].ID},
		{TenantID: 202, ConversationID: conversations[2].ID, StoreID: stores[2].ID},
	}
	if err := db.Create(&routes).Error; err != nil {
		t.Fatal(err)
	}
	relations := []models.StoreCustomerRelation{
		{TenantID: 101, CustomerID: customers[0].ID, StoreID: stores[0].ID, LastConversationID: conversations[0].ID, Status: enums.StatusOk},
		{TenantID: 101, CustomerID: customers[0].ID, StoreID: stores[1].ID, LastConversationID: conversations[1].ID, Status: enums.StatusOk},
		{TenantID: 202, CustomerID: customers[1].ID, StoreID: stores[2].ID, LastConversationID: conversations[2].ID, Status: enums.StatusOk},
	}
	if err := db.Create(&relations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreStaffBinding{TenantID: 101, UserID: 7001, StoreID: stores[0].ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatal(err)
	}

	fixture := &customerTagMutationFixture{
		db:            db,
		adminA:        &dto.AuthPrincipal{UserID: 5001, Username: "tenant-admin-a", ActiveTenantID: 101, Roles: []string{constants.RoleCodeTenantAdmin}},
		adminB:        &dto.AuthPrincipal{UserID: 5002, Username: "tenant-admin-b", ActiveTenantID: 202, Roles: []string{constants.RoleCodeTenantAdmin}},
		storeAStaff:   &dto.AuthPrincipal{UserID: 7001, Username: "store-a", ActiveTenantID: 101, Roles: []string{constants.RoleCodeStoreStaff}},
		conversationA: conversations[0], conversationB: conversations[1], conversationOtherTenant: conversations[2],
		relationA: relations[0], relationB: relations[1], nextTagID: 100000,
	}
	fixture.parentA = fixture.createCategoryTag(t, 101, 1001, "hotel.preferences")
	fixture.parentB = fixture.createCategoryTag(t, 202, 2001, "hotel.preferences")
	return fixture
}

func (f *customerTagMutationFixture) createCategoryTag(t *testing.T, tenantID, profileID int64, semanticKey string) models.Tag {
	t.Helper()
	f.nextTagID++
	templateID := f.nextTagID + 100000
	item := models.Tag{
		ID: f.nextTagID, TenantID: tenantID, IntentProfileID: profileID, TemplateDefinitionID: &templateID,
		Name: semanticKey, SemanticKey: semanticKey, SystemDefined: true, Status: enums.StatusOk,
	}
	if err := f.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func (f *customerTagMutationFixture) createLeafTag(t *testing.T, tenantID, profileID, parentID int64, semanticKey, conflictGroup string) models.Tag {
	t.Helper()
	f.nextTagID++
	templateID := f.nextTagID + 100000
	item := models.Tag{
		ID: f.nextTagID, TenantID: tenantID, IntentProfileID: profileID, TemplateDefinitionID: &templateID,
		ParentID: parentID, Name: semanticKey, SemanticKey: semanticKey, ConflictGroup: conflictGroup,
		ApplicableScene: "customer_service", AIEnabled: true, ReplyEnabled: true, SystemDefined: true, Status: enums.StatusOk,
	}
	if err := f.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func assertCustomerTagIDs(t *testing.T, actual []response.CustomerTagResponse, expected ...int64) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("customer tags=%#v, want IDs %v", actual, expected)
	}
	for i, tagID := range expected {
		if actual[i].TagID != tagID {
			t.Fatalf("customer tag[%d]=%d, want %d", i, actual[i].TagID, tagID)
		}
	}
}

func assertActiveCustomerTagCount(t *testing.T, fixture *customerTagMutationFixture, relation models.StoreCustomerRelation, want int) {
	t.Helper()
	count, err := repositories.CustomerTagRelationRepository.CountActiveByStoreRelation(fixture.db, relation.TenantID, relation.StoreID, relation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != int64(want) {
		t.Fatalf("active customer tags=%d, want %d", count, want)
	}
}

func assertCustomerTagRelationStatus(t *testing.T, fixture *customerTagMutationFixture, storeRelation models.StoreCustomerRelation, tagID int64, wantStatus string, wantProtected bool) {
	t.Helper()
	item, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTag(fixture.db, storeRelation.TenantID, storeRelation.StoreID, storeRelation.ID, tagID)
	if err != nil {
		t.Fatal(err)
	}
	if item == nil || item.RelationStatus != wantStatus || item.ManualProtected != wantProtected {
		t.Fatalf("customer tag relation=%#v, want status=%s protected=%t", item, wantStatus, wantProtected)
	}
}
