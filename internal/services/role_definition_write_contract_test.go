package services

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	roleTablePattern       = regexp.MustCompile(`\bt_role\b`)
	permissionTablePattern = regexp.MustCompile(`\bt_permission\b`)
)

func TestRoleDefinitionRuntimeWritesStayBehindDomainService(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]struct{}{
		"role_service.go": {
			"CreateRole":   {},
			"UpdateRole":   {},
			"UpdateSort":   {},
			"DeleteRole":   {},
			"UpdateStatus": {},
		},
	}
	assertRuntimeWritesStayBehindAllowedFunctions(t, "Role", isRoleDefinitionMutationCall, allowed)
}

func TestPermissionDefinitionsRemainMigrationOwned(t *testing.T) {
	t.Parallel()
	assertRuntimeWritesStayBehindAllowedFunctions(t, "Permission", isPermissionDefinitionMutationCall, nil)
}

func TestIsRoleDefinitionMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.RoleRepository.Create(db, item)", want: true},
		{name: "service update", expression: "RoleService.Update(item)", want: true},
		{name: "gorm create", expression: "db.Create(&models.Role{})", want: true},
		{name: "gorm delete", expression: "db.Delete(&models.Role{}, id)", want: true},
		{name: "gorm model update", expression: "db.Model(&models.Role{}).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"DELETE FROM t_role WHERE id = ?\", id)", want: true},
		{name: "repository read", expression: "repositories.RoleRepository.Get(db, id)", want: false},
		{name: "domain service call", expression: "RoleService.UpdateRole(req, operator)", want: false},
		{name: "role permission write", expression: "db.Create(&models.RolePermission{})", want: false},
		{name: "role permission SQL", expression: "db.Exec(\"DELETE FROM t_role_permission WHERE role_id = ?\", id)", want: false},
	}
	assertMutationDetectorCases(t, tests, isRoleDefinitionMutationCall)
}

func TestIsPermissionDefinitionMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.PermissionRepository.Create(db, item)", want: true},
		{name: "service update", expression: "PermissionService.Update(item)", want: true},
		{name: "gorm create", expression: "db.Create(&models.Permission{})", want: true},
		{name: "gorm delete", expression: "db.Delete(&models.Permission{}, id)", want: true},
		{name: "gorm model update", expression: "db.Model(&models.Permission{}).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"UPDATE t_permission SET status = ?\", status)", want: true},
		{name: "repository read", expression: "repositories.PermissionRepository.Get(db, id)", want: false},
		{name: "service read", expression: "PermissionService.Find(cnd)", want: false},
		{name: "role permission write", expression: "db.Create(&models.RolePermission{})", want: false},
		{name: "role permission SQL", expression: "db.Exec(\"DELETE FROM t_role_permission WHERE permission_id = ?\", id)", want: false},
	}
	assertMutationDetectorCases(t, tests, isPermissionDefinitionMutationCall)
}

func assertRuntimeWritesStayBehindAllowedFunctions(
	t *testing.T,
	label string,
	detector func(*ast.CallExpr) bool,
	allowed map[string]map[string]struct{},
) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve %s write contract test path", label)
	}
	serviceFiles, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("list service files: %v", err)
	}

	violations := make([]string, 0)
	fileset := token.NewFileSet()
	for _, filename := range serviceFiles {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileset, filename, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", filepath.Base(filename), parseErr)
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(candidate ast.Node) bool {
				call, isCall := candidate.(*ast.CallExpr)
				if !isCall || !detector(call) {
					return true
				}
				base := filepath.Base(filename)
				if functions := allowed[base]; functions != nil {
					if _, exists := functions[function.Name.Name]; exists {
						return true
					}
				}
				position := fileset.Position(call.Pos())
				violations = append(violations, fmt.Sprintf("%s:%s:%d", base, function.Name.Name, position.Line))
				return true
			})
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("%s runtime writes bypass the approved ownership boundary: %v", label, violations)
	}
}

func assertMutationDetectorCases(
	t *testing.T,
	tests []struct {
		name       string
		expression string
		want       bool
	},
	detector func(*ast.CallExpr) bool,
) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expression, err := parser.ParseExpr(tt.expression)
			if err != nil {
				t.Fatalf("parse expression %q: %v", tt.expression, err)
			}
			call, ok := expression.(*ast.CallExpr)
			if !ok {
				t.Fatalf("expression %q is not a call", tt.expression)
			}
			if got := detector(call); got != tt.want {
				t.Fatalf("detector(%q) = %v, want %v", tt.expression, got, tt.want)
			}
		})
	}
}

func isRoleDefinitionMutationCall(call *ast.CallExpr) bool {
	return isDefinitionMutationCall(call, "RoleRepository", "RoleService", "Role", roleTablePattern)
}

func isPermissionDefinitionMutationCall(call *ast.CallExpr) bool {
	return isDefinitionMutationCall(call, "PermissionRepository", "PermissionService", "Permission", permissionTablePattern)
}

func isDefinitionMutationCall(
	call *ast.CallExpr,
	repositoryName string,
	serviceName string,
	modelName string,
	tablePattern *regexp.Regexp,
) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	writeMethods := map[string]struct{}{
		"Create": {}, "Save": {}, "Update": {}, "Updates": {}, "UpdateColumn": {}, "Delete": {}, "Exec": {},
	}
	if _, exists := writeMethods[selector.Sel.Name]; !exists {
		return false
	}
	if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == repositoryName {
		return true
	}
	if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == serviceName {
		return true
	}
	found := false
	ast.Inspect(call, func(candidate ast.Node) bool {
		if found {
			return false
		}
		switch item := candidate.(type) {
		case *ast.SelectorExpr:
			packageName, isPackage := item.X.(*ast.Ident)
			if isPackage && packageName.Name == "models" && item.Sel.Name == modelName {
				found = true
			}
		case *ast.BasicLit:
			if item.Kind != token.STRING {
				break
			}
			value, err := strconv.Unquote(item.Value)
			if err == nil && tablePattern.MatchString(strings.ToLower(value)) {
				found = true
			}
		}
		return !found
	})
	return found
}
