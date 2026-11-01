package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PaymentDatabaseRole identifies a database LOGIN, never a platform user role.
type PaymentDatabaseRole string

const (
	PaymentApplicationRole PaymentDatabaseRole = "application"
	PaymentWorkerRole      PaymentDatabaseRole = "worker"
	PaymentProjectionRole  PaymentDatabaseRole = "projection"
)

// Deliberately do not attach driver errors, identifiers, SQL, or credentials.
var ErrPaymentRolePrivileges = errors.New("payment database role privileges do not match required boundary")

// PostgreSQL's built-in database owner role owns default public schemas,
// while migration objects belong to the actual owner of the current database.
// Resolve only this built-in identity, never an arbitrary effective caller.
const paymentSchemaObjectOwnerSQL = `(CASE WHEN n.nspowner='pg_database_owner'::regrole::oid THEN (SELECT datdba FROM pg_catalog.pg_database WHERE datname=current_database()) ELSE n.nspowner END)`

var paymentTableNames = []string{
	"payment_sandbox_environment", "payment_runtime_attestations", "payment_policy", "payment_skus", "payment_sku_versions", "payment_sku_changes", "payment_orders", "payment_cancel_intents", "payment_command_outbox", "payment_command_observations", "payment_query_attempts", "payment_inbox", "payment_event_conflicts", "payment_verified_queries", "payment_query_conflicts", "payment_verified_evidence", "payment_evidence_processing", "payment_provider_revisions", "payment_transaction_identities", "payment_order_capture_selections", "payment_captures", "payment_effects", "payment_review_facts",
}

type paymentTableGrant struct {
	selectAll, insertAll               bool
	selectCols, insertCols, updateCols []string
}

func paymentGrants(role PaymentDatabaseRole) map[string]paymentTableGrant {
	m := make(map[string]paymentTableGrant)
	add := func(names string, selectAll, insertAll bool) {
		for _, name := range strings.Fields(names) {
			v := m[name]
			v.selectAll = v.selectAll || selectAll
			v.insertAll = v.insertAll || insertAll
			m[name] = v
		}
	}
	if role == PaymentApplicationRole {
		add("payment_sandbox_environment payment_runtime_attestations payment_policy payment_skus payment_sku_versions payment_sku_changes payment_orders payment_cancel_intents payment_command_outbox payment_command_observations payment_query_attempts payment_inbox payment_event_conflicts payment_verified_queries payment_verified_evidence payment_evidence_processing payment_provider_revisions payment_transaction_identities payment_order_capture_selections payment_captures payment_effects payment_review_facts", true, false)
		add("payment_skus payment_sku_versions payment_sku_changes payment_orders payment_cancel_intents payment_command_outbox payment_inbox payment_event_conflicts payment_verified_evidence payment_evidence_processing", false, true)
		for name, cols := range map[string][]string{
			"payment_policy": {"enabled"},
			"payment_skus":   {"revision", "last_version", "enabled", "current_version_id", "last_change_operation_id"},
			"payment_orders": {"id"},
		} {
			v := m[name]
			v.updateCols = cols
			m[name] = v
		}
	}
	if role == PaymentWorkerRole {
		add("payment_sandbox_environment payment_runtime_attestations payment_sku_versions payment_orders payment_cancel_intents payment_command_outbox payment_command_observations payment_query_attempts payment_inbox payment_event_conflicts payment_verified_queries payment_query_conflicts payment_verified_evidence payment_evidence_processing payment_provider_revisions payment_transaction_identities payment_order_capture_selections payment_captures payment_effects payment_review_facts ledger_accounts journals journal_lines funding_receipts projection_outbox audit_events schema_migrations", true, false)
		add("payment_command_outbox payment_command_observations payment_query_attempts payment_verified_queries payment_query_conflicts payment_verified_evidence payment_evidence_processing payment_provider_revisions payment_transaction_identities payment_order_capture_selections payment_captures payment_effects payment_review_facts journals journal_lines funding_receipts projection_outbox audit_events", false, true)
		for name, cols := range map[string][]string{
			"tenants": {"id"}, "payment_orders": {"id"}, "ledger_accounts": {"balance"},
			"payment_command_outbox":      {"state", "generation", "attempt_count", "current_query_id", "next_attempt_at", "lease_until", "last_error_code", "last_submission_unknown_at"},
			"payment_evidence_processing": {"state", "generation", "attempt_count", "next_attempt_at", "lease_until", "last_error_code"},
		} {
			v := m[name]
			v.updateCols = cols
			m[name] = v
		}
		v := m["tenants"]
		v.selectCols = []string{"id"}
		m["tenants"] = v
		v = m["ledger_accounts"]
		v.insertCols = []string{"tenant_id", "kind"}
		m["ledger_accounts"] = v
	}
	return m
}

type paymentMarker struct {
	present                 bool
	app, worker, projection string
}

func paymentTableCount(ctx context.Context, tx pgx.Tx, schema string) (int, error) {
	var n int
	e := tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])`, schema, paymentTableNames).Scan(&n)
	return n, e
}

func paymentReadMarker(ctx context.Context, tx pgx.Tx, schema string) (paymentMarker, error) {
	var m paymentMarker
	var valid bool
	rows, e := tx.Query(ctx, "SELECT app_role::text,worker_role::text,projector_role::text,singleton AND protocol_version=1 AND database_oid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()) AND schema_oid=(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=$1) FROM "+pgx.Identifier{schema, "payment_sandbox_environment"}.Sanitize(), schema)
	if e != nil {
		return m, ErrPaymentRolePrivileges
	}
	defer rows.Close()
	for rows.Next() {
		if m.present || rows.Scan(&m.app, &m.worker, &m.projection, &valid) != nil || !valid || m.app == m.worker || m.app == m.projection || m.worker == m.projection {
			return paymentMarker{}, ErrPaymentRolePrivileges
		}
		m.present = true
	}
	if rows.Err() != nil {
		return paymentMarker{}, ErrPaymentRolePrivileges
	}
	return m, nil
}

// VerifyPaymentRole is a bounded, read-only RR check on an actual LOGIN.
func VerifyPaymentRole(ctx context.Context, pool *pgxpool.Pool, role PaymentDatabaseRole) error {
	if pool == nil || role != PaymentApplicationRole && role != PaymentWorkerRole && role != PaymentProjectionRole {
		return ErrPaymentRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer tx.Rollback(ctx)
	var schema *string
	var current, session string
	if tx.QueryRow(ctx, "SELECT current_schema(),current_user,session_user").Scan(&schema, &current, &session) != nil || schema == nil || current != session {
		return ErrPaymentRolePrivileges
	}
	if e = verifyPaymentRole(ctx, tx, *schema, current, role, paymentMarker{}, true); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return ErrPaymentRolePrivileges
	}
	return nil
}

// VerifyPaymentRolesFor inspects named roles without SET ROLE or ACL mutation.
func VerifyPaymentRolesFor(ctx context.Context, pool *pgxpool.Pool, schema, appRole, workerRole, projectionRole string) error {
	if pool == nil {
		return ErrPaymentRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer tx.Rollback(ctx)
	if e = verifyPaymentRolesFor(ctx, tx, schema, appRole, workerRole, projectionRole); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return ErrPaymentRolePrivileges
	}
	return nil
}

func verifyPaymentRolesFor(ctx context.Context, tx pgx.Tx, schema, app, worker, projection string) error {
	if app == "" || projection == "" || app == projection || worker == app || worker == projection {
		return ErrPaymentRolePrivileges
	}
	m, e := paymentReadMarker(ctx, tx, schema)
	if e != nil {
		return e
	}
	if m.present && (m.app != app || m.worker != worker || m.projection != projection) {
		return ErrPaymentRolePrivileges
	}
	if !m.present && worker != "" {
		return ErrPaymentRolePrivileges
	}
	// Even an empty marker must not discard the offline caller's explicit peer
	// identities: SET/ADMIN/inheritance into the other runtime remains forbidden.
	if !m.present {
		m.app, m.worker, m.projection = app, worker, projection
	}
	for _, v := range []struct {
		name string
		role PaymentDatabaseRole
	}{{app, PaymentApplicationRole}, {projection, PaymentProjectionRole}, {worker, PaymentWorkerRole}} {
		if v.name != "" {
			if e = verifyPaymentRole(ctx, tx, schema, v.name, v.role, m, false); e != nil {
				return e
			}
		}
	}
	return nil
}

// ProvisionPaymentWorkerFor is owner-only offline configuration of existing
// identities. It never creates roles, marker, credentials, or environment.
func ProvisionPaymentWorkerFor(ctx context.Context, pool *pgxpool.Pool, schema, app, worker, projection string) error {
	if pool == nil {
		return ErrPaymentRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := pool.Begin(ctx)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer tx.Rollback(ctx)
	var owner bool
	if tx.QueryRow(ctx, `SELECT `+paymentSchemaObjectOwnerSQL+`=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user) FROM pg_catalog.pg_namespace n WHERE n.nspname=$1`, schema).Scan(&owner) != nil || !owner {
		return ErrPaymentRolePrivileges
	}
	m, e := paymentReadMarker(ctx, tx, schema)
	if e != nil || !m.present || m.app != app || m.worker != worker || m.projection != projection {
		return ErrPaymentRolePrivileges
	}
	if n, e := paymentTableCount(ctx, tx, schema); e != nil || n != len(paymentTableNames) {
		return ErrPaymentRolePrivileges
	}
	if e = provisionPaymentRoleACL(ctx, tx, schema, worker, PaymentWorkerRole); e != nil {
		return e
	}
	if e = verifyPaymentRolesFor(ctx, tx, schema, app, worker, projection); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return ErrPaymentRolePrivileges
	}
	return nil
}

// Only historical prefixes with all 23 tables absent may skip this extension.
// This hook shares ProvisionFor's transaction, so a failure rolls back blanket grants.
func provisionPaymentRoles(ctx context.Context, tx pgx.Tx, schema, app, projection string) error {
	n, e := paymentTableCount(ctx, tx, schema)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	if n == 0 {
		return nil
	}
	if n != len(paymentTableNames) {
		return ErrPaymentRolePrivileges
	}
	for _, v := range []struct {
		name string
		role PaymentDatabaseRole
	}{{app, PaymentApplicationRole}, {projection, PaymentProjectionRole}} {
		if e = provisionPaymentRoleACL(ctx, tx, schema, v.name, v.role); e != nil {
			return e
		}
	}
	m, e := paymentReadMarker(ctx, tx, schema)
	if e != nil {
		return e
	}
	worker := ""
	if m.present {
		worker = m.worker
	}
	return verifyPaymentRolesFor(ctx, tx, schema, app, worker, projection)
}

func provisionPaymentRoleACL(ctx context.Context, tx pgx.Tx, schema, name string, role PaymentDatabaseRole) error {
	qrole, qs := pgx.Identifier{name}.Sanitize(), pgx.Identifier{schema}.Sanitize()
	if _, e := tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+qs+" TO "+qrole+"; REVOKE CREATE ON SCHEMA "+qs+" FROM "+qrole); e != nil {
		return ErrPaymentRolePrivileges
	}
	rows, e := tx.Query(ctx, `SELECT c.relname,ARRAY(SELECT a.attname::text FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','f') AND ($2 OR c.relname=ANY($3::text[])) ORDER BY c.relname`, schema, role == PaymentWorkerRole, paymentTableNames)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	type relation struct {
		name string
		cols []string
	}
	var relations []relation
	for rows.Next() {
		var r relation
		if rows.Scan(&r.name, &r.cols) != nil {
			rows.Close()
			return ErrPaymentRolePrivileges
		}
		relations = append(relations, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	grants := paymentGrants(role)
	for _, r := range relations {
		qt := pgx.Identifier{schema, r.name}.Sanitize()
		if _, e = tx.Exec(ctx, "REVOKE ALL ON TABLE "+qt+" FROM "+qrole); e != nil {
			return ErrPaymentRolePrivileges
		}
		if len(r.cols) > 0 {
			if _, e = tx.Exec(ctx, "REVOKE ALL ("+paymentQuoteColumns(r.cols)+") ON "+qt+" FROM "+qrole); e != nil {
				return ErrPaymentRolePrivileges
			}
		}
		g := grants[r.name]
		for _, v := range []struct {
			priv string
			all  bool
			cols []string
		}{{"SELECT", g.selectAll, g.selectCols}, {"INSERT", g.insertAll, g.insertCols}, {"UPDATE", false, g.updateCols}} {
			priv := v.priv
			if !v.all {
				if len(v.cols) == 0 {
					continue
				}
				priv += "(" + paymentQuoteColumns(v.cols) + ")"
			}
			if _, e = tx.Exec(ctx, "GRANT "+priv+" ON "+qt+" TO "+qrole); e != nil {
				return ErrPaymentRolePrivileges
			}
		}
	}
	if e = verifyPaymentFunctionDefinitions(ctx, tx, schema); e != nil {
		return e
	}
	for _, f := range paymentFunctionSpecs {
		fn := pgx.Identifier{schema, f.name}.Sanitize() + "(" + f.arguments + ")"
		if _, e = tx.Exec(ctx, "REVOKE ALL ON FUNCTION "+fn+" FROM "+qrole+",PUBLIC"); e != nil {
			return ErrPaymentRolePrivileges
		}
		if role == PaymentApplicationRole || role == PaymentWorkerRole && f.worker {
			if _, e = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+fn+" TO "+qrole); e != nil {
				return ErrPaymentRolePrivileges
			}
		}
	}
	return nil
}

func paymentQuoteColumns(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = pgx.Identifier{c}.Sanitize()
	}
	return strings.Join(q, ",")
}

func verifyPaymentRole(ctx context.Context, tx pgx.Tx, schema, name string, role PaymentDatabaseRole, pins paymentMarker, runtime bool) error {
	if n, e := paymentTableCount(ctx, tx, schema); e != nil || n != len(paymentTableNames) {
		return ErrPaymentRolePrivileges
	}
	m := pins
	var e error
	// Projection intentionally has no SELECT on the marker or any B table.
	// Its runtime check proves only its catalog-visible privilege boundary;
	// environment membership is attested by the offline named-role verifier.
	if !runtime || role != PaymentProjectionRole {
		m, e = paymentReadMarker(ctx, tx, schema)
		if e != nil {
			return e
		}
	}
	if m.present {
		pins = m
		want := m.app
		if role == PaymentWorkerRole {
			want = m.worker
		}
		if role == PaymentProjectionRole {
			want = m.projection
		}
		if name != want {
			return ErrPaymentRolePrivileges
		}
	} else if role == PaymentWorkerRole {
		return ErrPaymentRolePrivileges
	}
	var oid, schemaOID, schemaOwner uint32
	var login bool
	if tx.QueryRow(ctx, `SELECT oid,rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1`, name).Scan(&oid, &login) != nil || (runtime || m.present) && !login {
		return ErrPaymentRolePrivileges
	}
	if tx.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1`, schema).Scan(&schemaOID, &schemaOwner) != nil {
		return ErrPaymentRolePrivileges
	}
	contexts, e := periodicBudgetContexts(ctx, tx, oid)
	if e != nil || len(contexts) == 0 {
		return ErrPaymentRolePrivileges
	}
	if e = verifyPaymentFunctionDefinitions(ctx, tx, schema); e != nil {
		return e
	}
	for _, r := range contexts {
		if r.high {
			return ErrPaymentRolePrivileges
		}
		var usage, create, grantable, owner bool
		if tx.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE'),pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE'),pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE WITH GRANT OPTION') OR pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE WITH GRANT OPTION'),pg_catalog.pg_has_role($1::oid,$3::oid,'USAGE')`, r.oid, schemaOID, schemaOwner).Scan(&usage, &create, &grantable, &owner) != nil || create || grantable || owner || r.oid == oid && !usage {
			return ErrPaymentRolePrivileges
		}
		for _, peer := range []string{pins.app, pins.worker, pins.projection} {
			if peer != "" && peer != name {
				var reachable bool
				if tx.QueryRow(ctx, `SELECT pg_catalog.pg_has_role($1::oid,oid,'USAGE') FROM pg_catalog.pg_roles WHERE rolname=$2`, r.oid, peer).Scan(&reachable) != nil || reachable {
					return ErrPaymentRolePrivileges
				}
			}
		}
		if e = verifyPaymentTables(ctx, tx, schemaOID, r.oid, role, r.oid == oid); e != nil {
			return e
		}
		if e = verifyPaymentFunctionPrivileges(ctx, tx, schemaOID, r.oid, role, r.oid == oid); e != nil {
			return e
		}
	}
	return nil
}

const paymentPrivilegesSQL = `SELECT c.relname,c.relkind::text,pg_catalog.pg_has_role($1::oid,c.relowner,'USAGE') OR (c.relname=ANY($4::text[]) AND c.relowner<>(SELECT ` + paymentSchemaObjectOwnerSQL + ` FROM pg_catalog.pg_namespace n WHERE n.oid=$2::oid)),
 ARRAY(SELECT pg_catalog.has_table_privilege($1::oid,c.oid,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) WITH ORDINALITY x(p,n) ORDER BY n),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) x(p) WHERE pg_catalog.has_table_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')) OR EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) x(p) WHERE pg_catalog.has_any_column_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')),
 ARRAY(SELECT a.attname::text FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,c.oid,a.attnum,'SELECT') FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,c.oid,a.attnum,'INSERT') FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,c.oid,a.attnum,'UPDATE') FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,c.oid,a.attnum,'REFERENCES') FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 EXISTS(SELECT 1 FROM pg_catalog.aclexplode(c.relacl) acl WHERE acl.grantee=0) OR EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a CROSS JOIN LATERAL pg_catalog.aclexplode(a.attacl) acl WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND acl.grantee=0)
 FROM pg_catalog.pg_class c WHERE c.relnamespace=$2::oid AND c.relkind IN ('r','p','v','m','f') AND ($3 OR c.relname=ANY($4::text[])) ORDER BY c.relname`

func verifyPaymentTables(ctx context.Context, tx pgx.Tx, schemaOID, oid uint32, role PaymentDatabaseRole, required bool) error {
	rows, e := tx.Query(ctx, paymentPrivilegesSQL, oid, schemaOID, role == PaymentWorkerRole, paymentTableNames)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer rows.Close()
	grants := paymentGrants(role)
	seen := map[string]bool{}
	for rows.Next() {
		var name, kind string
		var owner, grantable, public bool
		var table, sel, ins, upd, refs []bool
		var cols []string
		if rows.Scan(&name, &kind, &owner, &table, &grantable, &cols, &sel, &ins, &upd, &refs, &public) != nil {
			return ErrPaymentRolePrivileges
		}
		isPayment := slices.Contains(paymentTableNames, name)
		if owner || grantable || isPayment && (kind != "r" || public) {
			return ErrPaymentRolePrivileges
		}
		g := grants[name]
		want := []bool{g.selectAll, g.insertAll, false, false, false, false, false}
		if !alertPrivilegesMatch(table, want, required) {
			return ErrPaymentRolePrivileges
		}
		if len(cols) != len(sel) || len(cols) != len(ins) || len(cols) != len(upd) || len(cols) != len(refs) {
			return ErrPaymentRolePrivileges
		}
		for i, col := range cols {
			expected := []bool{g.selectAll || slices.Contains(g.selectCols, col), g.insertAll || slices.Contains(g.insertCols, col), slices.Contains(g.updateCols, col), false}
			actual := []bool{sel[i], ins[i], upd[i], refs[i]}
			if !alertPrivilegesMatch(actual, expected, required) {
				return ErrPaymentRolePrivileges
			}
		}
		for _, list := range [][]string{g.selectCols, g.insertCols, g.updateCols} {
			for _, col := range list {
				if !slices.Contains(cols, col) {
					return ErrPaymentRolePrivileges
				}
			}
		}
		seen[name] = true
	}
	if rows.Err() != nil {
		return ErrPaymentRolePrivileges
	}
	for _, name := range paymentTableNames {
		if !seen[name] {
			return ErrPaymentRolePrivileges
		}
	}
	for name := range grants {
		if !seen[name] {
			return ErrPaymentRolePrivileges
		}
	}
	return nil
}

type paymentFunctionSpec struct {
	name, arguments, argOIDs, resultOID, language, volatility, bodySHA string
	argNames                                                           []string
	worker                                                             bool
}

func verifyPaymentFunctionDefinitions(ctx context.Context, tx pgx.Tx, schema string) error {
	rows, e := tx.Query(ctx, `SELECT p.proname,p.proargtypes::text,p.proargnames,p.prorettype::oid::text,l.lanname,p.provolatile::text,p.prosrc,
 p.prokind='f' AND NOT p.prosecdef AND NOT p.proisstrict AND NOT p.proleakproof AND NOT p.proretset AND p.proparallel='u' AND p.pronargdefaults=0 AND p.provariadic=0 AND p.proargmodes IS NULL AND p.proallargtypes IS NULL AND p.proargdefaults IS NULL AND p.probin IS NULL AND p.prosqlbody IS NULL AND p.prosupport=0 AND p.protrftypes IS NULL AND p.proconfig=ARRAY['search_path='||pg_catalog.quote_ident($1)||', pg_catalog, pg_temp'] AND p.proowner=`+paymentSchemaObjectOwnerSQL+`
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND (left(p.proname,8)='payment_' OR p.proname IN ('funding_journal_valid','funding_fact_guard')) ORDER BY p.proname,p.proargtypes`, schema)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name, argOIDs, result, language, volatility, body string
		var args []string
		var valid bool
		if rows.Scan(&name, &argOIDs, &args, &result, &language, &volatility, &body, &valid) != nil || !valid || seen[name] {
			return ErrPaymentRolePrivileges
		}
		f, ok := paymentFunctionSpecs[name]
		if !ok || f.argOIDs != argOIDs || f.resultOID != result || f.language != language || f.volatility != volatility || !slices.Equal(f.argNames, args) || fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != f.bodySHA {
			return ErrPaymentRolePrivileges
		}
		seen[name] = true
	}
	if rows.Err() != nil || len(seen) != len(paymentFunctionSpecs) {
		return ErrPaymentRolePrivileges
	}
	return nil
}

func verifyPaymentFunctionPrivileges(ctx context.Context, tx pgx.Tx, schemaOID, oid uint32, role PaymentDatabaseRole, required bool) error {
	rows, e := tx.Query(ctx, `SELECT p.proname,pg_catalog.has_function_privilege($1::oid,p.oid,'EXECUTE'),p.prosecdef,pg_catalog.has_function_privilege($1::oid,p.oid,'EXECUTE WITH GRANT OPTION'),EXISTS(SELECT 1 FROM pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_catalog.pg_proc p WHERE p.pronamespace=$2::oid`, oid, schemaOID)
	if e != nil {
		return ErrPaymentRolePrivileges
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var execute, definer, grantable, public bool
		if rows.Scan(&name, &execute, &definer, &grantable, &public) != nil || grantable || execute && definer {
			return ErrPaymentRolePrivileges
		}
		if f, ok := paymentFunctionSpecs[name]; ok {
			want := role == PaymentApplicationRole || role == PaymentWorkerRole && f.worker
			if public || execute && !want || required && execute != want {
				return ErrPaymentRolePrivileges
			}
		}
	}
	if rows.Err() != nil {
		return ErrPaymentRolePrivileges
	}
	return nil
}
