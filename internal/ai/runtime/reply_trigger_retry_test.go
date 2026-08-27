package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestReplyTriggerEntrypointsRunWholeTriggerExactlyOnce(t *testing.T) {
	file := parseReplyTriggerServiceSource(t)
	for _, methodName := range []string{"TriggerReplyAsync", "TriggerReplySync"} {
		t.Run(methodName, func(t *testing.T) {
			method := findReplyTriggerMethod(t, file, methodName)
			triggerCalls := 0
			ast.Inspect(method.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isReplyTriggerSelectorCall(call, "TriggerReply") {
					return true
				}
				triggerCalls++
				if nestedFunctionDepth(method.Body, call) > expectedReplyTriggerFunctionDepth(methodName) {
					t.Fatalf("%s must call TriggerReply directly, not through a retry callback", methodName)
				}
				return true
			})
			if triggerCalls != 1 {
				t.Fatalf("%s must execute the whole TriggerReply pipeline exactly once, got %d calls", methodName, triggerCalls)
			}
			if containsReplyTriggerRetryControl(method.Body) {
				t.Fatalf("%s must not wrap TriggerReply in an outer retry loop or retry helper", methodName)
			}
		})
	}
}

func TestReplyTriggerServiceDoesNotRestoreOuterProtocolRetry(t *testing.T) {
	file := parseReplyTriggerServiceSource(t)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name != nil {
			switch function.Name.Name {
			case "triggerReplyWithProtocolRetry", "resolveAsyncReplyAttempt":
				t.Fatalf("outer protocol retry helper %s must remain removed", function.Name.Name)
			}
		}
	}

	forbiddenIdentifiers := map[string]bool{
		"generatedReplyProtocolMaxAttempts":  true,
		"generatedReplyProtocolRetryBackoff": true,
		"IsGeneratedReplyProtocolError":      true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if forbiddenIdentifiers[value.Name] {
				t.Fatalf("outer trigger must not classify or retry Generate protocol failures via %s", value.Name)
			}
		case *ast.SelectorExpr:
			if value.Sel != nil && forbiddenIdentifiers[value.Sel.Name] {
				t.Fatalf("outer trigger must not classify or retry Generate protocol failures via %s", value.Sel.Name)
			}
		}
		return true
	})
}

func parseReplyTriggerServiceSource(t *testing.T) *ast.File {
	t.Helper()
	_, currentFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("resolve current test source path")
	}
	path := filepath.Join(filepath.Dir(currentFile), "reply_trigger_service.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func findReplyTriggerMethod(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name == nil || function.Name.Name != name {
			continue
		}
		return function
	}
	t.Fatalf("reply trigger method %s not found", name)
	return nil
}

func isReplyTriggerSelectorCall(call *ast.CallExpr, selectorName string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == selectorName
}

func nestedFunctionDepth(root ast.Node, target ast.Node) int {
	depth := 0
	found := false
	var visit func(ast.Node, int)
	visit = func(node ast.Node, functionDepth int) {
		if node == nil || found {
			return
		}
		if node == target {
			depth = functionDepth
			found = true
			return
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil || child == node || found {
				return !found
			}
			nextDepth := functionDepth
			if _, ok := child.(*ast.FuncLit); ok {
				nextDepth++
			}
			visit(child, nextDepth)
			return false
		})
	}
	visit(root, 0)
	return depth
}

func expectedReplyTriggerFunctionDepth(methodName string) int {
	if methodName == "TriggerReplyAsync" {
		return 1
	}
	return 0
}

func containsReplyTriggerRetryControl(body *ast.BlockStmt) bool {
	retryControl := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			retryControl = true
			return false
		case *ast.CallExpr:
			name := replyTriggerCallName(value)
			if strings.Contains(strings.ToLower(name), "retry") {
				retryControl = true
				return false
			}
		}
		return !retryControl
	})
	return retryControl
}

func replyTriggerCallName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		if function.Sel != nil {
			return function.Sel.Name
		}
	}
	return ""
}
