// Command check-architecture makes architecture review part of the source-change contract.
// It deliberately does not infer whether architectural prose is semantically correct.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	documentPath = "docs/architecture/README.md"
	historyPath  = "docs/architecture/changes.md"
	manifestPath = "docs/architecture/architecture-manifest.json"
	coverage     = "source-and-design-v1"
)

var sections = []string{"context", "modules", "protocols", "request", "data", "security", "deployment", "status", "evolution"}
var markdownLink = regexp.MustCompile(`\[[^\]\n]*\]\(([^\s)]+)\)`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type fileDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Schema             int          `json:"schema"`
	Coverage           string       `json:"coverage"`
	ReviewedAt         string       `json:"reviewed_at"`
	ReviewReason       string       `json:"review_reason"`
	SnapshotSHA256     string       `json:"snapshot_sha256"`
	ArchitectureSHA256 string       `json:"architecture_sha256"`
	HistorySHA256      string       `json:"history_sha256"`
	Files              []fileDigest `json:"files"`
}

type change struct{ Kind, Path string }

func main() {
	root := flag.String("root", ".", "repository root")
	refresh := flag.Bool("refresh", false, "explicitly refresh the reviewed snapshot and append architecture history")
	reviewed := flag.Bool("reviewed", false, "confirm architecture prose, source links and capability status were reviewed")
	reason := flag.String("reason", "", "single-line description of the change and its architectural impact")
	flag.Parse()
	if flag.NArg() != 0 || (!*refresh && (*reviewed || *reason != "")) {
		fmt.Fprintln(os.Stderr, "reviewed/reason are only valid with --refresh; positional arguments are not accepted")
		os.Exit(2)
	}
	if err := run(*root, *refresh, *reviewed, *reason, time.Now().UTC(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "architecture:", err)
		os.Exit(1)
	}
}

func run(root string, refresh, reviewed bool, reason string, now time.Time, out io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("repository root is not a directory")
	}
	if refresh && (!reviewed || strings.TrimSpace(reason) == "" || len(reason) > 1000 || strings.ContainsAny(reason, "\r\n")) {
		return fmt.Errorf("refresh requires --reviewed and a nonempty single-line --reason (at most 1000 bytes); review %s first", documentPath)
	}
	doc, err := validateDocument(root)
	if err != nil {
		return err
	}
	history, err := readRegular(root, historyPath)
	if err != nil {
		return err
	}
	files, err := inventory(root)
	if err != nil {
		return err
	}
	previous, err := readManifest(root)
	if err != nil && !(refresh && errors.Is(err, fs.ErrNotExist)) {
		return fmt.Errorf("%w; review the architecture and use make architecture-refresh REASON='change and architectural impact'", err)
	}
	changes := difference(previous.Files, files)
	docChanged := previous.ArchitectureSHA256 != hash(doc)
	historyChanged := previous.HistorySHA256 != hash(history)
	if !refresh {
		if len(changes) != 0 || docChanged || historyChanged {
			for _, c := range changes {
				fmt.Fprintf(out, "%s %s\n", c.Kind, c.Path)
			}
			if docChanged {
				fmt.Fprintln(out, "modified", documentPath)
			}
			if historyChanged {
				fmt.Fprintln(out, "modified", historyPath)
			}
			return fmt.Errorf("architecture snapshot is stale; review prose/status/diagrams, then run make architecture-refresh REASON='change and architectural impact'")
		}
		fmt.Fprintf(out, "architecture snapshot verified: %d files, %s\n", len(files), previous.SnapshotSHA256[:12])
		return nil
	}
	if len(changes) == 0 && !docChanged && !historyChanged {
		fmt.Fprintln(out, "architecture snapshot already current; no history entry added")
		return nil
	}
	next := manifest{Schema: 1, Coverage: coverage, ReviewedAt: now.Format(time.RFC3339), ReviewReason: strings.TrimSpace(reason), Files: files, SnapshotSHA256: snapshotHash(files), ArchitectureSHA256: hash(doc)}
	var entry strings.Builder
	fmt.Fprintf(&entry, "\n## %s · %s\n\n%s\n\n", next.ReviewedAt, next.SnapshotSHA256[:12], next.ReviewReason)
	fmt.Fprintf(&entry, "已显式复核统一架构正文、能力状态和源码链接。快照覆盖 %d 个源文件；", len(files))
	if previous.Schema == 0 {
		entry.WriteString("首次建立基线，完整路径与摘要见机器清单。\n")
	} else {
		fmt.Fprintf(&entry, "本次 %d 个源文件变化，架构正文变化：%t。\n", len(changes), docChanged)
		if len(changes) > 0 {
			entry.WriteString("\n<details><summary>源文件变化</summary>\n\n")
			for _, c := range changes {
				fmt.Fprintf(&entry, "- %s `%s`\n", c.Kind, c.Path)
			}
			entry.WriteString("\n</details>\n")
		}
	}
	newHistory := append(append([]byte(nil), history...), []byte(entry.String())...)
	next.HistorySHA256 = hash(newHistory)
	encoded, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	// Avoid recording a moving source tree as reviewed. The final read-only check
	// also catches edits after this guard; refresh never runs in default CI.
	latest, err := inventory(root)
	if err != nil {
		return err
	}
	latestDoc, err := validateDocument(root)
	if err != nil {
		return err
	}
	latestHistory, err := readRegular(root, historyPath)
	if err != nil {
		return err
	}
	if len(difference(files, latest)) != 0 || !bytes.Equal(doc, latestDoc) || !bytes.Equal(history, latestHistory) {
		return fmt.Errorf("source or architecture changed during refresh; wait for parallel edits to finish and review again")
	}
	if err := atomicWrite(root, historyPath, newHistory); err != nil {
		return err
	}
	if err := atomicWrite(root, manifestPath, append(encoded, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(out, "architecture snapshot refreshed: %d files, %s; review recorded in %s\n", len(files), next.SnapshotSHA256[:12], historyPath)
	return nil
}

func excluded(name string, dir bool) bool {
	for _, component := range strings.Split(name, "/") {
		switch component {
		case ".git", ".local", "node_modules", "bin", ".cache", ".vite", "coverage", "vendor", "__pycache__":
			return true
		}
	}
	base := path.Base(name)
	privateEnv := base == ".env" || (strings.HasPrefix(base, ".env.") && base != ".env.example")
	return name == "web/dist" || strings.HasPrefix(name, "web/dist/") || strings.HasPrefix(name, "docs/architecture/.architecture-") || strings.HasSuffix(name, ".tsbuildinfo") || strings.HasSuffix(name, ".DS_Store") || strings.HasSuffix(name, ".pyc") || (!dir && privateEnv)
}

func covered(name string) bool {
	if excluded(name, false) || name == documentPath || name == historyPath || name == manifestPath {
		return false
	}
	for _, prefix := range []string{"cmd/", "internal/", "migrations/", "scripts/", "deploy/", ".github/", "web/", "specs/", "docs/architecture/", "docs/design/", "docs/runbooks/", "docs/verification/", "docs/decisions/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "AGENTS.md", "README.md", "Makefile", "Dockerfile", ".dockerignore", ".gitignore", ".env.example", "go.mod", "go.sum":
		return true
	}
	switch path.Ext(name) {
	case ".go", ".mod", ".sum", ".json", ".yaml", ".yml", ".toml", ".sh", ".py", ".sql", ".conf", ".ts", ".js", ".mjs", ".vue", ".css", ".html", ".proto", ".graphql", ".rs", ".c", ".h", ".lock", ".xml":
		return true
	}
	return false
}

func inventory(root string) ([]fileDigest, error) {
	files := []fileDigest{}
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && covered(rel) {
			data, err := readRegular(root, rel)
			if err != nil {
				return err
			}
			files = append(files, fileDigest{Path: rel, SHA256: hash(data)})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func readManifest(root string) (manifest, error) {
	var m manifest
	data, err := readRegular(root, manifestPath)
	if err != nil {
		return m, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid architecture manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return m, fmt.Errorf("invalid architecture manifest: trailing JSON")
	}
	if m.Schema != 1 || m.Coverage != coverage || !digestPattern.MatchString(m.ArchitectureSHA256) || !digestPattern.MatchString(m.HistorySHA256) || len(m.Files) == 0 || strings.TrimSpace(m.ReviewReason) == "" {
		return m, fmt.Errorf("invalid architecture manifest metadata")
	}
	if _, err := time.Parse(time.RFC3339, m.ReviewedAt); err != nil {
		return m, fmt.Errorf("invalid architecture review time")
	}
	for i, f := range m.Files {
		if !validPath(f.Path) || !covered(f.Path) || !digestPattern.MatchString(f.SHA256) || (i > 0 && m.Files[i-1].Path >= f.Path) {
			return m, fmt.Errorf("invalid, uncovered or unsorted manifest entry %q", f.Path)
		}
	}
	if m.SnapshotSHA256 != snapshotHash(m.Files) {
		return m, fmt.Errorf("architecture manifest snapshot digest is inconsistent")
	}
	return m, nil
}

func validateDocument(root string) ([]byte, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	doc, err := readRegular(root, documentPath)
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		marker := "<!-- architecture:" + section + " -->"
		if bytes.Count(doc, []byte(marker)) != 1 {
			return nil, fmt.Errorf("architecture document must contain exactly one %s marker", section)
		}
	}
	if bytes.Count(doc, []byte("```mermaid")) < 3 {
		return nil, fmt.Errorf("architecture document requires context, lifecycle and deployment/data diagrams")
	}
	for _, match := range markdownLink.FindAllSubmatch(doc, -1) {
		raw := string(match[1])
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid architecture link %q", raw)
		}
		if u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto" {
			continue
		}
		if u.Scheme != "" || u.Host != "" || strings.Contains(u.Path, "\\") || path.IsAbs(u.Path) {
			return nil, fmt.Errorf("unsafe architecture link %q", raw)
		}
		if u.Path == "" {
			continue
		}
		target := path.Clean(path.Join(path.Dir(documentPath), u.Path))
		if !validPath(target) {
			return nil, fmt.Errorf("architecture link escapes repository: %q", raw)
		}
		// The manifest is created on the initial refresh, so only this known
		// generated file may be absent before a baseline exists.
		if target == manifestPath {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(target))
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, fmt.Errorf("broken architecture link %q: %w", raw, err)
		}
		rel, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("architecture link escapes repository: %q", raw)
		}
	}
	if !bytes.Contains(doc, []byte("](changes.md)")) || !bytes.Contains(doc, []byte("](architecture-manifest.json)")) {
		return nil, fmt.Errorf("architecture document must link changes.md and architecture-manifest.json")
	}
	return doc, nil
}

func validPath(name string) bool {
	return name != "" && name != "." && path.Clean(name) == name && !path.IsAbs(name) && name != ".." && !strings.HasPrefix(name, "../") && !strings.Contains(name, "\\")
}

func readRegular(root, name string) ([]byte, error) {
	if !validPath(name) {
		return nil, fmt.Errorf("unsafe path %q", name)
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	// Inspect every component: a directory symlink would otherwise bypass the
	// leaf Lstat check and could import content outside this repository.
	current := root
	for _, component := range strings.Split(name, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symbolic links are not accepted in architecture inputs: %s", name)
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("architecture input is not a regular file: %s", name)
	}
	return os.ReadFile(full)
}

func atomicWrite(root, name string, data []byte) error {
	full := filepath.Join(root, filepath.FromSlash(name))
	f, err := os.CreateTemp(filepath.Dir(full), ".architecture-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), full)
}

func difference(before, after []fileDigest) []change {
	old := make(map[string]string, len(before))
	for _, f := range before {
		old[f.Path] = f.SHA256
	}
	result := []change{}
	for _, f := range after {
		if sha, ok := old[f.Path]; !ok {
			result = append(result, change{"added", f.Path})
		} else if sha != f.SHA256 {
			result = append(result, change{"modified", f.Path})
		}
		delete(old, f.Path)
	}
	for name := range old {
		result = append(result, change{"deleted", name})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func hash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func snapshotHash(files []fileDigest) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\n", f.Path, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}
