package payments

import (
	"bytes"
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func runtimeVerifyCatalog(ctx context.Context, tx pgx.Tx, o Options, component runtimeComponent) error {
	p := o.proof
	own := p.App.Runtime
	if component == workerComponent {
		own = p.Worker
	}
	var db, user, session, address, encoding, syncCommit, fsync, fullPage, schema string
	var dbOID, userOID uint32
	var port, version int
	e := tx.QueryRow(ctx, `SELECT current_database(),current_user,session_user,
 (SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),
 (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user),host(inet_server_addr()),inet_server_port(),
 current_setting('server_encoding'),current_setting('synchronous_commit'),current_setting('fsync'),
 current_setting('full_page_writes'),current_setting('server_version_num')::integer,current_schema()`).Scan(&db, &user, &session, &dbOID, &userOID, &address, &port, &encoding, &syncCommit, &fsync, &fullPage, &version, &schema)
	if e != nil || db != p.Database.Name || dbOID != p.Database.OID || user != own.Name || session != user || userOID != own.OID || address != p.Database.Host || port != int(p.Database.Port) || encoding != "UTF8" || syncCommit != "on" || fsync != "on" || fullPage != "on" || version < 160000 || schema != p.App.Name {
		return ErrPaymentsEnvironment
	}
	for _, s := range []runtimeSchemaPin{p.App, p.Mock, p.Receiver} {
		var oid, owner uint32
		if tx.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1`, s.Name).Scan(&oid, &owner) != nil || oid != s.OID || owner != s.Owner.OID {
			return ErrPaymentsEnvironment
		}
	}
	// The file proof pins eight isolated roles. Reject membership in either
	// direction, including SET/ADMIN paths; no role may be replaced by its name.
	for _, r := range runtimeRoles(p) {
		var oid uint32
		var login, inherit, super, createdb, createrole, replication, bypass, member bool
		if tx.QueryRow(ctx, `SELECT oid,rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls,
   EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members a WHERE a.member=pg_roles.oid OR a.roleid=pg_roles.oid)
   FROM pg_catalog.pg_roles WHERE rolname=$1`, r.Name).Scan(&oid, &login, &inherit, &super, &createdb, &createrole, &replication, &bypass, &member) != nil || oid != r.OID || login != r.Login || inherit || super || createdb || createrole || replication || bypass || member {
			return ErrPaymentsEnvironment
		}
	}
	for _, r := range []runtimeRolePin{p.App.Runtime, p.Worker, p.Projector, p.Mock.Runtime, p.Receiver.Runtime} {
		var bad bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspowner=$1::oid)
   OR EXISTS(SELECT 1 FROM pg_catalog.pg_class WHERE relowner=$1::oid)
   OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc WHERE proowner=$1::oid)
   OR EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datdba=$1::oid)`, r.OID).Scan(&bad) != nil || bad {
			return ErrPaymentsEnvironment
		}
	}
	// Peer content is deliberately unreadable. Catalog-only negative permission
	// checks cover table-level and column-level grants as well as grant options.
	pairs := []struct {
		r runtimeRolePin
		s runtimeSchemaPin
	}{
		{p.App.Runtime, p.Mock}, {p.App.Runtime, p.Receiver}, {p.Worker, p.Mock}, {p.Worker, p.Receiver},
		{p.Projector, p.Mock}, {p.Projector, p.Receiver}, {p.Mock.Runtime, p.App}, {p.Mock.Runtime, p.Receiver},
		{p.Receiver.Runtime, p.App}, {p.Receiver.Runtime, p.Mock},
	}
	for _, pair := range pairs {
		if e := runtimeNoForeignPrivileges(ctx, tx, pair.r.OID, pair.s.OID); e != nil {
			return e
		}
	}
	return nil
}

func runtimeNoForeignPrivileges(ctx context.Context, tx pgx.Tx, role, schema uint32) error {
	var bad bool
	e := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['USAGE','CREATE','USAGE WITH GRANT OPTION','CREATE WITH GRANT OPTION']) p WHERE pg_catalog.has_schema_privilege($1::oid,$2::oid,p))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER','SELECT WITH GRANT OPTION','INSERT WITH GRANT OPTION','UPDATE WITH GRANT OPTION','DELETE WITH GRANT OPTION','TRUNCATE WITH GRANT OPTION','REFERENCES WITH GRANT OPTION','TRIGGER WITH GRANT OPTION']) p WHERE c.relnamespace=$2::oid AND c.relkind IN ('r','p','v','m','f','S') AND pg_catalog.has_table_privilege($1::oid,c.oid,p))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES','SELECT WITH GRANT OPTION','INSERT WITH GRANT OPTION','UPDATE WITH GRANT OPTION','REFERENCES WITH GRANT OPTION']) p WHERE c.relnamespace=$2::oid AND c.relkind IN ('r','p','v','m','f') AND pg_catalog.has_any_column_privilege($1::oid,c.oid,p))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc f WHERE f.pronamespace=$2::oid AND (pg_catalog.has_function_privilege($1::oid,f.oid,'EXECUTE') OR pg_catalog.has_function_privilege($1::oid,f.oid,'EXECUTE WITH GRANT OPTION')))`, role, schema).Scan(&bad)
	if e != nil || bad {
		return ErrPaymentsEnvironment
	}
	return nil
}

func runtimeVerifyAttestation(ctx context.Context, tx pgx.Tx, o Options) error {
	p := o.proof
	marker := pgx.Identifier{p.App.Name, "payment_sandbox_environment"}.Sanitize()
	att := pgx.Identifier{p.App.Name, "payment_runtime_attestations"}.Sanitize()
	for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
		if e := runtimeReadableTable(ctx, tx, p.App.Name, table, p.App.Owner.OID); e != nil {
			return e
		}
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM `+marker).Scan(&count) != nil || count != 1 {
		return ErrPaymentsEnvironment
	}
	if tx.QueryRow(ctx, `SELECT count(*) FROM `+att).Scan(&count) != nil || count != 1 {
		return ErrPaymentsEnvironment
	}
	var id, merchant, payee, app, worker, projector, nonce string
	var dbOID, schemaOID uint32
	var version int
	var singleton bool
	if tx.QueryRow(ctx, `SELECT singleton,id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role::text,worker_role::text,projector_role::text,protocol_version FROM `+marker).Scan(&singleton, &id, &merchant, &payee, &dbOID, &schemaOID, &nonce, &app, &worker, &projector, &version) != nil || !singleton || id != p.EnvironmentID || merchant != p.MerchantID || payee != p.PayeeLabel || dbOID != p.Database.OID || schemaOID != p.App.OID || nonce != p.AppNonceDigest || app != p.App.Runtime.Name || worker != p.Worker.Name || projector != p.Projector.Name || version != p.ProtocolVersion {
		return ErrPaymentsEnvironment
	}
	var ownerOID, appOID, workerOID, projectorOID uint32
	var appNonce, workerNonce, digest string
	var canonical []byte
	var created time.Time
	if tx.QueryRow(ctx, `SELECT singleton,environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256,created_at FROM `+att).Scan(&singleton, &id, &dbOID, &schemaOID, &ownerOID, &appOID, &workerOID, &projectorOID, &appNonce, &workerNonce, &version, &canonical, &digest, &created) != nil || !singleton || id != p.EnvironmentID || dbOID != p.Database.OID || schemaOID != p.App.OID || ownerOID != p.App.Owner.OID || appOID != p.App.Runtime.OID || workerOID != p.Worker.OID || projectorOID != p.Projector.OID || appNonce != p.AppNonceDigest || workerNonce != p.WorkerNonceDigest || version != p.ProofVersion || created.IsZero() || created.Year() < 1 || created.Year() > 9999 || runtimeSHA(canonical) != digest || !bytes.Equal(canonical, o.proofBytes) {
		return ErrPaymentsEnvironment
	}
	var decoded runtimeProof
	if runtimeStrictDecode(canonical, &decoded) != nil || runtimeValidateProof(decoded) != nil {
		return ErrPaymentsEnvironment
	}
	recoded, e := runtimeCanonicalProof(decoded)
	if e != nil || !bytes.Equal(recoded, canonical) {
		return ErrPaymentsEnvironment
	}
	want := p.AppNonceDigest
	if o.component == workerComponent {
		want = p.WorkerNonceDigest
	}
	if o.nonceDigest != want {
		return ErrPaymentsEnvironment
	}
	return nil
}

// Reject row hiding, unlogged/foreign/view replacements, and changed owners
// before relying on an exact count or emptiness read.
func runtimeReadableTable(ctx context.Context, tx pgx.Tx, schema, table string, owner uint32) error {
	var valid bool
	if tx.QueryRow(ctx, `SELECT c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND ($3::oid=0 OR c.relowner=$3::oid) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2`, schema, table, owner).Scan(&valid) != nil || !valid {
		return ErrPaymentsEnvironment
	}
	return nil
}

var runtimeHistoryTables = []string{"payment_sandbox_environment", "payment_runtime_attestations", "payment_skus", "payment_sku_versions", "payment_sku_changes", "payment_orders", "payment_cancel_intents", "payment_command_outbox", "payment_command_observations", "payment_query_attempts", "payment_inbox", "payment_event_conflicts", "payment_verified_queries", "payment_verified_evidence", "payment_evidence_processing", "payment_provider_revisions", "payment_transaction_identities", "payment_order_capture_selections", "payment_captures", "payment_effects", "payment_review_facts"}

func runtimeVerifyOrdinaryOff(ctx context.Context, tx pgx.Tx) error {
	var schema string
	if tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil || schema == "" {
		return ErrPaymentsEnvironment
	}
	for _, table := range runtimeHistoryTables {
		if e := runtimeReadableTable(ctx, tx, schema, table, 0); e != nil {
			return e
		}
		var present bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+pgx.Identifier{schema, table}.Sanitize()+`)`).Scan(&present) != nil || present {
			return ErrPaymentsEnvironment
		}
	}
	// APP intentionally cannot SELECT conflict evidence. Its parent was proved
	// empty in this same RR snapshot; prove the actual mandatory FK before using
	// that fact to infer an empty child without broadening APP permissions.
	if e := runtimeQueryConflictsEmptyProof(ctx, tx, schema); e != nil {
		return e
	}
	if e := runtimeReadableTable(ctx, tx, schema, "payment_policy", 0); e != nil {
		return e
	}
	var policyCount int
	var defaultPolicy bool
	if tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_and(singleton AND NOT enabled AND revision=1),false) FROM `+pgx.Identifier{schema, "payment_policy"}.Sanitize()).Scan(&policyCount, &defaultPolicy) != nil || policyCount != 1 || !defaultPolicy {
		return ErrPaymentsEnvironment
	}
	for _, q := range []struct{ table, predicate string }{{"commerce_commands", `left(kind,8)='payment.'`}, {"commerce_operation_receipts", `left(kind,8)='payment.'`}, {"audit_events", `left(action,8)='payment.'`}, {"funding_receipts", `source_kind='sandbox_capture'`}, {"journals", `kind='sandbox_capture'`}, {"ledger_accounts", `kind='SANDBOX_FUNDING_CLEARING'`}} {
		if e := runtimeReadableTable(ctx, tx, schema, q.table, 0); e != nil {
			return e
		}
		var present bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+pgx.Identifier{schema, q.table}.Sanitize()+` WHERE `+q.predicate+`)`).Scan(&present) != nil || present {
			return ErrPaymentsEnvironment
		}
	}
	return nil
}
func runtimeQueryConflictsEmptyProof(ctx context.Context, tx pgx.Tx, schema string) error {
	if e := runtimeReadableTable(ctx, tx, schema, "payment_query_conflicts", 0); e != nil {
		return e
	}
	var valid bool
	e := tx.QueryRow(ctx, `SELECT child.attnotnull AND EXISTS(
 SELECT 1 FROM pg_catalog.pg_constraint f WHERE f.contype='f' AND f.conrelid=c.oid AND f.confrelid=p.oid
 AND f.convalidated AND NOT f.condeferrable AND NOT f.condeferred
 AND f.conkey=ARRAY[child.attnum]::smallint[] AND f.confkey=ARRAY[parent.attnum]::smallint[])
 FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid AND c.relname='payment_query_conflicts'
 JOIN pg_catalog.pg_class p ON p.relnamespace=n.oid AND p.relname='payment_verified_queries'
 JOIN pg_catalog.pg_attribute child ON child.attrelid=c.oid AND child.attname='verified_query_id' AND child.attnum>0 AND NOT child.attisdropped
 JOIN pg_catalog.pg_attribute parent ON parent.attrelid=p.oid AND parent.attname='id' AND parent.attnum>0 AND NOT parent.attisdropped
 WHERE n.nspname=$1`, schema).Scan(&valid)
	if e != nil || !valid {
		return ErrPaymentsEnvironment
	}
	return nil
}
