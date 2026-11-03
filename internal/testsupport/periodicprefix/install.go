// Package periodicprefix provides isolated PostgreSQL candidate test support.
// It is not an offline production installer or an authority issuer.
package periodicprefix

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

var (
	ErrInstall        = errors.New("periodic_prefix_candidate_install_failed")
	ErrInstallCleanup = errors.New("periodic_prefix_candidate_cleanup_required")
)

var isolatedSchema = regexp.MustCompile(`^pb_tx_([0-9a-f]{32})$`)

// InstallTx verifies original22, installs the fixed candidate, and checks its
// minimal ACL in a private savepoint of the caller's original RR RW transaction.
// Only the dedicated local test database and an exclusively owned random fixture
// are supported. It never opens/finishes the top-level transaction, takes global
// session locks, retries an unknown commit, or returns installation authority.
// On ErrInstallCleanup the caller must roll back its complete transaction and
// discard a connection whose idle state cannot be confirmed.
func InstallTx(ctx context.Context, tx pgx.Tx, pins Pins) (result error) {
	if ctx == nil || tx == nil || ctx.Err() != nil || !validFixturePins(pins) {
		return ErrInstall
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sp, err := tx.Begin(work)
	if err != nil {
		// A failed SAVEPOINT reply cannot prove that no subtransaction exists.
		return ErrInstallCleanup
	}
	released := false
	defer func() {
		if released {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if sp.Rollback(cleanup) != nil {
			result = ErrInstallCleanup
		}
	}()
	if fixtureIdentity(work, sp, pins) != nil {
		return ErrInstall
	}
	// Explicitly place pg_temp last before executing the frozen unqualified DDL.
	if _, err = sp.Exec(work, "SET LOCAL search_path TO "+pgx.Identifier{pins.Schema}.Sanitize()+",pg_catalog,pg_temp"); err != nil {
		return ErrInstall
	}
	if migrations.VerifyOriginal22SnapshotTx(work, sp, pins.Schema) != nil || candidateAbsent(work, sp, pins.Schema) != nil {
		return ErrInstall
	}
	ddl, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		return ErrInstall
	}
	acl, err := periodicbudgetprefixschema.Permissions(pins.AppRole, pins.ProjectionRole)
	if err != nil {
		return ErrInstall
	}
	if _, err = sp.Exec(work, ddl); err != nil {
		return ErrInstall
	}
	if _, err = sp.Exec(work, acl); err != nil {
		return ErrInstall
	}
	if VerifyRolesTx(work, sp, pins) != nil || work.Err() != nil {
		return ErrInstall
	}
	if sp.Commit(work) != nil {
		// RELEASE has no top-level financial commit, but an uncertain savepoint
		// state still requires the caller to abandon the complete transaction.
		return ErrInstallCleanup
	}
	released = true
	return nil
}

func validFixturePins(p Pins) bool {
	match := isolatedSchema.FindStringSubmatch(p.Schema)
	return len(match) == 2 && p.AppRole == "pb_tx_app_"+match[1] && p.ProjectionRole == "pb_tx_projection_"+match[1]
}

func fixtureIdentity(ctx context.Context, tx pgx.Tx, pins Pins) error {
	var current, session, database, address, schema, isolation, access string
	var port int
	var owner bool
	err := tx.QueryRow(ctx, `SELECT current_user,session_user,pg_catalog.current_database(),
 pg_catalog.host(pg_catalog.inet_server_addr()),pg_catalog.inet_server_port(),pg_catalog.current_schema(),
 pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only'),
 n.nspowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)
 FROM pg_catalog.pg_namespace n WHERE n.nspname=$1`, pins.Schema).
		Scan(&current, &session, &database, &address, &port, &schema, &isolation, &access, &owner)
	if err != nil || current != "moni_test" || session != current || database != "moni_test" || address != "127.0.0.1" || port != 55439 ||
		schema != pins.Schema || isolation != "repeatable read" || access != "off" || !owner {
		return ErrInstall
	}
	return nil
}

func candidateAbsent(ctx context.Context, tx pgx.Tx, schema string) error {
	var absent bool
	err := tx.QueryRow(ctx, `SELECT NOT (
 EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=$1 AND c.relname=ANY($2::text[]))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace
  WHERE n.nspname=$1 AND t.typname=ANY($3::text[]))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=$1 AND NOT a.attisdropped AND a.attnum>0
   AND ((c.relname='periodic_budget_operations' AND a.attname='observation_id')
    OR (c.relname='periodic_budget_windows' AND a.attname IN ('observation_id','observation_created_xid'))))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=$1 AND pg_catalog.left(p.proname,10)='pb_prefix_')
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1
  AND (pg_catalog.left(t.tgname,10)='pb_prefix_' OR t.tgname=ANY($4::text[])))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_namespace n ON n.oid=c.connamespace
  WHERE n.nspname=$1 AND c.conname LIKE 'pb_%observation%'))`, schema,
		[]string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal"},
		[]string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal", "_periodic_budget_observation_origins", "_periodic_budget_observation_heads", "_periodic_budget_observation_journal"},
		[]string{"pb_control_zz_prefix_origin", "pb_operation_00_prefix_lock", "pb_operation_zz_prefix_capture", "pb_window_zz_prefix_capture"}).Scan(&absent)
	if err != nil || !absent {
		return ErrInstall
	}
	return nil
}
