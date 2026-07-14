package dashboard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

type dashboardHandlerPermissionNode struct {
	isGinHandler       bool
	hasPermissionCheck bool
	hasAuthentication  bool
	localCalls         []string
}

func TestDashboardHandlersHaveExplicitPermissionContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve permission contract test path")
	}
	handlerFiles, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*_handler.go"))
	if err != nil {
		t.Fatalf("list dashboard handler files: %v", err)
	}

	nodes := make(map[string]*dashboardHandlerPermissionNode)
	fileset := token.NewFileSet()
	for _, filename := range handlerFiles {
		file, parseErr := parser.ParseFile(fileset, filename, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", filepath.Base(filename), parseErr)
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			node := &dashboardHandlerPermissionNode{
				isGinHandler: ast.IsExported(function.Name.Name) && acceptsGinContext(function.Type),
			}
			ast.Inspect(function.Body, func(candidate ast.Node) bool {
				call, isCall := candidate.(*ast.CallExpr)
				if !isCall {
					return true
				}
				switch target := call.Fun.(type) {
				case *ast.Ident:
					node.localCalls = append(node.localCalls, target.Name)
				case *ast.SelectorExpr:
					switch target.Sel.Name {
					case "RequirePermission", "HasPermission":
						node.hasPermissionCheck = true
					case "Authenticate":
						node.hasAuthentication = true
					}
				}
				return true
			})
			nodes[function.Name.Name] = node
		}
	}

	authenticatedSelfService := map[string]struct{}{
		"UserPostChange_password": {},
	}
	memo := make(map[string]bool)
	missing := make([]string, 0)
	for name, node := range nodes {
		if !node.isGinHandler || resolvesPermissionCheck(name, nodes, memo, make(map[string]bool)) {
			continue
		}
		if _, allowed := authenticatedSelfService[name]; allowed && node.hasAuthentication {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("dashboard handlers lack an explicit permission contract: %v", missing)
	}
}

func acceptsGinContext(functionType *ast.FuncType) bool {
	if functionType == nil || functionType.Params == nil {
		return false
	}
	for _, parameter := range functionType.Params.List {
		pointer, isPointer := parameter.Type.(*ast.StarExpr)
		if !isPointer {
			continue
		}
		selector, isSelector := pointer.X.(*ast.SelectorExpr)
		if !isSelector || selector.Sel.Name != "Context" {
			continue
		}
		packageName, isIdentifier := selector.X.(*ast.Ident)
		if isIdentifier && packageName.Name == "gin" {
			return true
		}
	}
	return false
}

func resolvesPermissionCheck(
	name string,
	nodes map[string]*dashboardHandlerPermissionNode,
	memo map[string]bool,
	visiting map[string]bool,
) bool {
	if value, exists := memo[name]; exists {
		return value
	}
	if visiting[name] {
		return false
	}
	node := nodes[name]
	if node == nil {
		return false
	}
	if node.hasPermissionCheck {
		memo[name] = true
		return true
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, dependency := range node.localCalls {
		if resolvesPermissionCheck(dependency, nodes, memo, visiting) {
			memo[name] = true
			return true
		}
	}
	memo[name] = false
	return false
}
