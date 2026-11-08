// financial-write-batch-qa runs the fixed ordinary capacity test binaries in
// a deterministic, serial before/after order. It records the actual test
// reports and process logs. It does not generate a result when a binary fails.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var sampleOrder = []string{"before1", "after1", "after2", "before2", "before3", "after3"}

type binaryDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type sourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type sourceDigest struct {
	Root       string       `json:"root"`
	Commit     string       `json:"commit"`
	TreeSHA256 string       `json:"tree_sha256"`
	FileCount  int          `json:"file_count"`
	Files      []sourceFile `json:"files"`
}

type runResult struct {
	Name       string       `json:"name"`
	Pair       string       `json:"pair"`
	Role       string       `json:"role"`
	Binary     binaryDigest `json:"binary"`
	ReportPath string       `json:"report_path"`
	LogPath    string       `json:"log_path"`
	StartedAt  string       `json:"started_at_utc"`
	DurationMS int64        `json:"duration_ms"`
	ExitCode   int          `json:"exit_code"`
	Passed     bool         `json:"passed"`
	Error      string       `json:"error,omitempty"`
}

type manifest struct {
	FormatVersion int                 `json:"format_version"`
	Scope         string              `json:"scope"`
	Database      map[string]string   `json:"database"`
	Command       []string            `json:"command"`
	Order         []string            `json:"order"`
	Pairs         []map[string]string `json:"pairs"`
	Before        binaryDigest        `json:"before"`
	After         binaryDigest        `json:"after"`
	BeforeSource  sourceDigest        `json:"before_source"`
	AfterSource   sourceDigest        `json:"after_source"`
	Runs          []runResult         `json:"runs"`
	Passed        bool                `json:"passed"`
}

func main() {
	var before, after, output, root, beforeSourceRoot, afterSourceRoot, dsn string
	var timeout time.Duration
	flag.StringVar(&before, "before", "", "fixed before test binary")
	flag.StringVar(&after, "after", "", "fixed after test binary")
	flag.StringVar(&output, "output", "", "directory for raw reports and logs")
	flag.StringVar(&root, "root", "", "repository root, defaults to the current directory")
	flag.StringVar(&beforeSourceRoot, "before-source-root", "", "source tree used to build the before binary")
	flag.StringVar(&afterSourceRoot, "after-source-root", "", "source tree used to build the after binary")
	flag.StringVar(&dsn, "database", os.Getenv("TEST_DATABASE_URL"), "dedicated PostgreSQL test URL")
	flag.DurationVar(&timeout, "timeout", 2*time.Minute, "maximum time for each binary")
	flag.Parse()

	if err := run(before, after, output, root, beforeSourceRoot, afterSourceRoot, dsn, timeout); err != nil {
		fmt.Fprintln(os.Stderr, "financial-write-batch-qa:", err)
		os.Exit(1)
	}
}

func run(beforePath, afterPath, output, root, beforeSourceRoot, afterSourceRoot, dsn string, timeout time.Duration) error {
	if beforePath == "" || afterPath == "" {
		return errors.New("-before and -after are required")
	}
	if timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	if err := validateDatabase(dsn); err != nil {
		return err
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("current directory: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("repository root: %w", err)
	}
	if output == "" {
		output = filepath.Join(os.TempDir(), "moni-aigate-financial-write-batch-qa-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("output directory: %w", err)
	}
	if beforeSourceRoot == "" {
		beforeSourceRoot = root
	}
	if afterSourceRoot == "" {
		afterSourceRoot = root
	}
	beforeSourceRoot, err = filepath.Abs(beforeSourceRoot)
	if err != nil {
		return fmt.Errorf("before source root: %w", err)
	}
	afterSourceRoot, err = filepath.Abs(afterSourceRoot)
	if err != nil {
		return fmt.Errorf("after source root: %w", err)
	}
	before, err := digestBinary(beforePath)
	if err != nil {
		return fmt.Errorf("before binary: %w", err)
	}
	after, err := digestBinary(afterPath)
	if err != nil {
		return fmt.Errorf("after binary: %w", err)
	}
	if before.SHA256 == after.SHA256 {
		return errors.New("before and after binaries have the same SHA-256")
	}
	beforeSource, err := digestSource(beforeSourceRoot)
	if err != nil {
		return fmt.Errorf("before source digest: %w", err)
	}
	afterSource, err := digestSource(afterSourceRoot)
	if err != nil {
		return fmt.Errorf("after source digest: %w", err)
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := writeJSON(filepath.Join(output, "before-source-manifest.json"), beforeSource); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(output, "after-source-manifest.json"), afterSource); err != nil {
		return err
	}

	m := manifest{
		FormatVersion: 1,
		Scope:         "serial fixed ordinary test-binary samples; local burst fixture only",
		Database:      map[string]string{"target": "127.0.0.1:55439/moni_test", "role": "moni_test"},
		Command:       []string{"<binary>", "-test.run=^TestCapacityConcurrentStreams$", "-test.count=1", "-test.timeout=45s"},
		Order:         append([]string(nil), sampleOrder...),
		Pairs:         []map[string]string{{"pair": "1", "before": "before1", "after": "after1"}, {"pair": "2", "before": "before2", "after": "after2"}, {"pair": "3", "before": "before3", "after": "after3"}},
		Before:        before,
		After:         after,
		BeforeSource:  beforeSource,
		AfterSource:   afterSource,
	}
	for _, name := range sampleOrder {
		role, binary := "before", before
		if strings.HasPrefix(name, "after") {
			role, binary = "after", after
		}
		pair := string(name[len(name)-1])
		result := runSample(root, output, dsn, timeout, name, pair, role, binary)
		m.Runs = append(m.Runs, result)
		if !result.Passed {
			m.Passed = false
		}
	}
	m.Passed = len(m.Runs) == len(sampleOrder)
	for _, result := range m.Runs {
		m.Passed = m.Passed && result.Passed
	}
	if err := writeJSON(filepath.Join(output, "manifest.json"), m); err != nil {
		return err
	}
	if !m.Passed {
		return fmt.Errorf("one or more fixed samples failed; inspect %s", filepath.Join(output, "manifest.json"))
	}
	fmt.Println(filepath.Join(output, "manifest.json"))
	return nil
}

func runSample(root, output, dsn string, timeout time.Duration, name, pair, role string, binary binaryDigest) runResult {
	started := time.Now().UTC()
	reportPath := filepath.Join(output, name+".report.json")
	logPath := filepath.Join(output, name+".log")
	result := runResult{Name: name, Pair: pair, Role: role, Binary: binary, ReportPath: reportPath, LogPath: logPath, StartedAt: started.Format(time.RFC3339Nano)}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary.Path, "-test.run=^TestCapacityConcurrentStreams$", "-test.count=1", "-test.timeout=45s")
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = withEnv(os.Environ(), map[string]string{
		"TEST_DATABASE_URL":              dsn,
		"MONI_CAPACITY_TEST":             "true",
		"MONI_CAPACITY_CONCURRENCY":      "100",
		"MONI_CAPACITY_DB_POOL":          "32",
		"MONI_CAPACITY_CONTENT_DELAY_MS": "0",
		"MONI_CAPACITY_REPORT":           reportPath,
	})
	err = cmd.Run()
	_ = logFile.Close()
	result.DurationMS = time.Since(started).Milliseconds()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	} else if err != nil {
		result.Error = err.Error()
	}
	if err == nil {
		result.Passed = validateReport(reportPath)
		if !result.Passed && result.Error == "" {
			result.Error = "capacity report failed semantic validation"
		}
	}
	return result
}

func validateDatabase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.Port() != "55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.User.String() != "moni_test" {
		return errors.New("-database must be TEST_DATABASE_URL for moni_test on 127.0.0.1:55439")
	}
	return nil
}

func digestBinary(path string) (binaryDigest, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return binaryDigest{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return binaryDigest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return binaryDigest{}, errors.New("must be a regular executable")
	}
	hash, err := hashFile(path)
	if err != nil {
		return binaryDigest{}, err
	}
	return binaryDigest{Path: path, SHA256: hash, Bytes: info.Size()}, nil
}

func digestSource(root string) (sourceDigest, error) {
	commitRaw, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return sourceDigest{}, fmt.Errorf("git rev-parse: %w", err)
	}
	commit := strings.TrimSpace(string(commitRaw))
	if commit == "" {
		return sourceDigest{}, errors.New("git rev-parse returned an empty commit")
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	raw, err := cmd.Output()
	if err != nil {
		return sourceDigest{}, fmt.Errorf("git ls-files: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	sort.Strings(paths)
	files := make([]sourceFile, 0, len(paths))
	tree := sha256.New()
	for _, rel := range paths {
		if rel == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return sourceDigest{}, fmt.Errorf("source file %q unavailable", rel)
		}
		fileHash, hashErr := hashFile(path)
		if hashErr != nil {
			return sourceDigest{}, fmt.Errorf("hash source %q: %w", rel, hashErr)
		}
		files = append(files, sourceFile{Path: filepath.ToSlash(rel), SHA256: fileHash, Bytes: info.Size()})
		_, _ = io.WriteString(tree, rel+"\x00"+fileHash+"\n")
	}
	return sourceDigest{Root: root, Commit: commit, TreeSHA256: hex.EncodeToString(tree.Sum(nil)), FileCount: len(files), Files: files}, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func withEnv(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, replace := values[key]; !replace {
			result = append(result, item)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func validateReport(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var report struct {
		Results struct {
			Accepted         int   `json:"accepted"`
			SuccessfulDone   int   `json:"successful_done"`
			NetworkErrors    int   `json:"network_errors"`
			UpstreamPosts    int   `json:"upstream_posts"`
			Settlements      int   `json:"settlements"`
			Charged          int64 `json:"charged_microcurrency"`
			RemainingHold    int64 `json:"remaining_hold_microcurrency"`
			KeyHold          int64 `json:"key_remaining_hold_microcurrency"`
			KeySpent         int64 `json:"key_spent_microcurrency"`
			LedgerBalanced   bool  `json:"ledger_balanced"`
			RemainingLanes   int   `json:"remaining_admission_lanes"`
			RemainingTickets int   `json:"remaining_admission_tickets"`
			RemainingPermits int   `json:"remaining_runtime_permits"`
			RemainingStreams int   `json:"remaining_streams"`
		} `json:"results"`
		ContentObservations int `json:"content_observations"`
	}
	if json.NewDecoder(file).Decode(&report) != nil {
		return false
	}
	r := report.Results
	return r.Accepted == 100 && r.SuccessfulDone == 100 && report.ContentObservations == 100 && r.NetworkErrors == 0 && r.UpstreamPosts == 100 && r.Settlements == 100 && r.Charged == 2000 && r.RemainingHold == 0 && r.KeyHold == 0 && r.KeySpent == 2000 && r.LedgerBalanced && r.RemainingLanes == 0 && r.RemainingTickets == 0 && r.RemainingPermits == 0 && r.RemainingStreams == 0
}

func writeJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("write %s: %w", path, encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", path, closeErr)
	}
	return nil
}
