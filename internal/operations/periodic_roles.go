package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PeriodicBudgetDatabaseRole describes a database runtime, not a user's role.
type PeriodicBudgetDatabaseRole string

const (
	PeriodicBudgetApplicationRole PeriodicBudgetDatabaseRole = "application"
	PeriodicBudgetProjectionRole  PeriodicBudgetDatabaseRole = "projection"
)

var ErrPeriodicBudgetRolePrivileges = errors.New("periodic budget database role privileges do not match required boundary")
var periodicBudgetTableNames = []string{"periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "periodic_budget_operations", "periodic_budget_configuration_versions"}
var periodicBudgetMutableColumns = map[string][]string{
	"periodic_budget_controls":    {"enabled", "limit_amount", "configuration_operation_id", "revision", "held_amount", "spent_amount", "event_seq", "last_event_id", "max_observed_window_start"},
	"periodic_budget_windows":     {"held_amount", "spent_amount", "revision", "last_event_id"},
	"periodic_budget_allocations": {"held_amount", "spent_amount", "source_held", "source_spent", "state", "revision", "last_event_id"},
}

func periodicBudgetRoleError(role PeriodicBudgetDatabaseRole, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrPeriodicBudgetRolePrivileges, role, reason)
}
func validPeriodicBudgetRole(role PeriodicBudgetDatabaseRole) bool {
	return role == PeriodicBudgetApplicationRole || role == PeriodicBudgetProjectionRole
}

// VerifyPeriodicBudgetRole is a read-only startup check on an actual LOGIN.
// It neither SET ROLEs nor accepts a privileged session impersonating runtime.
func VerifyPeriodicBudgetRole(ctx context.Context, pool *pgxpool.Pool, role PeriodicBudgetDatabaseRole) error {
	if pool == nil || !validPeriodicBudgetRole(role) {
		return ErrPeriodicBudgetRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return periodicBudgetRoleError(role, "verification unavailable")
	}
	defer tx.Rollback(ctx)
	var schema *string
	var current, session string
	if e = tx.QueryRow(ctx, "SELECT current_schema(),current_user,session_user").Scan(&schema, &current, &session); e != nil || schema == nil || current != session {
		return periodicBudgetRoleError(role, "actual login identity unavailable")
	}
	if e = verifyPeriodicBudgetRole(ctx, tx, *schema, current, role); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return periodicBudgetRoleError(role, "verification unavailable")
	}
	return nil
}

// VerifyPeriodicBudgetRolesFor is the offline named-role check used by owner
// tooling. Unlike runtime verification it may run on an owner connection.
func VerifyPeriodicBudgetRolesFor(ctx context.Context, pool *pgxpool.Pool, schema, appRole, projectionRole string) error {
	if pool == nil {
		return ErrPeriodicBudgetRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrPeriodicBudgetRolePrivileges
	}
	defer tx.Rollback(ctx)
	if e = verifyPeriodicBudgetRolesFor(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return ErrPeriodicBudgetRolePrivileges
	}
	return nil
}
func verifyPeriodicBudgetRolesFor(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	if appRole == projectionRole {
		return ErrPeriodicBudgetRolePrivileges
	}
	if e := verifyPeriodicBudgetRole(ctx, tx, schema, appRole, PeriodicBudgetApplicationRole); e != nil {
		return e
	}
	return verifyPeriodicBudgetRole(ctx, tx, schema, projectionRole, PeriodicBudgetProjectionRole)
}

// provisionPeriodicBudgetRoles runs inside the same transaction as every other
// module ACL. 019 fixtures may skip only when all six budget objects are absent.
func provisionPeriodicBudgetRoles(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	var count int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])`, schema, periodicBudgetTableNames).Scan(&count); e != nil {
		return ErrPeriodicBudgetRolePrivileges
	}
	if count == 0 {
		return nil
	}
	if count != len(periodicBudgetTableNames) {
		return ErrPeriodicBudgetRolePrivileges
	}
	qa, qp := pgx.Identifier{appRole}.Sanitize(), pgx.Identifier{projectionRole}.Sanitize()
	for _, name := range periodicBudgetTableNames {
		table := pgx.Identifier{schema, name}.Sanitize()
		if _, e := tx.Exec(ctx, "REVOKE ALL ON "+table+" FROM "+qa+","+qp); e != nil {
			return ErrPeriodicBudgetRolePrivileges
		}
		// REVOKE ALL ON TABLE alone leaves separately granted column ACLs behind.
		rows, e := tx.Query(ctx, `SELECT a.attname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, schema, name)
		if e != nil {
			return ErrPeriodicBudgetRolePrivileges
		}
		var columns []string
		for rows.Next() {
			var column string
			if e = rows.Scan(&column); e != nil {
				break
			}
			columns = append(columns, pgx.Identifier{column}.Sanitize())
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil || len(columns) == 0 {
			return ErrPeriodicBudgetRolePrivileges
		}
		if _, e = tx.Exec(ctx, "REVOKE ALL ("+strings.Join(columns, ",")+") ON "+table+" FROM "+qa+","+qp); e != nil {
			return ErrPeriodicBudgetRolePrivileges
		}
		if _, e = tx.Exec(ctx, "GRANT SELECT,INSERT ON "+table+" TO "+qa); e != nil {
			return ErrPeriodicBudgetRolePrivileges
		}
		if mutable := periodicBudgetMutableColumns[name]; len(mutable) > 0 {
			cols := make([]string, len(mutable))
			for i, col := range mutable {
				cols[i] = pgx.Identifier{col}.Sanitize()
			}
			if _, e = tx.Exec(ctx, "GRANT UPDATE("+strings.Join(cols, ",")+") ON "+table+" TO "+qa); e != nil {
				return ErrPeriodicBudgetRolePrivileges
			}
		}
	}
	return verifyPeriodicBudgetRolesFor(ctx, tx, schema, appRole, projectionRole)
}

type periodicRoleContext struct {
	oid  uint32
	high bool
}

// Match the already exercised alert verifier's SET/ADMIN closure without
// changing alert_roles.go. ADMIN held through an effective membership can enable
// a later SET edge; check every resulting context's effective allowed subset.
func periodicBudgetContexts(ctx context.Context, tx pgx.Tx, oid uint32) ([]periodicRoleContext, error) {
	rows, e := tx.Query(ctx, `WITH RECURSIVE role_contexts(oid) AS (
 SELECT $1::oid UNION
 SELECT m.roleid FROM role_contexts c JOIN pg_catalog.pg_auth_members m
 ON (m.member=c.oid AND m.set_option)
 OR (m.admin_option AND pg_catalog.pg_has_role(c.oid,m.member,'USAGE'))
 ) SELECT r.oid,r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication OR r.rolbypassrls
 FROM pg_catalog.pg_roles r JOIN role_contexts c ON c.oid=r.oid ORDER BY r.oid`, oid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var contexts []periodicRoleContext
	for rows.Next() {
		var r periodicRoleContext
		if e = rows.Scan(&r.oid, &r.high); e != nil {
			return nil, e
		}
		contexts = append(contexts, r)
	}
	return contexts, rows.Err()
}
func verifyPeriodicBudgetRole(ctx context.Context, tx pgx.Tx, schema, name string, role PeriodicBudgetDatabaseRole) error {
	var oid, schemaOID, schemaOwner uint32
	if e := tx.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", name).Scan(&oid); e != nil {
		return periodicBudgetRoleError(role, "role unavailable")
	}
	if e := tx.QueryRow(ctx, "SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1", schema).Scan(&schemaOID, &schemaOwner); e != nil {
		return periodicBudgetRoleError(role, "schema unavailable")
	}
	contexts, e := periodicBudgetContexts(ctx, tx, oid)
	if e != nil || len(contexts) == 0 {
		return periodicBudgetRoleError(role, "role contexts unavailable")
	}
	for _, r := range contexts {
		if r.high {
			return periodicBudgetRoleError(role, "has elevated role attributes")
		}
		var usage, create, grantable, owner bool
		e = tx.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE'),pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE'),
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE WITH GRANT OPTION') OR pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE WITH GRANT OPTION'),
 pg_catalog.pg_has_role($1::oid,$3::oid,'USAGE')`, r.oid, schemaOID, schemaOwner).Scan(&usage, &create, &grantable, &owner)
		if e != nil {
			return periodicBudgetRoleError(role, "schema privileges unavailable")
		}
		if create || grantable || owner || r.oid == oid && !usage {
			return periodicBudgetRoleError(role, "has invalid schema privileges")
		}
		if e = verifyPeriodicBudgetTables(ctx, tx, schemaOID, r.oid, role, r.oid == oid); e != nil {
			return e
		}
	}
	return nil
}

const periodicBudgetPrivilegesSQL = `SELECT c.relname,c.relkind::text,
 pg_catalog.pg_has_role($1::oid,c.relowner,'USAGE'),
 ARRAY(SELECT pg_catalog.has_table_privilege($1::oid,c.oid,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) WITH ORDINALITY AS x(p,n) ORDER BY n),
 ARRAY(SELECT pg_catalog.has_any_column_privilege($1::oid,c.oid,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) WITH ORDINALITY AS x(p,n) ORDER BY n),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) AS x(p) WHERE pg_catalog.has_table_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')) OR
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) AS x(p) WHERE pg_catalog.has_any_column_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')),
 ARRAY(SELECT a.attname::text FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,c.oid,a.attnum,'UPDATE') FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum)
 FROM pg_catalog.pg_class c WHERE c.relnamespace=$2::oid AND c.relname=ANY($3::text[]) ORDER BY c.relname`

func verifyPeriodicBudgetTables(ctx context.Context, tx pgx.Tx, schemaOID, roleOID uint32, role PeriodicBudgetDatabaseRole, required bool) error {
	rows, e := tx.Query(ctx, periodicBudgetPrivilegesSQL, roleOID, schemaOID, periodicBudgetTableNames)
	if e != nil {
		return periodicBudgetRoleError(role, "table privileges unavailable")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, kind string
		var owner, grantable bool
		var tables, columns, updates []bool
		var names []string
		if e = rows.Scan(&table, &kind, &owner, &tables, &columns, &grantable, &names, &updates); e != nil {
			return periodicBudgetRoleError(role, "table privileges unavailable")
		}
		if kind != "r" || owner || grantable || len(names) == 0 || len(names) != len(updates) {
			return periodicBudgetRoleError(role, "has invalid table ownership or grants")
		}
		tableWant := []bool{false, false, false, false, false, false, false}
		columnWant := []bool{false, false, false, false}
		allowed := map[string]bool{}
		if role == PeriodicBudgetApplicationRole {
			tableWant[0], tableWant[1] = true, true
			columnWant[0], columnWant[1] = true, true
			for _, column := range periodicBudgetMutableColumns[table] {
				allowed[column] = true
			}
			columnWant[2] = len(allowed) > 0
		}
		if !alertPrivilegesMatch(tables, tableWant, required) || !alertPrivilegesMatch(columns, columnWant, required) {
			return periodicBudgetRoleError(role, "has invalid table or column privileges")
		}
		found := map[string]bool{}
		for i, column := range names {
			found[column] = true
			if updates[i] && !allowed[column] || required && updates[i] != allowed[column] {
				return periodicBudgetRoleError(role, "has invalid mutable column privileges")
			}
		}
		for column := range allowed {
			if !found[column] {
				return periodicBudgetRoleError(role, "mutable column unavailable")
			}
		}
		count++
	}
	if rows.Err() != nil || count != len(periodicBudgetTableNames) {
		return periodicBudgetRoleError(role, "table set unavailable")
	}
	return nil
}
