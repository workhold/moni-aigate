package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPartitionPackages(t *testing.T) {
	module := "example.com/mod"
	packages := []string{module + "/internal/server", module + "/internal/billing", module + "/internal/identity", module + "/internal/procurementattemptbirth", module + "/internal/provider"}
	got, err := partitionPackages(module, packages)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["other"]) != 1 || got["other"][0] != module+"/internal/provider" {
		t.Fatalf("new package did not fall into other: %+v", got)
	}
	for _, class := range []string{"billing", "server", "attempt", "identity"} {
		if len(got[class]) != 1 {
			t.Fatalf("large class %s was not isolated: %+v", class, got[class])
		}
	}
	for _, invalid := range [][]string{{module + "/internal/server"}, {module + "/internal/server", module + "/internal/server"}} {
		if _, err := partitionPackages(module, invalid); err == nil {
			t.Fatalf("invalid package inventory accepted: %v", invalid)
		}
	}
}

func TestParseAndPartitionTests(t *testing.T) {
	discovery, tests, err := parseDiscovery("TestB\nTestA\nExampleC\nTestD\nok example.com/mod\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(discovery, []string{"TestB", "TestA", "ExampleC", "TestD"}) || !reflect.DeepEqual(tests, []string{"ExampleC", "TestA", "TestB", "TestD"}) {
		t.Fatalf("discovery order was not preserved: discovery=%v sorted=%v", discovery, tests)
	}
	parts, err := partitionTests("server", tests)
	if err != nil {
		t.Fatal(err)
	}
	if parts[0].Tests[0] != "ExampleC" || parts[1].Tests[0] != "TestA" || parts[2].Tests[0] != "TestB" || parts[3].RunFilter != "^(TestD)$" {
		t.Fatalf("unexpected round-robin partitions: %+v", parts)
	}
	for _, raw := range []string{"Test-Invalid\n", "Benchmark-Invalid\n", "warning: hidden\n", "TestA\nTestA\n", "okay\n", " TestA\n"} {
		if _, _, err := parseDiscovery(raw); err == nil {
			t.Fatalf("invalid discovery accepted: %q", raw)
		}
	}
	if _, err := partitionTests("server", []string{"TestOnly"}); err == nil {
		t.Fatal("an empty fourth test partition was accepted")
	}
}

func legacyFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	moduleRaw, err := runAt(context.Background(), root, "go", "list", "-m")
	if err != nil {
		t.Fatal(err)
	}
	module := strings.TrimSpace(moduleRaw)
	packageRaw, err := runAt(context.Background(), root, "go", "list", "./...")
	if err != nil {
		t.Fatal(err)
	}
	packages := map[string][]string{"billing": {}, "identity": {}, "server": {}, "attempt": {}, "other": {}}
	for _, pkg := range lines(packageRaw) {
		class := packageClass(module, pkg)
		packages[class] = append(packages[class], pkg)
	}
	files := map[string]string{}
	for class, classPackages := range packages {
		sort.Strings(classPackages)
		files["ci-packages-"+class+".txt"] = strings.Join(classPackages, "\n") + "\n"
		if class != "other" {
			raw, err := runAt(context.Background(), root, "go", "test", "-race", "-count=1", "-list", ".", classPackages[0])
			if err != nil {
				t.Fatal(err)
			}
			var discovery, tests []string
			for _, name := range strings.Split(raw, "\n") {
				if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "Example") {
					discovery = append(discovery, name)
					tests = append(tests, name)
				}
			}
			sort.Strings(tests)
			files["ci-tests-"+class+"-discovery.txt"] = strings.Join(discovery, "\n") + "\n"
			files["ci-tests-"+class+"-unsorted.txt"] = files["ci-tests-"+class+"-discovery.txt"]
			files["ci-tests-"+class+"-all.txt"] = strings.Join(tests, "\n") + "\n"
			files["ci-tests-"+class+"-actual.txt"] = files["ci-tests-"+class+"-all.txt"]
			for index, name := range tests {
				part := index % 4
				path := "ci-tests-" + class + "-" + string(rune('0'+part)) + ".txt"
				files[path] += name + "\n"
			}
		}
	}
	var all []string
	for _, class := range []string{"billing", "identity", "server", "attempt", "other"} {
		all = append(all, packages[class]...)
	}
	sort.Strings(all)
	for _, name := range []string{"all", "expected", "actual"} {
		files["ci-packages-"+name+".txt"] = strings.Join(all, "\n") + "\n"
	}
	return files
}

func detachedWorktree(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if _, err := run(context.Background(), "git", "worktree", "add", "--detach", directory, "HEAD"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = run(context.Background(), "git", "worktree", "remove", "--force", directory) })
	return directory
}

func TestCurrentTreeMatchesLegacyPartitionArtifacts(t *testing.T) {
	root := detachedWorktree(t)
	commit, err := run(context.Background(), "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := run(context.Background(), "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildPlanAt(context.Background(), root, strings.TrimSpace(commit), strings.TrimSpace(tree))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Digest) != 64 {
		t.Fatalf("plan digest is missing: %q", plan.Digest)
	}
	for _, shard := range plan.Shards {
		for _, part := range shard.Parts {
			if len(part.Args) < 7 || part.Args[0] != "go" || part.Args[1] != "test" || part.Args[2] != "-json" || part.Args[3] != "-race" || part.Args[4] != "-p" || part.Args[5] != "1" || part.Args[6] != "-count=1" {
				t.Fatalf("exact execution argv missing for %s: %v", part.Name, part.Args)
			}
			if part.RunFilter != "" && (len(part.Args) < 2 || part.Args[len(part.Args)-len(part.Packages)-2] != "-run" || part.Args[len(part.Args)-len(part.Packages)-1] != part.RunFilter || !strings.HasSuffix(part.RunFilter, ")$")) {
				t.Fatalf("run filter argv is not exact for %s: %v", part.Name, part.Args)
			}
			if !reflect.DeepEqual(part.Args[len(part.Args)-len(part.Packages):], part.Packages) {
				t.Fatalf("package argv suffix mismatch for %s: %v", part.Name, part.Args)
			}
		}
	}
	directory := t.TempDir()
	if err := emitLegacy(plan, directory); err != nil {
		t.Fatal(err)
	}
	expected := legacyFiles(t, root)
	for name, want := range expected {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != want {
			t.Errorf("%s differs from legacy artifact: got %q want %q", name, string(raw), want)
		}
	}
	var decoded Plan
	raw, err := os.ReadFile(filepath.Join(directory, "shard-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Shards) != 14 || decoded.Commit != strings.TrimSpace(commit) || decoded.Tree != strings.TrimSpace(tree) || decoded.Digest != plan.Digest {
		t.Fatalf("unexpected shard plan identity or count: %+v", decoded)
	}
	plan.Shards[0].Parts[0].Tests[0] = "TestDuplicate"
	plan.Shards[1].Parts[0].Tests[0] = "TestDuplicate"
	plan.Digest, err = planDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlan(plan); err == nil {
		t.Fatal("duplicate tests across shards were accepted")
	}
}

func TestBuildPlanRejectsDirtyTree(t *testing.T) {
	root := detachedWorktree(t)
	commit, err := run(context.Background(), "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := run(context.Background(), "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty-test-marker"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildPlanAt(context.Background(), root, strings.TrimSpace(commit), strings.TrimSpace(tree)); err == nil {
		t.Fatal("dirty worktree was stamped as HEAD evidence")
	}
}

func TestValidatePlanRejectsWrongShape(t *testing.T) {
	plan := Plan{SchemaVersion: 1, Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), Module: "example.com/mod"}
	if err := validatePlan(plan); err == nil {
		t.Fatal("empty plan was accepted")
	}
}
