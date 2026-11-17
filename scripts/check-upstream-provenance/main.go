package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type fileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type noticeRecord struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	AbsenceReason string `json:"absence_reason"`
}
type vendoredFile struct {
	UpstreamPath   string `json:"upstream_path"`
	UpstreamSHA256 string `json:"upstream_sha256"`
	LocalPath      string `json:"local_path"`
	LocalSHA256    string `json:"local_sha256"`
	Status         string `json:"status"`
}
type component struct {
	ID                 string         `json:"id"`
	SourceMode         string         `json:"source_mode"`
	Repository         string         `json:"repository"`
	Commit             string         `json:"commit"`
	ReferencePaths     []string       `json:"reference_paths"`
	License            fileRecord     `json:"license"`
	Notice             noticeRecord   `json:"notice"`
	AdditionalNotices  []fileRecord   `json:"additional_notices"`
	SourceRecord       string         `json:"source_record"`
	ModificationRecord string         `json:"modification_record"`
	SourceOffer        string         `json:"source_offer"`
	VendoredRoots      []string       `json:"vendored_roots"`
	VendoredFiles      []vendoredFile `json:"vendored_files"`
}
type manifest struct {
	SchemaVersion int         `json:"schema_version"`
	Components    []component `json:"components"`
}

var hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var componentID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var jsonKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func safePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "/../")
}
func noSymlink(root, rel string) error {
	if !safePath(rel) {
		return fmt.Errorf("unsafe path %q", rel)
	}
	current := root
	for _, part := range strings.Split(rel, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed: %s", rel)
		}
	}
	return nil
}
func readRegular(root, rel string) ([]byte, error) {
	if err := noSymlink(root, rel); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", rel)
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	return body, nil
}
func digest(body []byte) string { return fmt.Sprintf("%x", sha256.Sum256(body)) }
func checkFile(root string, record fileRecord) error {
	if !safePath(record.Path) || !hex64.MatchString(record.SHA256) {
		return fmt.Errorf("invalid file record %q", record.Path)
	}
	body, err := readRegular(root, record.Path)
	if err != nil {
		return err
	}
	if digest(body) != record.SHA256 {
		return fmt.Errorf("digest drift: %s", record.Path)
	}
	return nil
}
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for dec.More() {
					key, err := dec.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] || !jsonKey.MatchString(name) {
						return fmt.Errorf("duplicate or invalid JSON key %v", key)
					}
					seen[name] = true
					if err = walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			case '[':
				for dec.More() {
					if err = walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			}
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func loadManifest(root string) (manifest, error) {
	var value manifest
	body, err := readRegular(root, "third_party/provenance.json")
	if err != nil {
		return value, err
	}
	if err = rejectDuplicateKeys(body); err != nil {
		return value, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&value); err != nil {
		return value, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return value, fmt.Errorf("trailing manifest JSON")
	}
	return value, nil
}
func validateManifest(root string, value manifest) error {
	if value.SchemaVersion != 1 || len(value.Components) == 0 {
		return fmt.Errorf("unsupported or empty provenance manifest")
	}
	ids := map[string]bool{}
	for _, c := range value.Components {
		if !componentID.MatchString(c.ID) || ids[c.ID] || !safePath(c.SourceRecord) || !safePath(c.ModificationRecord) || !safePath(c.SourceOffer) {
			return fmt.Errorf("invalid component identity or record: %s", c.ID)
		}
		ids[c.ID] = true
		if c.SourceMode != "reference_only" && c.SourceMode != "vendored" || !strings.HasPrefix(c.Repository, "https://") || strings.ContainsAny(c.Repository, "\r\n @") || !hex40.MatchString(c.Commit) {
			return fmt.Errorf("invalid source identity: %s", c.ID)
		}
		if len(c.ReferencePaths) == 0 {
			return fmt.Errorf("missing reference paths: %s", c.ID)
		}
		for _, p := range c.ReferencePaths {
			if !safePath(p) {
				return fmt.Errorf("unsafe upstream path: %s", c.ID)
			}
		}
		for _, record := range []string{c.SourceRecord, c.ModificationRecord, c.SourceOffer} {
			if _, err := readRegular(root, record); err != nil {
				return err
			}
		}
		if err := checkFile(root, c.License); err != nil {
			return err
		}
		if c.Notice.Path == "" {
			if c.Notice.SHA256 != "" || c.Notice.AbsenceReason == "" {
				return fmt.Errorf("notice absence needs a reason: %s", c.ID)
			}
		} else {
			if err := checkFile(root, fileRecord{Path: c.Notice.Path, SHA256: c.Notice.SHA256}); err != nil {
				return err
			}
		}
		for _, record := range c.AdditionalNotices {
			if err := checkFile(root, record); err != nil {
				return err
			}
		}
		if c.SourceMode == "reference_only" && (len(c.VendoredRoots) != 0 || len(c.VendoredFiles) != 0) {
			return fmt.Errorf("reference-only component has vendored inventory: %s", c.ID)
		}
		if c.SourceMode == "vendored" && (len(c.VendoredRoots) == 0 || len(c.VendoredFiles) == 0) {
			return fmt.Errorf("vendored component has no inventory: %s", c.ID)
		}
		if err := validateVendored(root, c); err != nil {
			return err
		}
	}
	return nil
}
func validateVendored(root string, c component) error {
	roots := map[string]bool{}
	for _, r := range c.VendoredRoots {
		if !safePath(r) || roots[r] {
			return fmt.Errorf("invalid vendored root: %s", r)
		}
		roots[r] = true
	}
	declared := map[string]vendoredFile{}
	for _, f := range c.VendoredFiles {
		if !safePath(f.UpstreamPath) || !safePath(f.LocalPath) || !hex64.MatchString(f.UpstreamSHA256) || !hex64.MatchString(f.LocalSHA256) || declared[f.LocalPath].LocalPath != "" {
			return fmt.Errorf("invalid vendored file: %s", f.LocalPath)
		}
		inside := false
		for r := range roots {
			inside = inside || strings.HasPrefix(f.LocalPath, r+"/")
		}
		if !inside {
			return fmt.Errorf("file outside vendored roots: %s", f.LocalPath)
		}
		if f.Status != "unchanged" && f.Status != "modified" {
			return fmt.Errorf("invalid vendored status: %s", f.LocalPath)
		}
		if f.Status == "unchanged" && f.UpstreamSHA256 != f.LocalSHA256 || f.Status == "modified" && f.UpstreamSHA256 == f.LocalSHA256 {
			return fmt.Errorf("inconsistent vendored modification: %s", f.LocalPath)
		}
		body, err := readRegular(root, f.LocalPath)
		if err != nil {
			return err
		}
		if digest(body) != f.LocalSHA256 {
			return fmt.Errorf("vendored digest drift: %s", f.LocalPath)
		}
		declared[f.LocalPath] = f
	}
	for r := range roots {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(r)), func(full string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in vendored root: %s", rel)
			}
			if d.IsDir() {
				return nil
			}
			if _, ok := declared[rel]; !ok {
				return fmt.Errorf("extra vendored file: %s", rel)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func validateFixedComponents(value manifest) error {
	expected := map[string]struct{ repository, commit, record string }{
		"new-api": {"https://github.com/QuantumNous/new-api", "e449cc700eb569cdfc2199cb73e97de46944f43c", "third_party/new-api/SOURCE.md"},
		"sub2api": {"https://github.com/Wei-Shaw/sub2api", "10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "third_party/sub2api/SOURCE.md"},
	}
	components := map[string]component{}
	for _, c := range value.Components {
		components[c.ID] = c
	}
	for id, want := range expected {
		c, ok := components[id]
		if !ok || c.SourceMode != "reference_only" || c.Repository != want.repository || c.Commit != want.commit || c.SourceRecord != want.record || c.ModificationRecord != want.record || c.SourceOffer != want.record {
			return fmt.Errorf("fixed component identity drift: %s", id)
		}
	}
	return nil
}
func validateComponentInventory(root string, value manifest) error {
	components := map[string]bool{}
	for _, c := range value.Components {
		components[c.ID] = true
	}
	entries, err := os.ReadDir(filepath.Join(root, "third_party"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("third-party symlink: %s", entry.Name())
		}
		if entry.IsDir() {
			if !components[entry.Name()] {
				return fmt.Errorf("unlisted third-party component: %s", entry.Name())
			}
			continue
		}
		if entry.Name() != "README.md" && entry.Name() != "provenance.json" {
			return fmt.Errorf("unlisted third-party file: %s", entry.Name())
		}
	}
	for id := range components {
		info, err := os.Lstat(filepath.Join(root, "third_party", id))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("missing component directory: %s", id)
		}
	}
	return nil
}
func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("check-upstream-provenance", flag.ContinueOnError)
	flags.SetOutput(errOut)
	root := flags.String("root", ".", "repository root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	value, err := loadManifest(*root)
	if err != nil {
		return err
	}
	if err = validateManifest(*root, value); err != nil {
		return err
	}
	if err = validateFixedComponents(value); err != nil {
		return err
	}
	if err = validateComponentInventory(*root, value); err != nil {
		return err
	}
	hashes := map[string]string{"third_party/new-api/LICENSE": "8486a10c4393cee1c25392769ddd3b2d6c242d6ec7928e1414efff7dfb2f07ef", "third_party/new-api/NOTICE": "528067fcdf4f9d7e3fdb489d02cbdd36a0efa63fc2eb1686340612c26beb9f33", "third_party/new-api/THIRD-PARTY-LICENSES.md": "3848811f99c7b7ad802eea23775ccd262228c980f81eef64027f587689b0841d", "third_party/sub2api/LICENSE": "a5681bf9b05db14d86776930017c647ad9e6e56ff6bbcfdf21e5848288dfaf1b"}
	for rel, expected := range hashes {
		body, err := readRegular(*root, rel)
		if err != nil {
			return err
		}
		if digest(body) != expected {
			return fmt.Errorf("upstream license content changed: %s", rel)
		}
	}
	contains := map[string][]string{"third_party/new-api/SOURCE.md": {"e449cc700eb569cdfc2199cb73e97de46944f43c", "Local upstream source modifications: **none**", "docs/verification/phase0-upstream-provenance.md", "https://github.com/QuantumNous/new-api"}, "third_party/sub2api/SOURCE.md": {"10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13", "Local upstream source modifications: **none**", "docs/verification/phase0-upstream-provenance.md"}, "deploy/external-upstreams.compose.yaml": {"local/new-api:e449cc7", "weishaw/sub2api@sha256:868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13", "new-api-db", "sub2api-db"}, "docs/verification/phase0-external-upstream.md": {"e449cc700eb569cdfc2199cb73e97de46944f43c", "10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "supplier_authenticated\":false"}, "third_party/new-api/NOTICE": {"https://github.com/QuantumNous/new-api", "Frontend design and development by New API contributors.", "THIRD-PARTY-LICENSES.md"}, "docs/verification/phase0-upstream-provenance.md": {"e449cc700eb569cdfc2199cb73e97de46944f43c", "10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "upstream-provenance-check"}}
	for rel, needles := range contains {
		body, err := readRegular(*root, rel)
		if err != nil {
			return err
		}
		for _, needle := range needles {
			if !strings.Contains(string(body), needle) {
				return fmt.Errorf("provenance requirement missing in %s", rel)
			}
		}
	}
	fmt.Fprintln(out, "PASS upstream provenance metadata/reference integrity only; no supplier, legal, SBOM or production attestation")
	return nil
}
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
