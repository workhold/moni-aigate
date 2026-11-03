package periodicbudget_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/periodicprefix"
)

func pbPrefixPins(f *pbTxFixture) periodicprefix.Pins {
	return periodicprefix.Pins{Schema: f.schema, AppRole: f.appRole, ProjectionRole: f.projectionRole}
}

func pbPrefixInstallTx(t *testing.T, f *pbTxFixture, options pgx.TxOptions) pgx.Tx {
	t.Helper()
	tx, err := f.owner.BeginTx(f.ctx, options)
	pbTxMust(t, "begin candidate caller transaction", err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	})
	return tx
}

// Snapshot catalogs rather than financial caches to detect any partial DDL or
// grants surviving a failed private savepoint. OIDs are compared within the same
// original transaction, never used as a portable expected catalog/root.
func pbPrefixInstallCatalog(t *testing.T, f *pbTxFixture, tx pgx.Tx) string {
	t.Helper()
	var state string
	err := tx.QueryRow(f.ctx, `SELECT pg_catalog.jsonb_build_object(
 'relations',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.oid) FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid),'[]'),
 'columns',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.attrelid,a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid),'[]'),
 'functions',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.oid) FROM pg_catalog.pg_proc p WHERE p.pronamespace=n.oid),'[]'),
 'types',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY t.oid) FROM pg_catalog.pg_type t WHERE t.typnamespace=n.oid),'[]'),
 'constraints',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.oid) FROM pg_catalog.pg_constraint c WHERE c.connamespace=n.oid),'[]'),
 'triggers',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY t.oid) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=n.oid),'[]'))::text
 FROM pg_catalog.pg_namespace n WHERE n.nspname=$1`, f.schema).Scan(&state)
	pbTxMust(t, "read candidate test-only catalog snapshot", err)
	return state
}

func pbPrefixInstallReject(t *testing.T, f *pbTxFixture, tx pgx.Tx, pins periodicprefix.Pins) {
	t.Helper()
	before := pbPrefixInstallCatalog(t, f, tx)
	ddlAttempted := false
	probe := &pbPrefixInstallFaultTx{Tx: tx, stage: "observeBeforeDDL", fired: &ddlAttempted}
	if err := periodicprefix.InstallTx(f.ctx, probe, pins); err != periodicprefix.ErrInstall {
		t.Fatalf("candidate rejection must return the fixed safe install error, got %T", err)
	}
	if ddlAttempted {
		t.Fatal("candidate preflight rejection occurred after attempting DDL")
	}
	if pbPrefixInstallCatalog(t, f, tx) != before {
		t.Fatal("failed candidate installation changed caller-visible catalogs or grants")
	}
	var usable int
	pbTxMust(t, "logical failure preserves usable caller transaction", tx.QueryRow(f.ctx, `SELECT 1`).Scan(&usable))
}

func TestPBPrefixInstallCallerOwnsVisibilityRollbackAndCommit(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	pbTxMust(t, "install exact candidate in caller transaction", periodicprefix.InstallTx(f.ctx, tx, pbPrefixPins(f)))
	var inside, outside int
	pbTxMust(t, "candidate visible inside original transaction", tx.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace=$1::regnamespace AND relname LIKE 'periodic_budget_observation_%' AND relkind='r'`, f.schema).Scan(&inside))
	pbTxMust(t, "candidate not yet visible outside original transaction", f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace=$1::regnamespace AND relname LIKE 'periodic_budget_observation_%' AND relkind='r'`, f.schema).Scan(&outside))
	if inside != 3 || outside != 0 {
		t.Fatal("installer ended or lost the caller transaction")
	}
	pbTxMust(t, "caller can roll back successful candidate", tx.Rollback(f.ctx))
	pbTxMust(t, "rolled-back candidate absent", f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace=$1::regnamespace AND relname LIKE 'periodic_budget_observation_%' AND relkind='r'`, f.schema).Scan(&outside))
	if outside != 0 {
		t.Fatal("candidate DDL escaped caller rollback")
	}
	pbPrefixInstall(t, f)
	tx = pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	pbTxMust(t, "committed candidate exact named-role ACL", periodicprefix.VerifyRolesTx(f.ctx, tx, pbPrefixPins(f)))
	pbPrefixInstallReject(t, f, tx, pbPrefixPins(f))
	pbTxMust(t, "caller rollback repeated-install rejection", tx.Rollback(f.ctx))
	for _, pool := range []*pgxpool.Pool{f.app, f.projection} {
		runtimeTx, err := pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		pbTxMust(t, "begin actual LOGIN read-only local privilege check", err)
		pbTxMust(t, "actual LOGIN can observe the same precise ACL", periodicprefix.VerifyRolesTx(f.ctx, runtimeTx, pbPrefixPins(f)))
		pbTxMust(t, "actual LOGIN caller rollback", runtimeTx.Rollback(f.ctx))
	}
}

func TestPBPrefixInstallRejectsModesPinsAndOwnHistoryDrift(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	for _, options := range []pgx.TxOptions{{}, {IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, {IsoLevel: pgx.Serializable}} {
		t.Run(string(options.IsoLevel)+"/"+string(options.AccessMode), func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, options)
			pbPrefixInstallReject(t, f, tx, pbPrefixPins(f))
			pbTxMust(t, "caller closes rejected mode", tx.Rollback(f.ctx))
		})
	}
	for _, mutate := range []func(*periodicprefix.Pins){
		func(p *periodicprefix.Pins) { p.Schema = "public" },
		func(p *periodicprefix.Pins) { p.AppRole = p.ProjectionRole },
		func(p *periodicprefix.Pins) { p.ProjectionRole = "moni_test" },
	} {
		tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		pins := pbPrefixPins(f)
		mutate(&pins)
		pbPrefixInstallReject(t, f, tx, pins)
		pbTxMust(t, "caller closes rejected pins", tx.Rollback(f.ctx))
	}
	t.Run("wrong_current_schema", func(t *testing.T) {
		tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		_, err := tx.Exec(f.ctx, `SET LOCAL search_path TO pg_catalog`)
		pbTxMust(t, "stage changed caller search path", err)
		pbPrefixInstallReject(t, f, tx, pbPrefixPins(f))
		pbTxMust(t, "caller closes wrong current schema", tx.Rollback(f.ctx))
	})
	for label, sql := range map[string]string{
		"missing": `DELETE FROM schema_migrations WHERE name='022_payment_runtime_attestation.sql'`,
		"wrong":   `UPDATE schema_migrations SET checksum='untrusted-history-secret' WHERE name='001_core.sql'`,
		"future":  `INSERT INTO schema_migrations(name,checksum) VALUES('023_future.sql','untrusted-history-secret')`,
		"unknown": `INSERT INTO schema_migrations(name,checksum) VALUES('999_unknown.sql','untrusted-history-secret')`,
	} {
		t.Run(label, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, err := tx.Exec(f.ctx, sql)
			pbTxMust(t, "stage own uncommitted history mutation", err)
			pbPrefixInstallReject(t, f, tx, pbPrefixPins(f))
			pbTxMust(t, "caller rollback history mutation", tx.Rollback(f.ctx))
		})
	}
}

func TestPBPrefixInstallRejectsEveryPartialCandidateSurface(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	for label, sql := range map[string]string{
		"table":      `CREATE TABLE periodic_budget_observation_heads(id integer)`,
		"type":       `CREATE TYPE periodic_budget_observation_origins AS (id integer)`,
		"arraytype":  `CREATE TYPE _periodic_budget_observation_journal AS (id integer)`,
		"operation":  `ALTER TABLE periodic_budget_operations ADD COLUMN observation_id text`,
		"window":     `ALTER TABLE periodic_budget_windows ADD COLUMN observation_created_xid xid8`,
		"function":   `CREATE FUNCTION pb_prefix_unreviewed(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'`,
		"trigger":    `CREATE TRIGGER pb_control_zz_prefix_origin BEFORE INSERT ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_control()`,
		"constraint": `ALTER TABLE periodic_budget_operations ADD CONSTRAINT pb_operation_observation_uuid CHECK(true)`,
	} {
		t.Run(label, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, err := tx.Exec(f.ctx, sql)
			pbTxMust(t, "stage pre-existing partial candidate", err)
			pbPrefixInstallReject(t, f, tx, pbPrefixPins(f))
			pbTxMust(t, "caller rollback pre-existing partial", tx.Rollback(f.ctx))
		})
	}
}

type pbPrefixInstallFaultTx struct {
	pgx.Tx
	stage  string
	cancel context.CancelFunc
	fired  *bool
}

func (f *pbPrefixInstallFaultTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := f.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if f.stage == "beginReplyLost" {
		*f.fired = true
		return nil, errors.New("injected-savepoint-reply-loss")
	}
	return &pbPrefixInstallFaultTx{Tx: tx, stage: f.stage, cancel: f.cancel, fired: f.fired}, nil
}

func (f *pbPrefixInstallFaultTx) Commit(ctx context.Context) error {
	err := f.Tx.Commit(ctx)
	if err == nil && f.stage == "releaseReplyLost" {
		*f.fired = true
		return errors.New("injected-release-reply-loss")
	}
	return err
}

func (f *pbPrefixInstallFaultTx) Rollback(ctx context.Context) error {
	if f.stage == "rollbackFailure" {
		*f.fired = true
		return errors.New("injected-rollback-failure")
	}
	err := f.Tx.Rollback(ctx)
	if err == nil && f.stage == "rollbackReplyLost" {
		*f.fired = true
		return errors.New("injected-rollback-reply-loss")
	}
	return err
}

func (f *pbPrefixInstallFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	ddl := strings.Contains(sql, "-- B-P1 SQL CANDIDATE")
	acl := strings.Contains(sql, "-- B-P1 PERMISSION CANDIDATE")
	if f.stage == "observeBeforeDDL" && ddl {
		*f.fired = true
	}
	tag, err := f.Tx.Exec(ctx, sql, args...)
	if err != nil {
		var pg *pgconn.PgError
		if f.stage == "midDDL" && ddl && errors.As(err, &pg) && pg.Code == "42P07" {
			*f.fired = true
		}
		return tag, err
	}
	if f.stage == "afterDDL" && ddl || f.stage == "afterACL" && acl {
		*f.fired = true
		return tag, errors.New("injected-driver-secret-must-never-escape")
	}
	if (f.stage == "rollbackFailure" || f.stage == "rollbackReplyLost") && ddl {
		return tag, errors.New("injected-failure-to-exercise-cleanup")
	}
	if f.stage == "cancelAfterDDL" && ddl {
		*f.fired = true
		f.cancel()
	}
	if f.stage == "observeACL" && acl {
		*f.fired = true
	}
	return tag, nil
}

func TestPBPrefixInstallExplicitPathIgnoresTemporaryHistoryAndSourceNames(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, err := tx.Exec(f.ctx, `CREATE TEMP TABLE schema_migrations(marker text);
 CREATE TEMP TABLE periodic_budget_operations(marker text);
 CREATE TEMP TABLE periodic_budget_windows(marker text);
 CREATE TEMP TABLE pg_proc(marker text);
 INSERT INTO pg_temp.schema_migrations VALUES('temporary-history-marker');
 INSERT INTO pg_temp.periodic_budget_windows VALUES('temporary-source-marker')`)
	pbTxMust(t, "stage same-named temporary history/source/catalog relations", err)
	pbTxMust(t, "fixed installer ignores temporary names", periodicprefix.InstallTx(f.ctx, tx, pbPrefixPins(f)))
	var fields int
	var history, window string
	pbTxMust(t, "read real schema new source columns", tx.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND a.attname IN ('observation_id','observation_created_xid') AND c.relname IN ('periodic_budget_operations','periodic_budget_windows')`, f.schema).Scan(&fields))
	pbTxMust(t, "temporary history unchanged", tx.QueryRow(f.ctx, `SELECT marker FROM pg_temp.schema_migrations`).Scan(&history))
	pbTxMust(t, "temporary source unchanged", tx.QueryRow(f.ctx, `SELECT marker FROM pg_temp.periodic_budget_windows`).Scan(&window))
	if fields != 3 || history != "temporary-history-marker" || window != "temporary-source-marker" {
		t.Fatal("candidate installer selected a temporary replacement")
	}
	pbTxMust(t, "caller rolls back temporary-name installation", tx.Rollback(f.ctx))
}

func TestPBPrefixInstallUncertainSavepointRequiresWholeCallerRollback(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	for _, stage := range []string{"beginReplyLost", "rollbackFailure", "rollbackReplyLost", "releaseReplyLost"} {
		t.Run(stage, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			before := pbPrefixInstallCatalog(t, f, tx)
			fired := false
			fault := &pbPrefixInstallFaultTx{Tx: tx, stage: stage, fired: &fired}
			if periodicprefix.InstallTx(f.ctx, fault, pbPrefixPins(f)) != periodicprefix.ErrInstallCleanup || !fired {
				t.Fatal("uncertain savepoint did not require whole caller rollback")
			}
			pbTxMust(t, "caller rolls back complete uncertain transaction", tx.Rollback(f.ctx))
			check := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if pbPrefixInstallCatalog(t, f, check) != before {
				t.Fatal("whole caller rollback leaked the uncertain candidate")
			}
			pbTxMust(t, "caller closes cleanup observation", check.Rollback(f.ctx))
		})
	}
}

func TestPBPrefixInstallSavepointRollsBackDDLACLAndCancellation(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	for _, stage := range []string{"midDDL", "afterDDL", "afterACL", "cancelAfterDDL"} {
		t.Run(stage, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if stage == "midDDL" {
				// This relation collides with the candidate's later CREATE INDEX,
				// after source columns and earlier evidence relations were created.
				_, err := tx.Exec(f.ctx, `CREATE TABLE pb_operation_observation_identity(id integer)`)
				pbTxMust(t, "stage actual mid-DDL name conflict", err)
			}
			before := pbPrefixInstallCatalog(t, f, tx)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			fired := false
			target := &pbPrefixInstallFaultTx{Tx: tx, stage: stage, cancel: cancel, fired: &fired}
			if err := periodicprefix.InstallTx(ctx, target, pbPrefixPins(f)); err != periodicprefix.ErrInstall {
				t.Fatalf("stage %s did not produce a safe ordinary rejection (%T)", stage, err)
			}
			if !fired {
				t.Fatal("installer rejected before reaching the intended actual failure stage")
			}
			if pbPrefixInstallCatalog(t, f, tx) != before {
				t.Fatal("failed stage leaked partial DDL or ACL into caller transaction")
			}
			pbTxMust(t, "caller closes failed-stage transaction", tx.Rollback(f.ctx))
		})
	}
}

func TestPBPrefixCandidateACLRejectsPrivilegeAndFunctionDrift(t *testing.T) {
	f := newPBPrefixFixture(t)
	app, projection := pgx.Identifier{f.appRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize()
	for label, sql := range map[string]string{
		"public_table":            `GRANT SELECT ON periodic_budget_observation_origins TO PUBLIC`,
		"public_type":             `GRANT USAGE ON TYPE periodic_budget_observation_origins TO PUBLIC`,
		"public_function":         `GRANT EXECUTE ON FUNCTION pb_prefix_immutable() TO PUBLIC`,
		"projection_column":       `GRANT SELECT(control_id) ON periodic_budget_observation_origins TO ` + projection,
		"projection_type":         `GRANT USAGE ON TYPE periodic_budget_observation_origins TO ` + projection,
		"projection_function":     `GRANT EXECUTE ON FUNCTION pb_prefix_immutable() TO ` + projection,
		"app_insert_stamp":        `GRANT INSERT(created_xid) ON periodic_budget_observation_origins TO ` + app,
		"app_update_head_stamp":   `GRANT UPDATE(updated_xid) ON periodic_budget_observation_heads TO ` + app,
		"app_update_source":       `GRANT UPDATE(observation_id) ON periodic_budget_operations TO ` + app,
		"app_update_window_stamp": `GRANT UPDATE(observation_created_xid) ON periodic_budget_windows TO ` + app,
		"app_table_update":        `GRANT UPDATE ON periodic_budget_observation_heads TO ` + app,
		"app_grant_option":        `GRANT SELECT ON periodic_budget_observation_origins TO ` + app + ` WITH GRANT OPTION`,
		"app_function_grant":      `GRANT EXECUTE ON FUNCTION pb_prefix_immutable() TO ` + app + ` WITH GRANT OPTION`,
		"definer":                 `ALTER FUNCTION pb_prefix_immutable() SECURITY DEFINER`,
		"lookup_path":             `ALTER FUNCTION pb_prefix_immutable() SET search_path TO public`,
		"overload":                `CREATE FUNCTION pb_prefix_immutable(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'`,
	} {
		t.Run(label, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, err := tx.Exec(f.ctx, sql)
			pbTxMust(t, "stage actual candidate ACL/function corruption", err)
			if periodicprefix.VerifyRolesTx(f.ctx, tx, pbPrefixPins(f)) != periodicprefix.ErrRolePrivileges {
				t.Fatal("candidate privilege drift was accepted or leaked diagnostics")
			}
			pbTxMust(t, "caller rollback local ACL corruption", tx.Rollback(f.ctx))
		})
	}
	// This is the specific legacy provisioning hazard identified in B-P2.
	pbTxMust(t, "deliberately repeat old broad provision", operations.ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if periodicprefix.VerifyRolesTx(f.ctx, tx, pbPrefixPins(f)) != periodicprefix.ErrRolePrivileges {
		t.Fatal("old broad provisioning silently reauthorized the new tables")
	}
	pbTxMust(t, "caller closes broad provisioning negative", tx.Rollback(f.ctx))
}

func TestPBPrefixInstallRejectsInheritedDefaultAndSetAdminPrivileges(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	for _, mode := range []string{"INHERIT TRUE, SET TRUE", "INHERIT FALSE, SET TRUE", "INHERIT FALSE, SET FALSE, ADMIN TRUE"} {
		t.Run(mode, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			extra := pgx.Identifier{"pb_extra_" + strings.TrimPrefix(f.schema, "pb_tx_")}.Sanitize()
			app := pgx.Identifier{f.appRole}.Sanitize()
			_, err := tx.Exec(f.ctx, "CREATE ROLE "+extra+" NOLOGIN; GRANT "+extra+" TO "+app+" WITH "+mode+"; ALTER DEFAULT PRIVILEGES IN SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" GRANT UPDATE ON TABLES TO "+extra)
			pbTxMust(t, "stage actual inherited/default/SET/ADMIN privilege path", err)
			before := pbPrefixInstallCatalog(t, f, tx)
			fired := false
			if periodicprefix.InstallTx(f.ctx, &pbPrefixInstallFaultTx{Tx: tx, stage: "observeACL", fired: &fired}, pbPrefixPins(f)) != periodicprefix.ErrInstall || !fired {
				t.Fatal("default privilege negative did not reach post-ACL validation")
			}
			if pbPrefixInstallCatalog(t, f, tx) != before {
				t.Fatal("post-ACL failure left partial catalogs or grants")
			}
			pbTxMust(t, "caller rollback extra-role and default-ACL setup", tx.Rollback(f.ctx))
		})
	}
}

func TestPBPrefixCandidateACLRejectsReachableElevatedRoleWithoutObjectGrantDrift(t *testing.T) {
	f := newPBPrefixFixture(t)
	for _, mode := range []string{"INHERIT TRUE, SET TRUE", "INHERIT FALSE, SET TRUE", "INHERIT FALSE, SET FALSE, ADMIN TRUE"} {
		t.Run(mode, func(t *testing.T) {
			tx := pbPrefixInstallTx(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			extra := pgx.Identifier{"pb_high_" + strings.TrimPrefix(f.schema, "pb_tx_")}.Sanitize()
			_, err := tx.Exec(f.ctx, "CREATE ROLE "+extra+" NOLOGIN CREATEROLE; GRANT "+extra+" TO "+pgx.Identifier{f.appRole}.Sanitize()+" WITH "+mode)
			pbTxMust(t, "stage actual reachable elevated role without candidate grants", err)
			if periodicprefix.VerifyRolesTx(f.ctx, tx, pbPrefixPins(f)) != periodicprefix.ErrRolePrivileges {
				t.Fatal("effective SET/ADMIN role closure accepted an elevated context")
			}
			pbTxMust(t, "caller rollback elevated role relationship", tx.Rollback(f.ctx))
		})
	}
}
