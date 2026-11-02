package fundinglotruntime

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type applicationSnapshot struct {
	proof          *fundinglotproof.CanonicalV1
	bindings       fundinglotproof.V1Bindings
	appNonceDigest string
}

type runtimeBinding struct {
	epoch, proofSHA, basisSHA, manifestSHA string
	basisDescriptorSHA, predecessorSHA     string
	compatibility                          compatibilityRecord
}

func cloneRuntimePins(p fundinglotcatalog.Pins) fundinglotcatalog.Pins {
	if p.Worker != nil {
		r := *p.Worker
		p.Worker = &r
	}
	if p.Sandbox != nil {
		s := *p.Sandbox
		p.Sandbox = &s
	}
	return p
}

// Snapshot only private, already-loaded immutable state. The result retains no
// caller wrapper, plaintext material, file descriptor, or loader context.
func snapshotApplication(w *verificationWindow, p *ApplicationState) (*applicationSnapshot, error) {
	if w.check() != nil || p == nil || p.state == nil || p.state.proof == nil {
		return nil, ErrRuntime
	}
	s := p.state
	canonical, err := fundinglotproof.DecodeV1(s.proof.Bytes())
	if err != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	b, err := canonical.Bindings()
	if err != nil || b.AppNonceDigest != s.appNonceDigest || w.check() != nil {
		return nil, ErrRuntime
	}
	return &applicationSnapshot{proof: canonical, bindings: b, appNonceDigest: s.appNonceDigest}, nil
}

// This comparison consumes only deep-owned originals projected from one full
// scan. It issues ordinary private values; catalog/transaction authentication
// and complete scanner qualification belong to the outer composer.
func bindRuntimeOriginals(w *verificationWindow, pins fundinglotcatalog.Pins, current currentCatalog,
	expected *fundinglotproof.CanonicalV2, rows fundinglotscan.EpochProofRows,
	payment fundinglotscan.ApplicationPaymentProofRows, local *applicationSnapshot) (runtimeBinding, error) {
	if w.check() != nil || expected == nil {
		return runtimeBinding{}, ErrRuntime
	}
	e, a := rows.Epoch, rows.Attestation
	decoded, err := fundinglotproof.DecodeV2(a.ProofCanonical)
	if err != nil || w.check() != nil || decoded.SHA256() != a.ProofSHA256 ||
		decoded.SHA256() != expected.SHA256() || !bytes.Equal(decoded.Bytes(), expected.Bytes()) {
		return runtimeBinding{}, ErrRuntime
	}
	b, err := decoded.Bindings()
	if err != nil || w.check() != nil {
		return runtimeBinding{}, ErrRuntime
	}
	mapping, err := matchCompatibility(b, current)
	if err != nil || w.check() != nil {
		return runtimeBinding{}, ErrRuntime
	}
	mode := "ordinary"
	if pins.Mode == fundinglotcatalog.Sandbox {
		mode = "sandbox"
	} else if pins.Mode != fundinglotcatalog.Ordinary {
		return runtimeBinding{}, ErrRuntime
	}
	if b.Domain != fundinglotproof.DomainV2 || b.ProofVersion != 2 || b.Mode != mode || b.EpochID != e.ID ||
		b.Database != (fundinglotproof.Database{Host: pins.Database.Host, Port: pins.Database.Port, Name: pins.Database.Name, OID: pins.Database.OID}) ||
		b.AppSchema != runtimeProofSchema(pins.AppSchema) || b.AppRole != runtimeProofRole(pins.App) ||
		b.ProjectorRole != runtimeProofRole(pins.Projector) || b.BasisDigest != e.BasisDigest {
		return runtimeBinding{}, ErrRuntime
	}
	if !e.Singleton || !a.Singleton || e.SchemaVersion != "1" || a.ProofVersion != "2" ||
		a.EpochID != e.ID || a.Mode != mode || e.DatabaseOID != runtimeOID(pins.Database.OID) || a.DatabaseOID != e.DatabaseOID ||
		e.SchemaOID != runtimeOID(pins.AppSchema.OID) || a.SchemaOID != e.SchemaOID ||
		a.SchemaOwnerOID != runtimeOID(pins.AppSchema.ObjectOwner.OID) || a.AppRoleOID != runtimeOID(pins.App.OID) ||
		a.ProjectorRoleOID != runtimeOID(pins.Projector.OID) || e.ManifestDigest != b.ManifestDigest || a.ManifestDigest != e.ManifestDigest ||
		e.DescriptorDigest != b.BasisDescriptorDigest || a.DescriptorDigest != e.DescriptorDigest ||
		e.RuntimeProofDigest != decoded.SHA256() || e.Actor != "offline-maintenance" ||
		!runtimeEpochTime(e.CreatedAt) || !runtimeEpochTime(a.CreatedAt) || !e.CreatedAt.Value.Equal(a.CreatedAt.Value) ||
		!runtimeStringEqual(a.PredecessorPaymentDigest, b.PredecessorPaymentDigest) {
		return runtimeBinding{}, ErrRuntime
	}
	predecessor := ""
	if pins.Mode == fundinglotcatalog.Ordinary {
		if pins.Worker != nil || pins.Sandbox != nil || b.Sandbox != nil || b.PredecessorPaymentDigest != nil ||
			b.PredecessorPaymentCanonicalBase64URL != nil || a.WorkerRoleOID != nil ||
			payment.Environment != nil || payment.Attestation != nil || local != nil {
			return runtimeBinding{}, ErrRuntime
		}
	} else {
		if pins.Worker == nil || pins.Sandbox == nil || b.Sandbox == nil || a.WorkerRoleOID == nil ||
			*a.WorkerRoleOID != runtimeOID(pins.Worker.OID) || b.PredecessorPaymentDigest == nil || b.PredecessorPaymentCanonicalBase64URL == nil {
			return runtimeBinding{}, ErrRuntime
		}
		want := fundinglotproof.Sandbox{
			MockSchema: runtimeProofSchema(pins.Sandbox.Mock), MockRole: runtimeProofRole(pins.Sandbox.MockRuntime),
			ReceiverSchema: runtimeProofSchema(pins.Sandbox.Receiver), ReceiverRole: runtimeProofRole(pins.Sandbox.ReceiverRuntime),
			WorkerRole: runtimeProofRole(*pins.Worker),
		}
		if *b.Sandbox != want || bindApplicationOriginals(w, pins, b, payment, local) != nil {
			return runtimeBinding{}, ErrRuntime
		}
		predecessor = *b.PredecessorPaymentDigest
	}
	if w.check() != nil {
		return runtimeBinding{}, ErrRuntime
	}
	return runtimeBinding{epoch: e.ID, proofSHA: decoded.SHA256(), basisSHA: b.BasisDigest, manifestSHA: b.ManifestDigest,
		basisDescriptorSHA: b.BasisDescriptorDigest, predecessorSHA: predecessor, compatibility: mapping}, nil
}

func bindApplicationOriginals(w *verificationWindow, pins fundinglotcatalog.Pins, v2 fundinglotproof.V2Bindings,
	rows fundinglotscan.ApplicationPaymentProofRows, local *applicationSnapshot) error {
	if w.check() != nil || pins.Worker == nil || pins.Sandbox == nil || local == nil || local.proof == nil ||
		rows.Environment == nil || rows.Attestation == nil || v2.PredecessorPaymentDigest == nil || v2.PredecessorPaymentCanonicalBase64URL == nil {
		return ErrRuntime
	}
	e, a, b := rows.Environment, rows.Attestation, local.bindings
	// Decode again at this boundary rather than treating a DTO or a digest as
	// a substitute for complete canonical-format qualification.
	original, err := fundinglotproof.DecodeV1(a.ProofCanonical)
	if err != nil || original.SHA256() != a.ProofSHA256 || original.SHA256() != local.proof.SHA256() ||
		!bytes.Equal(original.Bytes(), local.proof.Bytes()) || w.check() != nil {
		return ErrRuntime
	}
	predecessor, err := base64.RawURLEncoding.DecodeString(*v2.PredecessorPaymentCanonicalBase64URL)
	if err != nil || base64.RawURLEncoding.EncodeToString(predecessor) != *v2.PredecessorPaymentCanonicalBase64URL ||
		*v2.PredecessorPaymentDigest != original.SHA256() || !bytes.Equal(predecessor, original.Bytes()) ||
		!runtimeV1Pins(b, pins) || local.appNonceDigest != b.AppNonceDigest || w.check() != nil {
		return ErrRuntime
	}
	if !e.Singleton || e.ID != b.EnvironmentID || e.MerchantID != b.MerchantID || e.PayeeLabel != b.PayeeLabel ||
		e.DatabaseOID != runtimeOID(pins.Database.OID) || e.SchemaOID != runtimeOID(pins.AppSchema.OID) ||
		e.NonceDigest != local.appNonceDigest || e.AppRole != pins.App.Name || e.WorkerRole != pins.Worker.Name ||
		e.ProjectorRole != pins.Projector.Name || e.ProtocolVersion != "1" || !runtimeApplicationTime(e.CreatedAt) {
		return ErrRuntime
	}
	if !a.Singleton || a.EnvironmentID != b.EnvironmentID || a.DatabaseOID != runtimeOID(pins.Database.OID) ||
		a.SchemaOID != runtimeOID(pins.AppSchema.OID) || a.SchemaOwnerOID != runtimeOID(pins.AppSchema.ObjectOwner.OID) ||
		a.AppRoleOID != runtimeOID(pins.App.OID) || a.WorkerRoleOID != runtimeOID(pins.Worker.OID) ||
		a.ProjectorRoleOID != runtimeOID(pins.Projector.OID) || a.AppNonceDigest != b.AppNonceDigest ||
		a.WorkerNonceDigest != b.WorkerNonceDigest || a.ProofVersion != "1" || a.CreatedAt.IsZero() || !runtimeApplicationTime(a.CreatedAt) {
		return ErrRuntime
	}
	return w.check()
}

func runtimeV1Pins(b fundinglotproof.V1Bindings, p fundinglotcatalog.Pins) bool {
	if p.Worker == nil || p.Sandbox == nil ||
		b.Database != (fundinglotproof.V1Database{Host: p.Database.Host, Port: p.Database.Port, Name: p.Database.Name, OID: p.Database.OID}) {
		return false
	}
	role := func(a fundinglotproof.V1Role, b fundinglotcatalog.RolePin, login bool) bool {
		return a.Name == b.Name && a.OID == b.OID && a.Login == login
	}
	schema := func(a fundinglotproof.V1Schema, b fundinglotcatalog.SchemaPin, r fundinglotcatalog.RolePin) bool {
		return a.Name == b.Name && a.OID == b.OID && b.NamespaceOwner == b.ObjectOwner && role(a.Owner, b.ObjectOwner, false) && role(a.Runtime, r, true)
	}
	return schema(b.App, p.AppSchema, p.App) && schema(b.Mock, p.Sandbox.Mock, p.Sandbox.MockRuntime) &&
		schema(b.Receiver, p.Sandbox.Receiver, p.Sandbox.ReceiverRuntime) && role(b.Worker, *p.Worker, true) && role(b.Projector, p.Projector, true)
}

func runtimeProofRole(p fundinglotcatalog.RolePin) fundinglotproof.Role {
	return fundinglotproof.Role{Name: p.Name, OID: p.OID}
}
func runtimeProofSchema(p fundinglotcatalog.SchemaPin) fundinglotproof.Schema {
	return fundinglotproof.Schema{Name: p.Name, OID: p.OID, NamespaceOwner: runtimeProofRole(p.NamespaceOwner), ObjectOwner: runtimeProofRole(p.ObjectOwner)}
}
func runtimeOID(oid uint32) string { return strconv.FormatUint(uint64(oid), 10) }
func runtimeStringEqual(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func runtimeEpochTime(t fundinglotverify.WalletLotsTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.UTC().Year() >= -4712 && t.Value.UTC().Year() <= 294276 && t.Value.Nanosecond()%1000 == 0
}
func runtimeApplicationTime(t time.Time) bool {
	return t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}
