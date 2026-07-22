package services

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSelectReplyTagCandidatesRequiresEnabledStorePolicyAndTenantScope(t *testing.T) {
	db := setupReplyTagCandidateTestDB(t)
	conversation := models.Conversation{ID: 71, TenantID: 101, CustomerID: 81, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: 71, StoreID: 91}).Error; err != nil {
		t.Fatal(err)
	}
	storeRelation := models.StoreCustomerRelation{ID: 61, TenantID: 101, CustomerID: 81, StoreID: 91, Status: enums.StatusOk}
	if err := db.Create(&storeRelation).Error; err != nil {
		t.Fatal(err)
	}
	templateID := int64(501)
	tag := models.Tag{
		ID: 41, TenantID: 101, IntentProfileID: 11, TemplateDefinitionID: &templateID,
		Name: "喜静", SemanticKey: "room.quiet", ConflictGroup: "room_noise",
		ApplicableScene: "room_assignment", ReplyEnabled: true, SystemDefined: true, Status: enums.StatusOk,
	}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.CustomerTagRelation{
		TenantID: 101, StoreID: 91, CustomerID: 81, StoreCustomerRelationID: 61,
		TagID: 41, Source: "manual", RelationStatus: "active", ManualProtected: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := CustomerTagService.SelectReplyTagCandidates(71, []string{"room_assignment"}, "帮我安排一个合适房间")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("reply tag context must default closed: %#v", candidates)
	}
	if err := db.Create(&models.StoreCustomerTagRuntimePolicy{
		TenantID: 101, StoreID: 91, ReplyTagContextEnabled: true, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
	candidates, err = CustomerTagService.SelectReplyTagCandidates(71, []string{"room_assignment"}, "帮我安排一个合适房间")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].TagID != 41 || candidates[0].SemanticKey != "room.quiet" {
		t.Fatalf("unexpected enabled candidates: %#v", candidates)
	}

	if err := db.Create(&models.CustomerTagRelation{
		TenantID: 202, StoreID: 191, CustomerID: 181, StoreCustomerRelationID: 161,
		TagID: 141, Source: "manual", RelationStatus: "active", ManualProtected: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	candidates, err = CustomerTagService.SelectReplyTagCandidates(71, []string{"room_assignment"}, "我明确想要喜静房间")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("current-turn tag mention must suppress stale context: %#v", candidates)
	}
}

func setupReplyTagCandidateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Conversation{}, &models.ConversationRouteState{}, &models.StoreCustomerRelation{},
		&models.StoreCustomerTagRuntimePolicy{}, &models.CustomerTagRelation{}, &models.Tag{},
	); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}
