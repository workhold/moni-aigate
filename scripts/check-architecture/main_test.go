package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func writeFixture(t *testing.T, root, name, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	var doc strings.Builder
	doc.WriteString("# Architecture\n\n[History](changes.md) [Manifest](architecture-manifest.json) [Code](../../internal/test.go)\n")
	for _, section := range sections {
		doc.WriteString("\n<!-- architecture:" + section + " -->\n")
	}
	for i := 0; i < 3; i++ {
		doc.WriteString("\n```mermaid\nflowchart LR\nA-->B\n```\n")
	}
	writeFixture(t, root, documentPath, doc.String())
	writeFixture(t, root, historyPath, "# Architecture changes\n")
	writeFixture(t, root, "internal/test.go", "package test\n")
	return root
}

func refreshFixture(t *testing.T, root string) {
	t.Helper()
	if err := run(root, true, true, "reviewed implementation and architecture", fixedTime, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshAndReadOnlyCheck(t *testing.T) {
	root := fixture(t)
	var output bytes.Buffer
	if err := run(root, false, false, "", fixedTime, &output); err == nil || !strings.Contains(err.Error(), "architecture-refresh") {
		t.Fatalf("missing baseline must fail with a remedy: %v", err)
	}
	refreshFixture(t, root)
	before, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := run(root, false, false, "", fixedTime, &output); err != nil {
		t.Fatal(err)
	}
	// A successful check must neither rewrite the snapshot nor its review time.
	after, _ := os.ReadFile(filepath.Join(root, manifestPath))
	if !bytes.Equal(before, after) {
		t.Fatal("read-only check changed the snapshot")
	}
	historyBefore, _ := os.ReadFile(filepath.Join(root, historyPath))
	if err := run(root, true, true, "same source", fixedTime.Add(time.Hour), &output); err != nil {
		t.Fatal(err)
	}
	historyAfter, _ := os.ReadFile(filepath.Join(root, historyPath))
	if !bytes.Equal(historyBefore, historyAfter) {
		t.Fatal("no-op refresh created misleading evolution history")
	}
}

func TestDetectsContentAdditionsAndDeletionsWithoutGit(t *testing.T) {
	root := fixture(t)
	writeFixture(t, root, "web/src/delete.ts", "export const oldValue = 1\n")
	refreshFixture(t, root)
	// Preserve mtime to prove that synchronization uses content rather than dates.
	info, _ := os.Stat(filepath.Join(root, "internal/test.go"))
	writeFixture(t, root, "internal/test.go", "package changed\n")
	if err := os.Chtimes(filepath.Join(root, "internal/test.go"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "migrations/002_added.sql", "SELECT 1;\n")
	if err := os.Remove(filepath.Join(root, "web/src/delete.ts")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(root, false, false, "", fixedTime, &output); err == nil {
		t.Fatal("stale snapshot was accepted")
	}
	for _, want := range []string{"modified internal/test.go", "added migrations/002_added.sql", "deleted web/src/delete.ts"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing change %q in %s", want, output.String())
		}
	}
	if err := run(root, true, true, "schema and client changed; diagrams reviewed", fixedTime.Add(time.Hour), &output); err != nil {
		t.Fatal(err)
	}
	if err := run(root, false, false, "", fixedTime, &output); err != nil {
		t.Fatal(err)
	}
	history, _ := os.ReadFile(filepath.Join(root, historyPath))
	if !bytes.Contains(history, []byte("deleted `web/src/delete.ts`")) || !bytes.Contains(history, []byte("schema and client changed")) {
		t.Fatal("refresh did not preserve the review rationale and change paths")
	}
}

func TestCoverageIncludesContractsAndExcludesArtifactsAndSecrets(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"specs/018-test/spec.md", "docs/design/HLD.md", "docs/runbooks/deployment.md", "docs/verification/proof.json", "AGENTS.md", "Makefile", "compose.yaml", ".env.example", ".github/workflows/ci.yml", "web/package-lock.json", "web/tests/test.mjs", "scripts/new-tool.py", "future-module/new.go", ".vite-config.json", "web/.vite-source/entry.ts"} {
		writeFixture(t, root, name, "covered\n")
	}
	refreshFixture(t, root)
	manifestBefore, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git/config", ".local/secrets", "bin/moni-aigate", "web/node_modules/test/index.js", "web/dist/index.html", "web/dist/assets/app.js", "web/tsconfig.tsbuildinfo", "scripts/__pycache__/tool.pyc", ".env", "web/.env.local", "web/.env.production", "docs/architecture/.architecture-temporary"} {
		writeFixture(t, root, name, "generated or private\n")
	}
	cacheNames := []string{".vite/deps/_metadata.json", ".vite/deps/package.json", "web/.vite/deps/chunk.js", "scripts/tool/.vite/cache.json"}
	for _, name := range cacheNames {
		writeFixture(t, root, name, "generated Vite cache\n")
	}
	if err := run(root, false, false, "", fixedTime, new(bytes.Buffer)); err != nil {
		t.Fatalf("generated/private files should not stale architecture: %v", err)
	}
	for _, name := range cacheNames {
		writeFixture(t, root, name, "rebuilt Vite cache\n")
	}
	if err := run(root, false, false, "", fixedTime, new(bytes.Buffer)); err != nil {
		t.Fatalf("Vite cache modifications should not stale architecture: %v", err)
	}
	for _, name := range cacheNames {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(root, false, false, "", fixedTime, new(bytes.Buffer)); err != nil {
		t.Fatalf("Vite cache deletions should not stale architecture: %v", err)
	}
	manifestAfter, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil || !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatal("cache changes must not rewrite the manifest")
	}
	writeFixture(t, root, "web/.vite-source/entry.ts", "revised real source\n")
	var output bytes.Buffer
	if err := run(root, false, false, "", fixedTime, &output); err == nil || !strings.Contains(output.String(), "modified web/.vite-source/entry.ts") {
		t.Fatal("similarly named real source must remain covered")
	}
	writeFixture(t, root, "web/.vite-source/entry.ts", "covered\n")
	writeFixture(t, root, "specs/018-test/spec.md", "revised design\n")
	if err := run(root, false, false, "", fixedTime, new(bytes.Buffer)); err == nil {
		t.Fatal("design changes must require architecture review")
	}
}

func TestRefreshRequiresExplicitReviewAndReason(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		reviewed     bool
	}{
		{"no confirmation", "implementation changed", false},
		{"empty reason", "  ", true},
		{"multiline reason", "change\nforged history", true},
		{"oversized reason", strings.Repeat("x", 1001), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			if err := run(root, true, tc.reviewed, tc.reason, fixedTime, new(bytes.Buffer)); err == nil {
				t.Fatal("refresh without explicit review contract was accepted")
			}
			if _, err := os.Stat(filepath.Join(root, manifestPath)); !os.IsNotExist(err) {
				t.Fatal("invalid refresh wrote a baseline")
			}
		})
	}
}

func TestArchitectureSupportingDocumentsAndImagesRequireReview(t *testing.T) {
	root := fixture(t)
	refreshFixture(t, root)
	names := []string{"docs/architecture/decisions.md", "docs/architecture/images/deployment.png"}
	for _, kind := range []string{"added", "modified", "deleted"} {
		for _, name := range names {
			if kind == "deleted" {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(t, root, name, kind+" architecture support content\n")
			}
		}
		var output bytes.Buffer
		if err := run(root, false, false, "", fixedTime, &output); err == nil {
			t.Fatalf("%s architecture support files bypassed review", kind)
		}
		for _, name := range names {
			if !strings.Contains(output.String(), kind+" "+name) {
				t.Fatalf("missing %s %s in changes: %s", kind, name, output.String())
			}
		}
		if err := run(root, true, true, kind+" supporting diagrams and decisions reviewed", fixedTime, &output); err != nil {
			t.Fatal(err)
		}
		if err := run(root, false, false, "", fixedTime, &output); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchitectureAndHistoryEditsRequireReview(t *testing.T) {
	for _, name := range []string{documentPath, historyPath} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			refreshFixture(t, root)
			data, _ := os.ReadFile(filepath.Join(root, name))
			writeFixture(t, root, name, string(data)+"\nChanged prose.\n")
			if err := run(root, false, false, "", fixedTime, new(bytes.Buffer)); err == nil {
				t.Fatal("architecture edits must not bypass review")
			}
		})
	}
}

func TestRejectsBrokenUnsafeLinksAndMissingSections(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"broken link", "../../internal/test.go", "../../internal/missing.go"},
		{"outside repository", "../../internal/test.go", "../../../outside.go"},
		{"absolute link", "../../internal/test.go", "/etc/passwd"},
		{"file scheme", "../../internal/test.go", "file:///etc/passwd"},
		{"missing marker", "<!-- architecture:data -->", "<!-- removed -->"},
		{"missing manifest link", "](architecture-manifest.json)", "](#elsewhere)"},
		{"missing diagram", "```mermaid", "```text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			doc, _ := os.ReadFile(filepath.Join(root, documentPath))
			writeFixture(t, root, documentPath, strings.ReplaceAll(string(doc), tc.from, tc.to))
			if err := run(root, true, true, "reviewed", fixedTime, new(bytes.Buffer)); err == nil {
				t.Fatal("invalid architecture document was accepted")
			}
		})
	}
}

func TestRejectsSymlinkInputsAndLinkEscapes(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root := fixture(t)
			outside := t.TempDir()
			writeFixture(t, outside, "other.go", "private\n")
			target := filepath.Join(outside, "other.go")
			link := filepath.Join(root, "internal", "link.go")
			if directory {
				target, link = outside, filepath.Join(root, "internal", "linked")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := inventory(root); err == nil {
				t.Fatal("inventory must reject symlink inputs")
			}
			doc, _ := os.ReadFile(filepath.Join(root, documentPath))
			ref := "../../internal/link.go"
			if directory {
				ref = "../../internal/linked/other.go"
			}
			writeFixture(t, root, documentPath, string(doc)+"\n[Escape]("+ref+")\n")
			if _, err := validateDocument(root); err == nil {
				t.Fatal("architecture links must not escape through symlinks")
			}
		})
	}
}

func TestRejectsCorruptedManifest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*manifest)
	}{
		{"snapshot mismatch", func(m *manifest) { m.Files[0].SHA256 = strings.Repeat("0", 64) }},
		{"unsafe path", func(m *manifest) { m.Files[0].Path = "../escape.go"; m.SnapshotSHA256 = snapshotHash(m.Files) }},
		{"duplicate path", func(m *manifest) { m.Files = append(m.Files, m.Files[0]); m.SnapshotSHA256 = snapshotHash(m.Files) }},
		{"unknown coverage", func(m *manifest) { m.Coverage = "ignore-new-files" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			refreshFixture(t, root)
			m, err := readManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(&m)
			data, _ := json.Marshal(m)
			writeFixture(t, root, manifestPath, string(data))
			for _, refresh := range []bool{false, true} {
				if err := run(root, refresh, true, "reviewed", fixedTime, new(bytes.Buffer)); err == nil {
					t.Fatal("corrupt manifest must not be accepted or silently replaced")
				}
			}
		})
	}
}

func TestStableInventoryAndExternalLinks(t *testing.T) {
	root := fixture(t)
	writeFixture(t, root, "internal/z.go", "package z\n")
	writeFixture(t, root, "internal/a.go", "package a\n")
	doc, _ := os.ReadFile(filepath.Join(root, documentPath))
	writeFixture(t, root, documentPath, string(doc)+"\n[Web](https://example.com/reference) [Anchor](#architecture)\n")
	refreshFixture(t, root)
	m, err := readManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 3 || m.Files[0].Path != "internal/a.go" || m.Files[2].Path != "internal/z.go" {
		t.Fatalf("inventory order is unstable: %+v", m.Files)
	}
}
