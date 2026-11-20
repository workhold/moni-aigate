package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type Part struct {
	Name      string   `json:"name"`
	Packages  []string `json:"packages"`
	Tests     []string `json:"tests"`
	RunFilter string   `json:"run_filter"`
	Args      []string `json:"args"`
}

type Shard struct {
	Name      string   `json:"name"`
	Class     string   `json:"class"`
	Packages  []string `json:"packages"`
	Discovery []string `json:"discovery,omitempty"`
	Parts     []Part   `json:"parts"`
}

type Plan struct {
	SchemaVersion int     `json:"schema_version"`
	Commit        string  `json:"commit"`
	Tree          string  `json:"tree"`
	Module        string  `json:"module"`
	Shards        []Shard `json:"shards"`
	Digest        string  `json:"digest"`
}

var (
	hex40       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goTestName  = regexp.MustCompile(`^(Test|Fuzz|Example)[A-Za-z0-9_]*$`)
	benchmarkID = regexp.MustCompile(`^Benchmark[A-Za-z0-9_]*$`)
	shardNames  = []string{"billing-0", "billing-1", "billing-2", "billing-3", "server-0", "server-1", "server-2", "server-3", "attempt-0", "attempt-1", "attempt-2", "attempt-3", "identity", "other"}
	classes     = []string{"billing", "identity", "server", "attempt", "other"}
	repoRoot    = sync.OnceValues(func() (string, error) {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	})
)

func runAt(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	// Output captures stdout only. Go may legitimately write module download
	// progress to stderr, which must not become a fake package or test.
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func run(ctx context.Context, args ...string) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	return runAt(ctx, root, args...)
}

func lines(raw string) []string {
	var result []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func uniqueSorted(values []string) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return nil, fmt.Errorf("duplicate or empty value %q", value)
		}
		seen[value] = true
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result, nil
}

func packageClass(module, pkg string) string {
	switch pkg {
	case module + "/internal/billing":
		return "billing"
	case module + "/internal/identity":
		return "identity"
	case module + "/internal/server":
		return "server"
	case module + "/internal/procurementattemptbirth":
		return "attempt"
	default:
		return "other"
	}
}

func partitionPackages(module string, packages []string) (map[string][]string, error) {
	result := map[string][]string{}
	for _, class := range classes {
		result[class] = []string{}
	}
	for _, pkg := range packages {
		result[packageClass(module, pkg)] = append(result[packageClass(module, pkg)], pkg)
	}
	for _, class := range classes {
		if len(result[class]) == 0 {
			return nil, fmt.Errorf("empty Go test partition: %s", class)
		}
		if class != "other" && len(result[class]) != 1 {
			return nil, fmt.Errorf("large-package partition %s must contain exactly one package", class)
		}
	}
	all, err := uniqueSorted(packages)
	if err != nil || len(all) != len(packages) {
		return nil, errors.New("package inventory contains duplicates")
	}
	return result, nil
}

func parseDiscovery(raw string) (discovery []string, sorted []string, err error) {
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Test"), strings.HasPrefix(line, "Fuzz"), strings.HasPrefix(line, "Example"):
			if !goTestName.MatchString(line) {
				return nil, nil, fmt.Errorf("invalid top-level Go test identifier %q", line)
			}
			if seen[line] {
				return nil, nil, fmt.Errorf("duplicate top-level Go test %q", line)
			}
			seen[line] = true
			discovery = append(discovery, line)
		case strings.HasPrefix(line, "Benchmark"):
			if !benchmarkID.MatchString(line) {
				return nil, nil, fmt.Errorf("invalid Go benchmark identifier %q", line)
			}
		case strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "ok\t"):
			continue
		default:
			return nil, nil, fmt.Errorf("unexpected go test discovery output %q", line)
		}
	}
	sorted = append([]string(nil), discovery...)
	sort.Strings(sorted)
	return discovery, sorted, nil
}

func testArgs(part Part) []string {
	args := []string{"go", "test", "-json", "-race", "-p", "1", "-count=1", "-timeout=10m"}
	if part.RunFilter != "" {
		args = append(args, "-run", part.RunFilter)
	}
	return append(args, part.Packages...)
}

func partitionTests(class string, tests []string) ([]Part, error) {
	if class == "other" {
		return []Part{{Name: "other"}}, nil
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("empty top-level Go test discovery for %s", class)
	}
	parts := make([]Part, 4)
	for index, name := range tests {
		part := index % 4
		parts[part].Tests = append(parts[part].Tests, name)
	}
	for index := range parts {
		parts[index].Name = fmt.Sprintf("%s-%d", class, index)
		if len(parts[index].Tests) == 0 {
			return nil, fmt.Errorf("empty top-level Go test partition: %s", parts[index].Name)
		}
		parts[index].RunFilter = "^(" + strings.Join(parts[index].Tests, "|") + ")$"
	}
	return parts, nil
}

func buildPlan(ctx context.Context, commit, tree string) (Plan, error) {
	root, err := repoRoot()
	if err != nil {
		return Plan{}, err
	}
	return buildPlanAt(ctx, root, commit, tree)
}

func buildPlanAt(ctx context.Context, root, commit, tree string) (Plan, error) {
	if !hex40.MatchString(commit) || !hex40.MatchString(tree) {
		return Plan{}, errors.New("commit and tree must be full lowercase SHA-256 identities")
	}
	status, err := runAt(ctx, root, "git", "status", "--porcelain")
	if err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(status) != "" {
		return Plan{}, errors.New("acceptance shard planning requires a clean worktree")
	}
	moduleRaw, err := runAt(ctx, root, "go", "list", "-m")
	if err != nil {
		return Plan{}, err
	}
	module := strings.TrimSpace(moduleRaw)
	if module == "" {
		return Plan{}, errors.New("empty Go module identity")
	}
	packageRaw, err := runAt(ctx, root, "go", "list", "./...")
	if err != nil {
		return Plan{}, err
	}
	packagesByClass, err := partitionPackages(module, lines(packageRaw))
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{SchemaVersion: 1, Commit: commit, Tree: tree, Module: module}
	for _, class := range []string{"billing", "server", "attempt"} {
		packages := packagesByClass[class]
		raw, err := runAt(ctx, root, "go", "test", "-race", "-count=1", "-list", ".", packages[0])
		if err != nil {
			return Plan{}, err
		}
		discovery, tests, err := parseDiscovery(raw)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", class, err)
		}
		parts, err := partitionTests(class, tests)
		if err != nil {
			return Plan{}, err
		}
		for _, part := range parts {
			part.Packages = append([]string(nil), packages...)
			part.Args = testArgs(part)
			plan.Shards = append(plan.Shards, Shard{Name: part.Name, Class: class, Packages: append([]string(nil), packages...), Discovery: discovery, Parts: []Part{part}})
		}
	}
	identityPackages := packagesByClass["identity"]
	raw, err := runAt(ctx, root, "go", "test", "-race", "-count=1", "-list", ".", identityPackages[0])
	if err != nil {
		return Plan{}, err
	}
	identityDiscovery, identityTests, err := parseDiscovery(raw)
	if err != nil {
		return Plan{}, fmt.Errorf("identity: %w", err)
	}
	identityParts, err := partitionTests("identity", identityTests)
	if err != nil {
		return Plan{}, err
	}
	for index := range identityParts {
		identityParts[index].Packages = append([]string(nil), identityPackages...)
		identityParts[index].Args = testArgs(identityParts[index])
	}
	plan.Shards = append(plan.Shards, Shard{Name: "identity", Class: "identity", Packages: append([]string(nil), identityPackages...), Discovery: identityDiscovery, Parts: identityParts})
	other := Part{Name: "other", Packages: append([]string(nil), packagesByClass["other"]...)}
	other.Args = testArgs(other)
	plan.Shards = append(plan.Shards, Shard{Name: "other", Class: "other", Packages: append([]string(nil), packagesByClass["other"]...), Parts: []Part{other}})
	plan.Digest, err = planDigest(plan)
	if err != nil {
		return Plan{}, err
	}
	if err := validatePlan(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func planDigest(plan Plan) (string, error) {
	plan.Digest = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != 1 || !hex40.MatchString(plan.Commit) || !hex40.MatchString(plan.Tree) || plan.Module == "" || len(plan.Shards) != len(shardNames) {
		return errors.New("invalid shard plan identity or shape")
	}
	seenTests := map[string]string{}
	for index, shard := range plan.Shards {
		expectedClass := shard.Name
		if separator := strings.LastIndex(expectedClass, "-"); separator >= 0 {
			expectedClass = expectedClass[:separator]
		}
		if shard.Name != shardNames[index] || shard.Class != expectedClass || len(shard.Packages) == 0 {
			return fmt.Errorf("unexpected shard at position %d: %s", index, shard.Name)
		}
		if shard.Class == "identity" && len(shard.Parts) != 4 {
			return errors.New("identity must contain four sequential test processes")
		}
		if shard.Class == "other" && len(shard.Parts) != 1 {
			return errors.New("other must contain exactly one part")
		}
		if shard.Class != "identity" && shard.Class != "other" && len(shard.Parts) != 1 {
			return fmt.Errorf("shard %s must contain exactly one part", shard.Name)
		}
		for partIndex, part := range shard.Parts {
			expectedName := shard.Name
			if shard.Class == "identity" {
				expectedName = fmt.Sprintf("%s-%d", shard.Class, partIndex)
			}
			if part.Name != expectedName || !reflect.DeepEqual(part.Packages, shard.Packages) {
				return fmt.Errorf("part identity or package consistency failed for %s", part.Name)
			}
			if shard.Class == "other" {
				if len(part.Tests) != 0 || part.RunFilter != "" {
					return errors.New("other must not have a test filter")
				}
			} else {
				if len(part.Tests) == 0 || part.RunFilter != "^("+strings.Join(part.Tests, "|")+")$" {
					return fmt.Errorf("invalid test filter for %s", part.Name)
				}
				for _, test := range part.Tests {
					key := shard.Class + "\x00" + test
					if previous, exists := seenTests[key]; exists {
						return fmt.Errorf("duplicate top-level test %s in %s and %s", test, previous, part.Name)
					}
					seenTests[key] = part.Name
				}
			}
			if !reflect.DeepEqual(part.Args, testArgs(part)) {
				return fmt.Errorf("invalid execution argv for %s", part.Name)
			}
		}
	}
	expected, err := planDigest(plan)
	if err != nil {
		return err
	}
	if plan.Digest != expected {
		return errors.New("shard plan digest mismatch")
	}
	return nil
}

func writeFile(path string, values []string) error {
	content := ""
	if len(values) > 0 {
		content = strings.Join(values, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func emitLegacy(plan Plan, directory string) error {
	if err := validatePlan(plan); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(directory, "shard-plan.json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	byClass := map[string][]string{}
	for _, class := range classes {
		byClass[class] = []string{}
	}
	allSet := map[string]bool{}
	var all []string
	for _, shard := range plan.Shards {
		if len(byClass[shard.Class]) == 0 {
			byClass[shard.Class] = append(byClass[shard.Class], shard.Packages...)
		}
		for _, pkg := range shard.Packages {
			if !allSet[pkg] {
				allSet[pkg] = true
				all = append(all, pkg)
			}
		}
	}
	for _, class := range classes {
		sort.Strings(byClass[class])
	}
	sort.Strings(all)
	for _, class := range classes {
		if err := writeFile(filepath.Join(directory, "ci-packages-"+class+".txt"), byClass[class]); err != nil {
			return err
		}
	}
	for _, name := range []string{"all", "expected", "actual"} {
		if err := writeFile(filepath.Join(directory, "ci-packages-"+name+".txt"), all); err != nil {
			return err
		}
	}
	for _, class := range []string{"billing", "server", "attempt", "identity"} {
		var allTests, discovery []string
		for _, shard := range plan.Shards {
			if shard.Class != class {
				continue
			}
			if len(discovery) == 0 {
				discovery = append(discovery, shard.Discovery...)
			}
			for _, part := range shard.Parts {
				allTests = append(allTests, part.Tests...)
				if err := writeFile(filepath.Join(directory, "ci-tests-"+part.Name+".txt"), part.Tests); err != nil {
					return err
				}
			}
		}
		sort.Strings(allTests)
		for _, name := range []string{"all", "actual", "unsorted", "discovery"} {
			values := allTests
			if name == "unsorted" || name == "discovery" {
				values = discovery
			}
			if err := writeFile(filepath.Join(directory, "ci-tests-"+class+"-"+name+".txt"), values); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	directory := ""
	for index, arg := range os.Args {
		if arg == "--emit-legacy" && index+1 < len(os.Args) {
			directory = os.Args[index+1]
		}
	}
	if directory == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/acceptance-shard-plan --emit-legacy <directory>")
		os.Exit(2)
	}
	commit, err := run(context.Background(), "git", "rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tree, err := run(context.Background(), "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	plan, err := buildPlan(context.Background(), strings.TrimSpace(commit), strings.TrimSpace(tree))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = emitLegacy(plan, directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("shard plan OK: %d shards, %d packages\n", len(plan.Shards), len(allPackages(plan)))
}

func allPackages(plan Plan) []string {
	seen := map[string]bool{}
	for _, shard := range plan.Shards {
		for _, pkg := range shard.Packages {
			seen[pkg] = true
		}
	}
	result := make([]string, 0, len(seen))
	for pkg := range seen {
		result = append(result, pkg)
	}
	sort.Strings(result)
	return result
}
