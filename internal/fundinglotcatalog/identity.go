package fundinglotcatalog

import (
	"context"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type transactionIdentity struct {
	tx                        pgx.Tx
	conn                      *pgx.Conn
	pgconn                    *pgconn.PgConn
	pid, databaseOID, roleOID uint32
	database, role, xid       string
	backendStart              time.Time
	readOnly                  bool
}

func copyPins(p Pins) Pins {
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

func pinsEqual(a, b Pins) bool {
	if a.Mode != b.Mode || a.Database != b.Database || a.AppSchema != b.AppSchema || a.App != b.App || a.Projector != b.Projector ||
		(a.Worker == nil) != (b.Worker == nil) || (a.Sandbox == nil) != (b.Sandbox == nil) {
		return false
	}
	return (a.Worker == nil || *a.Worker == *b.Worker) && (a.Sandbox == nil || *a.Sandbox == *b.Sandbox)
}

func validName(s string) bool {
	return len(s) > 0 && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func validRole(p RolePin) bool {
	return validName(p.Name) && p.OID != 0 && !strings.HasPrefix(p.Name, "pg_")
}

func validSchema(p SchemaPin) bool {
	if !validName(p.Name) || p.OID == 0 || strings.HasPrefix(p.Name, "pg_") || p.Name == "information_schema" || !validRole(p.ObjectOwner) {
		return false
	}
	return p.NamespaceOwner == p.ObjectOwner || p.Name == "public" && p.NamespaceOwner == (RolePin{Name: "pg_database_owner", OID: 6171})
}

func validPins(p Pins) bool {
	if p.Database.Host == "" || len(p.Database.Host) > 253 || !utf8.ValidString(p.Database.Host) || strings.ContainsRune(p.Database.Host, 0) ||
		strings.HasPrefix(p.Database.Host, "/") || p.Database.Port == 0 || !validName(p.Database.Name) || p.Database.OID == 0 ||
		!validSchema(p.AppSchema) || !validRole(p.App) || !validRole(p.Projector) {
		return false
	}
	roles := []RolePin{p.AppSchema.ObjectOwner, p.App, p.Projector}
	schemas := []SchemaPin{p.AppSchema}
	switch p.Mode {
	case Ordinary:
		if p.Worker != nil || p.Sandbox != nil {
			return false
		}
	case Sandbox:
		if p.Worker == nil || p.Sandbox == nil || !validRole(*p.Worker) ||
			!validSchema(p.Sandbox.Mock) || !validSchema(p.Sandbox.Receiver) ||
			!validRole(p.Sandbox.MockRuntime) || !validRole(p.Sandbox.ReceiverRuntime) {
			return false
		}
		roles = append(roles, *p.Worker, p.Sandbox.Mock.ObjectOwner, p.Sandbox.MockRuntime, p.Sandbox.Receiver.ObjectOwner, p.Sandbox.ReceiverRuntime)
		schemas = append(schemas, p.Sandbox.Mock, p.Sandbox.Receiver)
	default:
		return false
	}
	for i, role := range roles {
		for j := 0; j < i; j++ {
			if role.Name == roles[j].Name || role.OID == roles[j].OID {
				return false
			}
		}
	}
	for i, schema := range schemas {
		if p.Mode == Sandbox && schema.NamespaceOwner != schema.ObjectOwner {
			return false
		}
		for j := 0; j < i; j++ {
			if schema.Name == schemas[j].Name || schema.OID == schemas[j].OID {
				return false
			}
		}
	}
	return true
}

func validTx(tx pgx.Tx) bool {
	if tx == nil {
		return false
	}
	v := reflect.ValueOf(tx)
	if !v.Type().Comparable() || v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	return tx.Conn() != nil && tx.Conn().PgConn() != nil && tx.Conn().PgConn().TxStatus() == 'T'
}

// Every called function, cast and operator is bound to pg_catalog before any
// untrusted schema is inspected. No caller-controlled identifier enters SQL.
const transactionSQL = `SELECT pg_catalog.current_database()::pg_catalog.text,d.oid,d.datdba,
 current_user::pg_catalog.text,session_user::pg_catalog.text,r.oid,r.rolcanlogin,
 pg_catalog.pg_backend_pid(),a.backend_start,pg_catalog.pg_current_xact_id()::pg_catalog.text,
 pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only'),
 pg_catalog.current_setting('search_path'),pg_catalog.current_schema()::pg_catalog.text,
 pg_catalog.host(pg_catalog.inet_server_addr()),pg_catalog.inet_server_port(),
 pg_catalog.current_setting('server_version_num')
 FROM pg_catalog.pg_database d JOIN pg_catalog.pg_roles r ON r.rolname OPERATOR(pg_catalog.=) current_user
 JOIN pg_catalog.pg_stat_activity a ON a.pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()
 WHERE d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`

func identifyTx(ctx context.Context, tx pgx.Tx, p Pins) (transactionIdentity, error) {
	if ctx == nil || ctx.Err() != nil || !validTx(tx) || !validPins(p) {
		return transactionIdentity{}, ErrCatalog
	}
	c := tx.Conn()
	cfg := c.Config()
	if cfg.Host != p.Database.Host || cfg.Port != p.Database.Port || cfg.Database != p.Database.Name || cfg.User == "" {
		return transactionIdentity{}, ErrCatalog
	}
	for _, f := range cfg.Fallbacks {
		if f == nil || f.Host != cfg.Host || f.Port != cfg.Port {
			return transactionIdentity{}, ErrCatalog
		}
	}
	remote, ok := c.PgConn().Conn().RemoteAddr().(*net.TCPAddr)
	if !ok || remote == nil || remote.Port != int(p.Database.Port) {
		return transactionIdentity{}, ErrCatalog
	}
	if literal := net.ParseIP(p.Database.Host); literal != nil && !literal.Equal(remote.IP) {
		return transactionIdentity{}, ErrCatalog
	}
	id := transactionIdentity{tx: tx, conn: c, pgconn: c.PgConn()}
	var databaseOwner uint32
	var session, isolation, access, path, schema, server, version string
	var login bool
	var port int32
	if tx.QueryRow(ctx, transactionSQL).Scan(&id.database, &id.databaseOID, &databaseOwner,
		&id.role, &session, &id.roleOID, &login, &id.pid, &id.backendStart, &id.xid,
		&isolation, &access, &path, &schema, &server, &port, &version) != nil {
		return transactionIdentity{}, ErrCatalog
	}
	xid, err := strconv.ParseUint(id.xid, 10, 64)
	major, versionErr := strconv.ParseUint(version, 10, 32)
	wantPath := []string{p.AppSchema.Name, "pg_catalog", "pg_temp"}
	gotPath, pathErr := identityParsePath(path)
	if !login || id.role != session || session != cfg.User || id.roleOID == 0 ||
		id.database != p.Database.Name || id.databaseOID != p.Database.OID ||
		id.pid == 0 || id.pid != c.PgConn().PID() || id.backendStart.IsZero() ||
		isolation != "repeatable read" || access != "on" && access != "off" ||
		err != nil || xid == 0 || strconv.FormatUint(xid, 10) != id.xid ||
		versionErr != nil || major/10000 != 16 || pathErr != nil || !reflect.DeepEqual(gotPath, wantPath) ||
		schema != p.AppSchema.Name || net.ParseIP(server) == nil || !remote.IP.Equal(net.ParseIP(server)) || port != int32(remote.Port) {
		return transactionIdentity{}, ErrCatalog
	}
	id.readOnly = access == "on"
	if p.AppSchema.Name == "public" && p.AppSchema.NamespaceOwner.Name == "pg_database_owner" && databaseOwner != p.AppSchema.ObjectOwner.OID {
		return transactionIdentity{}, ErrCatalog
	}
	roles := []RolePin{p.AppSchema.ObjectOwner, p.App, p.Projector}
	schemas := []SchemaPin{p.AppSchema}
	if p.Mode == Sandbox {
		roles = append(roles, *p.Worker, p.Sandbox.Mock.ObjectOwner, p.Sandbox.MockRuntime, p.Sandbox.Receiver.ObjectOwner, p.Sandbox.ReceiverRuntime)
		schemas = append(schemas, p.Sandbox.Mock, p.Sandbox.Receiver)
	}
	for _, role := range roles {
		var oid uint32
		if tx.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_roles WHERE rolname OPERATOR(pg_catalog.=) $1::pg_catalog.name`, role.Name).Scan(&oid) != nil || oid != role.OID {
			return transactionIdentity{}, ErrCatalog
		}
	}
	for _, schema := range schemas {
		var oid, owner uint32
		if tx.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname OPERATOR(pg_catalog.=) $1::pg_catalog.name`, schema.Name).Scan(&oid, &owner) != nil || oid != schema.OID || owner != schema.NamespaceOwner.OID {
			return transactionIdentity{}, ErrCatalog
		}
	}
	return id, nil
}

// This parser accepts identifier lists only; it does not interpret GUC SQL.
func identityParsePath(s string) ([]string, error) {
	var out []string
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, ErrCatalog
		}
		var word strings.Builder
		if s[0] == '"' {
			s = s[1:]
			closed := false
			for len(s) > 0 {
				c := s[0]
				s = s[1:]
				if c == '"' {
					if len(s) > 0 && s[0] == '"' {
						word.WriteByte('"')
						s = s[1:]
						continue
					}
					closed = true
					break
				}
				word.WriteByte(c)
			}
			if !closed || word.Len() == 0 {
				return nil, ErrCatalog
			}
		} else {
			i := 0
			for i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
				i++
			}
			if i == 0 {
				return nil, ErrCatalog
			}
			word.WriteString(s[:i])
			s = s[i:]
		}
		out = append(out, word.String())
		if len(out) > 3 {
			return nil, ErrCatalog
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return out, nil
		}
		if s[0] != ',' {
			return nil, ErrCatalog
		}
		s = s[1:]
	}
}

func (a transactionIdentity) same(b transactionIdentity) bool {
	return a == b
}
