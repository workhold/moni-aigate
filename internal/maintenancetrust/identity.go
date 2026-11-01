package maintenancetrust

import (
	"context"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func copyPins(p fundinglotcatalog.Pins) fundinglotcatalog.Pins {
	if p.Worker != nil {
		v := *p.Worker
		p.Worker = &v
	}
	if p.Sandbox != nil {
		v := *p.Sandbox
		p.Sandbox = &v
	}
	return p
}
func validName(s string) bool {
	return len(s) > 0 && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validRole(p fundinglotcatalog.RolePin) bool {
	return validName(p.Name) && p.OID != 0 && !strings.HasPrefix(p.Name, "pg_")
}
func validSchema(p fundinglotcatalog.SchemaPin) bool {
	if !validName(p.Name) || p.OID == 0 || strings.HasPrefix(p.Name, "pg_") || p.Name == "information_schema" || !validRole(p.ObjectOwner) {
		return false
	}
	return p.NamespaceOwner == p.ObjectOwner || p.Name == "public" && p.NamespaceOwner == (fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171})
}
func validPins(p fundinglotcatalog.Pins) bool {
	if p.Database.Host == "" || len(p.Database.Host) > 253 || !utf8.ValidString(p.Database.Host) || strings.ContainsRune(p.Database.Host, 0) || strings.HasPrefix(p.Database.Host, "/") ||
		p.Database.Port == 0 || !validName(p.Database.Name) || p.Database.OID == 0 || !validSchema(p.AppSchema) || !validRole(p.App) || !validRole(p.Projector) {
		return false
	}
	roles := []fundinglotcatalog.RolePin{p.AppSchema.ObjectOwner, p.App, p.Projector}
	schemas := []fundinglotcatalog.SchemaPin{p.AppSchema}
	switch p.Mode {
	case fundinglotcatalog.Ordinary:
		if p.Worker != nil || p.Sandbox != nil {
			return false
		}
	case fundinglotcatalog.Sandbox:
		if p.Worker == nil || p.Sandbox == nil || !validRole(*p.Worker) || !validSchema(p.Sandbox.Mock) || !validSchema(p.Sandbox.Receiver) || !validRole(p.Sandbox.MockRuntime) || !validRole(p.Sandbox.ReceiverRuntime) {
			return false
		}
		roles = append(roles, *p.Worker, p.Sandbox.Mock.ObjectOwner, p.Sandbox.MockRuntime, p.Sandbox.Receiver.ObjectOwner, p.Sandbox.ReceiverRuntime)
		schemas = append(schemas, p.Sandbox.Mock, p.Sandbox.Receiver)
	default:
		return false
	}
	for i, r := range roles {
		for j := 0; j < i; j++ {
			if r.Name == roles[j].Name || r.OID == roles[j].OID {
				return false
			}
		}
	}
	for i, s := range schemas {
		if p.Mode == fundinglotcatalog.Sandbox && s.NamespaceOwner != s.ObjectOwner {
			return false
		}
		for j := 0; j < i; j++ {
			if s.Name == schemas[j].Name || s.OID == schemas[j].OID {
				return false
			}
		}
	}
	return true
}

func validConfig(c *pgx.ConnConfig, p fundinglotcatalog.Pins) bool {
	if c.Host != p.Database.Host || c.Port != p.Database.Port || c.Database != p.Database.Name || c.User == "" || c.DialFunc == nil || c.LookupFunc == nil {
		return false
	}
	for _, f := range c.Fallbacks {
		if f == nil || f.Host != c.Host || f.Port != c.Port {
			return false
		}
	}
	if p.Mode == fundinglotcatalog.Sandbox && (c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test") {
		return false
	}
	return true
}

const preflightSQL = `SELECT pg_catalog.current_database()::pg_catalog.text,d.oid,d.datdba,
 current_user::pg_catalog.text,session_user::pg_catalog.text,r.oid,r.rolcanlogin,
 pg_catalog.pg_backend_pid(),a.backend_start,
 pg_catalog.current_schema()::pg_catalog.text,n.oid,n.nspowner,
 pg_catalog.host(pg_catalog.inet_server_addr()),pg_catalog.inet_server_port()
 FROM pg_catalog.pg_database d
 JOIN pg_catalog.pg_roles r ON r.rolname OPERATOR(pg_catalog.=) current_user
 JOIN pg_catalog.pg_stat_activity a ON a.pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()
 JOIN pg_catalog.pg_namespace n ON n.nspname OPERATOR(pg_catalog.=) pg_catalog.current_schema()
 WHERE d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`

func (s *gateState) preflight(ctx context.Context) error {
	if !s.idle() {
		return ErrIdentity
	}
	cfg := s.conn.Config()
	if !validConfig(cfg, s.pins) {
		return ErrIdentity
	}
	remote, ok := s.conn.PgConn().Conn().RemoteAddr().(*net.TCPAddr)
	if !ok || remote == nil || remote.Port != int(s.pins.Database.Port) {
		return ErrIdentity
	}
	if ip := net.ParseIP(cfg.Host); ip != nil && !ip.Equal(remote.IP) {
		return ErrIdentity
	}
	if s.pins.Mode == fundinglotcatalog.Sandbox && (remote.IP.To4() == nil || !remote.IP.Equal(net.IPv4(127, 0, 0, 1))) {
		return ErrIdentity
	}
	var db, role, session, schema, server string
	var dbOID, dbOwner, roleOID, schemaOID, owner uint32
	var port int32
	var login bool
	if err := s.conn.QueryRow(ctx, preflightSQL, pgx.QueryExecModeSimpleProtocol).Scan(&db, &dbOID, &dbOwner, &role, &session, &roleOID, &login, &s.pid, &s.backendStart, &schema, &schemaOID, &owner, &server, &port); err != nil {
		return executionError(ctx)
	}
	p := s.pins
	if !login || role != session || role != cfg.User || roleOID == 0 || s.pid == 0 || s.pid != s.conn.PgConn().PID() || s.backendStart.IsZero() ||
		db != p.Database.Name || dbOID != p.Database.OID || schema != p.AppSchema.Name || schemaOID != p.AppSchema.OID || owner != p.AppSchema.NamespaceOwner.OID ||
		net.ParseIP(server) == nil || !remote.IP.Equal(net.ParseIP(server)) || port != int32(remote.Port) {
		return ErrIdentity
	}
	// Also bind the raw namespace owner's name; the public builtin exception
	// resolves only to this actual database's datdba, never the current LOGIN.
	var ownerName string
	if err := s.conn.QueryRow(ctx, `SELECT rolname::pg_catalog.text FROM pg_catalog.pg_roles WHERE oid OPERATOR(pg_catalog.=) $1::pg_catalog.oid`, pgx.QueryExecModeSimpleProtocol, owner).Scan(&ownerName); err != nil {
		return executionError(ctx)
	}
	if ownerName != p.AppSchema.NamespaceOwner.Name {
		return ErrIdentity
	}
	if p.AppSchema.Name == "public" && p.AppSchema.NamespaceOwner.Name == "pg_database_owner" {
		if dbOwner != p.AppSchema.ObjectOwner.OID {
			return ErrIdentity
		}
		var effectiveOwner string
		if err := s.conn.QueryRow(ctx, `SELECT rolname::pg_catalog.text FROM pg_catalog.pg_roles WHERE oid OPERATOR(pg_catalog.=) $1::pg_catalog.oid`, pgx.QueryExecModeSimpleProtocol, dbOwner).Scan(&effectiveOwner); err != nil {
			return executionError(ctx)
		}
		if effectiveOwner != p.AppSchema.ObjectOwner.Name {
			return ErrIdentity
		}
	}
	return s.checkLocks(ctx)
}

const locksSQL = `SELECT database,mode,granted,classid,objid,objsubid
 FROM pg_catalog.pg_locks
 WHERE locktype OPERATOR(pg_catalog.=) 'advisory'::pg_catalog.text
 AND pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()`

func (s *gateState) checkLocks(ctx context.Context) error {
	if !s.idle() {
		return ErrIdentity
	}
	rows, err := s.conn.Query(ctx, locksSQL, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return executionError(ctx)
	}
	defer rows.Close()
	instance, migration := false, false
	count := 0
	for rows.Next() {
		count++
		var db, class, key uint32
		var mode string
		var granted bool
		var sub int16
		if rows.Scan(&db, &mode, &granted, &class, &key, &sub) != nil {
			return ErrExecution
		}
		if count > 2 || db != s.pins.Database.OID || mode != "ExclusiveLock" || !granted || class != 0 || sub != 1 {
			return ErrIdentity
		}
		switch key {
		case instanceKey:
			if instance || !s.heldInstance {
				return ErrIdentity
			}
			instance = true
		case migrationKey:
			if migration || !s.heldMigration {
				return ErrIdentity
			}
			migration = true
		default:
			return ErrIdentity
		}
	}
	if rows.Err() != nil {
		return executionError(ctx)
	}
	if instance != s.heldInstance || migration != s.heldMigration {
		return ErrIdentity
	}
	return nil
}
