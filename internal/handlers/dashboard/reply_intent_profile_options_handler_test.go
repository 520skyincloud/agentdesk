package dashboard

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestReplyIntentProfileOptionsIncludeDraftAndDisabledProfiles(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.ReplyIntentProfile{}); err != nil {
		t.Fatalf("migrate reply intent profile: %v", err)
	}
	profiles := []models.ReplyIntentProfile{
		{ID: 11, Code: "disabled", IndustryCode: "hotel", Name: "停用行业", Revision: 3, Status: enums.StatusDisabled, SortNo: 20},
		{ID: 12, Code: "draft", IndustryCode: "retail", Name: "草稿行业", Revision: 1, Status: enums.StatusOk, SortNo: 10},
		{ID: 13, Code: "deleted", IndustryCode: "legacy", Name: "已删除行业", Revision: 4, Status: enums.StatusDeleted, SortNo: 0},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("seed reply intent profiles: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:            301,
		Username:          "platform_ai_config_viewer",
		IsPlatformAccount: true,
		Permissions:       []string{constants.PermissionAIConfigView.Code},
	})

	ReplyIntentConfigGetProfileOptions(ctx)

	var result struct {
		Success   bool `json:"success"`
		ErrorCode int  `json:"errorCode"`
		Data      []struct {
			ID           int64        `json:"id"`
			Code         string       `json:"code"`
			IndustryCode string       `json:"industryCode"`
			Name         string       `json:"name"`
			Revision     int64        `json:"revision"`
			Status       enums.Status `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if !result.Success || result.ErrorCode != 0 {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
	if len(result.Data) != 2 {
		t.Fatalf("profile option count = %d, want 2; response=%s", len(result.Data), recorder.Body.String())
	}
	if result.Data[0].ID != 12 || result.Data[1].ID != 11 {
		t.Fatalf("profile option order = [%d, %d], want [12, 11]", result.Data[0].ID, result.Data[1].ID)
	}
	if result.Data[1].Status != enums.StatusDisabled || result.Data[1].Revision != 3 {
		t.Fatalf("disabled profile metadata missing: %+v", result.Data[1])
	}
}

func TestReplyIntentProfileOptionsRejectTenantAccount(t *testing.T) {
	ctx, recorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID:            302,
		TenantID:          9,
		ActiveTenantID:    9,
		Username:          "tenant_ai_config_viewer",
		IsPlatformAccount: false,
		Permissions:       []string{constants.PermissionAIConfigView.Code},
	})

	ReplyIntentConfigGetProfileOptions(ctx)
	assertAuthzErrorCode(t, recorder, errorsx.CodeAuthForbidden)
}
