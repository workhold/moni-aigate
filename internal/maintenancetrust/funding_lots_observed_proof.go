package maintenancetrust

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// These descriptive roots have no transaction or maintenance authority. Only
// the outer observer can authenticate the CatalogProof and its live binding.
type fundingLotsObservedCatalog struct {
	version                 uint32
	expected, schema, roles string
}

// fundingLotsObservedProof validates the actual immutable originals against
// already authenticated current evidence. It issues comparison values only;
// the caller must independently check the same-tx catalog and complete graph.
func fundingLotsObservedProof(rows fundinglotscan.EpochProofRows, pins fundinglotcatalog.Pins,
	catalog *fundinglotcatalog.CatalogProof, manifest []byte, predecessorMode fundinglotcatalog.Mode,
	predecessorCanonical []byte, predecessorDigest string) (FundingLotsExpected, error) {
	if !validPins(pins) || catalog == nil || !catalog.MatchesPins(pins) {
		return FundingLotsExpected{}, ErrSchema
	}
	current := fundingLotsObservedCatalog{version: catalog.DescriptorVersion(), expected: catalog.ExpectedSHA256(),
		schema: catalog.SchemaSHA256(), roles: catalog.RolesSHA256()}
	return fundingLotsObservedProofAgainst(rows, pins, current, manifest, predecessorMode, predecessorCanonical, predecessorDigest)
}

// This pure comparison is also exercised without manufacturing an opaque
// CatalogProof. Neither these inputs nor its ordinary output authenticate a DB.
func fundingLotsObservedProofAgainst(rows fundinglotscan.EpochProofRows, pins fundinglotcatalog.Pins,
	current fundingLotsObservedCatalog, manifest []byte, predecessorMode fundinglotcatalog.Mode,
	predecessorCanonical []byte, predecessorDigest string) (FundingLotsExpected, error) {
	e, a := rows.Epoch, rows.Attestation
	if !validPins(pins) || predecessorMode != pins.Mode || current.version == 0 ||
		!fundingLotsDigest(current.expected) || !fundingLotsDigest(current.schema) || !fundingLotsDigest(current.roles) ||
		!authorityManifestValid(manifest, authorityManifestSHA) || !fundingLotsUUID(e.ID) ||
		!fundingLotsDigest(e.BasisDigest) || len(a.ProofCanonical) == 0 || len(a.ProofCanonical) > fundingLotsProofLimit {
		return FundingLotsExpected{}, ErrSchema
	}
	var migrations []fundinglotproof.Migration
	if json.Unmarshal(manifest, &migrations) != nil || len(migrations) != authorityManifestCount {
		return FundingLotsExpected{}, ErrSchema
	}
	// The initial data basis remains historical. Its saved digest is compared
	// with the original, never recomputed from business rows after cutover.
	want := fundinglotproof.V2Input{
		Domain: fundinglotproof.DomainV2, ProofVersion: 2, EpochID: e.ID,
		Database:  fundinglotproof.Database{Host: pins.Database.Host, Port: pins.Database.Port, Name: pins.Database.Name, OID: pins.Database.OID},
		AppSchema: fundingLotsProofSchema(pins.AppSchema), AppRole: fundingLotsProofRole(pins.App), ProjectorRole: fundingLotsProofRole(pins.Projector),
		BasisVersion: 1, BasisDigest: e.BasisDigest, BasisDescriptorDigest: fundingLotsBasisDescriptor,
		MigrationManifest: migrations, ManifestDigest: authorityManifestSHA,
		CatalogDescriptorVersion: current.version, CatalogPostgresMajor: 16,
		CatalogExpectedDigest: current.expected, CatalogSchemaDigest: current.schema, CatalogRolesDigest: current.roles,
	}
	switch predecessorMode {
	case fundinglotcatalog.Ordinary:
		if len(predecessorCanonical) != 0 || predecessorDigest != "" {
			return FundingLotsExpected{}, ErrSchema
		}
		want.Mode = "ordinary"
	case fundinglotcatalog.Sandbox:
		if pins.Sandbox == nil || pins.Worker == nil || len(predecessorCanonical) == 0 || len(predecessorCanonical) > 65536 || !fundingLotsDigest(predecessorDigest) {
			return FundingLotsExpected{}, ErrSchema
		}
		want.Mode = "sandbox"
		want.Sandbox = &fundinglotproof.Sandbox{
			MockSchema: fundingLotsProofSchema(pins.Sandbox.Mock), MockRole: fundingLotsProofRole(pins.Sandbox.MockRuntime),
			ReceiverSchema: fundingLotsProofSchema(pins.Sandbox.Receiver), ReceiverRole: fundingLotsProofRole(pins.Sandbox.ReceiverRuntime), WorkerRole: fundingLotsProofRole(*pins.Worker),
		}
		original := base64.RawURLEncoding.EncodeToString(predecessorCanonical)
		want.PredecessorPaymentDigest, want.PredecessorPaymentCanonicalBase64URL = &predecessorDigest, &original
	default:
		return FundingLotsExpected{}, ErrSchema
	}
	decoded, err := fundinglotproof.DecodeV2(a.ProofCanonical)
	if err != nil || decoded.SHA256() != a.ProofSHA256 {
		return FundingLotsExpected{}, ErrSchema
	}
	bindings, err := decoded.Bindings()
	if err != nil || !fundingLotsProofBindingsEqual(bindings, want) {
		return FundingLotsExpected{}, ErrSchema
	}
	u := func(value uint32) string { return strconv.FormatUint(uint64(value), 10) }
	if !e.Singleton || !a.Singleton || e.SchemaVersion != "1" || a.ProofVersion != "2" || a.EpochID != e.ID || a.Mode != want.Mode ||
		e.DatabaseOID != u(pins.Database.OID) || a.DatabaseOID != e.DatabaseOID || e.SchemaOID != u(pins.AppSchema.OID) || a.SchemaOID != e.SchemaOID ||
		a.SchemaOwnerOID != u(pins.AppSchema.ObjectOwner.OID) || a.AppRoleOID != u(pins.App.OID) || a.ProjectorRoleOID != u(pins.Projector.OID) ||
		e.ManifestDigest != authorityManifestSHA || a.ManifestDigest != e.ManifestDigest || e.DescriptorDigest != fundingLotsBasisDescriptor || a.DescriptorDigest != e.DescriptorDigest ||
		e.RuntimeProofDigest != a.ProofSHA256 || e.Actor != "offline-maintenance" || !fundingLotsProofTimeEqual(e.CreatedAt, a.CreatedAt) ||
		!fundingLotsProofStringEqual(a.PredecessorPaymentDigest, want.PredecessorPaymentDigest) {
		return FundingLotsExpected{}, ErrSchema
	}
	if predecessorMode == fundinglotcatalog.Ordinary {
		if a.WorkerRoleOID != nil {
			return FundingLotsExpected{}, ErrSchema
		}
	} else if a.WorkerRoleOID == nil || *a.WorkerRoleOID != u(pins.Worker.OID) {
		return FundingLotsExpected{}, ErrSchema
	}
	return FundingLotsExpected{EpochID: e.ID, BasisDigest: e.BasisDigest, ManifestDigest: e.ManifestDigest,
		DescriptorDigest: e.DescriptorDigest, RuntimeProofDigest: e.RuntimeProofDigest}, nil
}
