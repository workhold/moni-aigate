package paymentsandboxqa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"

	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

func (e *Environment) rolePin(key string) map[string]any {
	return map[string]any{"name": e.Roles[key], "oid": e.roleOIDs[key], "login": len(key) < 6 || key[len(key)-6:] != "_owner"}
}
func (e *Environment) schemaPin(key string) map[string]any {
	return map[string]any{"name": e.Schemas[key], "oid": e.schemaOIDs[key], "owner": e.rolePin(key + "_owner"), "runtime": e.rolePin(key + "_runtime")}
}
func writeSecret(dir, name string, b []byte) (map[string]any, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, stage("secret_file")
	}
	_, err = f.Write(b)
	closed := f.Close()
	if err != nil || closed != nil {
		return nil, stage("secret_file")
	}
	h := sha256.Sum256(b)
	return map[string]any{"name": name, "sha256": hex.EncodeToString(h[:])}, nil
}
func (e *Environment) writeManifests() error {
	for _, c := range []string{"mock", "receiver"} {
		dir := filepath.Join(e.directory, c)
		if os.Mkdir(dir, 0700) != nil {
			return stage("component_directory")
		}
		dsn := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(e.Roles[c+"_runtime"], e.passwords[c+"_runtime"]), RawQuery: "sslmode=disable"}
		dsnFile, err := writeSecret(dir, "runtime_dsn", []byte(dsn.String()))
		if err != nil {
			return err
		}
		nonce := e.mockNonce
		if c == "receiver" {
			nonce = e.receiverNonce
		}
		nonceFile, err := writeSecret(dir, "nonce", []byte(encoded(nonce)))
		if err != nil {
			return err
		}
		hash, err := paymentsandboxschema.Digest(paymentsandboxschema.Component(c))
		if err != nil {
			return stage("sql_digest")
		}
		m := map[string]any{"manifest_version": 1, "protocol_version": 1, "component": c, "environment_id": e.Protocol.ID, "state_directory_id": e.stateDirectoryID, "merchant_id": e.Protocol.MerchantID, "payee_label": e.Protocol.PayeeLabel,
			"database": map[string]any{"host": "127.0.0.1", "port": 55439, "name": "moni_test", "oid": e.dbOID}, "app": e.schemaPin("app"), "mock": e.schemaPin("mock"), "receiver": e.schemaPin("receiver"), "app_projector": e.rolePin("app_projector"),
			"own_schema_sql": map[string]any{"schema_version": 1, "sha256": hash}, "runtime_dsn_file": dsnFile, "nonce_file": nonceFile, "callback_url": e.ReceiverOrigin + "/api/payments/sandbox/notifications", "active_key_id": "qa-key",
			"keys": []map[string]any{{"id": "qa-key", "material_base64_url": encoded(e.key), "digest": digest("moni-mock-signing-key-v1", e.key), "not_before": "2020-01-01T00:00:00.000000Z", "not_after": "2030-01-01T00:00:00.000000Z"}}}
		if c == "mock" {
			a, x := writeSecret(dir, "api_token", []byte(encoded(e.api)))
			if x != nil {
				return x
			}
			b, x := writeSecret(dir, "control_token", []byte(encoded(e.control)))
			if x != nil {
				return x
			}
			m["api_origin"] = e.APIOrigin
			m["control_origin"] = e.ControlOrigin
			m["api_token_file"] = a
			m["control_token_file"] = b
		} else {
			m["listen_origin"] = e.ReceiverOrigin
		}
		b, err := json.Marshal(m)
		if err != nil {
			return stage("manifest")
		}
		if _, err = writeSecret(dir, "manifest.json", b); err != nil {
			return err
		}
		if c == "mock" {
			e.MockManifestPath = filepath.Join(dir, "manifest.json")
		} else {
			e.ReceiverManifestPath = filepath.Join(dir, "manifest.json")
		}
	}
	return nil
}
