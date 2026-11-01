// Package fundinglotcatalog verifies reviewed static installation metadata in a
// caller-owned transaction. Its proofs do not authorize maintenance or money.
package fundinglotcatalog

import "errors"

type Mode uint8

const (
	Ordinary Mode = iota + 1
	Sandbox
)

type RuntimeRole uint8

const (
	Application RuntimeRole = iota + 1
	Worker
	Projector
)

type RolePin struct {
	Name string
	OID  uint32
}

type DatabasePin struct {
	Host string
	Port uint16
	Name string
	OID  uint32
}

type SchemaPin struct {
	Name           string
	OID            uint32
	NamespaceOwner RolePin
	ObjectOwner    RolePin
}

type SandboxPins struct {
	Mock            SchemaPin
	MockRuntime     RolePin
	Receiver        SchemaPin
	ReceiverRuntime RolePin
}

type Pins struct {
	Mode      Mode
	Database  DatabasePin
	AppSchema SchemaPin
	App       RolePin
	Projector RolePin
	Worker    *RolePin
	Sandbox   *SandboxPins
}

var ErrCatalog = errors.New("funding_lot_catalog_verification_failed")

// CatalogProof is evidence of a complete static comparison in one transaction.
// Zero values are invalid. A copy shares, and cannot rebind, the original proof.
type CatalogProof struct{ identity *proofIdentity }

// InstallationProof additionally records that all sixteen C0 tables were empty
// when issued. VerifyTx does not require them to remain empty after init writes.
type InstallationProof struct{ identity *proofIdentity }
