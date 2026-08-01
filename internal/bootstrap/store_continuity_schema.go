package bootstrap

import (
	"fmt"
	"slices"

	"agent-desk/internal/models"

	"gorm.io/gorm"
)

type storeContinuityLegacyIndexSpec struct {
	model         any
	name          string
	legacyFields  []string
	currentFields []string
	currentUnique bool
}

func storeContinuityLegacyIndexSpecs() []storeContinuityLegacyIndexSpec {
	return []storeContinuityLegacyIndexSpec{
		{
			model: &models.StoreStaffBinding{}, name: "idx_t_store_staff_binding_store_id",
			legacyFields: []string{"store_id"}, currentFields: []string{"store_id"}, currentUnique: false,
		},
		{
			model: &models.CustomerIdentity{}, name: "uk_customer_external",
			legacyFields:  []string{"customer_id", "external_source", "external_id"},
			currentFields: []string{"tenant_id", "external_source", "external_id"}, currentUnique: true,
		},
		{
			model: &models.StoreModelCredential{}, name: "uk_store_model_credential",
			legacyFields: []string{"tenant_id", "store_id"}, currentFields: nil, currentUnique: false,
		},
		{
			model: &models.WxWorkCustomerHandoffSetting{}, name: "uk_customer_wxwork_handoff_setting",
			legacyFields: []string{"customer_id", "wx_work_instance_id"}, currentFields: nil, currentUnique: false,
		},
	}
}

func normalizeStoreContinuitySchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("store continuity schema database is nil")
	}
	for _, spec := range storeContinuityLegacyIndexSpecs() {
		if !db.Migrator().HasTable(spec.model) {
			continue
		}
		definition, err := readIndexDefinition(db, spec.model, spec.name)
		if err != nil {
			return fmt.Errorf("inspect store continuity index %s: %w", spec.name, err)
		}
		if !definition.exists {
			continue
		}
		if definition.unique == spec.currentUnique && slices.Equal(definition.fields, spec.currentFields) {
			continue
		}
		if !definition.unique || !slices.Equal(definition.fields, spec.legacyFields) {
			return fmt.Errorf("store continuity index %s has unexpected definition unique=%t fields=%v", spec.name, definition.unique, definition.fields)
		}
		if spec.name == "uk_customer_external" {
			duplicateGroups, err := countCustomerIdentityTenantKeyDuplicates(db)
			if err != nil {
				return err
			}
			if duplicateGroups > 0 {
				return fmt.Errorf("customer identity tenant key has %d duplicate groups; repair them before startup", duplicateGroups)
			}
		}
		if err := db.Migrator().DropIndex(spec.model, spec.name); err != nil {
			return fmt.Errorf("drop verified legacy index %s: %w", spec.name, err)
		}
	}
	if db.Migrator().HasTable(&models.ConversationContinuityLink{}) {
		definition, err := readIndexDefinition(db, &models.ConversationContinuityLink{}, "uk_conversation_continuity_successor")
		if err != nil {
			return fmt.Errorf("inspect conversation continuity successor index: %w", err)
		}
		if definition.exists {
			if !definition.unique || !slices.Equal(definition.fields, []string{"tenant_id", "successor_conversation_id"}) {
				return fmt.Errorf("conversation continuity successor index has unexpected definition unique=%t fields=%v", definition.unique, definition.fields)
			}
		} else {
			duplicateGroups, err := countConversationContinuitySuccessorDuplicates(db)
			if err != nil {
				return err
			}
			if duplicateGroups > 0 {
				return fmt.Errorf("conversation continuity has %d duplicate successor groups; repair them before startup", duplicateGroups)
			}
		}
	}
	return nil
}

func validateStoreContinuityIndexes(db *gorm.DB) error {
	checks := []struct {
		model  any
		name   string
		fields []string
		unique bool
	}{
		{&models.StoreStaffBinding{}, "idx_t_store_staff_binding_store_id", []string{"store_id"}, false},
		{&models.StoreStaffBinding{}, "idx_store_staff_tenant_store", []string{"tenant_id", "store_id"}, false},
		{&models.CustomerIdentity{}, "uk_customer_external", []string{"tenant_id", "external_source", "external_id"}, true},
		{&models.Conversation{}, "uk_conversation_thread_key", []string{"thread_key"}, true},
		{&models.ConversationChannelSession{}, "uk_conversation_channel_session", []string{"conversation_id", "session_no"}, true},
		{&models.ConversationContinuityLink{}, "uk_conversation_continuity_predecessor", []string{"tenant_id", "predecessor_conversation_id"}, true},
		{&models.ConversationContinuityLink{}, "uk_conversation_continuity_successor", []string{"tenant_id", "successor_conversation_id"}, true},
		{&models.WxWorkCustomerHandoffSetting{}, "uk_customer_store_staff_handoff_setting", []string{"tenant_id", "customer_id", "store_staff_binding_id"}, true},
		{&models.StoreModelCredential{}, "uk_store_staff_model_credential", []string{"tenant_id", "store_id", "store_staff_binding_id"}, true},
	}
	for _, check := range checks {
		definition, err := readIndexDefinition(db, check.model, check.name)
		if err != nil {
			return fmt.Errorf("read store continuity index %s: %w", check.name, err)
		}
		if !definition.exists || definition.unique != check.unique || !slices.Equal(definition.fields, check.fields) {
			return fmt.Errorf("store continuity index %s invalid: exists=%t unique=%t fields=%v", check.name, definition.exists, definition.unique, definition.fields)
		}
	}
	return nil
}

func countConversationContinuitySuccessorDuplicates(db *gorm.DB) (int64, error) {
	table, err := tenantUniqueModelTable(db, &models.ConversationContinuityLink{})
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM (SELECT tenant_id, successor_conversation_id FROM %s GROUP BY tenant_id, successor_conversation_id HAVING COUNT(*) > 1) AS duplicate_groups",
		table,
	)
	var count int64
	if err := db.Raw(query).Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("count conversation continuity successor duplicates: %w", err)
	}
	return count, nil
}

func countCustomerIdentityTenantKeyDuplicates(db *gorm.DB) (int64, error) {
	table, err := tenantUniqueModelTable(db, &models.CustomerIdentity{})
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM (SELECT tenant_id, external_source, external_id FROM %s GROUP BY tenant_id, external_source, external_id HAVING COUNT(*) > 1) AS duplicate_groups",
		table,
	)
	var count int64
	if err := db.Raw(query).Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("count customer identity tenant-key duplicates: %w", err)
	}
	return count, nil
}
