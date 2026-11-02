package fundinglotcatalog

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// VerifySnapshotProfileTx compares the explicitly selected complete catalog.
// It neither modifies privileges nor authenticates funding rows or an epoch.
func VerifySnapshotProfileTx(ctx context.Context, tx pgx.Tx, pins Pins, profile Profile) (*CatalogProof, error) {
	pins = copyPins(pins)
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyCatalogProfileTx(c, tx, pins, 0, profile)
}

// VerifyRuntimeProfileTx additionally binds an actual selected LOGIN in RR RO.
// Its CatalogProof is not a full funding-lot RuntimeProof or startup permit.
func VerifyRuntimeProfileTx(ctx context.Context, tx pgx.Tx, pins Pins, role RuntimeRole, profile Profile) (*CatalogProof, error) {
	pins = copyPins(pins)
	if role != Application && role != Worker && role != Projector || role == Worker && pins.Mode != Sandbox {
		return nil, ErrCatalog
	}
	c, cancel, err := boundedContext(ctx)
	if err != nil {
		return nil, ErrCatalog
	}
	defer cancel()
	return verifyCatalogProfileTx(c, tx, pins, role, profile)
}

func (p *CatalogProof) Profile() Profile {
	if p == nil || p.identity == nil {
		return 0
	}
	return p.identity.profile
}
