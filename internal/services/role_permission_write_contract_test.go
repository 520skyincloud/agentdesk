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

var rolePermissionTablePattern = regexp.MustCompile(`\bt_role_permission\b`)

func TestRolePermissionRuntimeWritesStayBehindAuditedRoleService(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve role permission write contract test path")
	}
	serviceFiles, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("list service files: %v", err)
	}
	allowed := map[string]map[string]struct{}{
		"role_service.go": {
			"replaceRolePermissionsDB": {},
		},
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
				if !isCall || !isRolePermissionMutationCall(call) {
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
		t.Fatalf("RolePermission runtime writes bypass audited role service: %v", violations)
	}
}

func TestIsRolePermissionMutationCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{name: "repository create", expression: "repositories.RolePermissionRepository.Create(db, item)", want: true},
		{name: "repository delete by role", expression: "repositories.RolePermissionRepository.DeleteByRoleID(db, roleID)", want: true},
		{name: "service update", expression: "RolePermissionService.Update(item)", want: true},
		{name: "gorm create", expression: "db.Create(&models.RolePermission{})", want: true},
		{name: "gorm delete chain", expression: "db.Where(\"role_id = ?\", id).Delete(&models.RolePermission{})", want: true},
		{name: "gorm model update chain", expression: "db.Model(&models.RolePermission{}).Where(\"role_id = ?\", id).Updates(values)", want: true},
		{name: "raw SQL", expression: "db.Exec(\"DELETE FROM t_role_permission WHERE role_id = ?\", id)", want: true},
		{name: "repository read", expression: "repositories.RolePermissionRepository.Find(db, cnd)", want: false},
		{name: "service read", expression: "RolePermissionService.Find(cnd)", want: false},
		{name: "change log write", expression: "db.Create(&models.RolePermissionChangeLog{})", want: false},
		{name: "change log raw SQL", expression: "db.Exec(\"DELETE FROM t_role_permission_change_log WHERE id = ?\", id)", want: false},
		{name: "other model write", expression: "db.Create(&models.Permission{})", want: false},
	}
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
			if got := isRolePermissionMutationCall(call); got != tt.want {
				t.Fatalf("isRolePermissionMutationCall(%q) = %v, want %v", tt.expression, got, tt.want)
			}
		})
	}
}

func isRolePermissionMutationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	writeMethod := map[string]struct{}{
		"Create": {}, "Save": {}, "Update": {}, "Updates": {}, "UpdateColumn": {}, "Delete": {}, "DeleteByRoleID": {}, "Exec": {},
	}
	if _, ok := writeMethod[selector.Sel.Name]; !ok {
		return false
	}
	if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "RolePermissionRepository" {
		return true
	}
	if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "RolePermissionService" {
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
			if isPackage && packageName.Name == "models" && item.Sel.Name == "RolePermission" {
				found = true
			}
		case *ast.BasicLit:
			if item.Kind != token.STRING {
				break
			}
			value, err := strconv.Unquote(item.Value)
			if err == nil && rolePermissionTablePattern.MatchString(strings.ToLower(value)) {
				found = true
			}
		}
		return !found
	})
	return found
}
