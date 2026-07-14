package migration

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestValidateMigrationDefinitionRejectsReusedVersion(t *testing.T) {
	stored := models.Migration{Version: 25, Remark: "backfill wxwork protocol instance agent team bindings", Success: true}
	current := MigrationFunc{Version: 25, Remark: "refresh default hotel intent profile human route rules"}

	err := validateMigrationDefinition(stored, current)
	if err == nil || !strings.Contains(err.Error(), "definition mismatch") {
		t.Fatalf("validateMigrationDefinition() error=%v want definition mismatch", err)
	}
}

func TestValidateMigrationDefinitionAcceptsMatchingIdentity(t *testing.T) {
	stored := models.Migration{Version: 39, Remark: "backfill agent organization tenants", Success: true}
	current := MigrationFunc{Version: 39, Remark: "backfill agent organization tenants"}

	if err := validateMigrationDefinition(stored, current); err != nil {
		t.Fatalf("validateMigrationDefinition() error=%v", err)
	}
}
