package fundinglotproof

import (
	"bytes"
	"unicode/utf8"
)

// V1Role preserves the historical payment role format, including Login and
// role names which the distinct V2 outer envelope may reject.
type V1Role struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type V1Schema struct {
	Name    string `json:"name"`
	OID     uint32 `json:"oid"`
	Owner   V1Role `json:"owner"`
	Runtime V1Role `json:"runtime"`
}
type V1Database struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type V1KeyPin struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}

// V1Input contains historical wire-format claims, never authenticated file,
// signing material, catalog, database, or process-startup facts.
type V1Input struct {
	ProofVersion      int        `json:"proof_version"`
	ProtocolVersion   int        `json:"protocol_version"`
	EnvironmentID     string     `json:"environment_id"`
	MerchantID        string     `json:"merchant_id"`
	PayeeLabel        string     `json:"payee_label"`
	Database          V1Database `json:"database"`
	App               V1Schema   `json:"app"`
	Mock              V1Schema   `json:"mock"`
	Receiver          V1Schema   `json:"receiver"`
	Worker            V1Role     `json:"worker"`
	Projector         V1Role     `json:"projector"`
	AppNonceDigest    string     `json:"app_nonce_digest"`
	WorkerNonceDigest string     `json:"worker_nonce_digest"`
	APITokenDigest    string     `json:"api_token_digest"`
	APIOrigin         string     `json:"api_origin"`
	ControlOrigin     string     `json:"control_origin"`
	AppListenOrigin   string     `json:"app_listen_origin"`
	CallbackURL       string     `json:"callback_url"`
	Keys              []V1KeyPin `json:"keys"`
}

// V1Bindings is an owned copy of format claims, not an authenticated capability.
type V1Bindings = V1Input

// CanonicalV1 proves only strict historical format and canonical-byte validity.
// Its zero value is invalid; copies share immutable owned state.
type CanonicalV1 struct{ state *canonicalV1State }
type canonicalV1State struct {
	raw      []byte
	sha      string
	bindings V1Bindings
}

func (CanonicalV1) String() string               { return "fundinglotproof.CanonicalV1(redacted)" }
func (p CanonicalV1) GoString() string           { return p.String() }
func (CanonicalV1) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

func (p *CanonicalV1) Bytes() []byte {
	if p == nil || p.state == nil {
		return nil
	}
	return bytes.Clone(p.state.raw)
}
func (p *CanonicalV1) SHA256() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.sha
}
func (p *CanonicalV1) Bindings() (V1Bindings, error) {
	if p == nil || p.state == nil {
		return V1Bindings{}, ErrCanonical
	}
	return copyV1Bindings(p.state.bindings), nil
}

// EncodeV1 preserves the old semantic boundary before canonical encoding. It
// rejects invalid UTF-8 before encoding/json can replace malformed text.
func EncodeV1(input V1Input) (*CanonicalV1, error) {
	p := privateV1(input)
	if !v1TextValid(p) || v1ValidateProof(p) != nil {
		return nil, ErrCanonical
	}
	raw, err := v1CanonicalProof(p)
	if err != nil {
		return nil, ErrCanonical
	}
	return DecodeV1(raw)
}

// DecodeV1 retains the historical private parser and semantic validator. It
// additionally requires the supplied bytes to be the exact canonical original.
func DecodeV1(raw []byte) (*CanonicalV1, error) {
	var p v1Proof
	if v1StrictDecode(raw, &p) != nil || v1ValidateProof(p) != nil {
		return nil, ErrCanonical
	}
	canonical, err := v1CanonicalProof(p)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrCanonical
	}
	return &CanonicalV1{state: &canonicalV1State{
		raw: canonical, sha: v1SHA(canonical), bindings: publicV1(p),
	}}, nil
}

func copyV1Bindings(p V1Bindings) V1Bindings {
	p.Keys = append([]V1KeyPin(nil), p.Keys...)
	return p
}

func privateV1(p V1Input) v1Proof {
	role := func(r V1Role) v1RolePin { return v1RolePin(r) }
	schema := func(s V1Schema) v1SchemaPin {
		return v1SchemaPin{Name: s.Name, OID: s.OID, Owner: role(s.Owner), Runtime: role(s.Runtime)}
	}
	keys := make([]v1KeyPin, len(p.Keys))
	for i, k := range p.Keys {
		keys[i] = v1KeyPin(k)
	}
	return v1Proof{
		ProofVersion: p.ProofVersion, ProtocolVersion: p.ProtocolVersion,
		EnvironmentID: p.EnvironmentID, MerchantID: p.MerchantID, PayeeLabel: p.PayeeLabel,
		Database: v1DatabasePin(p.Database), App: schema(p.App), Mock: schema(p.Mock), Receiver: schema(p.Receiver),
		Worker: role(p.Worker), Projector: role(p.Projector),
		AppNonceDigest: p.AppNonceDigest, WorkerNonceDigest: p.WorkerNonceDigest, APITokenDigest: p.APITokenDigest,
		APIOrigin: p.APIOrigin, ControlOrigin: p.ControlOrigin, AppListenOrigin: p.AppListenOrigin,
		CallbackURL: p.CallbackURL, Keys: keys,
	}
}

func publicV1(p v1Proof) V1Bindings {
	role := func(r v1RolePin) V1Role { return V1Role(r) }
	schema := func(s v1SchemaPin) V1Schema {
		return V1Schema{Name: s.Name, OID: s.OID, Owner: role(s.Owner), Runtime: role(s.Runtime)}
	}
	keys := make([]V1KeyPin, len(p.Keys))
	for i, k := range p.Keys {
		keys[i] = V1KeyPin(k)
	}
	return V1Bindings{
		ProofVersion: p.ProofVersion, ProtocolVersion: p.ProtocolVersion,
		EnvironmentID: p.EnvironmentID, MerchantID: p.MerchantID, PayeeLabel: p.PayeeLabel,
		Database: V1Database(p.Database), App: schema(p.App), Mock: schema(p.Mock), Receiver: schema(p.Receiver),
		Worker: role(p.Worker), Projector: role(p.Projector),
		AppNonceDigest: p.AppNonceDigest, WorkerNonceDigest: p.WorkerNonceDigest, APITokenDigest: p.APITokenDigest,
		APIOrigin: p.APIOrigin, ControlOrigin: p.ControlOrigin, AppListenOrigin: p.AppListenOrigin,
		CallbackURL: p.CallbackURL, Keys: keys,
	}
}

func v1TextValid(p v1Proof) bool {
	values := []string{p.EnvironmentID, p.MerchantID, p.PayeeLabel, p.Database.Host, p.Database.Name,
		p.App.Name, p.Mock.Name, p.Receiver.Name, p.AppNonceDigest, p.WorkerNonceDigest,
		p.APITokenDigest, p.APIOrigin, p.ControlOrigin, p.AppListenOrigin, p.CallbackURL}
	for _, r := range v1Roles(p) {
		values = append(values, r.Name)
	}
	for _, k := range p.Keys {
		values = append(values, k.ID, k.Digest, k.NotBefore, k.NotAfter)
	}
	for _, s := range values {
		if !utf8.ValidString(s) {
			return false
		}
	}
	return true
}
