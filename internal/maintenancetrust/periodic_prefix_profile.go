package maintenancetrust

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/migrations"
)

func (g *Gate) BeginPeriodicPrefixPlan(ctx context.Context) (*Snapshot, error) {
	return g.beginSnapshotProfile(ctx, snapshotPlan, fundinglotcatalog.PeriodicPrefixV3)
}

func (g *Gate) BeginPeriodicPrefixInit(ctx context.Context) (*Snapshot, error) {
	return g.beginSnapshotProfile(ctx, snapshotInit, fundinglotcatalog.PeriodicPrefixV3)
}

func periodicPrefixInitLockSQL(schema string) string {
	// Both parts are fixed build-time lists. No live metadata or caller-supplied
	// name can add, remove or reorder a lock target.
	names := append([]string(nil), initLockTables[:]...)
	names = append(names, "periodic_budget_observation_heads", "periodic_budget_observation_journal", "periodic_budget_observation_origins")
	sort.Strings(names)
	var sql strings.Builder
	sql.WriteString("LOCK TABLE ")
	for i, table := range names {
		if i != 0 {
			sql.WriteString(", ")
		}
		sql.WriteString("ONLY ")
		sql.WriteString(pgx.Identifier{schema, table}.Sanitize())
	}
	sql.WriteString(" IN SHARE ROW EXCLUSIVE MODE")
	return sql.String()
}

// VerifyPeriodicPrefixInstallation consumes the same one installation attempt
// as VerifyInstallation. Profile selection belongs to this original Snapshot;
// callers cannot supply an external proof or change the profile of a Permit.
func (s *Snapshot) VerifyPeriodicPrefixInstallation(ctx context.Context) (*fundinglotcatalog.InstallationProof, error) {
	return s.verifyInstallation(ctx, fundinglotcatalog.PeriodicPrefixV3)
}

func verifyOwnedInstallationCatalog(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins, profile fundinglotcatalog.Profile) (*fundinglotcatalog.InstallationProof, error) {
	switch profile {
	case fundinglotcatalog.LegacyV1:
		return fundinglotcatalog.VerifyInstallationTx(ctx, tx, pins)
	case fundinglotcatalog.PeriodicPrefixV3:
		return fundinglotcatalog.VerifyPeriodicPrefixInstallationTx(ctx, tx, pins)
	default:
		return nil, ErrSchema
	}
}

func verifyOwnedSnapshotCatalog(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins, profile fundinglotcatalog.Profile) (*fundinglotcatalog.CatalogProof, error) {
	switch profile {
	case fundinglotcatalog.LegacyV1:
		return fundinglotcatalog.VerifySnapshotTx(ctx, tx, pins)
	case fundinglotcatalog.PeriodicPrefixV3:
		return fundinglotcatalog.VerifyPeriodicPrefixSnapshotTx(ctx, tx, pins)
	default:
		return nil, ErrSchema
	}
}

// Only owned immutable catalog evidence selects a maintenance data profile.
func (st *snapshotState) sourceProfile() fundinglotcatalog.Profile {
	if st == nil || st.installation == nil || st.installation.Catalog() == nil {
		return 0
	}
	profile := st.installation.Catalog().Profile()
	if profile != st.profile {
		return 0
	}
	return profile
}

func readPeriodicPrefixManifestSnapshot(ctx context.Context, st *snapshotState) ([]byte, error) {
	canonical, err := migrations.Original22Manifest()
	if err != nil || !authorityManifestValid(canonical, authorityManifestSHA) ||
		migrations.VerifyOriginal22SnapshotTx(ctx, st.rawTx, st.gate.pins.AppSchema.Name) != nil {
		return nil, authorityLeafFailure(ctx, ErrSchema)
	}
	return canonical, authorityLeafFailure(ctx, nil)
}

// SourceProfile reads the original installation's private selection. Like
// SourceMode it performs no SQL and grants no new authority to its caller.
func (l *ReadLease) SourceProfile() (fundinglotcatalog.Profile, error) {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return 0, ErrInput
	}
	ls := l.state
	if err := ls.beginMethod(); err != nil {
		return 0, err
	}
	defer ls.endMethod()
	if ls.kind != ScanCompleteSources && ls.kind != ScanSourceWallet {
		return 0, ls.fail(ErrTransaction)
	}
	a := ls.permit.authority
	if a == nil || a.snapshot != ls.snapshot || a.installed == nil || a.installed != ls.snapshot.installation {
		return 0, ls.fail(ErrTransaction)
	}
	profile := ls.snapshot.sourceProfile()
	if profile != fundinglotcatalog.LegacyV1 && profile != fundinglotcatalog.PeriodicPrefixV3 {
		return 0, ls.fail(ErrTransaction)
	}
	gs := ls.snapshot.gate
	gs.mu.Lock()
	err := ls.validLocked()
	gs.mu.Unlock()
	if err != nil {
		return 0, ls.fail(err)
	}
	return profile, nil
}
