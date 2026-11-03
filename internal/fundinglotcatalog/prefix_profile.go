package fundinglotcatalog

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// PeriodicPrefixV3 is the complete fixed periodic-observation catalog. The
// generic profile APIs deliberately retain their original accepted profiles.
const PeriodicPrefixV3 Profile = 3

// VerifyPeriodicPrefixSnapshotTx compares the complete V3 catalog and grants no
// maintenance or runtime authority.
func VerifyPeriodicPrefixSnapshotTx(ctx context.Context, tx pgx.Tx, pins Pins) (*CatalogProof, error) {
	pins = copyPins(pins)
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyPeriodicPrefixCatalogTx(c, tx, pins, 0)
}

// VerifyPeriodicPrefixRuntimeTx also binds the selected actual LOGIN in RR RO.
// The result is catalog evidence, not a full funding-lot RuntimeProof.
func VerifyPeriodicPrefixRuntimeTx(ctx context.Context, tx pgx.Tx, pins Pins, role RuntimeRole) (*CatalogProof, error) {
	pins = copyPins(pins)
	if role != Application && role != Worker && role != Projector || role == Worker && pins.Mode != Sandbox {
		return nil, ErrCatalog
	}
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyPeriodicPrefixCatalogTx(c, tx, pins, role)
}

func verifyPeriodicPrefixCatalogTx(ctx context.Context, tx pgx.Tx, pins Pins, runtime RuntimeRole) (*CatalogProof, error) {
	expected, root, err := loadPeriodicPrefixExpected(pins)
	if err != nil {
		return nil, ErrCatalog
	}
	return verifyCatalogExpectedTx(ctx, tx, pins, runtime, PeriodicPrefixV3, expected, root)
}

// VerifyPeriodicPrefixInstallationTx adds the original sixteen-output empty
// fact to V3 evidence using one shared five-second budget. Existing periodic
// observation history is not an initialization output and may be nonempty.
func VerifyPeriodicPrefixInstallationTx(ctx context.Context, tx pgx.Tx, pins Pins) (*InstallationProof, error) {
	pins = copyPins(pins)
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	proof, err := verifyPeriodicPrefixCatalogTx(c, tx, pins, 0)
	if err != nil {
		return nil, ErrCatalog
	}
	return verifyInstallationEmptyTx(c, tx, pins, proof)
}
