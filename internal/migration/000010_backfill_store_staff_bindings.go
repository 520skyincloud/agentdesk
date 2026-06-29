package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(10, "backfill store staff bindings", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			instances := repositories.WxWorkProtocolInstanceRepository.Find(ctx.Tx, sqls.NewCnd().Gt("store_id", 0).Where("status <> ?", enums.StatusDeleted).Asc("id"))
			now := time.Now()
			for i := range instances {
				instance := &instances[i]
				binding := repositories.StoreStaffBindingRepository.Take(ctx.Tx, "store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted)
				if binding == nil {
					companyID := int64(0)
					if store := repositories.StoreRepository.Get(ctx.Tx, instance.StoreID); store != nil {
						companyID = store.CompanyID
					}
					binding = &models.StoreStaffBinding{
						CompanyID:               companyID,
						StoreID:                 instance.StoreID,
						ManagedMode:             constants.StoreManagedModeSemi,
						ServiceHours:            instance.ServiceHours,
						StoreRoomConversationID: instance.StoreRoomConversationID,
						StoreRoomNotifyEnabled:  instance.StoreRoomNotifyEnabled,
						StoreRoomAtList:         instance.StoreRoomAtList,
						FallbackToHQ:            instance.FallbackToHQ,
						ManualTimeoutMinutes:    instance.ManualTimeoutMinutes,
						Status:                  enums.StatusOk,
						AuditFields:             models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserID: constants.SystemAuditUserID, UpdateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName, UpdateUserName: constants.SystemAuditUserName},
					}
					if err := repositories.StoreStaffBindingRepository.Create(ctx.Tx, binding); err != nil {
						return err
					}
				}
				if instance.StoreStaffBindingID != binding.ID {
					if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, instance.ID, map[string]any{"store_staff_binding_id": binding.ID, "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName}); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
}
