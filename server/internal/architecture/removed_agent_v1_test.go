package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRemovedAgentRESTV1IsNotReferencedByProductionCode(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	internalRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), ".."))

	err := filepath.WalkDir(
		internalRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() ||
				filepath.Ext(path) != ".go" ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}

			parsed, err := parser.ParseFile(
				token.NewFileSet(),
				path,
				nil,
				0,
			)
			if err != nil {
				t.Errorf("%s: parse source: %v", path, err)
				return nil
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Errorf("%s: parse string literal: %v", path, err)
					return true
				}
				if strings.Contains(value, "/api/v1") {
					t.Errorf(
						"%s: production code references removed Agent REST v1 path %q",
						path,
						value,
					)
				}
				return true
			})
			return nil
		},
	)
	if err != nil {
		t.Fatalf("walk internal production code: %v", err)
	}
}
