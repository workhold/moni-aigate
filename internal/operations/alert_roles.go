package operations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertDatabaseRole identifies a deployment database role, not a user's
// platform_role. Alert verification leaves all other module grants unchanged.
type AlertDatabaseRole string

const (
	AlertApplicationRole AlertDatabaseRole = "application"
	AlertProjectionRole  AlertDatabaseRole = "projection"
)

var ErrAlertRolePrivileges = errors.New("alert database role privileges do not match required boundary")

var alertTableNames = []string{
	"alert_control", "alert_commands", "alert_rules", "alert_rule_runtime",
	"alert_episodes", "alert_episode_events",
}

func alertRoleError(role AlertDatabaseRole, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrAlertRolePrivileges, role, reason)
}

func validAlertDatabaseRole(role AlertDatabaseRole) bool {
	return role == AlertApplicationRole || role == AlertProjectionRole
}

// VerifyAlertRole checks the actual connection identity and schema before a
// worker or HTTP server starts. It never grants, revokes, or changes role. A
// privileged session that SET ROLEs to an app role is still privileged because
// RESET ROLE can restore session_user.
func VerifyAlertRole(ctx context.Context, pool *pgxpool.Pool, role AlertDatabaseRole) error {
	if pool == nil || !validAlertDatabaseRole(role) {
		return ErrAlertRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return alertRoleError(role, "verification unavailable")
	}
	defer tx.Rollback(ctx)
	var schema *string
	var current, session string
	if err = tx.QueryRow(ctx, `SELECT current_schema(),current_user,session_user`).Scan(&schema, &current, &session); err != nil || schema == nil {
		return alertRoleError(role, "connection identity unavailable")
	}
	if err = verifyAlertRole(ctx, tx, *schema, current, role, true); err != nil {
		return err
	}
	if session != current {
		if err = verifyAlertRole(ctx, tx, *schema, session, role, false); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return alertRoleError(role, "verification unavailable")
	}
	return nil
}

// VerifyAlertRolesFor is the read-only offline check for explicit deployment
// roles. Unlike VerifyAlertRole it deliberately permits an owner connection to
// inspect the two named roles; ProvisionFor uses the same check in its ACL tx.
func VerifyAlertRolesFor(ctx context.Context, pool *pgxpool.Pool, schema, appRole, projectionRole string) error {
	if pool == nil {
		return ErrAlertRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ErrAlertRolePrivileges
	}
	defer tx.Rollback(ctx)
	if err = verifyAlertRolesFor(ctx, tx, schema, appRole, projectionRole); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrAlertRolePrivileges
	}
	return nil
}

func verifyAlertRolesFor(ctx context.Context, tx pgx.Tx, schema, appRole, projectionRole string) error {
	if appRole == projectionRole {
		return ErrAlertRolePrivileges
	}
	if err := verifyAlertRole(ctx, tx, schema, appRole, AlertApplicationRole, true); err != nil {
		return err
	}
	return verifyAlertRole(ctx, tx, schema, projectionRole, AlertProjectionRole, true)
}

// The current role needs the complete matrix. Reachable roles only need the
// allowed subset. ADMIN OPTION can enable SET on a membership even when it is
// currently neither inheritable nor settable, so include its escalation edges
// and continue through their SET/ADMIN descendants. Harmless inaccessible
// memberships remain outside the closure.
func verifyAlertRole(ctx context.Context, tx pgx.Tx, schema, name string, role AlertDatabaseRole, required bool) error {
	var oid, schemaOID, schemaOwner uint32
	if err := tx.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1`, name).Scan(&oid); err != nil {
		return alertRoleError(role, "role unavailable")
	}
	if err := tx.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1`, schema).Scan(&schemaOID, &schemaOwner); err != nil {
		return alertRoleError(role, "schema unavailable")
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE role_contexts(oid) AS (
 SELECT $1::oid
 UNION
 SELECT m.roleid FROM role_contexts c JOIN pg_catalog.pg_auth_members m
 ON (m.member=c.oid AND m.set_option)
 OR (m.admin_option AND pg_catalog.pg_has_role(c.oid,m.member,'USAGE'))
 ) SELECT r.oid,r.rolsuper,r.rolcreaterole,r.rolcreatedb,r.rolreplication,r.rolbypassrls
 FROM pg_catalog.pg_roles r JOIN role_contexts c ON c.oid=r.oid ORDER BY r.oid`, oid)
	if err != nil {
		return alertRoleError(role, "role contexts unavailable")
	}
	type roleContext struct {
		oid  uint32
		high bool
	}
	contexts := []roleContext{}
	for rows.Next() {
		var r roleContext
		var super, createRole, createDB, replication, bypass bool
		if err = rows.Scan(&r.oid, &super, &createRole, &createDB, &replication, &bypass); err != nil {
			break
		}
		r.high = super || createRole || createDB || replication || bypass
		contexts = append(contexts, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(contexts) == 0 {
		return alertRoleError(role, "role contexts unavailable")
	}
	for _, r := range contexts {
		if r.high {
			return alertRoleError(role, "has elevated role attributes")
		}
		var usage, create, grantable, owner bool
		if err = tx.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE'),
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE'),
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE WITH GRANT OPTION') OR
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE WITH GRANT OPTION'),
 pg_catalog.pg_has_role($1::oid,$3::oid,'USAGE')`, r.oid, schemaOID, schemaOwner).Scan(&usage, &create, &grantable, &owner); err != nil {
			return alertRoleError(role, "schema privileges unavailable")
		}
		if create || grantable || owner || required && r.oid == oid && !usage {
			return alertRoleError(role, "has invalid schema privileges")
		}
		if err = verifyAlertTables(ctx, tx, schemaOID, r.oid, role, required && r.oid == oid); err != nil {
			return err
		}
	}
	return nil
}

// Table and column ACLs are independent in PostgreSQL. has_any_column_privilege
// includes both, including an effective column grant with no table-level ACL.
const alertTablePrivilegesSQL = `SELECT c.relname,c.relkind::text,
 pg_catalog.pg_has_role($1::oid,c.relowner,'USAGE'),
 ARRAY(SELECT pg_catalog.has_table_privilege($1::oid,c.oid,p)
  FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) WITH ORDINALITY AS x(p,n) ORDER BY n),
 ARRAY(SELECT pg_catalog.has_any_column_privilege($1::oid,c.oid,p)
  FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) WITH ORDINALITY AS x(p,n) ORDER BY n),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) AS x(p)
  WHERE pg_catalog.has_table_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')) OR
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) AS x(p)
  WHERE pg_catalog.has_any_column_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')),
 (SELECT count(*) FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),
 pg_catalog.has_column_privilege($1::oid,c.oid,(SELECT a.attnum FROM pg_catalog.pg_attribute a
  WHERE a.attrelid=c.oid AND a.attname='singleton' AND a.attnum>0 AND NOT a.attisdropped),'UPDATE')
 FROM pg_catalog.pg_class c WHERE c.relnamespace=$2::oid AND c.relname=ANY($3::text[]) ORDER BY c.relname`

func verifyAlertTables(ctx context.Context, tx pgx.Tx, schemaOID, roleOID uint32, role AlertDatabaseRole, required bool) error {
	rows, err := tx.Query(ctx, alertTablePrivilegesSQL, roleOID, schemaOID, alertTableNames)
	if err != nil {
		return alertRoleError(role, "table privileges unavailable")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, kind string
		var owner, grantable bool
		var tablePrivileges, columnPrivileges []bool
		var columnCount int
		var singletonUpdate *bool
		if err = rows.Scan(&table, &kind, &owner, &tablePrivileges, &columnPrivileges, &grantable, &columnCount, &singletonUpdate); err != nil {
			return alertRoleError(role, "table privileges unavailable")
		}
		if kind != "r" || owner || grantable || table == "alert_control" && (columnCount != 1 || singletonUpdate == nil) {
			return alertRoleError(role, "has invalid table ownership or grants")
		}
		tableWant := []bool{false, false, false, false, false, false, false}
		columnWant := []bool{false, false, false, false}
		if role == AlertApplicationRole {
			tableWant[0], columnWant[0] = true, true
			if table != "alert_control" {
				tableWant[1], columnWant[1] = true, true
			}
			if table != "alert_episode_events" {
				columnWant[2] = true
				tableWant[2] = table != "alert_control"
			}
		}
		if !alertPrivilegesMatch(tablePrivileges, tableWant, required) || !alertPrivilegesMatch(columnPrivileges, columnWant, required) {
			return alertRoleError(role, "has invalid table or column privileges")
		}
		count++
	}
	if rows.Err() != nil || count != len(alertTableNames) {
		return alertRoleError(role, "table set unavailable")
	}
	return nil
}

func alertPrivilegesMatch(got, allowed []bool, required bool) bool {
	if len(got) != len(allowed) {
		return false
	}
	for i := range got {
		if got[i] && !allowed[i] || required && got[i] != allowed[i] {
			return false
		}
	}
	return true
}
