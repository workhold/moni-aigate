package fundinglotcatalog

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// These pins identify the unchanged v1 peer inputs used to review the embedded
// complete descriptors. They are provenance, never runtime installation input.
type peerSourcePin struct{ Name, SHA256 string }

var peerSourcePins = [...]peerSourcePin{
	{"internal/paymentsandboxschema/mock_v1.sql", "3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127"},
	{"internal/paymentsandboxschema/receiver_v1.sql", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e"},
	{"internal/paymentsandboxschema/mock_inventory.json", "3f5e926b8ed9152d6bf9ff757400ab3119241629cb3b00cdbd40974d9f818234"},
	{"internal/paymentsandboxschema/receiver_inventory.json", "73be5ea3231cd8eb3504d1e1b8b269fab6b92f10be6b8128e7feafa569b662d8"},
	{"internal/paymentsandboxqa/install.go", "dd0ac0d67bbe9b6e400f11ccff14479a9c156779b5fc2079ee24c7c830e18888"},
	{"internal/paymentsandboxqa/application.go", "b0d28048b9fa3d856b83e494d319158ad4c3937eb745dde88674536496254f33"},
}

type peerRoleExpectation struct {
	pin   RolePin
	login bool
}

func peerRoleRegistry(p Pins) ([]peerRoleExpectation, error) {
	if p.Mode != Sandbox || p.Worker == nil || p.Sandbox == nil {
		return nil, ErrCatalog
	}
	s := p.Sandbox
	if p.AppSchema.NamespaceOwner != p.AppSchema.ObjectOwner || s.Mock.NamespaceOwner != s.Mock.ObjectOwner || s.Receiver.NamespaceOwner != s.Receiver.ObjectOwner {
		return nil, ErrCatalog
	}
	schemas := []SchemaPin{p.AppSchema, s.Mock, s.Receiver}
	schemaNames, schemaIDs := map[string]bool{}, map[uint32]bool{}
	for _, schema := range schemas {
		if schema.Name == "" || schema.OID == 0 || schemaNames[schema.Name] || schemaIDs[schema.OID] {
			return nil, ErrCatalog
		}
		schemaNames[schema.Name], schemaIDs[schema.OID] = true, true
	}
	roles := []peerRoleExpectation{
		{p.AppSchema.ObjectOwner, false}, {p.App, true}, {*p.Worker, true}, {p.Projector, true},
		{s.Mock.ObjectOwner, false}, {s.MockRuntime, true}, {s.Receiver.ObjectOwner, false}, {s.ReceiverRuntime, true},
	}
	names, ids := map[string]bool{}, map[uint32]bool{}
	for _, r := range roles {
		if r.pin.Name == "" || r.pin.OID == 0 || names[r.pin.Name] || ids[r.pin.OID] {
			return nil, ErrCatalog
		}
		names[r.pin.Name], ids[r.pin.OID] = true, true
	}
	return roles, nil
}

const peerRolesSQL = `SELECT oid,rolname::pg_catalog.text,rolcanlogin,rolinherit,rolsuper,
 rolcreatedb,rolcreaterole,rolreplication,rolbypassrls,rolconnlimit,rolvaliduntil IS NULL,rolconfig IS NULL
 FROM pg_catalog.pg_roles WHERE oid OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[])`

const peerNoMembershipSQL = `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members
 WHERE roleid OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]) OR member OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))`

const peerNoRuntimeOwnerSQL = `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datdba OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspowner OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class WHERE relowner OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc WHERE proowner OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_type WHERE typowner OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]))`

const peerNoDatabaseElevationSQL = `SELECT
 NOT pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'CREATE')
 AND NOT pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'CREATE WITH GRANT OPTION')
 AND NOT pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'CONNECT WITH GRANT OPTION')
 AND NOT pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'TEMPORARY WITH GRANT OPTION')`

func verifyRuntimeDatabaseClosureTx(ctx context.Context, tx pgx.Tx, database uint32, runtimes []uint32) error {
	if database == 0 || len(runtimes) == 0 {
		return ErrCatalog
	}
	for _, runtime := range runtimes {
		var valid bool
		if runtime == 0 || tx.QueryRow(ctx, peerNoDatabaseElevationSQL, runtime, database).Scan(&valid) != nil || !valid {
			return ErrCatalog
		}
	}
	return nil
}

const peerNoForeignPrivilegesSQL = `SELECT
 NOT EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['USAGE','CREATE','USAGE WITH GRANT OPTION','CREATE WITH GRANT OPTION']::pg_catalog.text[]) p
 WHERE pg_catalog.has_schema_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,p))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER','SELECT WITH GRANT OPTION','INSERT WITH GRANT OPTION','UPDATE WITH GRANT OPTION','DELETE WITH GRANT OPTION','TRUNCATE WITH GRANT OPTION','REFERENCES WITH GRANT OPTION','TRIGGER WITH GRANT OPTION']::pg_catalog.text[]) p
 WHERE c.relnamespace OPERATOR(pg_catalog.=) $2::pg_catalog.oid AND c.relkind OPERATOR(pg_catalog.=) ANY(ARRAY['r','p','v','m','f','S']::pg_catalog."char"[]) AND pg_catalog.has_table_privilege($1::pg_catalog.oid,c.oid,p))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES','SELECT WITH GRANT OPTION','INSERT WITH GRANT OPTION','UPDATE WITH GRANT OPTION','REFERENCES WITH GRANT OPTION']::pg_catalog.text[]) p
 WHERE c.relnamespace OPERATOR(pg_catalog.=) $2::pg_catalog.oid AND c.relkind OPERATOR(pg_catalog.=) ANY(ARRAY['r','p','v','m','f']::pg_catalog."char"[]) AND pg_catalog.has_any_column_privilege($1::pg_catalog.oid,c.oid,p))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc f WHERE f.pronamespace OPERATOR(pg_catalog.=) $2::pg_catalog.oid
 AND (pg_catalog.has_function_privilege($1::pg_catalog.oid,f.oid,'EXECUTE') OR pg_catalog.has_function_privilege($1::pg_catalog.oid,f.oid,'EXECUTE WITH GRANT OPTION')))`

// Called after the complete same-RR schema/ACL comparison. Ordinary APP/P and
// all five sandbox runtimes also have a global no-object-ownership boundary.
// No peer business table or attestation is read, and no transaction or role is
// created here.
func verifyPeerRoleClosureTx(ctx context.Context, tx pgx.Tx, p Pins) error {
	if ctx == nil || ctx.Err() != nil || !validTx(tx) || !validPins(p) {
		return ErrCatalog
	}
	if p.Mode == Ordinary {
		runtimes := []uint32{p.App.OID, p.Projector.OID}
		var valid bool
		if tx.QueryRow(ctx, peerNoRuntimeOwnerSQL, runtimes).Scan(&valid) != nil || !valid {
			return ErrCatalog
		}
		return verifyRuntimeDatabaseClosureTx(ctx, tx, p.Database.OID, runtimes)
	}
	roles, e := peerRoleRegistry(p)
	if e != nil {
		return ErrCatalog
	}
	ids, runtimes := make([]uint32, 0, 8), make([]uint32, 0, 5)
	byID := map[uint32]peerRoleExpectation{}
	for _, r := range roles {
		ids = append(ids, r.pin.OID)
		byID[r.pin.OID] = r
		if r.login {
			runtimes = append(runtimes, r.pin.OID)
		}
	}
	rows, e := tx.Query(ctx, peerRolesSQL, ids)
	if e != nil {
		return ErrCatalog
	}
	count := 0
	for rows.Next() {
		var oid uint32
		var name string
		var login, inherit, super, createdb, createrole, replication, bypass, noExpiry, noConfig bool
		var limit int32
		if rows.Scan(&oid, &name, &login, &inherit, &super, &createdb, &createrole, &replication, &bypass, &limit, &noExpiry, &noConfig) != nil {
			rows.Close()
			return ErrCatalog
		}
		want, ok := byID[oid]
		if !ok || name != want.pin.Name || login != want.login || inherit || super || createdb || createrole || replication || bypass || limit != -1 || !noExpiry || !noConfig {
			rows.Close()
			return ErrCatalog
		}
		delete(byID, oid)
		count++
	}
	rows.Close()
	if rows.Err() != nil || count != 8 || len(byID) != 0 {
		return ErrCatalog
	}
	for _, q := range []struct {
		sql string
		ids []uint32
	}{{peerNoMembershipSQL, ids}, {peerNoRuntimeOwnerSQL, runtimes}} {
		var valid bool
		if tx.QueryRow(ctx, q.sql, q.ids).Scan(&valid) != nil || !valid {
			return ErrCatalog
		}
	}
	if verifyRuntimeDatabaseClosureTx(ctx, tx, p.Database.OID, runtimes) != nil {
		return ErrCatalog
	}
	s := p.Sandbox
	pairs := [][2]uint32{
		{p.App.OID, s.Mock.OID}, {p.App.OID, s.Receiver.OID}, {p.Worker.OID, s.Mock.OID}, {p.Worker.OID, s.Receiver.OID},
		{p.Projector.OID, s.Mock.OID}, {p.Projector.OID, s.Receiver.OID},
		{s.MockRuntime.OID, p.AppSchema.OID}, {s.MockRuntime.OID, s.Receiver.OID}, {s.ReceiverRuntime.OID, p.AppSchema.OID}, {s.ReceiverRuntime.OID, s.Mock.OID},
	}
	for _, pair := range pairs {
		var valid bool
		if tx.QueryRow(ctx, peerNoForeignPrivilegesSQL, pair[0], pair[1]).Scan(&valid) != nil || !valid {
			return ErrCatalog
		}
	}
	// Automatic row/array USAGE follows PostgreSQL defaults. Only the named
	// source-proof type and its dependent array have this additional zero bound.
	var countTypes int
	var valid bool
	if tx.QueryRow(ctx, `SELECT pg_catalog.count(*)::pg_catalog.int4,coalesce(pg_catalog.bool_and(
 NOT pg_catalog.has_type_privilege($2::pg_catalog.oid,t.oid,'USAGE') AND NOT pg_catalog.has_type_privilege($3::pg_catalog.oid,t.oid,'USAGE')),false)
 FROM pg_catalog.pg_type t WHERE t.typnamespace OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 AND t.typname OPERATOR(pg_catalog.=) ANY(ARRAY['fl_source_proof','_fl_source_proof']::pg_catalog.name[])`, p.AppSchema.OID, s.MockRuntime.OID, s.ReceiverRuntime.OID).Scan(&countTypes, &valid) != nil || countTypes != 2 || !valid {
		return ErrCatalog
	}
	return nil
}
