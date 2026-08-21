package services

import (
	"fmt"
	"reflect"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestReplyRuntimeGeneralKnowledgeServiceAppendsValidSameStoreFastGPT(t *testing.T) {
	db := setupReplyRuntimeGeneralKnowledgeTestDB(t)
	seedReplyRuntimeKnowledgeBase(t, db, models.KnowledgeBase{ID: 11, StoreID: 1, Status: enums.StatusOk})
	seedReplyRuntimeKnowledgeBase(t, db, models.KnowledgeBase{
		ID: 12, StoreID: 1, Status: enums.StatusOk, DatasetID: "general-dataset",
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
	})
	seedReplyRuntimeGeneralKnowledgeConfig(t, db, `{"1":"12"}`, enums.StatusOk)

	got := ReplyRuntimeGeneralKnowledgeService.ResolveKnowledgeBaseIDs([]int64{0, 999, 11, 11})
	want := []int64{999, 11, 12}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("knowledge base ids=%v, want %v", got, want)
	}
}

func TestReplyRuntimeGeneralKnowledgeServiceAcceptsNumericMapping(t *testing.T) {
	db := setupReplyRuntimeGeneralKnowledgeTestDB(t)
	seedReplyRuntimeKnowledgeBase(t, db, models.KnowledgeBase{ID: 21, StoreID: 2, Status: enums.StatusOk})
	seedReplyRuntimeKnowledgeBase(t, db, models.KnowledgeBase{
		ID: 22, StoreID: 2, Status: enums.StatusOk, DatasetID: "general-dataset",
		ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT),
	})
	seedReplyRuntimeGeneralKnowledgeConfig(t, db, `{"2":22}`, enums.StatusOk)

	got := ReplyRuntimeGeneralKnowledgeService.ResolveKnowledgeBaseIDs([]int64{21})
	want := []int64{21, 22}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("knowledge base ids=%v, want %v", got, want)
	}
}

func TestReplyRuntimeGeneralKnowledgeServiceRejectsInvalidMappings(t *testing.T) {
	tests := []struct {
		name        string
		configValue string
		configState enums.Status
		general     models.KnowledgeBase
		boundIDs    []int64
	}{
		{
			name: "invalid json", configValue: `{`, configState: enums.StatusOk,
			general: validReplyRuntimeGeneralKnowledgeBase(32, 3), boundIDs: []int64{31},
		},
		{
			name: "disabled config", configValue: `{"3":32}`, configState: enums.StatusDisabled,
			general: validReplyRuntimeGeneralKnowledgeBase(32, 3), boundIDs: []int64{31},
		},
		{
			name: "cross store", configValue: `{"3":32}`, configState: enums.StatusOk,
			general: validReplyRuntimeGeneralKnowledgeBase(32, 4), boundIDs: []int64{31},
		},
		{
			name: "disabled knowledge base", configValue: `{"3":32}`, configState: enums.StatusOk,
			general: func() models.KnowledgeBase {
				item := validReplyRuntimeGeneralKnowledgeBase(32, 3)
				item.Status = enums.StatusDisabled
				return item
			}(), boundIDs: []int64{31},
		},
		{
			name: "not fastgpt", configValue: `{"3":32}`, configState: enums.StatusOk,
			general: models.KnowledgeBase{ID: 32, StoreID: 3, Status: enums.StatusOk, DatasetID: "dataset"}, boundIDs: []int64{31},
		},
		{
			name: "missing dataset", configValue: `{"3":32}`, configState: enums.StatusOk,
			general: models.KnowledgeBase{ID: 32, StoreID: 3, Status: enums.StatusOk, KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud)}, boundIDs: []int64{31},
		},
		{
			name: "already bound", configValue: `{"3":31}`, configState: enums.StatusOk,
			general: validReplyRuntimeGeneralKnowledgeBase(32, 3), boundIDs: []int64{31, 31},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupReplyRuntimeGeneralKnowledgeTestDB(t)
			seedReplyRuntimeKnowledgeBase(t, db, models.KnowledgeBase{ID: 31, StoreID: 3, Status: enums.StatusOk})
			seedReplyRuntimeKnowledgeBase(t, db, tt.general)
			seedReplyRuntimeGeneralKnowledgeConfig(t, db, tt.configValue, tt.configState)

			got := ReplyRuntimeGeneralKnowledgeService.ResolveKnowledgeBaseIDs(tt.boundIDs)
			if !reflect.DeepEqual(got, []int64{31}) {
				t.Fatalf("knowledge base ids=%v, want [31]", got)
			}
		})
	}
}

func validReplyRuntimeGeneralKnowledgeBase(id, storeID int64) models.KnowledgeBase {
	return models.KnowledgeBase{
		ID: id, StoreID: storeID, Status: enums.StatusOk, DatasetID: "general-dataset",
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
	}
}

func setupReplyRuntimeGeneralKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.KnowledgeBase{}, &models.SystemConfig{}); err != nil {
		t.Fatalf("migrate reply runtime general knowledge tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func seedReplyRuntimeKnowledgeBase(t *testing.T, db *gorm.DB, item models.KnowledgeBase) {
	t.Helper()
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed knowledge base %d: %v", item.ID, err)
	}
}

func seedReplyRuntimeGeneralKnowledgeConfig(t *testing.T, db *gorm.DB, value string, status enums.Status) {
	t.Helper()
	if err := db.Create(&models.SystemConfig{
		ConfigKey: replyRuntimeGeneralKnowledgeBaseByStoreConfigKey, ConfigValue: value, Status: status,
	}).Error; err != nil {
		t.Fatalf("seed general knowledge config: %v", err)
	}
}
