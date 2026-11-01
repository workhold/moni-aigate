package payments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func runtimeTestMaterial(n byte) (v [32]byte) {
	for i := range v {
		v[i] = n
	}
	return v
}
func runtimeTestProof() runtimeProof {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	role := func(name string, oid uint32, login bool) runtimeRolePin {
		return runtimeRolePin{"rt_" + name + "_" + suffix, oid, login}
	}
	app := runtimeSchemaPin{"payment_sandbox_" + suffix, 20, role("ao", 10, false), role("ar", 11, true)}
	mock := runtimeSchemaPin{"payment_mock_" + suffix, 21, role("mo", 12, false), role("mr", 13, true)}
	receiver := runtimeSchemaPin{"payment_receiver_" + suffix, 22, role("qo", 14, false), role("qr", 15, true)}
	return runtimeProof{ProofVersion: 1, ProtocolVersion: 1, EnvironmentID: uuid.NewString(), MerchantID: "merchant:runtime-qa", PayeeLabel: "Runtime QA", Database: runtimeDatabasePin{"127.0.0.1", 55439, "moni_test", 1}, App: app, Mock: mock, Receiver: receiver, Worker: role("worker", 16, true), Projector: role("proj", 17, true), AppNonceDigest: runtimeDomainDigest("moni-payment-application-state-v1", runtimeTestMaterial(1)), WorkerNonceDigest: runtimeDomainDigest("moni-payment-worker-state-v1", runtimeTestMaterial(2)), APITokenDigest: runtimeDomainDigest("moni-mock-api-token-v1", runtimeTestMaterial(3)), APIOrigin: "http://127.0.0.1:19081", ControlOrigin: "http://127.0.0.1:19082", AppListenOrigin: "http://127.0.0.1:19083", CallbackURL: "http://127.0.0.1:19083/api/payments/sandbox/notifications", Keys: []runtimeKeyPin{{"qa-key", runtimeDomainDigest("moni-mock-signing-key-v1", runtimeTestMaterial(4)), "2020-01-01T00:00:00.000000Z", "2030-01-01T00:00:00.000000Z"}}}
}
func runtimeWriteFile(t *testing.T, dir, name string, raw []byte) runtimeSecretFile {
	t.Helper()
	if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
		t.Fatal("write runtime private fixture")
	}
	return runtimeSecretFile{name, runtimeSHA(raw)}
}
func runtimeWriteOptions(t *testing.T, p runtimeProof, component runtimeComponent, nonce [32]byte, keys []runtimeMaterial, token [32]byte, dsn string) string {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	canonical, e := runtimeCanonicalProof(p)
	if e != nil {
		t.Fatal(e)
	}
	m := runtimeManifest{ManifestVersion: 1, Component: "application", Proof: p, ProofSHA256: runtimeSHA(canonical), NonceFile: runtimeWriteFile(t, dir, "nonce", []byte(base64.RawURLEncoding.EncodeToString(nonce[:]))), Keys: keys}
	var wire any = m
	if component == workerComponent {
		m.Component = "worker"
		wire = runtimeWorkerManifest{m, runtimeWriteFile(t, dir, "dsn", []byte(dsn)), runtimeWriteFile(t, dir, "api", []byte(base64.RawURLEncoding.EncodeToString(token[:])))}
	}
	raw, e := json.Marshal(wire)
	if e != nil {
		t.Fatal(e)
	}
	runtimeWriteFile(t, dir, "manifest.json", raw)
	return filepath.Join(dir, "manifest.json")
}
func runtimeFakeOptions(t *testing.T) (Options, Options, string, string) {
	t.Helper()
	p := runtimeTestProof()
	key := runtimeTestMaterial(4)
	keys := []runtimeMaterial{{"qa-key", base64.RawURLEncoding.EncodeToString(key[:])}}
	aPath := runtimeWriteOptions(t, p, applicationComponent, runtimeTestMaterial(1), keys, [32]byte{}, "")
	wPath := runtimeWriteOptions(t, p, workerComponent, runtimeTestMaterial(2), keys, runtimeTestMaterial(3), "postgres://"+p.Worker.Name+":fixture-password@127.0.0.1:55439/moni_test?sslmode=disable")
	a, e := LoadSandboxOptions(context.Background(), aPath)
	if e != nil {
		t.Fatal("load application fixture", e)
	}
	w, e := LoadWorkerOptions(context.Background(), wPath)
	if e != nil {
		t.Fatal("load worker fixture", e)
	}
	return a, w, aPath, wPath
}
func runtimeRewrite(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("read fixture JSON")
	}
	mutate(m)
	raw, e = json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("rewrite fixture")
	}
}
func TestRuntimeOptionsComponentsPairBindingAndRedaction(t *testing.T) {
	a, w, ap, wp := runtimeFakeOptions(t)
	if ValidateOptionsPair(a, w) != nil {
		t.Fatal("unbound pair rejected")
	}
	if _, e := LoadSandboxOptions(context.Background(), wp); e == nil {
		t.Fatal("worker file accepted as application")
	}
	if _, e := LoadWorkerOptions(context.Background(), ap); e == nil {
		t.Fatal("application file accepted as worker")
	}
	guard := func(context.Context) error { return nil }
	bound, e := a.BindLifecycle(Lifecycle{Mode: ModeSandbox, Guard: guard})
	if e != nil {
		t.Fatal(e)
	}
	if ValidateOptionsPair(bound, w) == nil {
		t.Fatal("partly bound pair")
	}
	worker, e := w.BindLifecycle(Lifecycle{Mode: ModeSandbox, Guard: guard})
	if e != nil || ValidateOptionsPair(bound, worker) != nil {
		t.Fatal("bound pair rejected")
	}
	if _, e = bound.BindLifecycle(Lifecycle{Guard: guard}); e == nil {
		t.Fatal("rebound snapshot")
	}
	if _, e = a.BindLifecycle(Lifecycle{Guard: guard}); e != nil {
		t.Fatal("unbound copy must remain legal")
	}
	for _, mode := range []Mode{"live", "Sandbox", "invalid"} {
		if _, e := a.BindLifecycle(Lifecycle{Mode: mode, Guard: guard}); e == nil {
			t.Fatal("unknown mode accepted")
		}
	}
	if _, e = DisabledOptions().BindLifecycle(Lifecycle{Mode: ModeSandbox, Guard: guard}); e == nil {
		t.Fatal("disabled sandbox accepted")
	}
	off, e := DisabledOptions().BindLifecycle(Lifecycle{Guard: guard})
	if e != nil || off.lifecycle.Mode != ModeOff {
		t.Fatal("default off")
	}
	changed := w
	changed.proofBytes = bytes.Clone(w.proofBytes)
	changed.proofBytes[0] ^= 1
	if ValidateOptionsPair(a, changed) == nil {
		t.Fatal("proof mismatch")
	}
	changed = w
	changed.keyMaterials = append([][32]byte(nil), w.keyMaterials...)
	changed.keyMaterials[0][0] ^= 1
	if ValidateOptionsPair(a, changed) == nil {
		t.Fatal("key material mismatch")
	}
	for _, v := range []any{a, w, &paymentRuntime{options: w}} {
		raw, _ := json.Marshal(v)
		formatted := fmt.Sprintf("%v %#v %s", v, v, raw)
		if strings.Contains(formatted, "fixture-password") || strings.Contains(formatted, a.proof.EnvironmentID) || strings.Contains(formatted, a.proof.Keys[0].Digest) {
			t.Fatal("runtime formatting leaks configuration")
		}
	}
}
func TestRuntimeStrictFilesAndProof(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["unknown"] = true }, func(m map[string]any) { delete(m, "keys") }, func(m map[string]any) { m["keys"] = nil }, func(m map[string]any) { m["Component"] = m["component"]; delete(m, "component") }, func(m map[string]any) { m["proof_sha256"] = strings.Repeat("0", 64) },
		func(m map[string]any) { m["proof"].(map[string]any)["api_origin"] = "http://localhost:19081" }, func(m map[string]any) { m["proof"].(map[string]any)["callback_url"] = "http://127.0.0.1:19083/other" }, func(m map[string]any) { m["nonce_file"].(map[string]any)["name"] = "../nonce" },
		func(m map[string]any) {
			m["runtime_dsn_file"] = map[string]any{"name": "dsn", "sha256": strings.Repeat("a", 64)}
		},
	} {
		_, _, path, _ := runtimeFakeOptions(t)
		runtimeRewrite(t, path, mutate)
		if _, e := LoadSandboxOptions(context.Background(), path); e == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	for _, raw := range []string{`{"manifest_version":1,"manifest_version":1}`, `{"payee_label":"\ud800"}`, `null`, `{} {}`, "{\"x\":\"\xff\"}"} {
		var p runtimeManifest
		if runtimeStrictDecode([]byte(raw), &p) == nil {
			t.Fatal("non-strict JSON accepted")
		}
	}
	for _, kind := range []string{"manifest-permissions", "directory-permissions", "symlink", "fifo", "secret-hash", "oversize"} {
		_, _, path, _ := runtimeFakeOptions(t)
		dir := filepath.Dir(path)
		nonce := filepath.Join(dir, "nonce")
		switch kind {
		case "manifest-permissions":
			_ = os.Chmod(path, 0644)
		case "directory-permissions":
			_ = os.Chmod(dir, 0755)
		case "symlink":
			if os.Rename(nonce, nonce+"-actual") != nil || os.Symlink(nonce+"-actual", nonce) != nil {
				t.Fatal("symlink fixture")
			}
		case "fifo":
			if os.Remove(nonce) != nil || unix.Mkfifo(nonce, 0600) != nil {
				t.Fatal("FIFO fixture")
			}
		case "secret-hash":
			_ = os.WriteFile(nonce, bytes.Repeat([]byte{'a'}, 43), 0600)
		case "oversize":
			_ = os.WriteFile(path, bytes.Repeat([]byte{'x'}, 65537), 0600)
		}
		if _, e := LoadSandboxOptions(context.Background(), path); e == nil {
			t.Fatal("unsafe private file accepted", kind)
		}
	}
	p := runtimeTestProof()
	p.Keys = append(p.Keys, runtimeKeyPin{"a-key", strings.Repeat("a", 64), p.Keys[0].NotBefore, p.Keys[0].NotAfter})
	if runtimeValidateProof(p) == nil {
		t.Fatal("unsorted keys accepted")
	}
	raw, _ := runtimeCanonicalProof(runtimeTestProof())
	raw = bytes.Replace(raw, []byte(`"oid":1`), []byte(`"oid":1e0`), 1)
	var decoded runtimeProof
	if runtimeStrictDecode(raw, &decoded) == nil {
		t.Fatal("noninteger OID accepted")
	}
}
func TestRuntimeDSNPreparseAndAmbientIsolation(t *testing.T) {
	a, w, _, _ := runtimeFakeOptions(t)
	raw := "postgres://" + a.proof.App.Runtime.Name + ":fixture-password@127.0.0.1:55439/moni_test?sslmode=disable"
	t.Setenv("PGSERVICE", "untrusted-service")
	if _, e := ApplicationPoolConfig(context.Background(), raw, a); e == nil {
		t.Fatal("ambient service accepted")
	}
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGHOST", "example.invalid")
	t.Setenv("PGOPTIONS", "-c search_path=untrusted")
	t.Setenv("PGSERVICEFILE", "/missing-service")
	t.Setenv("PGPASSFILE", "/missing-passfile")
	cfg, e := ApplicationPoolConfig(context.Background(), raw, a)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 || len(cfg.ConnConfig.RuntimeParams) != 3 || strings.Contains(cfg.ConnConfig.RuntimeParams["search_path"], "untrusted") {
		t.Fatal("ambient discovery changed fixed connection")
	}
	if _, e = cfg.ConnConfig.LookupFunc(context.Background(), "example.invalid"); e == nil {
		t.Fatal("DNS accepted")
	}
	if _, e = cfg.ConnConfig.DialFunc(context.Background(), "tcp", "127.0.0.1:5432"); e == nil {
		t.Fatal("other target accepted")
	}
	for _, bad := range []string{strings.Replace(raw, "127.0.0.1", "localhost", 1), strings.Replace(raw, "127.0.0.1", "[::1]", 1), strings.Replace(raw, "55439", "5432", 1), strings.Replace(raw, "moni_test?", "postgres?", 1), strings.Replace(raw, "/moni_test?", "/%6doni_test?", 1), raw + "&options=x", raw + "&sslmode=disable", raw + "&service=x", raw + "#fragment", raw + "#", raw + "\n", "host=127.0.0.1 dbname=moni_test"} {
		if _, e := ApplicationPoolConfig(context.Background(), bad, a); e == nil {
			t.Fatal("unsafe DSN accepted")
		}
	}
	if _, e = ApplicationPoolConfig(context.Background(), raw, w); e == nil {
		t.Fatal("worker options accepted by APP connector")
	}
	if _, e = workerPoolConfig(context.Background(), w); e == nil {
		t.Fatal("unbound worker pool accepted")
	}
}
func TestRuntimeGuardCallerCancellationDoesNotFence(t *testing.T) {
	var calls atomic.Int64
	opts, _ := DisabledOptions().BindLifecycle(Lifecycle{Guard: func(context.Context) error { calls.Add(1); return nil }})
	r := &paymentRuntime{options: opts}
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := r.check(short); e != context.DeadlineExceeded || calls.Load() != 0 || r.fenced.Load() {
		t.Fatal("short caller budget reached Guard")
	}
	canceled, c := context.WithCancel(context.Background())
	c()
	if e := r.check(canceled); e != context.Canceled || r.fenced.Load() {
		t.Fatal("caller cancellation fenced")
	}
	started, release := make(chan struct{}), make(chan struct{})
	r.options.lifecycle.Guard = func(ctx context.Context) error { close(started); <-release; return ctx.Err() }
	active, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.check(active) }()
	<-started
	stop()
	close(release)
	if e := <-done; e != context.Canceled || r.fenced.Load() {
		t.Fatal("during-check caller cancellation fenced")
	}
	r.options.lifecycle.Guard = func(context.Context) error { calls.Add(1); return errors.New("private ownership diagnostic") }
	if e := r.check(context.Background()); e != ErrPaymentsGuard || !r.fenced.Load() {
		t.Fatal("real Guard failure did not fence")
	}
	before := calls.Load()
	if r.check(context.Background()) != ErrPaymentsGuard || calls.Load() != before {
		t.Fatal("fenced Guard called again")
	}
}
func TestRuntimeGuardIndependentTimeoutFences(t *testing.T) {
	opts, _ := DisabledOptions().BindLifecycle(Lifecycle{Guard: func(ctx context.Context) error { <-ctx.Done(); return nil }})
	r := &paymentRuntime{options: opts}
	if r.check(context.Background()) != ErrPaymentsGuard || !r.fenced.Load() {
		t.Fatal("independent guard timeout not fenced")
	}
}
func TestRuntimeTransportFixedTargetLimitsAndCancellation(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		_, _ = io.Copy(io.Discard, q.Body)
		_ = q.Body.Close()
		if q.URL.Path == "/v1/cancel" {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(302)
			return
		}
		if q.URL.Path == "/v1/query" {
			_, _ = w.Write(bytes.Repeat([]byte{'a'}, 65537))
			return
		}
		if q.Header.Get("Authorization") != "Bearer "+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)) || q.Header.Get("Accept-Encoding") != "" || q.Header.Get("Content-Type") != "application/json" {
			t.Error("transport header boundary")
		}
		if q.URL.Query().Get("hold") == "unused" {
			return
		}
		if q.ContentLength == 2 {
			select {
			case started <- struct{}{}:
			default:
				{
				}
			}
			<-q.Context().Done()
			return
		}
		w.Header().Set("X-QA", "yes")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	t.Setenv("ALL_PROXY", target.URL)
	o, _ := DisabledOptions().BindLifecycle(Lifecycle{Guard: func(context.Context) error { return nil }})
	o.disabled = false
	o.component = workerComponent
	o.proof.APIOrigin = server.URL
	o.apiToken = runtimeTestMaterial(3)
	r := &paymentRuntime{options: o}
	status, h, raw, e := r.doSandboxRequest(context.Background(), "checkout", []byte(`{"a":1}`))
	if e != nil || status != 200 || h.Get("X-QA") != "yes" || string(raw) != `{"ok":true}` || targetCalls.Load() != 0 {
		t.Fatal("fixed transport failed", e)
	}
	if _, _, _, e = r.doSandboxRequest(context.Background(), "cancel", []byte(`{"a":1}`)); e != errSandboxNetwork || targetCalls.Load() != 0 {
		t.Fatal("redirect followed")
	}
	if _, _, _, e = r.doSandboxRequest(context.Background(), "query", []byte(`{"a":1}`)); e != errSandboxNetwork {
		t.Fatal("oversize response accepted")
	}
	r.options.lifecycle.Recovery = true
	if _, _, _, e = r.doSandboxRequest(context.Background(), "checkout", []byte(`{"a":1}`)); e != ErrPaymentsRecovery {
		t.Fatal("Recovery mutation IO")
	}
	r.options.lifecycle.Recovery = false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, _, e := r.doSandboxRequest(ctx, "checkout", []byte(`{}`)); done <- e }()
	<-started
	cancel()
	if e := <-done; e != context.Canceled || r.fenced.Load() {
		t.Fatal("transport cancellation fenced", e)
	}
}
