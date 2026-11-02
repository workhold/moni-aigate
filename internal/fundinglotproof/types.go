// Package fundinglotproof encodes reviewed proof formats without authenticating
// a database, authorizing maintenance, or permitting process startup.
package fundinglotproof

import "errors"

const DomainV2 = "moni-aigate/funding-lots-runtime/v2"

var ErrCanonical = errors.New("funding_lot_proof_invalid")

type Database struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type Role struct {
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type Schema struct {
	Name           string `json:"name"`
	OID            uint32 `json:"oid"`
	NamespaceOwner Role   `json:"namespace_owner"`
	ObjectOwner    Role   `json:"object_owner"`
}
type Migration struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type Sandbox struct {
	MockSchema     Schema `json:"mock_schema"`
	MockRole       Role   `json:"mock_role"`
	ReceiverSchema Schema `json:"receiver_schema"`
	ReceiverRole   Role   `json:"receiver_role"`
	WorkerRole     Role   `json:"worker_role"`
}

// V2Input contains self-reported format values, never authenticated facts.
// Nullable union fields are mandatory in the wire even when their value is nil.
type V2Input struct {
	Domain                               string      `json:"domain"`
	ProofVersion                         uint32      `json:"proof_version"`
	Mode                                 string      `json:"mode"`
	EpochID                              string      `json:"epoch_id"`
	Database                             Database    `json:"database"`
	AppSchema                            Schema      `json:"app_schema"`
	AppRole                              Role        `json:"app_role"`
	ProjectorRole                        Role        `json:"projector_role"`
	Sandbox                              *Sandbox    `json:"sandbox"`
	BasisVersion                         uint32      `json:"basis_version"`
	BasisDigest                          string      `json:"basis_digest"`
	BasisDescriptorDigest                string      `json:"basis_descriptor_digest"`
	MigrationManifest                    []Migration `json:"migration_manifest"`
	ManifestDigest                       string      `json:"manifest_digest"`
	CatalogDescriptorVersion             uint32      `json:"catalog_descriptor_version"`
	CatalogPostgresMajor                 uint32      `json:"catalog_postgres_major"`
	CatalogExpectedDigest                string      `json:"catalog_expected_digest"`
	CatalogSchemaDigest                  string      `json:"catalog_schema_digest"`
	CatalogRolesDigest                   string      `json:"catalog_roles_digest"`
	PredecessorPaymentDigest             *string     `json:"predecessor_payment_digest"`
	PredecessorPaymentCanonicalBase64URL *string     `json:"predecessor_payment_canonical_base64url"`
}

// V2Bindings is an owned copy of untrusted format values, not a runtime proof.
type V2Bindings = V2Input

// CanonicalV2 proves only complete strict format/canonical-byte validity.
// Its zero value is invalid; copies share immutable owned state.
type CanonicalV2 struct{ state *canonicalState }
type canonicalState struct {
	raw      []byte
	sha      string
	bindings V2Bindings
}

func (CanonicalV2) String() string               { return "fundinglotproof.CanonicalV2(redacted)" }
func (p CanonicalV2) GoString() string           { return p.String() }
func (CanonicalV2) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

func (p *CanonicalV2) Bytes() []byte {
	if p == nil || p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.raw...)
}
func (p *CanonicalV2) SHA256() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.sha
}
func (p *CanonicalV2) Bindings() (V2Bindings, error) {
	if p == nil || p.state == nil {
		return V2Bindings{}, ErrCanonical
	}
	return copyBindings(p.state.bindings), nil
}
func copyBindings(v V2Bindings) V2Bindings {
	v.MigrationManifest = append([]Migration(nil), v.MigrationManifest...)
	if v.Sandbox != nil {
		s := *v.Sandbox
		v.Sandbox = &s
	}
	if v.PredecessorPaymentDigest != nil {
		s := *v.PredecessorPaymentDigest
		v.PredecessorPaymentDigest = &s
	}
	if v.PredecessorPaymentCanonicalBase64URL != nil {
		s := *v.PredecessorPaymentCanonicalBase64URL
		v.PredecessorPaymentCanonicalBase64URL = &s
	}
	return v
}
