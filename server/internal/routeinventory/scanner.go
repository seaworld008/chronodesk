// Package routeinventory discovers runtime Gin GET registrations from the
// non-test Go source that assembles the router. It is intentionally a source
// inventory rather than a second runtime router: CI uses it to make a newly
// registered GET fail closed until its Human, machine, or public behaviour is
// classified.
package routeinventory

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Registration identifies one Gin GET registration by stable source
// attributes. Line numbers and handler middleware are deliberately excluded
// so harmless formatting and middleware changes do not churn the manifest.
type Registration struct {
	Fingerprint    string
	File           string
	Function       string
	Receiver       string
	PathExpression string
}

// ScanRuntimeGETRoutes scans every non-test Go source below internal/. Today
// every .GET call there is a Gin registration. Scanning the whole tree also
// covers delegated Register*Routes implementations (including Human Agent
// administration, machine APIs, and public contract discovery) and fails
// conservatively if another package introduces a GET-shaped call.
//
// serverRoot is the directory containing go.mod and internal/.
func ScanRuntimeGETRoutes(serverRoot string) ([]Registration, error) {
	if strings.TrimSpace(serverRoot) == "" {
		return nil, errors.New("server root is required")
	}
	internalRoot := filepath.Join(serverRoot, "internal")
	paths := make([]string, 0)
	err := filepath.WalkDir(
		internalRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			paths = append(paths, path)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("walk internal source: %w", err)
	}
	sort.Strings(paths)

	registrations := make([]Registration, 0)
	for _, path := range paths {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		relative, relativeErr := filepath.Rel(serverRoot, path)
		if relativeErr != nil {
			return nil, fmt.Errorf("relativize %s: %w", path, relativeErr)
		}
		found, scanErr := scanSource(
			filepath.ToSlash(relative),
			source,
			true,
		)
		if scanErr != nil {
			return nil, scanErr
		}
		registrations = append(registrations, found...)
	}
	sort.Slice(registrations, func(i, j int) bool {
		return registrations[i].Fingerprint < registrations[j].Fingerprint
	})
	if err := rejectDuplicateFingerprints(registrations); err != nil {
		return nil, err
	}
	return registrations, nil
}

// CountRuntimeMethodPathRegistrations provides a focused inventory for
// security-sensitive non-GET routes without weakening the exhaustive GET
// classification manifest. It counts exact literal method/path registrations
// across production internal source.
func CountRuntimeMethodPathRegistrations(
	serverRoot string,
	method string,
	routePath string,
) (int, error) {
	if strings.TrimSpace(serverRoot) == "" {
		return 0, errors.New("server root is required")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT":
	default:
		return 0, fmt.Errorf("unsupported HTTP method %q", method)
	}
	if !strings.HasPrefix(routePath, "/") {
		return 0, errors.New("route path must be an absolute literal")
	}

	internalRoot := filepath.Join(serverRoot, "internal")
	count := 0
	err := filepath.WalkDir(
		internalRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() ||
				!strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(
				fileSet,
				path,
				source,
				parser.SkipObjectResolution,
			)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != method {
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil && value == routePath {
					count++
				}
				return true
			})
			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("inventory %s %s: %w", method, routePath, err)
	}
	return count, nil
}

func scanSource(
	filename string,
	source []byte,
	allFunctions bool,
) ([]Registration, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		filename,
		source,
		parser.SkipObjectResolution,
	)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	registrations := make([]Registration, 0)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		if !allFunctions && !isRouteRegistrationFunction(function.Name.Name) {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "GET" || len(call.Args) == 0 {
				return true
			}
			receiver, renderErr := renderExpression(fileSet, selector.X)
			if renderErr != nil {
				err = errors.Join(err, renderErr)
				return true
			}
			pathExpression, renderErr := renderPathExpression(
				fileSet,
				call.Args[0],
			)
			if renderErr != nil {
				err = errors.Join(err, renderErr)
				return true
			}
			fingerprint := makeFingerprint(
				filename,
				function.Name.Name,
				receiver,
				pathExpression,
			)
			registrations = append(registrations, Registration{
				Fingerprint:    fingerprint,
				File:           filename,
				Function:       function.Name.Name,
				Receiver:       receiver,
				PathExpression: pathExpression,
			})
			return true
		})
	}
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", filename, err)
	}
	return registrations, nil
}

func isRouteRegistrationFunction(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "register") &&
		strings.Contains(lower, "routes")
}

func renderPathExpression(
	fileSet *token.FileSet,
	expression ast.Expr,
) (string, error) {
	if literal, ok := expression.(*ast.BasicLit); ok &&
		literal.Kind == token.STRING {
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return "", fmt.Errorf("unquote route path: %w", err)
		}
		return strconv.Quote(value), nil
	}
	return renderExpression(fileSet, expression)
}

func renderExpression(fileSet *token.FileSet, expression ast.Expr) (string, error) {
	var output bytes.Buffer
	if err := format.Node(&output, fileSet, expression); err != nil {
		return "", fmt.Errorf("format route expression: %w", err)
	}
	return output.String(), nil
}

func makeFingerprint(
	filename string,
	function string,
	receiver string,
	pathExpression string,
) string {
	return strings.Join(
		[]string{
			filepath.ToSlash(filename),
			function,
			receiver + ".GET",
			pathExpression,
		},
		"|",
	)
}

func rejectDuplicateFingerprints(registrations []Registration) error {
	var duplicateErrors []error
	for index := 1; index < len(registrations); index++ {
		if registrations[index-1].Fingerprint == registrations[index].Fingerprint {
			duplicateErrors = append(
				duplicateErrors,
				fmt.Errorf(
					"duplicate GET route fingerprint %q",
					registrations[index].Fingerprint,
				),
			)
		}
	}
	return errors.Join(duplicateErrors...)
}
