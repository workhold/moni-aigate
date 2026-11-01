package payments

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ApplicationPoolConfig(ctx context.Context, raw string, opts Options) (*pgxpool.Config, error) {
	if !opts.valid || opts.disabled || opts.component != applicationComponent {
		return nil, ErrPaymentsConfiguration
	}
	return runtimePoolConfig(ctx, raw, opts, opts.proof.App.Runtime)
}
func ProjectionPoolConfig(ctx context.Context, raw string, opts Options) (*pgxpool.Config, error) {
	if !opts.valid || opts.disabled || opts.component != applicationComponent {
		return nil, ErrPaymentsConfiguration
	}
	return runtimePoolConfig(ctx, raw, opts, opts.proof.Projector)
}
func workerPoolConfig(ctx context.Context, opts Options) (*pgxpool.Config, error) {
	if !opts.valid || opts.disabled || !opts.bound || opts.component != workerComponent {
		return nil, ErrPaymentsConfiguration
	}
	return runtimePoolConfig(ctx, opts.workerDSN, opts, opts.proof.Worker)
}

// Lexical validation precedes pgx parsing, so service discovery and ambient
// target settings cannot cause a connection before the sandbox target is known.
func runtimePoolConfig(ctx context.Context, raw string, o Options, role runtimeRolePin) (*pgxpool.Config, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if os.Getenv("PGSERVICE") != "" || raw == "" || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00#") {
		return nil, ErrPaymentsConfiguration
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.RawPath != "" || u.EscapedPath() != "/moni_test" || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" {
		return nil, ErrPaymentsConfiguration
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) != 1 || len(q["sslmode"]) != 1 || q.Get("sslmode") != "disable" {
		return nil, ErrPaymentsConfiguration
	}
	password, ok := u.User.Password()
	if !ok || password == "" || strings.ContainsAny(password, "\x00\r\n") || u.User.Username() != role.Name || !runtimeRoleName.MatchString(role.Name) || role.OID == 0 || !role.Login {
		return nil, ErrPaymentsConfiguration
	}
	canonical := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(role.Name, password)}
	canonical.RawQuery = url.Values{"sslmode": {"disable"}, "passfile": {""}, "options": {""}, "target_session_attrs": {"any"}, "connect_timeout": {"2"}}.Encode()
	cfg, e := pgxpool.ParseConfig(canonical.String())
	if e != nil {
		return nil, ErrPaymentsConfiguration
	}
	cfg.ConnConfig.TLSConfig = nil
	cfg.ConnConfig.Fallbacks = nil
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{o.proof.App.Name}.Sanitize() + ",pg_catalog,pg_temp", "synchronous_commit": "on", "client_encoding": "UTF8"}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	cfg.ConnConfig.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		if host != "127.0.0.1" {
			return nil, ErrPaymentsConfiguration
		}
		return []string{"127.0.0.1"}, nil
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "127.0.0.1:55439" {
			return nil, ErrPaymentsConfiguration
		}
		return dialer.DialContext(ctx, "tcp", address)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error { return runtimePinConnection(ctx, conn, o, role) }
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return cfg, nil
}

func runtimePinConnection(ctx context.Context, conn *pgx.Conn, o Options, role runtimeRolePin) error {
	var database, current, session, address, encoding, synchronous, schema string
	var databaseOID, schemaOID, currentOID uint32
	var port int
	e := conn.QueryRow(ctx, `SELECT current_database(),current_user,session_user,
 (SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),host(inet_server_addr()),inet_server_port(),
 current_setting('server_encoding'),current_setting('synchronous_commit'),current_schema(),
 (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=current_schema()),
 (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)`).Scan(&database, &current, &session, &databaseOID, &address, &port, &encoding, &synchronous, &schema, &schemaOID, &currentOID)
	p := o.proof
	if e != nil || database != p.Database.Name || databaseOID != p.Database.OID || current != role.Name || session != current || currentOID != role.OID || address != p.Database.Host || port != int(p.Database.Port) || encoding != "UTF8" || synchronous != "on" || schema != p.App.Name || schemaOID != p.App.OID {
		return ErrPaymentsEnvironment
	}
	return nil
}
