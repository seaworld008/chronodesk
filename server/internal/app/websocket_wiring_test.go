package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestProductionWebSocketMarkReadUsesAtomicDatabaseHandler(t *testing.T) {
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"app.go",
		nil,
		parser.SkipObjectResolution,
	)
	if err != nil {
		t.Fatalf("parse app.go: %v", err)
	}

	var setter *ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || selectorName(call.Fun) !=
			"SetNotificationReadHandler" {
			return true
		}
		setter = call
		return false
	})
	if setter == nil {
		t.Fatal("production app does not configure WebSocket mark_read")
	}
	if len(setter.Args) != 1 {
		t.Fatalf(
			"SetNotificationReadHandler arguments = %d, want 1",
			len(setter.Args),
		)
	}
	constructor, ok := setter.Args[0].(*ast.CallExpr)
	if !ok || selectorName(constructor.Fun) !=
		"NewDatabaseNotificationReadHandler" {
		t.Fatal(
			"production WebSocket mark_read is not wired to the atomic database handler",
		)
	}
	if len(constructor.Args) != 4 {
		t.Fatalf(
			"atomic WebSocket mark_read dependencies = %d, want database, operation context, authorization, and store",
			len(constructor.Args),
		)
	}

	requiredCalls := map[string]bool{
		"WithOperationContext":         false,
		"RevalidateHumanProjectAccess": false,
	}
	for _, argument := range constructor.Args {
		ast.Inspect(argument, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, required := requiredCalls[selectorName(call.Fun)]; required {
				requiredCalls[selectorName(call.Fun)] = true
			}
			return true
		})
	}
	for required, found := range requiredCalls {
		if !found {
			t.Fatalf(
				"atomic WebSocket mark_read wiring is missing %s",
				required,
			)
		}
	}
}

func TestProductionAccessRevocationsUseCommittedOutboxAndHub(t *testing.T) {
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"app.go",
		nil,
		parser.SkipObjectResolution,
	)
	if err != nil {
		t.Fatalf("parse app.go: %v", err)
	}

	foundAdminOutbox := false
	foundHubConsumer := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch selectorName(call.Fun) {
		case "NewAdminUserServiceWithAccessRevocationOutbox":
			foundAdminOutbox = true
		case "NewNativeOutboxDeliverer":
			if len(call.Args) != 1 {
				return true
			}
			literal, ok := call.Args[0].(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, keyOK := field.Key.(*ast.Ident)
				value, valueOK := field.Value.(*ast.Ident)
				if keyOK && valueOK &&
					key.Name == "AccessRevocations" &&
					value.Name == "wsHub" {
					foundHubConsumer = true
				}
			}
		}
		return true
	})
	if !foundAdminOutbox {
		t.Fatal(
			"production admin user mutations do not require access-revocation Outbox",
		)
	}
	if !foundHubConsumer {
		t.Fatal(
			"production Outbox does not route committed access revocations to wsHub",
		)
	}
}

func selectorName(expression ast.Expr) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil {
		return ""
	}
	return selector.Sel.Name
}
