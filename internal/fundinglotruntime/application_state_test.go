package fundinglotruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"golang.org/x/sys/unix"
)

func applicationTestSHA(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func applicationTestManifest(t testing.TB, count int) (applicationManifest, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../fundinglotproof/testdata/predecessor_1keys.json")
	if err != nil {
		t.Fatal(err)
	}
	var p fundinglotproof.V1Input
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	nonce := sha256.Sum256([]byte("application-test-nonce"))
	nonceRaw := []byte(base64.RawURLEncoding.EncodeToString(nonce[:]))
	p.AppNonceDigest = applicationDomainDigest("moni-payment-application-state-v1", nonce)
	p.Keys = nil
	m := applicationManifest{ManifestVersion: 1, Component: "application", NonceFile: applicationSecretFile{Name: "app_nonce", SHA256: applicationTestSHA(nonceRaw)}}
	for i := 0; i < count; i++ {
		key := sha256.Sum256([]byte(fmt.Sprintf("application-test-key-%03d", i)))
		id := fmt.Sprintf("key_%03d", i)
		p.Keys = append(p.Keys, fundinglotproof.V1KeyPin{ID: id, Digest: applicationDomainDigest("moni-mock-signing-key-v1", key), NotBefore: "2020-01-01T00:00:00.000001Z", NotAfter: "2021-01-01T00:00:00.000001Z"})
		m.Keys = append(m.Keys, applicationMaterial{ID: id, MaterialBase64URL: base64.RawURLEncoding.EncodeToString(key[:])})
	}
	m.Proof = p
	applicationTestPin(t, &m)
	return m, nonceRaw
}

func applicationTestPin(t testing.TB, m *applicationManifest) {
	t.Helper()
	p, err := fundinglotproof.EncodeV1(m.Proof)
	if err != nil {
		t.Fatal(err)
	}
	m.ProofSHA256 = p.SHA256()
}

func applicationTestRaw(t testing.TB, m applicationManifest) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func applicationTestFiles(t *testing.T, raw, nonce []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "application.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_nonce"), nonce, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func applicationTestRejected(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	state, err := LoadSandboxApplicationState(ctx, path)
	if state != nil || err != ErrRuntime || !errors.Is(err, ErrRuntime) || err.Error() != "funding_lot_runtime_verification_failed" {
		t.Fatalf("failure must return nil state and fixed error, got %v / %v", state, err)
	}
}

func TestApplicationLoaderValidSnapshots(t *testing.T) {
	for _, count := range []int{1, 128} {
		for _, newline := range []bool{false, true} {
			t.Run(fmt.Sprintf("keys_%d_lf_%t", count, newline), func(t *testing.T) {
				m, nonce := applicationTestManifest(t, count)
				if newline {
					nonce = append(nonce, '\n')
					m.NonceFile.SHA256 = applicationTestSHA(nonce)
				}
				// Ordinary JSON map encoding changes field order throughout proof.
				var reordered any
				if err := json.Unmarshal(applicationTestRaw(t, m), &reordered); err != nil {
					t.Fatal(err)
				}
				raw, err := json.MarshalIndent(reordered, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				state, err := LoadSandboxApplicationState(context.Background(), applicationTestFiles(t, raw, nonce))
				if err != nil || state == nil || state.state == nil || state.state.appNonceDigest != m.Proof.AppNonceDigest || state.state.proof.SHA256() != m.ProofSHA256 {
					t.Fatalf("valid snapshot rejected: %v", err)
				}
				want, err := fundinglotproof.EncodeV1(m.Proof)
				if err != nil || !bytes.Equal(want.Bytes(), state.state.proof.Bytes()) {
					t.Fatal("noncanonical manifest did not retain identical canonical proof")
				}
			})
		}
	}
}

func TestApplicationLoaderPreservesHistoricalFormat(t *testing.T) {
	for _, scenario := range []string{"pg_role", "cross_catalog_oid", "equal_times", "future_keys", "expired_keys"} {
		t.Run(scenario, func(t *testing.T) {
			m, nonce := applicationTestManifest(t, 1)
			switch scenario {
			case "pg_role":
				m.Proof.App.Runtime.Name = "pg_application"
			case "cross_catalog_oid":
				m.Proof.Database.OID = m.Proof.App.OID
				m.Proof.App.Owner.OID = m.Proof.App.OID
			case "equal_times":
				m.Proof.Keys[0].NotAfter = m.Proof.Keys[0].NotBefore
			case "future_keys":
				m.Proof.Keys[0].NotBefore = "9998-01-01T00:00:00.000001Z"
				m.Proof.Keys[0].NotAfter = "9999-01-01T00:00:00.000001Z"
			}
			applicationTestPin(t, &m)
			if _, err := LoadSandboxApplicationState(context.Background(), applicationTestFiles(t, applicationTestRaw(t, m), nonce)); err != nil {
				t.Fatalf("historical legal boundary rejected: %v", err)
			}
		})
	}
}

func TestApplicationLoaderMaterialAndBindingRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*applicationManifest, *[]byte)
	}{
		{"wrong_component", func(m *applicationManifest, _ *[]byte) { m.Component = "worker" }},
		{"wrong_version", func(m *applicationManifest, _ *[]byte) { m.ManifestVersion = 2 }},
		{"wrong_proof_sha", func(m *applicationManifest, _ *[]byte) { m.ProofSHA256 = strings.Repeat("0", 64) }},
		{"wrong_nonce_sha", func(m *applicationManifest, _ *[]byte) { m.NonceFile.SHA256 = strings.Repeat("0", 64) }},
		{"uppercase_nonce_sha", func(m *applicationManifest, _ *[]byte) { m.NonceFile.SHA256 = strings.ToUpper(m.NonceFile.SHA256) }},
		{"nonce_same_manifest", func(m *applicationManifest, _ *[]byte) { m.NonceFile.Name = "application.json" }},
		{"nonce_path", func(m *applicationManifest, _ *[]byte) { m.NonceFile.Name = "../app_nonce" }},
		{"nonce_dot", func(m *applicationManifest, _ *[]byte) { m.NonceFile.Name = "app.nonce" }},
		{"nonce_65_name", func(m *applicationManifest, _ *[]byte) { m.NonceFile.Name = strings.Repeat("n", 65) }},
		{"nonce_wrong_domain", func(m *applicationManifest, _ *[]byte) {
			m.Proof.AppNonceDigest = m.Proof.WorkerNonceDigest
			applicationTestPin(t, m)
		}},
		{"nonce_empty", func(m *applicationManifest, n *[]byte) { *n = nil; m.NonceFile.SHA256 = applicationTestSHA(*n) }},
		{"nonce_multilf", func(m *applicationManifest, n *[]byte) {
			*n = append(*n, '\n', '\n')
			m.NonceFile.SHA256 = applicationTestSHA(*n)
		}},
		{"nonce_padding", func(m *applicationManifest, n *[]byte) {
			*n = append(*n, '=')
			m.NonceFile.SHA256 = applicationTestSHA(*n)
		}},
		{"nonce_cr", func(m *applicationManifest, n *[]byte) {
			*n = append(*n, '\r')
			m.NonceFile.SHA256 = applicationTestSHA(*n)
		}},
		{"nonce_standard_base64", func(m *applicationManifest, n *[]byte) {
			*n = []byte(strings.Repeat("/", 42) + "8")
			m.NonceFile.SHA256 = applicationTestSHA(*n)
		}},
		{"nonce_noncanonical_bits", func(m *applicationManifest, n *[]byte) {
			*n = applicationTestNoncanonical(*n)
			m.NonceFile.SHA256 = applicationTestSHA(*n)
		}},
		{"key_lf", func(m *applicationManifest, _ *[]byte) { m.Keys[0].MaterialBase64URL += "\n" }},
		{"key_padding", func(m *applicationManifest, _ *[]byte) { m.Keys[0].MaterialBase64URL += "=" }},
		{"key_noncanonical_bits", func(m *applicationManifest, _ *[]byte) {
			m.Keys[0].MaterialBase64URL = string(applicationTestNoncanonical([]byte(m.Keys[0].MaterialBase64URL)))
		}},
		{"key_missing", func(m *applicationManifest, _ *[]byte) { m.Keys = m.Keys[:1] }},
		{"key_extra", func(m *applicationManifest, _ *[]byte) { m.Keys = append(m.Keys, m.Keys[0]) }},
		{"key_id", func(m *applicationManifest, _ *[]byte) { m.Keys[0].ID = "different" }},
		{"key_order", func(m *applicationManifest, _ *[]byte) { m.Keys[0], m.Keys[1] = m.Keys[1], m.Keys[0] }},
		{"key_digest", func(m *applicationManifest, _ *[]byte) {
			m.Proof.Keys[0].Digest = strings.Repeat("0", 64)
			applicationTestPin(t, m)
		}},
		{"key_reuse", func(m *applicationManifest, _ *[]byte) {
			m.Keys[1].MaterialBase64URL = m.Keys[0].MaterialBase64URL
			m.Proof.Keys[1].Digest = m.Proof.Keys[0].Digest
			applicationTestPin(t, m)
		}},
		{"nonce_key_reuse", func(m *applicationManifest, n *[]byte) {
			key, _ := base64.RawURLEncoding.DecodeString(string(*n))
			var value [32]byte
			copy(value[:], key)
			m.Keys[0].MaterialBase64URL = string(*n)
			m.Proof.Keys[0].Digest = applicationDomainDigest("moni-mock-signing-key-v1", value)
			applicationTestPin(t, m)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, nonce := applicationTestManifest(t, 2)
			tc.mutate(&m, &nonce)
			applicationTestRejected(t, context.Background(), applicationTestFiles(t, applicationTestRaw(t, m), nonce))
		})
	}
}

func applicationTestNoncanonical(raw []byte) []byte {
	result := bytes.Clone(raw)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, result[len(result)-1])
	result[len(result)-1] = alphabet[i+1] // canonical 32-byte encoding has two zero low bits
	return result
}

func TestApplicationLoaderFileBoundaries(t *testing.T) {
	for _, scenario := range []string{"relative", "unclean", "dot_name", "long_name", "parent_symlink", "manifest_symlink", "nonce_symlink", "parent_mode", "manifest_mode", "nonce_mode", "parent_special_bits", "manifest_special_bits", "manifest_fifo", "nonce_fifo", "manifest_directory", "nonce_directory", "manifest_empty", "manifest_oversize", "missing_nonce", "wrong_owner"} {
		t.Run(scenario, func(t *testing.T) {
			m, nonce := applicationTestManifest(t, 1)
			path := applicationTestFiles(t, applicationTestRaw(t, m), nonce)
			dir := filepath.Dir(path)
			noncePath := filepath.Join(dir, "app_nonce")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "relative":
				path = "application.json"
			case "unclean":
				path = dir + "/./application.json"
			case "dot_name":
				path = filepath.Join(dir, ".application.json")
				must(os.Rename(filepath.Join(dir, "application.json"), path))
			case "long_name":
				path = filepath.Join(dir, strings.Repeat("a", 129))
				must(os.Rename(filepath.Join(dir, "application.json"), path))
			case "parent_symlink":
				link := filepath.Join(t.TempDir(), "link")
				must(os.Symlink(dir, link))
				path = filepath.Join(link, "application.json")
			case "manifest_symlink", "nonce_symlink":
				target := path
				if scenario == "nonce_symlink" {
					target = noncePath
				}
				must(os.Rename(target, target+"_real"))
				must(os.Symlink(target+"_real", target))
			case "parent_mode":
				must(os.Chmod(dir, 0750))
			case "manifest_mode":
				must(os.Chmod(path, 0640))
			case "nonce_mode":
				must(os.Chmod(noncePath, 0644))
			case "parent_special_bits":
				must(unix.Chmod(dir, 01700))
			case "manifest_special_bits":
				must(unix.Chmod(path, 01600))
			case "manifest_fifo", "nonce_fifo":
				target := path
				if scenario == "nonce_fifo" {
					target = noncePath
				}
				must(os.Remove(target))
				must(unix.Mkfifo(target, 0600))
			case "manifest_directory", "nonce_directory":
				target := path
				if scenario == "nonce_directory" {
					target = noncePath
				}
				must(os.Remove(target))
				must(os.Mkdir(target, 0600))
			case "manifest_empty":
				must(os.WriteFile(path, nil, 0600))
			case "manifest_oversize":
				must(os.WriteFile(path, bytes.Repeat([]byte(" "), 65537), 0600))
			case "missing_nonce":
				must(os.Remove(noncePath))
			case "wrong_owner":
				if err := os.Chown(path, os.Geteuid()+1, -1); err != nil {
					t.Skip("changing file ownership requires privilege")
				}
				t.Cleanup(func() { _ = os.Chown(path, os.Geteuid(), -1) })
			}
			applicationTestRejected(t, context.Background(), path)
		})
	}
}

func TestApplicationLoaderManifestSizeBoundary(t *testing.T) {
	m, nonce := applicationTestManifest(t, 1)
	raw := applicationTestRaw(t, m)
	raw = append(raw, bytes.Repeat([]byte(" "), 65536-len(raw))...)
	path := applicationTestFiles(t, raw, nonce)
	if _, err := LoadSandboxApplicationState(context.Background(), path); err != nil {
		t.Fatalf("65536-byte entire manifest rejected: %v", err)
	}
	if err := os.WriteFile(path, append(raw, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	applicationTestRejected(t, context.Background(), path)
}

func TestApplicationLoaderBudgetAndContexts(t *testing.T) {
	m, nonce := applicationTestManifest(t, 1)
	path := applicationTestFiles(t, applicationTestRaw(t, m), nonce)
	applicationTestRejected(t, nil, path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	applicationTestRejected(t, ctx, path)
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	applicationTestRejected(t, ctx, path)
	active := context.Background()
	b := applicationBudget{ctx: active, deadline: time.Now().Add(-time.Nanosecond)}
	if active.Err() != nil || b.check() != ErrRuntime {
		t.Fatal("absolute deadline must reject before context timer fires")
	}
	var parsed applicationManifest
	if applicationStrictDecode(b, applicationTestRaw(t, m), &parsed) != ErrRuntime {
		t.Fatal("expired CPU stage accepted")
	}
	if _, _, err := applicationOpenPrivate(b, path); err != ErrRuntime {
		t.Fatal("expired file stage accepted")
	}
	if _, err := applicationMaterial32(b, nonce); err != ErrRuntime {
		t.Fatal("expired material stage accepted")
	}
	start := time.Now()
	live, err := newApplicationBudget(active)
	if err == nil {
		defer live.cancel()
	}
	if err != nil || live.deadline.Before(start.Add(5*time.Second)) || live.deadline.After(time.Now().Add(5*time.Second)) {
		t.Fatal("loader must use one fixed independent five-second window")
	}
	callerDeadline := time.Now().Add(time.Second)
	ctx, cancel = context.WithDeadline(active, callerDeadline)
	defer cancel()
	live, err = newApplicationBudget(ctx)
	if err == nil {
		defer live.cancel()
	}
	if err != nil || !live.deadline.Equal(callerDeadline) {
		t.Fatal("earlier caller deadline lost")
	}
}

func TestApplicationStateRedactionOwnershipAndReadOnlyCopies(t *testing.T) {
	m, nonce := applicationTestManifest(t, 2)
	path := applicationTestFiles(t, applicationTestRaw(t, m), nonce)
	state, err := LoadSandboxApplicationState(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []ApplicationState{{}, *state} {
		if got := fmt.Sprintf("%v|%+v|%#v", v, v, v); strings.Count(got, "ApplicationState(redacted)") != 3 {
			t.Fatalf("unredacted formatting: %s", got)
		}
		raw, err := json.Marshal(v)
		if err != nil || string(raw) != `{"redacted":true}` {
			t.Fatal("unredacted JSON")
		}
	}
	var forged ApplicationState
	if err := json.Unmarshal([]byte(`{"state":{"proof":"fake","appNonceDigest":"fake"}}`), &forged); err != nil || forged.state != nil {
		t.Fatal("JSON must not construct valid state")
	}
	if reflect.TypeOf(applicationState{}).NumField() != 2 {
		t.Fatal("state must not retain manifest, secret, keyring or FD")
	}
	want := state.state.proof.SHA256()
	raw := state.state.proof.Bytes()
	raw[0] ^= 1
	bindings, err := state.state.proof.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	bindings.Keys[0].Digest = "mutated"
	if state.state.proof.SHA256() != want || state.state.proof.Bytes()[0] != '{' {
		t.Fatal("canonical ownership escaped")
	}
	if err := os.WriteFile(path, []byte("invalid replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "app_nonce")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(copy ApplicationState) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if copy.state.proof.SHA256() != want || len(copy.state.proof.Bytes()) == 0 {
					t.Error("immutable snapshot changed")
				}
				if _, err := copy.state.proof.Bindings(); err != nil {
					t.Error(err)
				}
			}
		}(*state)
	}
	wg.Wait()
}

func TestApplicationLoaderClosesFileDescriptors(t *testing.T) {
	m, nonce := applicationTestManifest(t, 1)
	path := applicationTestFiles(t, applicationTestRaw(t, m), nonce)
	count := func() int {
		t.Helper()
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Skip("descriptor inventory unavailable")
		}
		return len(entries)
	}
	before := count()
	for i := 0; i < 30; i++ {
		if _, err := LoadSandboxApplicationState(context.Background(), path); err != nil {
			t.Fatal(err)
		}
		bad := m
		bad.ProofSHA256 = strings.Repeat("0", 64)
		if err := os.WriteFile(path, applicationTestRaw(t, bad), 0600); err != nil {
			t.Fatal(err)
		}
		applicationTestRejected(t, context.Background(), path)
		bad = m
		bad.NonceFile.SHA256 = strings.Repeat("0", 64)
		if err := os.WriteFile(path, applicationTestRaw(t, bad), 0600); err != nil {
			t.Fatal(err)
		}
		applicationTestRejected(t, context.Background(), path)
		if err := os.WriteFile(path, applicationTestRaw(t, m), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("descriptor leak: before=%d after=%d", before, after)
	}
}
