package paymentsandboxqa

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// These are independent offline wire structs, not production payments types.
// Their field order is the frozen version-1 canonical proof protocol.
type applicationRolePin struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type applicationSchemaPin struct {
	Name    string             `json:"name"`
	OID     uint32             `json:"oid"`
	Owner   applicationRolePin `json:"owner"`
	Runtime applicationRolePin `json:"runtime"`
}
type applicationDatabasePin struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type applicationKeyPin struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}
type applicationProof struct {
	ProofVersion      int                    `json:"proof_version"`
	ProtocolVersion   int                    `json:"protocol_version"`
	EnvironmentID     string                 `json:"environment_id"`
	MerchantID        string                 `json:"merchant_id"`
	PayeeLabel        string                 `json:"payee_label"`
	Database          applicationDatabasePin `json:"database"`
	App               applicationSchemaPin   `json:"app"`
	Mock              applicationSchemaPin   `json:"mock"`
	Receiver          applicationSchemaPin   `json:"receiver"`
	Worker            applicationRolePin     `json:"worker"`
	Projector         applicationRolePin     `json:"projector"`
	AppNonceDigest    string                 `json:"app_nonce_digest"`
	WorkerNonceDigest string                 `json:"worker_nonce_digest"`
	APITokenDigest    string                 `json:"api_token_digest"`
	APIOrigin         string                 `json:"api_origin"`
	ControlOrigin     string                 `json:"control_origin"`
	AppListenOrigin   string                 `json:"app_listen_origin"`
	CallbackURL       string                 `json:"callback_url"`
	Keys              []applicationKeyPin    `json:"keys"`
}
type applicationSecretFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type applicationMaterial struct {
	ID                string `json:"id"`
	MaterialBase64URL string `json:"material_base64_url"`
}
type applicationManifest struct {
	ManifestVersion int                   `json:"manifest_version"`
	Component       string                `json:"component"`
	Proof           applicationProof      `json:"proof"`
	ProofSHA256     string                `json:"proof_sha256"`
	NonceFile       applicationSecretFile `json:"nonce_file"`
	Keys            []applicationMaterial `json:"keys"`
}
type applicationWorkerManifest struct {
	applicationManifest
	RuntimeDSNFile applicationSecretFile `json:"runtime_dsn_file"`
	APITokenFile   applicationSecretFile `json:"api_token_file"`
}

func applicationSHA(raw []byte) string { d := sha256.Sum256(raw); return hex.EncodeToString(d[:]) }
func applicationCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return nil, stage("application_codec")
	}
	raw := b.Bytes()
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, stage("application_codec")
	}
	return bytes.Clone(raw[:len(raw)-1]), nil
}
func (e *ApplicationEnvironment) role(key string) applicationRolePin {
	return applicationRolePin{e.Roles[key], e.roleOIDs[key], len(key) < 6 || key[len(key)-6:] != "_owner"}
}
func (e *ApplicationEnvironment) schema(key string) applicationSchemaPin {
	return applicationSchemaPin{e.Schemas[key], e.schemaOIDs[key], e.role(key + "_owner"), e.role(key + "_runtime")}
}
func (e *ApplicationEnvironment) proof() applicationProof {
	return applicationProof{ProofVersion: 1, ProtocolVersion: 1, EnvironmentID: e.Protocol.ID, MerchantID: e.Protocol.MerchantID, PayeeLabel: e.Protocol.PayeeLabel, Database: applicationDatabasePin{"127.0.0.1", 55439, "moni_test", e.dbOID}, App: e.schema("app"), Mock: e.schema("mock"), Receiver: e.schema("receiver"), Worker: e.role("app_worker"), Projector: e.role("app_projector"), AppNonceDigest: digest("moni-payment-application-state-v1", e.appNonce), WorkerNonceDigest: digest("moni-payment-worker-state-v1", e.workerNonce), APITokenDigest: digest("moni-mock-api-token-v1", e.api), APIOrigin: e.APIOrigin, ControlOrigin: e.ControlOrigin, AppListenOrigin: e.ReceiverOrigin, CallbackURL: e.ReceiverOrigin + "/api/payments/sandbox/notifications", Keys: []applicationKeyPin{{"qa-key", digest("moni-mock-signing-key-v1", e.key), "2020-01-01T00:00:00.000000Z", "2030-01-01T00:00:00.000000Z"}}}
}
