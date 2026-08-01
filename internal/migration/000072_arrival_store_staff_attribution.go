package migration

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func backfillArrivalStoreStaffAttribution(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("arrival Store staff attribution database is nil")
	}
	if err := backfillArrivalConnections(tx); err != nil {
		return err
	}
	if err := backfillArrivalBindings(tx); err != nil {
		return err
	}
	if err := backfillArrivalBindingTickets(tx); err != nil {
		return err
	}
	return validateArrivalStoreStaffAttribution(tx)
}

func backfillArrivalConnections(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.StoreArrivalConnection{}) {
		return nil
	}
	var connections []models.StoreArrivalConnection
	if err := tx.Order("id ASC").Find(&connections).Error; err != nil {
		return fmt.Errorf("load arrival connections: %w", err)
	}
	for i := range connections {
		connection := &connections[i]
		required := connection.StoreStaffBindingID > 0 || connection.WxWorkProtocolInstanceID > 0 ||
			strings.TrimSpace(connection.ContactMemberFingerprint) != "" ||
			connection.ConnectionStatus == enums.ArrivalConnectionStatusActive
		bindingID, err := resolveArrivalStoreStaffBindingID(
			tx,
			connection.TenantID,
			connection.StoreID,
			connection.StoreStaffBindingID,
			connection.WxWorkProtocolInstanceID,
			0,
			fmt.Sprintf("arrival connection %d", connection.ID),
			required,
		)
		if err != nil {
			return err
		}
		if bindingID > 0 && connection.StoreStaffBindingID != bindingID {
			if err := updateArrivalStoreStaffBindingID(tx, &models.StoreArrivalConnection{}, connection.ID, bindingID); err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillArrivalBindings(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.ArrivalStoreBinding{}) {
		return nil
	}
	var bindings []models.ArrivalStoreBinding
	if err := tx.Order("id ASC").Find(&bindings).Error; err != nil {
		return fmt.Errorf("load arrival Store bindings: %w", err)
	}
	for i := range bindings {
		binding := &bindings[i]
		bindingID, err := resolveArrivalStoreStaffBindingID(
			tx,
			binding.TenantID,
			binding.StoreID,
			binding.StoreStaffBindingID,
			binding.WxWorkProtocolInstanceID,
			binding.ConversationID,
			fmt.Sprintf("arrival Store binding %d", binding.ID),
			true,
		)
		if err != nil {
			return err
		}
		if binding.StoreStaffBindingID != bindingID {
			if err := updateArrivalStoreStaffBindingID(tx, &models.ArrivalStoreBinding{}, binding.ID, bindingID); err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillArrivalBindingTickets(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.ArrivalBindingTicket{}) {
		return nil
	}
	var tickets []models.ArrivalBindingTicket
	if err := tx.Order("id ASC").Find(&tickets).Error; err != nil {
		return fmt.Errorf("load arrival binding tickets: %w", err)
	}
	for i := range tickets {
		ticket := &tickets[i]
		bindingID, err := resolveArrivalStoreStaffBindingID(
			tx,
			ticket.TenantID,
			ticket.StoreID,
			ticket.StoreStaffBindingID,
			ticket.WxWorkProtocolInstanceID,
			ticket.ConversationID,
			fmt.Sprintf("arrival binding ticket %d", ticket.ID),
			true,
		)
		if err != nil {
			return err
		}
		if ticket.StoreStaffBindingID != bindingID {
			if err := updateArrivalStoreStaffBindingID(tx, &models.ArrivalBindingTicket{}, ticket.ID, bindingID); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveArrivalStoreStaffBindingID(
	tx *gorm.DB,
	tenantID, storeID, currentBindingID, instanceID, conversationID int64,
	context string,
	required bool,
) (int64, error) {
	evidence := positiveMigrationIDs(currentBindingID)
	if instanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(tx, instanceID, tenantID)
		if instance == nil || instance.StoreID != storeID || instance.StoreStaffBindingID <= 0 {
			return 0, fmt.Errorf("%s references an invalid protocol instance", context)
		}
		evidence = positiveMigrationIDs(append(evidence, instance.StoreStaffBindingID)...)
	}
	if conversationID > 0 {
		conversation := repositories.ConversationRepository.GetInTenant(tx, conversationID, tenantID)
		if conversation == nil || conversation.StoreID != storeID || conversation.StoreStaffBindingID <= 0 {
			return 0, fmt.Errorf("%s references an invalid Store conversation", context)
		}
		evidence = positiveMigrationIDs(append(evidence, conversation.StoreStaffBindingID)...)
	}
	if len(evidence) == 0 {
		if required {
			return 0, fmt.Errorf("%s has no deterministic Store staff binding", context)
		}
		return 0, nil
	}
	if len(evidence) > 1 {
		return 0, fmt.Errorf("%s has conflicting Store staff binding evidence", context)
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(tx, evidence[0], tenantID)
	if binding == nil || binding.Status == enums.StatusDeleted || binding.StoreID != storeID {
		return 0, fmt.Errorf("%s resolves to an invalid Store staff binding", context)
	}
	return binding.ID, nil
}

func updateArrivalStoreStaffBindingID(tx *gorm.DB, model any, id, bindingID int64) error {
	if err := tx.Model(model).Where("id = ?", id).Updates(map[string]any{
		"store_staff_binding_id": bindingID,
		"updated_at":             time.Now(),
		"update_user_id":         constants.SystemAuditUserID,
		"update_user_name":       constants.SystemAuditUserName,
	}).Error; err != nil {
		return fmt.Errorf("backfill arrival Store staff attribution for row %d: %w", id, err)
	}
	return nil
}

func validateArrivalStoreStaffAttribution(tx *gorm.DB) error {
	var connections []models.StoreArrivalConnection
	if tx.Migrator().HasTable(&models.StoreArrivalConnection{}) {
		if err := tx.Order("id ASC").Find(&connections).Error; err != nil {
			return err
		}
	}
	for i := range connections {
		connection := &connections[i]
		required := connection.WxWorkProtocolInstanceID > 0 || strings.TrimSpace(connection.ContactMemberFingerprint) != "" ||
			connection.ConnectionStatus == enums.ArrivalConnectionStatusActive
		if required && connection.StoreStaffBindingID <= 0 {
			return fmt.Errorf("arrival connection %d has no Store staff binding", connection.ID)
		}
		if connection.StoreStaffBindingID > 0 {
			if _, err := resolveArrivalStoreStaffBindingID(tx, connection.TenantID, connection.StoreID, connection.StoreStaffBindingID, connection.WxWorkProtocolInstanceID, 0, fmt.Sprintf("arrival connection %d", connection.ID), true); err != nil {
				return err
			}
		}
	}
	var bindings []models.ArrivalStoreBinding
	if tx.Migrator().HasTable(&models.ArrivalStoreBinding{}) {
		if err := tx.Order("id ASC").Find(&bindings).Error; err != nil {
			return err
		}
	}
	for i := range bindings {
		binding := &bindings[i]
		if _, err := resolveArrivalStoreStaffBindingID(tx, binding.TenantID, binding.StoreID, binding.StoreStaffBindingID, binding.WxWorkProtocolInstanceID, binding.ConversationID, fmt.Sprintf("arrival Store binding %d", binding.ID), true); err != nil {
			return err
		}
	}
	var tickets []models.ArrivalBindingTicket
	if tx.Migrator().HasTable(&models.ArrivalBindingTicket{}) {
		if err := tx.Order("id ASC").Find(&tickets).Error; err != nil {
			return err
		}
	}
	for i := range tickets {
		ticket := &tickets[i]
		if _, err := resolveArrivalStoreStaffBindingID(tx, ticket.TenantID, ticket.StoreID, ticket.StoreStaffBindingID, ticket.WxWorkProtocolInstanceID, ticket.ConversationID, fmt.Sprintf("arrival binding ticket %d", ticket.ID), true); err != nil {
			return err
		}
	}
	return nil
}
