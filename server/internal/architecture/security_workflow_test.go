package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecurityWorkflowPreservesIndependentGoReleaseGates(t *testing.T) {
	t.Parallel()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate security workflow contract test")
	}
	workflowPath := filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"..",
		".github",
		"workflows",
		"security.yml",
	)
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read security workflow: %v", err)
	}
	workflow := string(raw)

	requiredFragments := []string{
		"--ignore-gitleaks-allow",
		"  go-unit:\n",
		"run: go test ./... -count=1",
		"  go-sdk:\n",
		"working-directory: sdk/go",
		"  go-race:\n",
		"run: go test -race ./... -count=1",
		"  redis-integration:\n",
		"image: redis:7.4-alpine",
		"-run '^(TestRedisAgentExecutionGuardIntegration|TestSchedulerRedisLeaseIntegration)$'",
		"  go-vuln:\n",
		"component: server",
		"component: sdk",
		"govulncheck@v1.7.0 ./...",
		"  go:\n    name: go\n    if: ${{ always() }}\n",
		"${{ needs.go-unit.result }}",
		"${{ needs.go-sdk.result }}",
		"${{ needs.go-race.result }}",
		"${{ needs.redis-integration.result }}",
		"${{ needs.go-vuln.result }}",
		`if [ "${gate#*=}" != "success" ]; then`,
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(workflow, fragment) {
			t.Errorf("security workflow is missing contract fragment %q", fragment)
		}
	}
	if strings.Contains(workflow, "govulncheck@latest") {
		t.Error("security workflow uses an unpinned govulncheck")
	}
	checkouts := strings.Count(workflow, "uses: actions/checkout@v7")
	credentiallessCheckouts := strings.Count(
		workflow,
		"persist-credentials: false",
	)
	if checkouts == 0 || credentiallessCheckouts != checkouts {
		t.Errorf(
			"credentialless checkouts = %d, want every one of %d",
			credentiallessCheckouts,
			checkouts,
		)
	}
}
