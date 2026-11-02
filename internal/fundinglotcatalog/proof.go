package fundinglotcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
)

const catalogBudget = 5 * time.Second

type proofIdentity struct {
	transaction transactionIdentity
	pins        Pins
	version     uint32
	expected    string
	schema      string
	roles       string
	profile     Profile
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func boundedContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, nil, ErrCatalog
	}
	c, cancel := context.WithTimeout(ctx, catalogBudget)
	return c, cancel, nil
}

// VerifySnapshotTx compares the complete static catalog without taking over the
// caller's transaction. It grants no initialization or runtime authorization.
func VerifySnapshotTx(ctx context.Context, tx pgx.Tx, pins Pins) (*CatalogProof, error) {
	pins = copyPins(pins)
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyCatalogTx(c, tx, pins, 0)
}

// VerifyRuntimeTx additionally requires the selected real LOGIN and RR RO.
func VerifyRuntimeTx(ctx context.Context, tx pgx.Tx, pins Pins, role RuntimeRole) (*CatalogProof, error) {
	pins = copyPins(pins)
	if role != Application && role != Worker && role != Projector || role == Worker && pins.Mode != Sandbox {
		return nil, ErrCatalog
	}
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyCatalogTx(c, tx, pins, role)
}

func verifyCatalogTx(ctx context.Context, tx pgx.Tx, pins Pins, runtime RuntimeRole) (*CatalogProof, error) {
	return verifyCatalogProfileTx(ctx, tx, pins, runtime, LegacyV1)
}

func verifyCatalogProfileTx(ctx context.Context, tx pgx.Tx, pins Pins, runtime RuntimeRole, profile Profile) (*CatalogProof, error) {
	expected, root, err := loadExpectedProfile(pins, profile)
	if err != nil || expected == nil || expected.FormatVersion == 0 || len(root) != 64 {
		return nil, ErrCatalog
	}
	id, err := identifyTx(ctx, tx, pins)
	if err != nil {
		return nil, ErrCatalog
	}
	if runtime != 0 {
		selected := pins.App
		if runtime == Projector {
			selected = pins.Projector
		} else if runtime == Worker {
			if pins.Worker == nil {
				return nil, ErrCatalog
			}
			selected = *pins.Worker
		}
		if !id.readOnly || selected.Name != id.role || selected.OID != id.roleOID {
			return nil, ErrCatalog
		}
	}
	snapshot, err := collectTx(ctx, tx, pins, expected)
	if err != nil || snapshot == nil || len(snapshot.schemaCanonical) == 0 || len(snapshot.rolesCanonical) == 0 {
		return nil, ErrCatalog
	}
	after, err := identifyTx(ctx, tx, pins)
	if err != nil || !id.same(after) || ctx.Err() != nil {
		return nil, ErrCatalog
	}
	return &CatalogProof{identity: &proofIdentity{
		transaction: id, pins: copyPins(pins), version: expected.FormatVersion, expected: root, profile: profile,
		schema: hashBytes(snapshot.schemaCanonical), roles: hashBytes(snapshot.rolesCanonical),
	}}, nil
}

var installationTables = [...]string{
	"funding_consumption_refund_lots", "funding_lot_balances", "funding_lot_epoch", "funding_lot_events",
	"funding_lot_legacy_approved_refunds", "funding_lot_legacy_openings", "funding_lot_legacy_receipts", "funding_lot_legacy_sandbox_receipts",
	"funding_lot_legacy_settlements", "funding_lot_runtime_attestations", "funding_lots", "funding_reservation_lots",
	"funding_settlement_lots", "funding_wallet_heads", "funding_wallet_legacy_sources", "funding_wallet_operations",
}

// VerifyInstallationTx records a one-time empty-at-issue fact; only a separate
// maintenance authority may use it to authorize controlled initialization.
func VerifyInstallationTx(ctx context.Context, tx pgx.Tx, pins Pins) (*InstallationProof, error) {
	pins = copyPins(pins)
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	proof, err := verifyCatalogTx(c, tx, pins, 0)
	if err != nil {
		return nil, ErrCatalog
	}
	for _, table := range installationTables {
		var nonempty bool
		q := "SELECT EXISTS(SELECT 1 FROM " + pgx.Identifier{pins.AppSchema.Name, table}.Sanitize() + ")"
		if tx.QueryRow(c, q).Scan(&nonempty) != nil || nonempty {
			return nil, ErrCatalog
		}
	}
	if proof.verifyTx(c, tx) != nil {
		return nil, ErrCatalog
	}
	return &InstallationProof{identity: proof.identity}, nil
}

func (p *CatalogProof) VerifyTx(ctx context.Context, tx pgx.Tx) error {
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return ErrCatalog
	}
	defer cancel()
	return p.verifyTx(c, tx)
}

func (p *CatalogProof) verifyTx(ctx context.Context, tx pgx.Tx) error {
	if p == nil || p.identity == nil || !validTx(tx) || tx != p.identity.transaction.tx ||
		tx.Conn() != p.identity.transaction.conn || tx.Conn().PgConn() != p.identity.transaction.pgconn {
		return ErrCatalog
	}
	id, err := identifyTx(ctx, tx, p.identity.pins)
	if err != nil || !id.same(p.identity.transaction) || ctx.Err() != nil {
		return ErrCatalog
	}
	return nil
}

func (p *InstallationProof) VerifyTx(ctx context.Context, tx pgx.Tx) error {
	if p == nil || p.identity == nil {
		return ErrCatalog
	}
	return (&CatalogProof{identity: p.identity}).VerifyTx(ctx, tx)
}

func (p *CatalogProof) DescriptorVersion() uint32 {
	if p == nil || p.identity == nil {
		return 0
	}
	return p.identity.version
}

func (p *CatalogProof) ExpectedSHA256() string {
	if p == nil || p.identity == nil {
		return ""
	}
	return p.identity.expected
}

func (p *CatalogProof) SchemaSHA256() string {
	if p == nil || p.identity == nil {
		return ""
	}
	return p.identity.schema
}

func (p *CatalogProof) RolesSHA256() string {
	if p == nil || p.identity == nil {
		return ""
	}
	return p.identity.roles
}

func (p *CatalogProof) MatchesPins(pins Pins) bool {
	return p != nil && p.identity != nil && pinsEqual(p.identity.pins, pins)
}

func (p *InstallationProof) Catalog() *CatalogProof {
	if p == nil || p.identity == nil {
		return nil
	}
	// Return a fresh wrapper. A caller may assign through this exported pointer;
	// that must never replace the private empty-at-issue identity.
	return &CatalogProof{identity: p.identity}
}
