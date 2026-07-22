package services

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func TestManualAddReplacesProtectedConflictTransactionally(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	scope, parent := createCustomerTagTestScope(t, db)
	oldTag := createCustomerTagTestTag(t, db, parent.ID, "大床", "room.king_bed", "room.bed", "room_assignment", true)
	newTag := createCustomerTagTestTag(t, db, parent.ID, "双床", "room.twin_bed", "room.bed", "room_assignment", true)
	now := time.Now()
	oldRelation := &models.CustomerTagRelation{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, TagID: oldTag.ID,
		Source: customerTagSourceManual, RelationStatus: customerTagRelationActive,
		Confidence: 1, EvidenceCount: 1, ManualProtected: true,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(oldRelation).Error; err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "root", Roles: []string{constants.RoleCodeSuperAdmin}}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{
		ConversationID: scope.Conversation.ID, TagID: newTag.ID,
	}, operator); err != nil {
		t.Fatal(err)
	}
	oldCurrent := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, oldTag.ID)
	newCurrent := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, newTag.ID)
	if oldCurrent == nil || oldCurrent.RelationStatus != customerTagRelationInactive {
		t.Fatalf("old conflicting relation=%#v", oldCurrent)
	}
	if newCurrent == nil || newCurrent.RelationStatus != customerTagRelationActive || !newCurrent.ManualProtected {
		t.Fatalf("new manual relation=%#v", newCurrent)
	}
	var log models.CustomerTagChangeLog
	if err := db.Where("action = ? AND old_tag_id = ? AND new_tag_id = ?", "replace", oldTag.ID, newTag.ID).Take(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.EvidenceMessageIDs != "[]" {
		t.Fatalf("manual change log evidence=%q, want []", log.EvidenceMessageIDs)
	}
	history, _, err := CustomerTagService.ListChangeLogsForConversation(scope.Conversation.ID, 1, 20, operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].EvidenceMessageIDs == nil || len(history[0].EvidenceMessageIDs) != 0 {
		t.Fatalf("manual change log history=%#v", history)
	}
}

func TestAIAddSkipsExistingNonManualConflict(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	scope, parent := createCustomerTagTestScope(t, db)
	oldTag := createCustomerTagTestTag(t, db, parent.ID, "大床", "room.king_bed", "room.bed", "room_assignment", true)
	newTag := createCustomerTagTestTag(t, db, parent.ID, "双床", "room.twin_bed", "room.bed", "room_assignment", true)
	now := time.Now()
	if err := db.Create(&models.CustomerTagRelation{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, TagID: oldTag.ID,
		Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
		Confidence: 0.9, EvidenceCount: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	changed, err := CustomerTagService.applyAIOperation(db, scope, 3, CustomerTagOperation{
		Op: "add", TagID: newTag.ID, Confidence: 0.99, EvidenceMessageIDs: []int64{5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("AI add must not implicitly replace a conflicting relation")
	}
	if next := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, newTag.ID); next != nil {
		t.Fatalf("conflicting AI tag was created: %#v", next)
	}
}

func TestAIReplaceRejectsUnrelatedReplaceIDs(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	scope, parent := createCustomerTagTestScope(t, db)
	oldTag := createCustomerTagTestTag(t, db, parent.ID, "大床", "room.king_bed", "room.bed", "room_assignment", true)
	newTag := createCustomerTagTestTag(t, db, parent.ID, "双床", "room.twin_bed", "room.bed", "room_assignment", true)
	unrelated := createCustomerTagTestTag(t, db, parent.ID, "要窗", "room.window", "", "room_assignment", true)
	now := time.Now()
	if err := db.Create(&models.CustomerTagRelation{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, TagID: oldTag.ID,
		Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
		Confidence: 0.9, EvidenceCount: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	changed, err := CustomerTagService.applyAIOperation(db, scope, 4, CustomerTagOperation{
		Op: "replace", TagID: newTag.ID, Replaces: []int64{oldTag.ID, unrelated.ID}, Confidence: 0.99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("AI replace must reject extra unrelated replace IDs")
	}
	oldCurrent := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, oldTag.ID)
	if oldCurrent == nil || oldCurrent.RelationStatus != customerTagRelationActive {
		t.Fatalf("old relation changed after rejected replace: %#v", oldCurrent)
	}
	if next := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, newTag.ID); next != nil {
		t.Fatalf("new relation was created after rejected replace: %#v", next)
	}
}

func TestReplyTagCandidatesRespectCurrentOverrideAndLimit(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	scope, parent := createCustomerTagTestScope(t, db)
	quiet := createCustomerTagTestTag(t, db, parent.ID, "喜静", "room.quiet", "", "room_assignment", true)
	king := createCustomerTagTestTag(t, db, parent.ID, "大床", "room.king_bed", "room.bed", "room_assignment", true)
	_ = createCustomerTagTestTag(t, db, parent.ID, "双床", "room.twin_bed", "room.bed", "room_assignment", true)
	window := createCustomerTagTestTag(t, db, parent.ID, "要窗", "room.window", "", "room_assignment", true)
	now := time.Now()
	for index, tag := range []*models.Tag{quiet, king, window} {
		matchedAt := now.Add(time.Duration(index) * time.Minute)
		if err := db.Create(&models.CustomerTagRelation{
			CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
			StoreCustomerRelationID: scope.Relation.ID, TagID: tag.ID,
			Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
			Confidence: 0.8 + float64(index)/100, LastMatchedAt: &matchedAt,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	overridden, err := CustomerTagService.SelectReplyTagCandidates(scope.Conversation.ID, []string{"room_assignment"}, "这次要双床")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range overridden {
		if candidate.TagID == king.ID {
			t.Fatal("historical big-bed preference must be excluded when current turn asks for twin beds")
		}
	}
	selected, err := CustomerTagService.SelectReplyTagCandidates(scope.Conversation.ID, []string{"room_assignment"}, "帮我安排一下房间")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 {
		t.Fatalf("selected candidate count=%d values=%#v", len(selected), selected)
	}
}

func TestSystemTagProtectionAndCustomDefaults(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	parent := &models.Tag{
		CompanyID: 0, Name: "房间偏好", SemanticKey: "category.room_preference",
		SystemDefined: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	standard := createCustomerTagTestTag(t, db, parent.ID, "大床", "room.king_bed", "room.bed", "room_assignment", true)
	standard.SystemDefined = true
	if err := db.Model(standard).Update("system_defined", true).Error; err != nil {
		t.Fatal(err)
	}
	admin := &dto.AuthPrincipal{UserID: 2, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	if err := TagService.UpdateTag(request.UpdateTagRequest{
		ID: standard.ID,
		CreateTagRequest: request.CreateTagRequest{
			CompanyID: 0, ParentID: parent.ID, Name: standard.Name,
			Aliases: standard.Aliases, AIEnabled: true, ReplyEnabled: true,
			ApplicableScene: "room_assignment",
		},
	}, admin); err == nil {
		t.Fatal("administrator must not update a system-defined tag")
	}
	if err := TagService.DeleteTag(standard.ID); err == nil {
		t.Fatal("system-defined tag deletion must be blocked in the service core")
	}
	customParent, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: 7, Name: "自定义偏好"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := TagService.CreateTag(request.CreateTagRequest{
		CompanyID: 7, ParentID: customParent.ID, Name: "近前台",
	}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if custom.SemanticKey == "" || custom.AIEnabled || custom.ReplyEnabled || custom.SystemDefined {
		t.Fatalf("unexpected custom tag defaults: %#v", custom)
	}
}

func TestCustomerTagAccessScopesForAdminLeaderAndAgent(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	if err := db.AutoMigrate(&models.AgentTeam{}, &models.AgentProfile{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{}); err != nil {
		t.Fatal(err)
	}
	scope, _ := createCustomerTagTestScope(t, db)
	admin := &dto.AuthPrincipal{UserID: 2, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	if _, err := CustomerTagService.ListOptionsForConversation(scope.Conversation.ID, admin); err != nil {
		t.Fatalf("administrator should have unrestricted customer tag access: %v", err)
	}

	now := time.Now()
	if err := db.Create(&models.AgentTeam{
		LeaderUserID: 88, Name: "公司客服组", StoreScopeIDs: fmt.Sprint(scope.StoreID), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	leader := &dto.AuthPrincipal{UserID: 88, Username: "leader", Roles: []string{constants.RoleCodeCsTeamLeader}}
	if _, err := CustomerTagService.ListOptionsForConversation(scope.Conversation.ID, leader); err != nil {
		t.Fatalf("in-scope customer-service leader was rejected: %v", err)
	}
	if _, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: scope.CompanyID, Name: "组内分类"}, leader); err != nil {
		t.Fatalf("in-scope leader could not create a company category: %v", err)
	}
	if _, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: scope.CompanyID + 1, Name: "越权分类"}, leader); err == nil {
		t.Fatal("leader must not create tags outside the managed company scope")
	}

	if err := db.Create(&models.AgentProfile{
		UserID: 77, AgentCode: "agent-77", TeamID: 0, StoreScopeIDs: fmt.Sprint(scope.StoreID), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	agent := &dto.AuthPrincipal{UserID: 77, Username: "agent", Roles: []string{constants.RoleCodeCsUser}}
	if _, err := CustomerTagService.ListOptionsForConversation(scope.Conversation.ID, agent); err != nil {
		t.Fatalf("in-scope customer-service agent was rejected: %v", err)
	}
	if _, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: scope.CompanyID, Name: "客服越权"}, agent); err == nil {
		t.Fatal("customer-service agent must not manage tag definitions")
	}
	if err := db.Create(&models.StoreStaffBinding{
		UserID: 66, CompanyID: scope.CompanyID, StoreID: scope.StoreID, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	storeStaff := &dto.AuthPrincipal{UserID: 66, Username: "store", Roles: []string{constants.RoleCodeStoreStaff}}
	if _, err := CustomerTagService.ListOptionsForConversation(scope.Conversation.ID, storeStaff); err != nil {
		t.Fatalf("in-scope store staff was rejected: %v", err)
	}
	if _, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: scope.CompanyID, Name: "门店越权"}, storeStaff); err == nil {
		t.Fatal("store staff must not manage tag definitions")
	}
	if err := db.Create(&models.AgentProfile{
		UserID: 78, AgentCode: "agent-78", TeamID: 0, StoreScopeIDs: "999999", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	outOfScope := &dto.AuthPrincipal{UserID: 78, Username: "other", Roles: []string{constants.RoleCodeCsUser}}
	if _, err := CustomerTagService.ListOptionsForConversation(scope.Conversation.ID, outOfScope); err == nil {
		t.Fatal("out-of-scope customer-service agent must be rejected")
	}
}

func TestCustomerTagReactivationRespectsActiveLimit(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	scope, parent := createCustomerTagTestScope(t, db)
	now := time.Now()
	for index := 0; index < maxActiveCustomerTags; index++ {
		tag := createCustomerTagTestTag(t, db, parent.ID, fmt.Sprintf("标%d", index), fmt.Sprintf("custom.active.%d", index), "", "room_service", true)
		if err := db.Create(&models.CustomerTagRelation{
			CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
			StoreCustomerRelationID: scope.Relation.ID, TagID: tag.ID,
			Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := createCustomerTagTestTag(t, db, parent.ID, "备用", "custom.inactive", "", "room_service", true)
	inactive := &models.CustomerTagRelation{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, TagID: target.ID,
		Source: customerTagSourceAI, RelationStatus: customerTagRelationInactive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(inactive).Error; err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "root", Roles: []string{constants.RoleCodeSuperAdmin}}
	if err := CustomerTagService.ManualAdd(request.AddCustomerTagRequest{ConversationID: scope.Conversation.ID, TagID: target.ID}, operator); err == nil {
		t.Fatal("manual reactivation must respect the 20 active tag limit")
	}
	changed, err := CustomerTagService.applyAIOperation(db, scope, 7, CustomerTagOperation{Op: "add", TagID: target.ID, Confidence: 0.95})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("AI reactivation must respect the 20 active tag limit")
	}
	if count := repositories.CustomerTagRelationRepository.CountActiveByRelationID(db, scope.Relation.ID); count != maxActiveCustomerTags {
		t.Fatalf("active tag count=%d", count)
	}
	current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, target.ID)
	if current == nil || current.RelationStatus != customerTagRelationInactive {
		t.Fatalf("inactive relation was unexpectedly reactivated: %#v", current)
	}
}

func TestConflictGroupLifecycleAndDeleteReferenceProtection(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	admin := &dto.AuthPrincipal{UserID: 2, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	parent, err := TagService.CreateTag(request.CreateTagRequest{CompanyID: 7, Name: "服务习惯"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	tags := make([]*models.Tag, 0, 3)
	for _, name := range []string{"多送水", "少送水", "常清洁"} {
		item, createErr := TagService.CreateTag(request.CreateTagRequest{CompanyID: 7, ParentID: parent.ID, Name: name}, admin)
		if createErr != nil {
			t.Fatal(createErr)
		}
		tags = append(tags, item)
	}
	key, err := TagService.CreateConflictGroup(request.CreateTagConflictGroupRequest{CompanyID: 7, TagIDs: []int64{tags[0].ID, tags[1].ID}}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Fatal("custom conflict group key was not generated")
	}
	if err := TagService.AssignConflictGroup(request.AssignTagConflictGroupRequest{TagID: tags[2].ID, GroupKey: key}, admin); err != nil {
		t.Fatal(err)
	}
	if err := TagService.AssignConflictGroup(request.AssignTagConflictGroupRequest{TagID: tags[0].ID, GroupKey: ""}, admin); err != nil {
		t.Fatal(err)
	}
	if err := TagService.AssignConflictGroup(request.AssignTagConflictGroupRequest{TagID: tags[1].ID, GroupKey: ""}, admin); err != nil {
		t.Fatal(err)
	}
	for _, item := range tags {
		current := TagService.Get(item.ID)
		if current == nil || current.ConflictGroup != "" {
			t.Fatalf("singleton group was not dissolved for tag %d: %#v", item.ID, current)
		}
	}
	if _, err := TagService.CreateConflictGroup(request.CreateTagConflictGroupRequest{CompanyID: 7, TagIDs: []int64{tags[0].ID, tags[1].ID}}, admin); err != nil {
		t.Fatal(err)
	}
	if err := TagService.UpdateStatus(tags[0].ID, int(enums.StatusDisabled), admin); err != nil {
		t.Fatal(err)
	}
	for _, item := range tags[:2] {
		if current := TagService.Get(item.ID); current == nil || current.ConflictGroup != "" {
			t.Fatalf("group with one effective member was not dissolved: %#v", current)
		}
	}
	if _, err := TagService.CreateConflictGroup(request.CreateTagConflictGroupRequest{CompanyID: 7, TagIDs: []int64{tags[1].ID, tags[2].ID}}, admin); err != nil {
		t.Fatal(err)
	}
	if err := TagService.DeleteTagAs(tags[2].ID, admin); err != nil {
		t.Fatal(err)
	}
	if TagService.Get(tags[2].ID) != nil {
		t.Fatal("unreferenced custom tag was not deleted")
	}
	if current := TagService.Get(tags[1].ID); current == nil || current.ConflictGroup != "" {
		t.Fatalf("deleting a conflict member left a singleton group: %#v", current)
	}

	scope, _ := createCustomerTagTestScope(t, db)
	if err := db.Create(&models.CustomerTagRelation{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, TagID: tags[0].ID,
		Source: customerTagSourceManual, RelationStatus: customerTagRelationInactive,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := TagService.DeleteTagAs(tags[0].ID, admin); err == nil {
		t.Fatal("tag referenced by customer history must not be deleted")
	}
	if TagService.Get(tags[0].ID) == nil {
		t.Fatal("referenced tag was deleted")
	}
}

func createCustomerTagTestScope(t *testing.T, db *gorm.DB) (*customerTagScope, *models.Tag) {
	t.Helper()
	now := time.Now()
	store := &models.Store{
		Name: "测试门店", StoreCode: "store-test-" + time.Now().Format("150405.000000"),
		CompanyID: 21, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		CustomerID: 31, CustomerName: "测试客户",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	route := &models.ConversationRouteState{
		ConversationID: conversation.ID, StoreID: store.ID,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	relation := &models.StoreCustomerRelation{
		CustomerID: conversation.CustomerID, StoreID: store.ID,
		LastConversationID: conversation.ID, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatal(err)
	}
	parent := &models.Tag{
		CompanyID: 0, Name: "房间偏好", SemanticKey: "category.room_preference", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	return &customerTagScope{
		Conversation: conversation, Route: route, Relation: relation,
		CompanyID: store.CompanyID, StoreID: store.ID,
	}, parent
}

func createCustomerTagTestTag(t *testing.T, db *gorm.DB, parentID int64, name, semanticKey, conflictGroup, scene string, aiEnabled bool) *models.Tag {
	t.Helper()
	now := time.Now()
	item := &models.Tag{
		CompanyID: 0, ParentID: parentID, Name: name, SemanticKey: semanticKey,
		ConflictGroup: conflictGroup, AIEnabled: aiEnabled, ReplyEnabled: true,
		ApplicableScene: scene, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}
