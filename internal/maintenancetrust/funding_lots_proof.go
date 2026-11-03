package maintenancetrust

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const fundingLotsProofLimit = 262144
const fundingLotsBasisDescriptor = "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"
const fundingLotsBasisSend = "1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b"

// The format leaf cannot authenticate these values. Only this original phase
// may assemble them, from its private evidence, before publishing owned bytes.
func (p *fundingLotsPhase) issueFundingLotsProof(ctx context.Context) error {
	if err := p.requireFundingLotsProofStage(ctx, fundingLotsDraft); err != nil {
		return err
	}
	if p.canonical != nil || p.expected != (FundingLotsExpected{}) {
		return ErrTransaction
	}
	if err := p.authority.verify(ctx); err != nil {
		return err
	}
	input, err := p.fundingLotsProofInput(ctx, p.authority.installed.Catalog())
	if err != nil {
		return err
	}
	canonical, err := fundinglotproof.EncodeV2(input)
	if err != nil {
		return ErrSchema
	}
	// Decode the actual complete original as well as comparing the bindings.
	decoded, err := fundinglotproof.DecodeV2(canonical.Bytes())
	if err != nil || decoded.SHA256() != canonical.SHA256() {
		return ErrSchema
	}
	bindings, err := decoded.Bindings()
	if err != nil || !fundingLotsProofBindingsEqual(bindings, input) {
		return ErrSchema
	}
	if err = p.requireFundingLotsProofStage(ctx, fundingLotsDraft); err != nil {
		return err
	}
	p.canonical = canonical
	p.expected = FundingLotsExpected{EpochID: input.EpochID, BasisDigest: input.BasisDigest,
		ManifestDigest: input.ManifestDigest, DescriptorDigest: input.BasisDescriptorDigest,
		RuntimeProofDigest: canonical.SHA256()}
	return nil
}

func (p *fundingLotsPhase) requireFundingLotsProofStage(ctx context.Context, stage fundingLotsStage) error {
	if err := p.verify(ctx); err != nil {
		return err
	}
	gs := p.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := p.validLocked(ctx); err != nil {
		return err
	}
	if p.stage != stage {
		return ErrTransaction
	}
	return nil
}

func fundingLotsProofRole(r fundinglotcatalog.RolePin) fundinglotproof.Role {
	return fundinglotproof.Role{Name: r.Name, OID: r.OID}
}

func fundingLotsProofSchema(s fundinglotcatalog.SchemaPin) fundinglotproof.Schema {
	return fundinglotproof.Schema{Name: s.Name, OID: s.OID,
		NamespaceOwner: fundingLotsProofRole(s.NamespaceOwner), ObjectOwner: fundingLotsProofRole(s.ObjectOwner)}
}

func fundingLotsProofBasisValid(b fundinglotbasis.Report) bool {
	return b.Scope == "fixed_109_table_basis_v1" && b.PhysicalEOFComplete && b.ScannedTables == 109 && b.ScannedColumns == 976 &&
		b.SourceRows <= 100000 && b.SourceBytes <= 64<<20 && fundingLotsDigest(b.BasisSHA256) &&
		b.ManifestSHA256 == authorityManifestSHA && b.DescriptorSHA256 == fundingLotsBasisDescriptor && b.SendMetadataSHA256 == fundingLotsBasisSend
}

// The supplied catalog is still required to belong to the original pins and
// transaction by the caller. This function exports neither evidence nor SQL.
func (p *fundingLotsPhase) fundingLotsProofInput(ctx context.Context, catalog *fundinglotcatalog.CatalogProof) (fundinglotproof.V2Input, error) {
	if err := p.verify(ctx); err != nil {
		return fundinglotproof.V2Input{}, err
	}
	if p == nil || p.snapshot == nil || p.snapshot.gate == nil || p.authority == nil || p.authority.manifest == nil ||
		p.authority.predecessor == nil || p.authority.installed == nil || catalog == nil || !fundingLotsProofBasisProfileValid(p.basis, p.snapshot.sourceProfile()) ||
		!fundingLotsUUID(p.epochID) || p.draft.Metadata.EpochID != p.epochID || !fundingLotsProofTimeEqual(p.draft.Metadata.EpochAt, p.epochAt) {
		return fundinglotproof.V2Input{}, ErrTransaction
	}
	a := p.authority
	pins := p.snapshot.gate.pins
	if !validPins(pins) || !catalog.MatchesPins(pins) || a.manifest.verifyAuthority(a) != nil || a.predecessor.verifyAuthority(a) != nil || a.predecessor.mode != pins.Mode {
		return fundinglotproof.V2Input{}, ErrTransaction
	}
	var manifest []fundinglotproof.Migration
	if json.Unmarshal(a.manifest.canonical, &manifest) != nil || len(manifest) != authorityManifestCount {
		return fundinglotproof.V2Input{}, ErrSchema
	}
	input := fundinglotproof.V2Input{
		Domain: fundinglotproof.DomainV2, ProofVersion: 2, EpochID: p.epochID,
		Database:  fundinglotproof.Database{Host: pins.Database.Host, Port: pins.Database.Port, Name: pins.Database.Name, OID: pins.Database.OID},
		AppSchema: fundingLotsProofSchema(pins.AppSchema), AppRole: fundingLotsProofRole(pins.App), ProjectorRole: fundingLotsProofRole(pins.Projector),
		BasisVersion: 1, BasisDigest: p.basis.BasisSHA256, BasisDescriptorDigest: p.basis.DescriptorSHA256,
		MigrationManifest: manifest, ManifestDigest: a.manifest.digest,
		CatalogDescriptorVersion: catalog.DescriptorVersion(), CatalogPostgresMajor: 16,
		CatalogExpectedDigest: catalog.ExpectedSHA256(), CatalogSchemaDigest: catalog.SchemaSHA256(), CatalogRolesDigest: catalog.RolesSHA256(),
	}
	switch a.predecessor.mode {
	case fundinglotcatalog.Ordinary:
		input.Mode = "ordinary"
		if len(a.predecessor.canonical) != 0 || a.predecessor.v1SHA256 != "" {
			return fundinglotproof.V2Input{}, ErrTransaction
		}
	case fundinglotcatalog.Sandbox:
		if pins.Sandbox == nil || pins.Worker == nil || len(a.predecessor.canonical) == 0 || !fundingLotsDigest(a.predecessor.v1SHA256) {
			return fundinglotproof.V2Input{}, ErrTransaction
		}
		input.Mode = "sandbox"
		input.Sandbox = &fundinglotproof.Sandbox{MockSchema: fundingLotsProofSchema(pins.Sandbox.Mock), MockRole: fundingLotsProofRole(pins.Sandbox.MockRuntime),
			ReceiverSchema: fundingLotsProofSchema(pins.Sandbox.Receiver), ReceiverRole: fundingLotsProofRole(pins.Sandbox.ReceiverRuntime), WorkerRole: fundingLotsProofRole(*pins.Worker)}
		digest := a.predecessor.v1SHA256
		original := base64.RawURLEncoding.EncodeToString(a.predecessor.canonical)
		input.PredecessorPaymentDigest, input.PredecessorPaymentCanonicalBase64URL = &digest, &original
	default:
		return fundinglotproof.V2Input{}, ErrTransaction
	}
	return input, nil
}

// Every format binding is compared explicitly, including the manifest entries
// and nullable mode union. A digest with valid syntax is never authentication.
func fundingLotsProofBindingsEqual(a, b fundinglotproof.V2Bindings) bool {
	if a.Domain != b.Domain || a.ProofVersion != b.ProofVersion || a.Mode != b.Mode || a.EpochID != b.EpochID ||
		a.Database != b.Database || a.AppSchema != b.AppSchema || a.AppRole != b.AppRole || a.ProjectorRole != b.ProjectorRole ||
		a.BasisVersion != b.BasisVersion || a.BasisDigest != b.BasisDigest || a.BasisDescriptorDigest != b.BasisDescriptorDigest ||
		a.ManifestDigest != b.ManifestDigest || a.CatalogDescriptorVersion != b.CatalogDescriptorVersion || a.CatalogPostgresMajor != b.CatalogPostgresMajor ||
		a.CatalogExpectedDigest != b.CatalogExpectedDigest || a.CatalogSchemaDigest != b.CatalogSchemaDigest || a.CatalogRolesDigest != b.CatalogRolesDigest ||
		len(a.MigrationManifest) != len(b.MigrationManifest) || !fundingLotsProofStringEqual(a.PredecessorPaymentDigest, b.PredecessorPaymentDigest) ||
		!fundingLotsProofStringEqual(a.PredecessorPaymentCanonicalBase64URL, b.PredecessorPaymentCanonicalBase64URL) || (a.Sandbox == nil) != (b.Sandbox == nil) {
		return false
	}
	for i := range a.MigrationManifest {
		if a.MigrationManifest[i] != b.MigrationManifest[i] {
			return false
		}
	}
	return a.Sandbox == nil || *a.Sandbox == *b.Sandbox
}

func fundingLotsProofStringEqual(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func fundingLotsProofTimeEqual(a, b fundinglotverify.WalletLotsTime) bool {
	return fundingLotsProofFinite(a) && fundingLotsProofFinite(b) && a.Value.Equal(b.Value)
}

func fundingLotsProofFinite(t fundinglotverify.WalletLotsTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276 && t.Value.Nanosecond()%1000 == 0
}

// Native timestamptz_send bytes preserve BC and five-or-more-digit years; no
// textual timestamp codec or RFC3339 range narrows the wallet time contract.
func fundingLotsProofTime(raw []byte) (fundinglotverify.WalletLotsTime, error) {
	if len(raw) != 8 {
		return fundinglotverify.WalletLotsTime{}, ErrSchema
	}
	var native pgtype.Timestamptz
	codec := &pgtype.TimestamptzCodec{}
	plan := codec.PlanScan(nil, pgtype.TimestamptzOID, pgtype.BinaryFormatCode, &native)
	if plan == nil || plan.Scan(raw, &native) != nil || !native.Valid || native.InfinityModifier != pgtype.Finite {
		return fundinglotverify.WalletLotsTime{}, ErrSchema
	}
	result := fundinglotverify.WalletLotsTime{Value: native.Time.UTC(), Valid: true}
	if !fundingLotsProofFinite(result) {
		return fundinglotverify.WalletLotsTime{}, ErrSchema
	}
	return result, nil
}

func (p *fundingLotsPhase) checkFundingLotsPostProof(ctx context.Context) error {
	if err := p.requireFundingLotsProofStage(ctx, fundingLotsPost); err != nil {
		return err
	}
	if p.canonical == nil || p.authority == nil || p.authority.installed == nil {
		return ErrTransaction
	}
	epoch, attestation, err := p.readFundingLotsProofRows(ctx)
	if err != nil {
		return err
	}
	// VerifyTx alone only checks identity. Recollect and compare the full static
	// catalog in this same original RR transaction after the fixed write set.
	pins := p.snapshot.gate.pins
	fresh, err := verifyOwnedSnapshotCatalog(ctx, p.snapshot.rawTx, pins, p.snapshot.sourceProfile())
	if err != nil || fresh == nil || fresh.VerifyTx(ctx, p.snapshot.rawTx) != nil || !fresh.MatchesPins(pins) {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	original := p.authority.installed.Catalog()
	if original == nil || fresh.DescriptorVersion() != original.DescriptorVersion() || fresh.ExpectedSHA256() != original.ExpectedSHA256() ||
		fresh.SchemaSHA256() != original.SchemaSHA256() || fresh.RolesSHA256() != original.RolesSHA256() {
		return ErrSchema
	}
	want, err := p.fundingLotsProofInput(ctx, fresh)
	if err != nil {
		return err
	}
	if !fundingLotsProofRowsMatch(epoch, attestation, p.canonical, want, p.draft.Metadata, p.epochAt) ||
		p.expected != (FundingLotsExpected{EpochID: want.EpochID, BasisDigest: want.BasisDigest, ManifestDigest: want.ManifestDigest,
			DescriptorDigest: want.BasisDescriptorDigest, RuntimeProofDigest: p.canonical.SHA256()}) {
		return ErrSchema
	}
	// The history helper actually rereads all 22 checksums; identity and fresh
	// activity samples are also exit facts, without repeating the empty gate.
	manifest, err := p.authority.attestManifest(ctx)
	if err != nil {
		return err
	}
	if manifest == nil || manifest.digest != p.authority.manifest.digest || !bytes.Equal(manifest.canonical, p.authority.manifest.canonical) {
		return ErrSchema
	}
	if err = p.authority.verifyEvidence(ctx); err != nil {
		return err
	}
	return p.requireFundingLotsProofStage(ctx, fundingLotsPost)
}

// The helper is descriptive only. Its caller must acquire the real rows under
// the original phase owner; no report or caller row can grant write authority.
func fundingLotsProofRowsMatch(e fundinglotverify.WalletLotsEpochRow, a fundinglotverify.WalletLotsAttestationRow,
	issued *fundinglotproof.CanonicalV2, want fundinglotproof.V2Input, metadata fundinglotinit.Metadata, at fundinglotverify.WalletLotsTime) bool {
	if issued == nil || len(a.ProofCanonical) == 0 || len(a.ProofCanonical) > fundingLotsProofLimit ||
		!bytes.Equal(a.ProofCanonical, issued.Bytes()) || a.ProofSHA256 != issued.SHA256() {
		return false
	}
	decoded, err := fundinglotproof.DecodeV2(a.ProofCanonical)
	if err != nil || decoded.SHA256() != a.ProofSHA256 {
		return false
	}
	bindings, err := decoded.Bindings()
	if err != nil || !fundingLotsProofBindingsEqual(bindings, want) {
		return false
	}
	issuedBindings, err := issued.Bindings()
	if err != nil || !fundingLotsProofBindingsEqual(issuedBindings, want) {
		return false
	}
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	if !e.Singleton || !a.Singleton || e.ID != want.EpochID || metadata.EpochID != want.EpochID || a.EpochID != want.EpochID || e.SchemaVersion != "1" || a.ProofVersion != "2" ||
		a.Mode != want.Mode || e.DatabaseOID != u(uint64(want.Database.OID)) || a.DatabaseOID != e.DatabaseOID ||
		e.SchemaOID != u(uint64(want.AppSchema.OID)) || a.SchemaOID != e.SchemaOID || a.SchemaOwnerOID != u(uint64(want.AppSchema.ObjectOwner.OID)) ||
		a.AppRoleOID != u(uint64(want.AppRole.OID)) || a.ProjectorRoleOID != u(uint64(want.ProjectorRole.OID)) ||
		e.BasisDigest != want.BasisDigest || e.ManifestDigest != want.ManifestDigest || a.ManifestDigest != e.ManifestDigest ||
		e.DescriptorDigest != want.BasisDescriptorDigest || a.DescriptorDigest != e.DescriptorDigest || e.RuntimeProofDigest != a.ProofSHA256 ||
		e.TenantCount != u(metadata.TenantCount) || e.ReceiptCount != u(metadata.ReceiptCount) || e.SettlementCount != u(metadata.SettlementCount) ||
		e.ApprovedRefundCount != u(metadata.ApprovedRefundCount) || e.OpeningCount != u(metadata.OpeningCount) || e.LegacySourceCount != u(metadata.LegacySourceCount) ||
		e.AvailableTotal != metadata.AvailableTotal || e.HeldTotal != metadata.HeldTotal || e.Actor != "offline-maintenance" ||
		!fundingLotsProofTimeEqual(e.CreatedAt, at) || !fundingLotsProofTimeEqual(a.CreatedAt, at) || !fundingLotsProofTimeEqual(metadata.EpochAt, at) ||
		!fundingLotsProofStringEqual(a.PredecessorPaymentDigest, want.PredecessorPaymentDigest) {
		return false
	}
	if want.Mode == "ordinary" {
		return a.WorkerRoleOID == nil && want.Sandbox == nil && a.PredecessorPaymentDigest == nil
	}
	return want.Mode == "sandbox" && want.Sandbox != nil && a.WorkerRoleOID != nil && *a.WorkerRoleOID == u(uint64(want.Sandbox.WorkerRole.OID))
}

func (p *fundingLotsPhase) fundingLotsProofOne(ctx context.Context, sql string, destinations ...any) error {
	rows, err := p.query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	more, err := rows.Next()
	if err != nil {
		return err
	}
	if !more {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	if err = rows.Scan(destinations...); err != nil {
		return err
	}
	if more, err = rows.Next(); err != nil {
		return err
	}
	if more {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	return rows.Err()
}

func (p *fundingLotsPhase) readFundingLotsProofRows(ctx context.Context) (fundinglotverify.WalletLotsEpochRow, fundinglotverify.WalletLotsAttestationRow, error) {
	var e fundinglotverify.WalletLotsEpochRow
	var a fundinglotverify.WalletLotsAttestationRow
	epochTable := pgx.Identifier{p.snapshot.gate.pins.AppSchema.Name, "funding_lot_epoch"}.Sanitize()
	attestationTable := pgx.Identifier{p.snapshot.gate.pins.AppSchema.Name, "funding_lot_runtime_attestations"}.Sanitize()
	var epochCount, attestationCount string
	var bounded bool
	if err := p.fundingLotsProofOne(ctx, `SELECT pg_catalog.count(*)::pg_catalog.text FROM ONLY `+epochTable, &epochCount); err != nil {
		return e, a, err
	}
	// Bound bytea before selecting it. This remains in the same immutable RR
	// view, and the protected writer has completed all writes before this call.
	if err := p.fundingLotsProofOne(ctx, `SELECT pg_catalog.count(*)::pg_catalog.text,
 COALESCE(pg_catalog.bool_and(pg_catalog.octet_length(proof_canonical) OPERATOR(pg_catalog.>=) 1
 AND pg_catalog.octet_length(proof_canonical) OPERATOR(pg_catalog.<=) 262144),false) FROM ONLY `+attestationTable,
		&attestationCount, &bounded); err != nil {
		return e, a, err
	}
	if epochCount != "1" || attestationCount != "1" || !bounded {
		return e, a, ErrSchema
	}
	var epochAt, attestationAt []byte
	if err := p.fundingLotsProofOne(ctx, `SELECT singleton,id,schema_version::pg_catalog.text,database_oid::pg_catalog.text,schema_oid::pg_catalog.text,
 basis_digest,manifest_digest,descriptor_digest,runtime_proof_digest,tenant_count::pg_catalog.text,receipt_count::pg_catalog.text,
 settlement_count::pg_catalog.text,approved_refund_count::pg_catalog.text,opening_count::pg_catalog.text,legacy_source_count::pg_catalog.text,
 available_total::pg_catalog.text,held_total::pg_catalog.text,actor,pg_catalog.timestamptz_send(created_at) FROM ONLY `+epochTable,
		&e.Singleton, &e.ID, &e.SchemaVersion, &e.DatabaseOID, &e.SchemaOID, &e.BasisDigest, &e.ManifestDigest, &e.DescriptorDigest, &e.RuntimeProofDigest,
		&e.TenantCount, &e.ReceiptCount, &e.SettlementCount, &e.ApprovedRefundCount, &e.OpeningCount, &e.LegacySourceCount,
		&e.AvailableTotal, &e.HeldTotal, &e.Actor, &epochAt); err != nil {
		return e, a, err
	}
	if err := p.fundingLotsProofOne(ctx, `SELECT singleton,epoch_id,proof_version::pg_catalog.text,mode,database_oid::pg_catalog.text,schema_oid::pg_catalog.text,
 schema_owner_oid::pg_catalog.text,app_role_oid::pg_catalog.text,projector_role_oid::pg_catalog.text,worker_role_oid::pg_catalog.text,
 predecessor_payment_digest,proof_canonical,proof_sha256,manifest_digest,descriptor_digest,pg_catalog.timestamptz_send(created_at) FROM ONLY `+attestationTable,
		&a.Singleton, &a.EpochID, &a.ProofVersion, &a.Mode, &a.DatabaseOID, &a.SchemaOID, &a.SchemaOwnerOID, &a.AppRoleOID, &a.ProjectorRoleOID,
		&a.WorkerRoleOID, &a.PredecessorPaymentDigest, &a.ProofCanonical, &a.ProofSHA256, &a.ManifestDigest, &a.DescriptorDigest, &attestationAt); err != nil {
		return e, a, err
	}
	var err error
	if e.CreatedAt, err = fundingLotsProofTime(epochAt); err != nil {
		return e, a, err
	}
	if a.CreatedAt, err = fundingLotsProofTime(attestationAt); err != nil {
		return e, a, err
	}
	return e, a, nil
}
