package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestWxWorkProtocolRemoteSetupCreatesInternalStoreForCompany(t *testing.T) {
	setupWxWorkProtocolInstanceCompanyTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin"}
	if err := sqls.DB().Create(&models.Company{ID: 11, Name: "测试公司", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create company: %v", err)
	}
	if err := sqls.DB().Create(&models.Channel{ID: 22, ChannelType: enums.ChannelTypeWxWorkProtocol, Name: "协议渠道", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	instance, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID: 22,
		Guid:      "guid-company-setup",
		CompanyID: 11,
	}, operator)
	if err != nil {
		t.Fatalf("CreateRemoteSetupInstance() error = %v", err)
	}
	if instance.CompanyID != 11 {
		t.Fatalf("expected company id on remote setup instance, got %d", instance.CompanyID)
	}
	if err := WxWorkProtocolInstanceService.UpdateRemoteSetup(request.UpdateWxWorkProtocolRemoteSetupRequest{
		Token:                   instance.RemoteSetupToken,
		EmployeeName:            "吴朝伟",
		StoreName:               "丽斯未来酒店测试门店",
		ManagedMode:             constants.StoreManagedModeSemi,
		StoreRoomNotifyEnabled:  true,
		FallbackToHQ:            true,
		ManualTimeoutMinutes:    10,
		AutoAcceptFriendRequest: true,
	}); err != nil {
		t.Fatalf("UpdateRemoteSetup() error = %v", err)
	}
	updated := WxWorkProtocolInstanceService.Get(instance.ID)
	if updated == nil {
		t.Fatalf("expected updated instance")
	}
	if updated.CompanyID != 11 || updated.StoreID <= 0 {
		t.Fatalf("expected company and generated store binding, got company=%d store=%d", updated.CompanyID, updated.StoreID)
	}
	store := StoreService.Get(updated.StoreID)
	if store == nil {
		t.Fatalf("expected generated store")
	}
	if store.CompanyID != 11 || store.Name != "丽斯未来酒店测试门店" {
		t.Fatalf("unexpected generated store: %#v", store)
	}
}

func TestWxWorkProtocolInstanceBackfillCompanyIDFromStore(t *testing.T) {
	setupWxWorkProtocolInstanceCompanyTestDB(t)
	if err := sqls.DB().Create(&models.Store{ID: 7, CompanyID: 5, Name: "门店", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.WxWorkProtocolInstance{ID: 3, Guid: "guid-backfill", StoreID: 7, CompanyID: 0, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := WxWorkProtocolInstanceService.BackfillCompanyIDFromStore(); err != nil {
		t.Fatalf("BackfillCompanyIDFromStore() error = %v", err)
	}
	updated := WxWorkProtocolInstanceService.Get(3)
	if updated == nil || updated.CompanyID != 5 {
		t.Fatalf("expected company id backfilled from store, got %#v", updated)
	}
}

func setupWxWorkProtocolInstanceCompanyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	if err := db.AutoMigrate(
		&models.Company{},
		&models.Store{},
		&models.Channel{},
		&models.WxWorkProtocolInstance{},
		&models.StoreStaffBinding{},
		&models.WxWorkProtocolDevicePoolInstance{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
