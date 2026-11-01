package onlinedependencyqa_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Process helpers restart the whole test binary. Check test imports as well as
// production imports so offline catalog authentication stays outside both.
func TestOnlineDependenciesExcludeOfflineMaintenance(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-race", "-deps", "-test", "./cmd/moni-aigate", "./internal/server", "./internal/billing", "./internal/periodicbudget")
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list online production and test dependencies: %v\n%s", err, raw)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ".test")
		seen[name] = true
		for _, blocked := range []string{"maintenancescan", "maintenancetrust", "fundinglotcatalog"} {
			if name == "github.com/moni-ai/moni-aigate/internal/"+blocked {
				t.Fatalf("online production/test dependency reaches offline %s", blocked)
			}
		}
	}
	for _, target := range []string{"cmd/moni-aigate", "internal/server", "internal/billing", "internal/periodicbudget"} {
		if !seen["github.com/moni-ai/moni-aigate/"+target] {
			t.Fatalf("dependency check omitted %s", target)
		}
	}
}
