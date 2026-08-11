package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	releaseVersion = "0.2.0"
	// Human Web is an independent API contract. It currently shares the same
	// number as the product release but must not move mechanically with it.
	humanContractVersion = "0.2.0"
)

func TestReleaseMetadataUsesOneProductVersion(t *testing.T) {
	root := repositoryRoot(t)

	for path, marker := range map[string]string{
		"Makefile":                    "VERSION ?= " + releaseVersion,
		"docker-compose.yml":          "VERSION: ${VERSION:-" + releaseVersion + "}",
		".github/workflows/smoke.yml": "export VERSION=" + releaseVersion,
		"server/.env.example":         "APP_VERSION=" + releaseVersion,
		"web/.env.example":            "VITE_APP_VERSION=" + releaseVersion,
		"sdk/python/pyproject.toml":   "version = \"" + releaseVersion + "\"",
		"sdk/generator/java.yaml":     "artifactVersion: " + releaseVersion,
		"sdk/generator/dotnet.yaml":   "packageVersion: " + releaseVersion,
	} {
		content := readRepositoryFile(t, root, path)
		if !strings.Contains(content, marker) {
			t.Errorf("%s is missing %q", path, marker)
		}
	}

	dockerfile := readRepositoryFile(t, root, "server/Dockerfile")
	if got := strings.Count(
		dockerfile,
		"ARG VERSION="+releaseVersion,
	); got != 2 {
		t.Errorf("server/Dockerfile has %d release VERSION args, want 2", got)
	}

	versionSource := readRepositoryFile(
		t,
		root,
		"server/internal/version/version.go",
	)
	for _, marker := range []string{
		"const DefaultVersion = \"" + releaseVersion + "\"",
		"Version   = DefaultVersion",
	} {
		if !strings.Contains(versionSource, marker) {
			t.Errorf("version.go is missing %q", marker)
		}
	}

	for _, path := range []string{
		"web/package.json",
		"web/package-lock.json",
		"sdk/typescript/package.json",
		"sdk/typescript/package-lock.json",
	} {
		assertRootPackageVersion(t, root, path, releaseVersion)
	}
}

func TestReleaseMetadataKeepsContractAndProtocolVersionsIndependent(
	t *testing.T,
) {
	root := repositoryRoot(t)
	var document map[string]any
	if err := yaml.Unmarshal(
		[]byte(readRepositoryFile(
			t,
			root,
			"server/internal/openapi/openapi.yaml",
		)),
		&document,
	); err != nil {
		t.Fatalf("decode Agent OpenAPI: %v", err)
	}

	info := nestedMap(t, document, "info")
	if got := info["version"]; got != releaseVersion {
		t.Errorf("Agent OpenAPI info.version = %v, want %s", got, releaseVersion)
	}

	openapi := readRepositoryFile(
		t,
		root,
		"server/internal/openapi/openapi.yaml",
	)
	for _, marker := range []string{
		"io.modelcontextprotocol/serverInfo:\n" +
			"                      name: chronodesk\n" +
			"                      version: " + releaseVersion,
		"io.modelcontextprotocol/clientInfo:\n" +
			"                        name: external-ticket-agent\n" +
			"                        version: 0.1.0",
		"io.modelcontextprotocol/protocolVersion: '2026-07-28'",
		"supportedVersions: ['2026-07-28']",
		"const: '1.0'",
	} {
		if !strings.Contains(openapi, marker) {
			t.Errorf("Agent OpenAPI is missing independent contract marker %q", marker)
		}
	}

	var human map[string]any
	if err := json.Unmarshal(
		[]byte(readRepositoryFile(
			t,
			root,
			"server/internal/humanopenapi/openapi.json",
		)),
		&human,
	); err != nil {
		t.Fatalf("decode Human OpenAPI: %v", err)
	}
	if got := nestedMap(t, human, "info")["version"]; got != humanContractVersion {
		t.Errorf(
			"Human OpenAPI info.version = %v, want independent contract %s",
			got,
			humanContractVersion,
		)
	}
	if got := human["x-chronodesk-types-generator"]; got != "2.1.0" {
		t.Errorf("Human OpenAPI generator = %v, want 2.1.0", got)
	}

	var asyncAPI map[string]any
	if err := yaml.Unmarshal(
		[]byte(readRepositoryFile(
			t,
			root,
			"server/internal/asyncapi/asyncapi.yaml",
		)),
		&asyncAPI,
	); err != nil {
		t.Fatalf("decode AsyncAPI: %v", err)
	}
	if got := nestedMap(t, asyncAPI, "info")["version"]; got != "2.0.0" {
		t.Errorf("AsyncAPI info.version = %v, want 2.0.0", got)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readRepositoryFile(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertRootPackageVersion(
	t *testing.T,
	root string,
	path string,
	want string,
) {
	t.Helper()
	var document struct {
		Version  string `json:"version"`
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(
		[]byte(readRepositoryFile(t, root, path)),
		&document,
	); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if document.Version != want {
		t.Errorf("%s root version = %q, want %q", path, document.Version, want)
	}
	if strings.HasSuffix(path, "package-lock.json") &&
		document.Packages[""].Version != want {
		t.Errorf(
			"%s root lock package version = %q, want %q",
			path,
			document.Packages[""].Version,
			want,
		)
	}
}

func nestedMap(
	t *testing.T,
	parent map[string]any,
	key string,
) map[string]any {
	t.Helper()
	child, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, parent[key])
	}
	return child
}
