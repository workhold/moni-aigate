package operations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMeterRolePrivileges = errors.New("text metering database role privileges do not match required boundary")

var meterHistoryTables = []string{
	"meter_schemas", "meter_schema_dimensions", "model_capability_variants", "model_capability_bound_lines",
	"price_rate_lines", "cost_rate_lines", "binding_cost_rate_limits", "request_meter_snapshots",
	"attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "meter_event_quantities",
}

func grantMeterFunctions(ctx context.Context, tx pgx.Tx, schema, appRole string) error {
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
	role := pgx.Identifier{appRole}.Sanitize()
	for _, signature := range signatures {
		if _, err = tx.Exec(ctx, "REVOKE ALL ON FUNCTION "+signature+" FROM PUBLIC"); err != nil {
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
	var appExecute, projectionExecute bool
	err := tx.QueryRow(ctx, `SELECT bool_and(has_function_privilege($2::name,p.oid,'EXECUTE')), bool_or(has_function_privilege($3::name,p.oid,'EXECUTE'))
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'meter\_%' ESCAPE '\'`, schema, appRole, projectionRole).Scan(&appExecute, &projectionExecute)
	if err != nil || !appExecute || projectionExecute {
		return fmt.Errorf("%w: meter function execute", ErrMeterRolePrivileges)
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
	return tx.Commit(ctx)
}
