package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/seaworld008/chronodesk/server"

func TestModuleDependencyRules(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	internalRoot := filepath.Join(moduleRoot, "internal")

	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		relativeFile, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		sourceModule := filepath.ToSlash(filepath.Dir(relativeFile))

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: parse imports: %v", relativeFile, err)
			return nil
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Errorf("%s: parse import %s: %v", relativeFile, spec.Path.Value, err)
				continue
			}
			assertAllowedDependency(t, filepath.ToSlash(relativeFile), sourceModule, importPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal modules: %v", err)
	}
}

func assertAllowedDependency(
	t *testing.T,
	sourceFile string,
	sourceModule string,
	importPath string,
) {
	t.Helper()

	domainModule := sourceModule == "internal/models" ||
		sourceModule == "internal/services"
	if domainModule && hasInternalPrefix(
		importPath,
		"mcp",
		"a2a",
		"agentplatform",
		"openapi",
		"handlers",
	) {
		t.Errorf(
			"%s: domain Module %s must not import protocol/transport Module %s",
			sourceFile,
			sourceModule,
			importPath,
		)
	}

	protocolModule := sourceModule == "internal/mcp" ||
		sourceModule == "internal/a2a" ||
		sourceModule == "internal/openapi"
	if protocolModule && hasInternalPrefix(importPath, "handlers") {
		t.Errorf(
			"%s: protocol Module %s must not import human REST handlers %s",
			sourceFile,
			sourceModule,
			importPath,
		)
	}

	if importPath == modulePath+"/internal/openapi" &&
		sourceModule != "internal/app" &&
		sourceModule != "internal/openapi" {
		t.Errorf(
			"%s: embedded OpenAPI contract may only be imported by internal/app",
			sourceFile,
		)
	}
}

func hasInternalPrefix(importPath string, modules ...string) bool {
	for _, module := range modules {
		prefix := modulePath + "/internal/" + module
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}
