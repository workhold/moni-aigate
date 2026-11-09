package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMeterRolePrivileges = errors.New("text metering database role privileges do not match required boundary")

var meterHistoryTables = []string{
	"meter_schemas", "meter_schema_dimensions", "model_capability_variants", "model_capability_bound_lines",
	"price_rate_lines", "cost_rate_lines", "binding_cost_rate_limits", "request_meter_snapshots",
	"attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "meter_event_quantities",
}

var mediaRuntimeTables = []string{
	"media_tasks", "media_task_events", "media_task_artifacts",
	"realtime_sessions", "realtime_turns", "realtime_events",
	"media_requests", "media_reservations", "media_attempts", "media_observations",
	"media_reservation_lots", "media_settlements", "media_settlement_lots",
}

var mediaFunctions = []struct {
	name         string
	identityArgs string
	callArgs     string
}{
	{name: "media_uuid", identityArgs: "v text", callArgs: "text"},
	{name: "media_profile_immutable"},
	{name: "media_amount", identityArgs: "quantity bigint, rate_per_million bigint", callArgs: "bigint,bigint"},
	{name: "media_request_guard"},
	{name: "media_reservation_guard"},
	{name: "media_attempt_guard"},
	{name: "media_observation_guard"},
	{name: "media_settlement_guard"},
	{name: "media_settlement_immutable"},
	{name: "media_task_identity_guard"},
	{name: "media_artifact_guard"},
	{name: "media_task_event_guard"},
	{name: "media_task_event_apply"},
	{name: "realtime_session_guard"},
	{name: "realtime_turn_guard"},
	{name: "realtime_turn_inflight_guard"},
	{name: "realtime_event_guard"},
}

func provisionMediaRuntimeRoles(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])`, schema, mediaRuntimeTables).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if count != len(mediaRuntimeTables) {
		return fmt.Errorf("%w: partial media runtime schema", ErrMeterRolePrivileges)
	}
	qtables := make([]string, len(mediaRuntimeTables))
	for i, name := range mediaRuntimeTables {
		qtables[i] = pgx.Identifier{schema, name}.Sanitize()
	}
	list := strings.Join(qtables, ",")
	app := pgx.Identifier{appRole}.Sanitize()
	projection := pgx.Identifier{projectionRole}.Sanitize()
	if _, err := tx.Exec(ctx, "REVOKE ALL ON TABLE "+list+" FROM "+projection+",PUBLIC; REVOKE DELETE ON "+list+" FROM "+app+"; GRANT SELECT,INSERT,UPDATE ON "+list+" TO "+app); err != nil {
		return err
	}
	for _, table := range mediaRuntimeTables {
		qualified := pgx.Identifier{schema, table}.Sanitize()
		var selectOK, insertOK, updateOK, deleteOK, projectionAny, publicAny bool
		if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, qualified, projectionRole).Scan(&selectOK, &insertOK, &updateOK, &deleteOK, &projectionAny, &publicAny); err != nil {
			return err
		}
		if !selectOK || !insertOK || !updateOK || deleteOK || projectionAny || publicAny {
			return fmt.Errorf("%w: media runtime table %s", ErrMeterRolePrivileges, table)
		}
	}
	return nil
}

func provisionMediaProfileRole(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	qualified := pgx.Identifier{schema, "media_model_profiles"}.Sanitize()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='media_model_profiles')`, schema).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := provisionMediaFunctions(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	app := pgx.Identifier{appRole}.Sanitize()
	projection := pgx.Identifier{projectionRole}.Sanitize()
	if _, err := tx.Exec(ctx, "REVOKE ALL ON TABLE "+qualified+" FROM "+app+","+projection+",PUBLIC; GRANT SELECT ON "+qualified+" TO "+app); err != nil {
		return err
	}
	var selectOK, insertOK, updateOK, deleteOK, projectionAny, publicAny bool
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, qualified, projectionRole).Scan(&selectOK, &insertOK, &updateOK, &deleteOK, &projectionAny, &publicAny); err != nil {
		return err
	}
	if !selectOK || insertOK || updateOK || deleteOK || projectionAny || publicAny {
		return fmt.Errorf("%w: media profile table", ErrMeterRolePrivileges)
	}
	return nil
}

func provisionMediaFunctions(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	app := pgx.Identifier{appRole}.Sanitize()
	projection := pgx.Identifier{projectionRole}.Sanitize()
	for _, function := range mediaFunctions {
		signature := pgx.Identifier{schema, function.name}.Sanitize() + "(" + function.callArgs + ")"
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2 AND COALESCE(pg_get_function_arguments(p.oid),'')=$3)`, schema, function.name, function.identityArgs).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: media function %s missing", ErrMeterRolePrivileges, signature)
		}
		if _, err := tx.Exec(ctx, "REVOKE ALL ON FUNCTION "+signature+" FROM "+projection+",PUBLIC; GRANT EXECUTE ON FUNCTION "+signature+" TO "+app); err != nil {
			return err
		}
	}
	return verifyMediaFunctions(ctx, tx, schema, appRole, projectionRole)
}

func verifyMediaFunctions(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	for _, function := range mediaFunctions {
		signature := pgx.Identifier{schema, function.name}.Sanitize() + "(" + function.callArgs + ")"
		var exists, appExecute, projectionExecute, publicExecute bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2 AND COALESCE(pg_get_function_arguments(p.oid),'')=$3),has_function_privilege($5::name,$4::text,'EXECUTE'),has_function_privilege($6::name,$4::text,'EXECUTE'),has_function_privilege('public',$4::text,'EXECUTE')`, schema, function.name, function.identityArgs, signature, appRole, projectionRole).Scan(&exists, &appExecute, &projectionExecute, &publicExecute); err != nil {
			return err
		}
		if !exists || !appExecute || projectionExecute || publicExecute {
			return fmt.Errorf("%w: media function %s", ErrMeterRolePrivileges, signature)
		}
	}
	return nil
}

func verifyMediaTable(ctx context.Context, tx pgx.Tx, table, appRole, projectionRole string, insert, update bool) error {
	var appSelect, appInsert, appUpdate, appDelete, projectionAny, publicAny bool
	qualified := table
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, qualified, projectionRole).Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &projectionAny, &publicAny); err != nil {
		return err
	}
	if !appSelect || appInsert != insert || appUpdate != update || appDelete || projectionAny || publicAny {
		return fmt.Errorf("%w: media table %s", ErrMeterRolePrivileges, qualified)
	}
	return nil
}

func verifyMediaRoles(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='media_model_profiles')`, schema).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := verifyMediaFunctions(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	for _, name := range mediaRuntimeTables {
		if err := verifyMediaTable(ctx, tx, pgx.Identifier{schema, name}.Sanitize(), appRole, projectionRole, true, true); err != nil {
			return err
		}
	}
	if err := verifyMediaTable(ctx, tx, pgx.Identifier{schema, "media_model_profiles"}.Sanitize(), appRole, projectionRole, false, false); err != nil {
		return err
	}
	return verifyMediaTable(ctx, tx, pgx.Identifier{schema, "media_result_artifacts"}.Sanitize(), appRole, projectionRole, true, false)
}

func provisionMediaResultArtifactRole(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	qualified := pgx.Identifier{schema, "media_result_artifacts"}.Sanitize()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='media_result_artifacts')`, schema).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	app := pgx.Identifier{appRole}.Sanitize()
	projection := pgx.Identifier{projectionRole}.Sanitize()
	if _, err := tx.Exec(ctx, "REVOKE ALL ON TABLE "+qualified+" FROM "+app+","+projection+",PUBLIC; GRANT SELECT,INSERT ON "+qualified+" TO "+app); err != nil {
		return err
	}
	var selectOK, insertOK, updateOK, deleteOK, projectionAny, publicAny bool
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, qualified, projectionRole).Scan(&selectOK, &insertOK, &updateOK, &deleteOK, &projectionAny, &publicAny); err != nil {
		return err
	}
	if !selectOK || !insertOK || updateOK || deleteOK || projectionAny || publicAny {
		return fmt.Errorf("%w: media result artifact table", ErrMeterRolePrivileges)
	}
	return nil
}

// provisionMeterRoles follows the payment and periodic-budget rule: only an
// offline historical prefix with the entire 024 set absent may skip it. A
// partial set fails, and runtime verification always requires the full set.
func provisionMeterRoles(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])`, schema, meterHistoryTables).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if count != len(meterHistoryTables) {
		return fmt.Errorf("%w: partial meter schema", ErrMeterRolePrivileges)
	}
	tables := make([]string, len(meterHistoryTables))
	for i, name := range meterHistoryTables {
		tables[i] = pgx.Identifier{schema, name}.Sanitize()
	}
	list := strings.Join(tables, ",")
	if _, err := tx.Exec(ctx, "REVOKE UPDATE,DELETE ON "+list+" FROM "+pgx.Identifier{appRole}.Sanitize()+"; REVOKE ALL ON "+list+" FROM "+pgx.Identifier{projectionRole}.Sanitize()); err != nil {
		return err
	}
	if err := grantMeterFunctions(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	return verifyMeterRoles(ctx, tx, schema, appRole, projectionRole)
}

func grantMeterFunctions(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	rows, err := tx.Query(ctx, `SELECT p.oid::regprocedure::text FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'meter\_%' ESCAPE '\' ORDER BY 1`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	var signatures []string
	for rows.Next() {
		var signature string
		if err = rows.Scan(&signature); err != nil {
			return err
		}
		signatures = append(signatures, signature)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(signatures) == 0 {
		return fmt.Errorf("%w: meter functions missing", ErrMeterRolePrivileges)
	}
	role, projection := pgx.Identifier{appRole}.Sanitize(), pgx.Identifier{projectionRole}.Sanitize()
	for _, signature := range signatures {
		// Repeated provisioning must also clear an earlier blanket projector grant.
		if _, err = tx.Exec(ctx, "REVOKE ALL ON FUNCTION "+signature+" FROM PUBLIC,"+projection); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+role); err != nil {
			return err
		}
	}
	return nil
}

func verifyMeterRoles(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	for _, table := range meterHistoryTables {
		qualified := pgx.Identifier{schema, table}.Sanitize()
		var appSelect, appInsert, appUpdate, appDelete bool
		var projectionAny, publicAny bool
		err := tx.QueryRow(ctx, `SELECT
 has_table_privilege($1::name,$2::text,'SELECT'), has_table_privilege($1::name,$2::text,'INSERT'),
 has_table_privilege($1::name,$2::text,'UPDATE'), has_table_privilege($1::name,$2::text,'DELETE'),
 has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),
 has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`,
			appRole, qualified, projectionRole).Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &projectionAny, &publicAny)
		if err != nil {
			return err
		}
		if !appSelect || !appInsert || appUpdate || appDelete || projectionAny || publicAny {
			return fmt.Errorf("%w: %s", ErrMeterRolePrivileges, table)
		}
	}
	var appExecute, projectionExecute, publicExecute, invoker, pinned bool
	err := tx.QueryRow(ctx, `SELECT bool_and(has_function_privilege($2::name,p.oid,'EXECUTE')), bool_or(has_function_privilege($3::name,p.oid,'EXECUTE')), bool_or(has_function_privilege('public',p.oid,'EXECUTE')), bool_and(NOT p.prosecdef), bool_and(p.proconfig=ARRAY[format('search_path=%I, pg_catalog, pg_temp',$1::text)])
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'meter\_%' ESCAPE '\'`, schema, appRole, projectionRole).Scan(&appExecute, &projectionExecute, &publicExecute, &invoker, &pinned)
	if err != nil || !appExecute || projectionExecute || publicExecute || !invoker || !pinned {
		return fmt.Errorf("%w: meter function execute or definition", ErrMeterRolePrivileges)
	}
	return nil
}

func VerifyMeterRolesFor(ctx context.Context, pool *pgxpool.Pool, schema, appRole, projectionRole string) error {
	if pool == nil {
		return ErrMeterRolePrivileges
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = verifyMeterRoles(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	if err = verifyMediaRoles(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifyMeterRoles checks the actual serving LOGIN and the separate projection
// role against the complete 024 boundary before any listener starts.
func VerifyMeterRoles(ctx context.Context, pool *pgxpool.Pool, projectionRole string) error {
	if pool == nil || projectionRole == "" {
		return ErrMeterRolePrivileges
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var schema *string
	var current, session string
	if err = tx.QueryRow(ctx, "SELECT current_schema(),current_user,session_user").Scan(&schema, &current, &session); err != nil || schema == nil || current != session || current == projectionRole {
		return fmt.Errorf("%w: actual login identity unavailable", ErrMeterRolePrivileges)
	}
	if err = verifyMeterRoles(ctx, tx, *schema, current, projectionRole); err != nil {
		return err
	}
	if err = verifyMediaRoles(ctx, tx, *schema, current, projectionRole); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
