package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtureFile(t *testing.T, root, rel, body string) fileRecord {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return fileRecord{Path: rel, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body)))}
}
func fixtureManifest(t *testing.T, root string) manifest {
	t.Helper()
	license := writeFixtureFile(t, root, "third_party/x/LICENSE", "license")
	writeFixtureFile(t, root, "third_party/x/SOURCE.md", "source record")
	return manifest{SchemaVersion: 1, Components: []component{{ID: "x", SourceMode: "reference_only", Repository: "https://github.com/example/x", Commit: strings.Repeat("a", 40), ReferencePaths: []string{"README.md"}, License: license, Notice: noticeRecord{AbsenceReason: "No upstream NOTICE"}, SourceRecord: "third_party/x/SOURCE.md", ModificationRecord: "third_party/x/SOURCE.md", SourceOffer: "third_party/x/SOURCE.md", VendoredRoots: []string{}, VendoredFiles: []vendoredFile{}}}}
}
func writeManifest(t *testing.T, root string, value manifest) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "third_party/provenance.json", string(body)+"\n")
}
func TestProvenanceReferenceOnlyManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifest)
	}{
		{"source mode", func(v *manifest) { v.Components[0].SourceMode = "unknown" }},
		{"floating commit", func(v *manifest) { v.Components[0].Commit = "main" }},
		{"parent path", func(v *manifest) { v.Components[0].ReferencePaths = []string{"../secret"} }},
		{"exact parent", func(v *manifest) { v.Components[0].ReferencePaths = []string{".."} }},
		{"unexpected vendored inventory", func(v *manifest) {
			v.Components[0].VendoredFiles = []vendoredFile{{LocalPath: "third_party/x/src/main.go"}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			value := fixtureManifest(t, root)
			tc.mutate(&value)
			if err := validateManifest(root, value); err == nil {
				t.Fatal("invalid reference-only manifest passed")
			}
		})
	}
	t.Run("license drift", func(t *testing.T) {
		root := t.TempDir()
		value := fixtureManifest(t, root)
		if err := os.WriteFile(filepath.Join(root, "third_party/x/LICENSE"), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateManifest(root, value); err == nil {
			t.Fatal("license drift passed")
		}
	})
}
func TestProvenanceStrictJSONAndSymlink(t *testing.T) {
	if err := rejectDuplicateKeys([]byte(`{"schema_version":1,"schema_version":1,"components":[]}`)); err == nil {
		t.Fatal("duplicate JSON key passed")
	}
	for _, raw := range []string{`{"schema_version":1,"components":[],"COMMIT":"x"}`, `{"schema_version":1,"components":[{"commit":"a","COMMIT":"b"}]}`} {
		if err := rejectDuplicateKeys([]byte(raw)); err == nil {
			t.Fatal("case-variant JSON key passed")
		}
	}
	root := t.TempDir()
	value := fixtureManifest(t, root)
	writeManifest(t, root, value)
	body, err := os.ReadFile(filepath.Join(root, "third_party/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.TrimSuffix(strings.TrimSpace(string(body)), "}") + ",\"unknown\":true}"
	if err = os.WriteFile(filepath.Join(root, "third_party/provenance.json"), []byte(unknown), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadManifest(root); err == nil {
		t.Fatal("unknown field passed")
	}
	writeManifest(t, root, value)
	license := filepath.Join(root, "third_party/x/LICENSE")
	if err = os.Remove(license); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err = os.WriteFile(outside, []byte("license"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, license); err != nil {
		t.Fatal(err)
	}
	if err = validateManifest(root, value); err == nil {
		t.Fatal("symlink passed")
	}
}
func TestProvenanceRejectsExactParentPaths(t *testing.T) {
	for _, value := range []string{"..", ".", "../x", "x/../y", "/tmp/x"} {
		if safePath(value) {
			t.Fatalf("unsafe path accepted: %q", value)
		}
	}
}
func TestProvenanceVendoredClosedInventory(t *testing.T) {
	root := t.TempDir()
	value := fixtureManifest(t, root)
	local := "third_party/x/src/main.go"
	body := "package x\n"
	record := writeFixtureFile(t, root, local, body)
	value.Components[0].SourceMode = "vendored"
	value.Components[0].VendoredRoots = []string{"third_party/x/src"}
	value.Components[0].VendoredFiles = []vendoredFile{{UpstreamPath: "main.go", UpstreamSHA256: record.SHA256, LocalPath: local, LocalSHA256: record.SHA256, Status: "unchanged"}}
	writeManifest(t, root, value)
	loaded, err := loadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateManifest(root, loaded); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(local)), []byte("package x // drift\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = validateManifest(root, loaded); err == nil {
		t.Fatal("vendored drift passed")
	}
	writeFixtureFile(t, root, local, body)
	writeFixtureFile(t, root, "third_party/x/src/extra.go", "package x\n")
	if err = validateManifest(root, loaded); err == nil {
		t.Fatal("extra vendored file passed")
	}
}
func TestProvenanceRealRepositoryIsReadOnly(t *testing.T) {
	root := "../.."
	before, err := os.ReadFile(filepath.Join(root, "third_party/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err = run([]string{"--root", root}, &out, &errOut); err != nil {
		t.Fatalf("real repository: %v %s", err, errOut.String())
	}
	after, err := os.ReadFile(filepath.Join(root, "third_party/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("checker modified provenance manifest")
	}
	if !strings.Contains(out.String(), "no supplier") {
		t.Fatal("checker overstated its evidence scope")
	}
}

func copyRealProvenanceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	paths := []string{"third_party/provenance.json", "third_party/new-api/LICENSE", "third_party/new-api/NOTICE", "third_party/new-api/THIRD-PARTY-LICENSES.md", "third_party/new-api/SOURCE.md", "third_party/sub2api/LICENSE", "third_party/sub2api/SOURCE.md", "deploy/external-upstreams.compose.yaml", "docs/verification/phase0-external-upstream.md", "docs/verification/phase0-upstream-provenance.md"}
	for _, rel := range paths {
		body, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, rel, string(body))
	}
	return root
}
func TestProvenanceRunRejectsCrossRecordDriftAndUnlistedComponents(t *testing.T) {
	root := copyRealProvenanceFixture(t)
	var out bytes.Buffer
	if err := run([]string{"--root", root}, &out, &out); err != nil {
		t.Fatalf("fixture baseline: %v", err)
	}
	value, err := loadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range value.Components {
		if value.Components[i].ID == "new-api" {
			value.Components[i].Commit = strings.Repeat("f", 40)
		}
	}
	writeManifest(t, root, value)
	if err = run([]string{"--root", root}, &out, &out); err == nil || !strings.Contains(err.Error(), "fixed component identity drift") {
		t.Fatalf("manifest/SOURCE drift passed: %v", err)
	}
	root = copyRealProvenanceFixture(t)
	writeFixtureFile(t, root, "third_party/unlisted/copied.go", "package unlisted\n")
	if err = run([]string{"--root", root}, &out, &out); err == nil || !strings.Contains(err.Error(), "unlisted third-party component") {
		t.Fatalf("unlisted component passed: %v", err)
	}
}
