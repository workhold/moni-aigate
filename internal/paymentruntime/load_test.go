package paymentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
	"golang.org/x/sys/unix"
)

func configFixture(t *testing.T, component Component) (string, map[string]any) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	role := func(name string, id uint32, login bool) rolePin {
		return rolePin{Name: "pm_" + name + "_" + suffix, OID: id, Login: login}
	}
	schema := func(name string, id uint32, owner, runtime rolePin) schemaPin {
		return schemaPin{Name: "payment_" + name + "_" + suffix, OID: id, Owner: owner, Runtime: runtime}
	}
	app := schema("sandbox", 20, role("app_owner", 10, false), role("app_runtime", 11, true))
	mock := schema("mock", 21, role("mock_owner", 13, false), role("mock_runtime", 14, true))
	receiver := schema("receiver", 22, role("receiver_owner", 15, false), role("receiver_runtime", 16, true))
	write := func(name string, b []byte) secretFile {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		return secretFile{Name: name, SHA256: hex.EncodeToString(h[:])}
	}
	material := func(n byte) ([32]byte, []byte) {
		var a [32]byte
		for i := range a {
			a[i] = n
		}
		return a, []byte(base64.RawURLEncoding.EncodeToString(a[:]))
	}
	_, nonce := material(1)
	_, api := material(2)
	_, control := material(3)
	key, keyRaw := material(4)
	sqlHash, _ := paymentsandboxschema.Digest(paymentsandboxschema.Component(component))
	m := commonManifest{ManifestVersion: 1, ProtocolVersion: 1, Component: component, EnvironmentID: uuid.NewString(), StateDirectoryID: uuid.NewString(), MerchantID: "merchant:runtime-test", PayeeLabel: "Local test", Database: databasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: 1}, App: app, Mock: mock, Receiver: receiver, AppProjector: role("app_projector", 12, true), OwnSchemaSQL: sqlPin{1, sqlHash}, NonceFile: write("nonce", nonce), CallbackURL: "http://127.0.0.1:19083/api/payments/sandbox/notifications", ActiveKeyID: "qa-key", Keys: []keyMaterial{{ID: "qa-key", MaterialBase64URL: string(keyRaw), Digest: domainDigest("moni-mock-signing-key-v1", key), NotBefore: "2020-01-01T00:00:00.000000Z", NotAfter: "2030-01-01T00:00:00.000000Z"}}}
	own := mock.Runtime
	if component == Receiver {
		own = receiver.Runtime
	}
	m.RuntimeDSNFile = write("dsn", []byte("postgres://"+own.Name+":password-"+uuid.NewString()+"@127.0.0.1:55439/moni_test?sslmode=disable"))
	var wire any
	if component == Mock {
		wire = mockManifest{commonManifest: m, APIOrigin: "http://127.0.0.1:19081", ControlOrigin: "http://127.0.0.1:19082", APITokenFile: write("api", api), ControlTokenFile: write("control", control)}
	} else {
		wire = receiverManifest{commonManifest: m, ListenOrigin: "http://127.0.0.1:19083"}
	}
	b, e := json.Marshal(wire)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "manifest.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	var object map[string]any
	if json.Unmarshal(b, &object) != nil {
		t.Fatal("fixture JSON")
	}
	return path, object
}
func rewriteManifest(t *testing.T, path string, m map[string]any) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestLoadComponentAndRedaction(t *testing.T) {
	for _, component := range []Component{Mock, Receiver} {
		path, _ := configFixture(t, component)
		cfg, e := Load(context.Background(), path, component)
		if e != nil {
			t.Fatal(e)
		}
		r := &Runtime{config: cfg}
		if r.Component() != component || r.Environment().ID == "" || r.Keyring() == nil || r.ActiveKeyID() != "qa-key" {
			t.Fatal("missing read-only accessors")
		}
		if _, _, ok := r.KeyWindow("qa-key"); !ok {
			t.Fatal("missing key window")
		}
		_, api := r.Origin(API)
		if api != (component == Mock) {
			t.Fatal("component origin leak")
		}
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
		if r.TokenMatches(APIToken, token) != (component == Mock) || r.TokenMatches(ControlToken, token) || r.TokenMatches(APIToken, "Bearer "+token) {
			t.Fatal("token boundary")
		}
		b, _ := json.Marshal(cfg)
		formatted := fmt.Sprintf("%v %#v %s", cfg, cfg, b)
		u, _ := url.Parse(cfg.dsn)
		password, _ := u.User.Password()
		if strings.Contains(formatted, password) || strings.Contains(formatted, cfg.manifest.Keys[0].MaterialBase64URL) {
			t.Fatal("config secrets formatted")
		}
		other := Mock
		if component == Mock {
			other = Receiver
		}
		if _, e = Load(context.Background(), path, other); e == nil {
			t.Fatal("wrong component accepted")
		}
	}
	if _, e := Open(context.Background(), Config{}); e != ErrConfiguration {
		t.Fatal("zero Config accepted")
	}
	var r Runtime
	if e := r.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestStrictManifestAndPrivateFiles(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["unknown"] = true }, func(m map[string]any) { delete(m, "payee_label") }, func(m map[string]any) { m["payee_label"] = nil }, func(m map[string]any) { m["Component"] = m["component"]; delete(m, "component") },
		func(m map[string]any) { m["manifest_version"] = 2 }, func(m map[string]any) { m["listen_origin"] = "http://127.0.0.1:19083" }, func(m map[string]any) { m["api_origin"] = "http://localhost:19081" },
		func(m map[string]any) { m["own_schema_sql"].(map[string]any)["sha256"] = strings.Repeat("0", 64) }, func(m map[string]any) { m["runtime_dsn_file"].(map[string]any)["name"] = "../dsn" },
	} {
		path, m := configFixture(t, Mock)
		mutate(m)
		rewriteManifest(t, path, m)
		if _, e := Load(context.Background(), path, Mock); e == nil {
			t.Fatal("bad manifest accepted")
		}
	}
	for _, raw := range []string{`{"manifest_version":1,"manifest_version":1}`, `{"payee_label":"\ud800"}`, `null`, `{} {}`, "{\"x\":\"\xff\"}"} {
		var m mockManifest
		if strictDecode([]byte(raw), &m) == nil {
			t.Fatal("non-strict JSON accepted")
		}
	}
	path, _ := configFixture(t, Mock)
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(context.Background(), path, Mock); e == nil {
		t.Fatal("broad manifest accepted")
	}
	path, _ = configFixture(t, Mock)
	if e := os.Chmod(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(context.Background(), path, Mock); e == nil {
		t.Fatal("broad directory accepted")
	}
	path, _ = configFixture(t, Mock)
	dsn := filepath.Join(filepath.Dir(path), "dsn")
	if e := os.Rename(dsn, dsn+"-actual"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(dsn+"-actual", dsn); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(context.Background(), path, Mock); e == nil {
		t.Fatal("symlink accepted")
	}
	path, _ = configFixture(t, Mock)
	dsn = filepath.Join(filepath.Dir(path), "dsn")
	if e := os.Remove(dsn); e != nil {
		t.Fatal(e)
	}
	if e := unix.Mkfifo(dsn, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(context.Background(), path, Mock); e == nil {
		t.Fatal("non-regular secret accepted")
	}
}
func TestDSNPinsIgnoreAmbientDiscovery(t *testing.T) {
	path, _ := configFixture(t, Mock)
	cfg, e := Load(context.Background(), path, Mock)
	if e != nil {
		t.Fatal(e)
	}
	good := cfg.dsn
	t.Setenv("PGSERVICE", "untrusted-service")
	t.Setenv("PGSERVICEFILE", "/missing-private-service")
	t.Setenv("PGOPTIONS", "-c search_path=public")
	t.Setenv("PGHOST", "example.invalid")
	if _, e := dsnConfig(cfg); e == nil {
		t.Fatal("ambient service discovery accepted")
	}
	t.Setenv("PGSERVICE", "")
	parsed, e := dsnConfig(cfg)
	if e != nil || parsed.ConnConfig.Host != "127.0.0.1" || len(parsed.ConnConfig.Fallbacks) != 0 || strings.Contains(parsed.ConnConfig.RuntimeParams["search_path"], "public") {
		t.Fatal("ambient discovery affected target")
	}
	for _, bad := range []string{strings.Replace(good, "127.0.0.1", "localhost", 1), strings.Replace(good, "55439", "5432", 1), good + "&options=x", good + "&sslmode=disable", good + "#fragment", strings.Replace(good, "moni_test?", "postgres?", 1), "host=127.0.0.1 dbname=moni_test", good + "\n"} {
		cfg.dsn = bad
		if _, e := dsnConfig(cfg); e == nil {
			t.Fatal("unsafe DSN accepted")
		}
	}
}
