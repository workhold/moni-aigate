package maintenancetrust

import (
	"context"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

// Every field is scalar so an exit re-read can compare the complete identity.
// These values are observations, never a substitute for the owned catalog proof.
type maintenanceIdentity struct {
	databaseName, loginName, sessionName                        string
	databaseOID, databaseOwner, loginOID                        uint32
	login, superuser                                            bool
	schemaName, currentSchema, rawOwnerName, effectiveOwnerName string
	schemaOID, rawOwnerOID, effectiveOwnerOID                   uint32
	serverAddress, remoteAddress                                string
	serverPort                                                  int32
	remotePort                                                  int
	pbAllocations, pbConfigurationVersions, pbControls          uint32
	pbEvents, pbOperations, pbWindows                           uint32
}

const maintenanceIdentitySQL = `SELECT d.datname::pg_catalog.text,d.oid,d.datdba,
 current_user::pg_catalog.text,session_user::pg_catalog.text,r.oid,r.rolcanlogin,r.rolsuper,
 n.nspname::pg_catalog.text,n.oid,n.nspowner,nr.rolname::pg_catalog.text,
 er.oid,er.rolname::pg_catalog.text,pg_catalog.current_schema()::pg_catalog.text,
 pg_catalog.host(pg_catalog.inet_server_addr()),pg_catalog.inet_server_port()
 FROM pg_catalog.pg_database d
 JOIN pg_catalog.pg_roles r ON r.rolname OPERATOR(pg_catalog.=) current_user
 JOIN pg_catalog.pg_namespace n ON n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.name
 JOIN pg_catalog.pg_roles nr ON nr.oid OPERATOR(pg_catalog.=) n.nspowner
 JOIN pg_catalog.pg_roles er ON er.oid OPERATOR(pg_catalog.=)
 CASE WHEN n.nspname OPERATOR(pg_catalog.=) 'public'::pg_catalog.name
 AND n.nspowner OPERATOR(pg_catalog.=) 6171::pg_catalog.oid
 AND nr.rolname OPERATOR(pg_catalog.=) 'pg_database_owner'::pg_catalog.name
 THEN d.datdba ELSE n.nspowner END
 WHERE d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`

var maintenancePBTables = [...]string{
	"periodic_budget_allocations", "periodic_budget_configuration_versions",
	"periodic_budget_controls", "periodic_budget_events",
	"periodic_budget_operations", "periodic_budget_windows",
}

const maintenancePBOwnersSQL = `SELECT c.relname::pg_catalog.text,c.oid,c.relowner,c.relkind::pg_catalog.text,
 c.relpersistence::pg_catalog.text,c.relrowsecurity,c.relforcerowsecurity
 FROM pg_catalog.pg_class c WHERE c.relnamespace OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 AND c.relname OPERATOR(pg_catalog.=) ANY(ARRAY[
 'periodic_budget_allocations','periodic_budget_configuration_versions','periodic_budget_controls',
 'periodic_budget_events','periodic_budget_operations','periodic_budget_windows']::pg_catalog.name[])
 ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog."C"`

func authorityLeafFailure(ctx context.Context, otherwise error) error {
	if ctx == nil {
		return ErrInput
	}
	if ctx.Err() != nil {
		return ErrTimeout
	}
	return otherwise
}

func (a *authorityStage) readMaintenanceIdentity(ctx context.Context) (*maintenanceIdentity, error) {
	if err := a.verify(ctx); err != nil {
		return nil, err
	}
	return readMaintenanceIdentitySnapshot(ctx, a.snapshot)
}

// This returns observed scalar identity only. Authority callers still verify
// their original InstallationProof and issuer binding before entering here.
func readMaintenanceIdentitySnapshot(ctx context.Context, st *snapshotState) (*maintenanceIdentity, error) {
	if err := maintenanceFactsSnapshot(ctx, st); err != nil {
		return nil, err
	}
	if st.conn == nil || st.pgconn == nil {
		return nil, ErrTransaction
	}
	gs := st.gate
	cfg := st.conn.Config()
	if !validConfig(cfg, gs.pins) {
		return nil, ErrIdentity
	}
	remote, ok := st.pgconn.Conn().RemoteAddr().(*net.TCPAddr)
	if !ok || remote == nil {
		return nil, ErrIdentity
	}
	id := &maintenanceIdentity{remoteAddress: remote.IP.String(), remotePort: remote.Port}
	if err := st.rawTx.QueryRow(ctx, maintenanceIdentitySQL, pgx.QueryExecModeSimpleProtocol, gs.pins.AppSchema.Name).Scan(
		&id.databaseName, &id.databaseOID, &id.databaseOwner, &id.loginName, &id.sessionName, &id.loginOID, &id.login, &id.superuser,
		&id.schemaName, &id.schemaOID, &id.rawOwnerOID, &id.rawOwnerName, &id.effectiveOwnerOID, &id.effectiveOwnerName,
		&id.currentSchema, &id.serverAddress, &id.serverPort); err != nil {
		return nil, authorityLeafFailure(ctx, ErrIdentity)
	}
	if !id.matches(cfg.User, cfg.Host, gs.pins) {
		return nil, ErrIdentity
	}
	if gs.pins.Mode == fundinglotcatalog.Ordinary {
		rows, err := st.rawTx.Query(ctx, maintenancePBOwnersSQL, pgx.QueryExecModeSimpleProtocol, id.schemaOID)
		if err != nil {
			return nil, authorityLeafFailure(ctx, ErrIdentity)
		}
		defer rows.Close()
		owners := []*uint32{&id.pbAllocations, &id.pbConfigurationVersions, &id.pbControls, &id.pbEvents, &id.pbOperations, &id.pbWindows}
		index := 0
		for rows.Next() {
			var name, kind, persistence string
			var oid, owner uint32
			var rls, forceRLS bool
			if rows.Scan(&name, &oid, &owner, &kind, &persistence, &rls, &forceRLS) != nil ||
				index >= len(maintenancePBTables) || name != maintenancePBTables[index] || oid == 0 || owner != id.loginOID ||
				kind != "r" || persistence != "p" || rls || forceRLS {
				return nil, authorityLeafFailure(ctx, ErrIdentity)
			}
			*owners[index] = oid
			index++
		}
		rows.Close()
		if rows.Err() != nil || index != len(maintenancePBTables) {
			return nil, authorityLeafFailure(ctx, ErrIdentity)
		}
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	return id, nil
}

func (id *maintenanceIdentity) matches(configUser, configHost string, p fundinglotcatalog.Pins) bool {
	if id == nil || !validPins(p) || configHost != p.Database.Host || !id.login || id.loginOID == 0 || id.databaseOwner == 0 ||
		id.loginName != configUser || id.sessionName != configUser ||
		id.databaseName != p.Database.Name || id.databaseOID != p.Database.OID ||
		id.schemaName != p.AppSchema.Name || id.currentSchema != p.AppSchema.Name || id.schemaOID != p.AppSchema.OID ||
		id.rawOwnerName != p.AppSchema.NamespaceOwner.Name || id.rawOwnerOID != p.AppSchema.NamespaceOwner.OID ||
		id.effectiveOwnerName != p.AppSchema.ObjectOwner.Name || id.effectiveOwnerOID != p.AppSchema.ObjectOwner.OID ||
		id.remotePort != int(p.Database.Port) || id.serverPort != int32(p.Database.Port) {
		return false
	}
	remote, server := net.ParseIP(id.remoteAddress), net.ParseIP(id.serverAddress)
	if remote == nil || server == nil || !remote.Equal(server) {
		return false
	}
	if ip := net.ParseIP(configHost); ip != nil && !ip.Equal(remote) {
		return false
	}
	if p.AppSchema.Name == "public" && p.AppSchema.NamespaceOwner == (fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}) {
		if id.databaseOwner != id.effectiveOwnerOID {
			return false
		}
	} else if id.rawOwnerOID != id.effectiveOwnerOID || id.rawOwnerName != id.effectiveOwnerName {
		return false
	}
	if p.Mode == fundinglotcatalog.Ordinary {
		return id.loginOID == id.effectiveOwnerOID && id.loginName == id.effectiveOwnerName
	}
	return configHost == "127.0.0.1" && configUser == "moni_test" && id.databaseName == "moni_test" &&
		id.serverPort == 55439 && remote.To4() != nil && remote.Equal(net.IPv4(127, 0, 0, 1)) &&
		id.superuser && id.databaseOwner == id.loginOID
}

func (maintenanceIdentity) String() string   { return "maintenance_identity_redacted" }
func (maintenanceIdentity) GoString() string { return "maintenance_identity_redacted" }
func (maintenanceIdentity) MarshalJSON() ([]byte, error) {
	return []byte(`{"identity":"redacted"}`), nil
}

func maintenanceRuntimeOIDs(p fundinglotcatalog.Pins) ([]uint32, error) {
	if !validPins(p) {
		return nil, ErrIdentity
	}
	ids := []uint32{p.App.OID, p.Projector.OID}
	if p.Mode == fundinglotcatalog.Sandbox {
		ids = append(ids, p.Worker.OID, p.Sandbox.MockRuntime.OID, p.Sandbox.ReceiverRuntime.OID)
	}
	return ids, nil
}

const clearMaintenanceActivitySQL = `SELECT pg_catalog.pg_stat_clear_snapshot()`
const stoppedMaintenanceActivitySQL = `SELECT pg_catalog.count(*) FROM pg_catalog.pg_stat_activity
 WHERE datid OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 AND usesysid OPERATOR(pg_catalog.=) ANY($2::pg_catalog.oid[])`

func (a *authorityStage) verifyStopped(ctx context.Context) error {
	if err := a.verify(ctx); err != nil {
		return err
	}
	return verifyMaintenanceStoppedSnapshot(ctx, a.snapshot)
}

func verifyMaintenanceStoppedSnapshot(ctx context.Context, st *snapshotState) error {
	if err := maintenanceFactsSnapshot(ctx, st); err != nil {
		return err
	}
	ids, err := maintenanceRuntimeOIDs(st.gate.pins)
	if err != nil {
		return err
	}
	// Keep these consecutive and separate: T1 already warmed the transaction's
	// activity cache. Clearing stats does not replace its commercial RR snapshot.
	if _, err = st.rawTx.Exec(ctx, clearMaintenanceActivitySQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return authorityLeafFailure(ctx, ErrIdentity)
	}
	var count int64
	if err = st.rawTx.QueryRow(ctx, stoppedMaintenanceActivitySQL, pgx.QueryExecModeSimpleProtocol, st.gate.pins.Database.OID, ids).Scan(&count); err != nil || count != 0 {
		return authorityLeafFailure(ctx, ErrIdentity)
	}
	return authorityLeafFailure(ctx, nil)
}
