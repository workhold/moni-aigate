package main

import (
	"os"
	"strings"
	"testing"
)

func TestWorkflowRestoresExactPackageAndRunFilterExecution(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		`mapfile -t packages < "$RUNNER_TEMP/ci-packages-$package_class.txt"`,
		`run_filter=()`,
		`expression="^($(paste -sd '|' "$RUNNER_TEMP/ci-tests-$GO_TEST_SHARD.txt"))$"`,
		`go run ./scripts/acceptance-shard-plan --emit-legacy "$RUNNER_TEMP"`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("workflow is missing exact execution contract %q", required)
		}
	}
}
