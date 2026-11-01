package paymentruntime

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

func dsnConfig(c Config) (*pgxpool.Config, error) {
	// pgconn treats even service="" as service-file discovery. Reject an
	// ambient service before ParseConfig; never mutate global environment.
	if os.Getenv("PGSERVICE") != "" {
		return nil, ErrConfiguration
	}
	raw := c.dsn
	if raw == "" || len(raw) > 8<<10 || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, ErrConfiguration
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.EscapedPath() != "/moni_test" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrConfiguration
	}
	query, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return nil, ErrConfiguration
	}
	password, ok := u.User.Password()
	if !ok || password == "" || strings.ContainsAny(password, "\x00\r\n") || u.User.Username() != ownPin(c.manifest).Runtime.Name {
		return nil, ErrConfiguration
	}
	// Empty passfile/options override ambient discovery without mutating
	// process-global environment. The user's input cannot supply those settings.
	canonical := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(u.User.Username(), password)}
	canonical.RawQuery = url.Values{"sslmode": {"disable"}, "passfile": {""}, "options": {""}, "target_session_attrs": {"any"}, "connect_timeout": {"2"}}.Encode()
	cfg, e := pgxpool.ParseConfig(canonical.String())
	if e != nil {
		return nil, ErrConfiguration
	}
	cfg.ConnConfig.TLSConfig = nil
	cfg.ConnConfig.Fallbacks = nil
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{ownPin(c.manifest).Name}.Sanitize() + ",pg_catalog", "synchronous_commit": "on", "client_encoding": "UTF8"}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	cfg.ConnConfig.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		if host != "127.0.0.1" {
			return nil, ErrConfiguration
		}
		return []string{"127.0.0.1"}, nil
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "127.0.0.1:55439" {
			return nil, ErrConfiguration
		}
		return dialer.DialContext(ctx, "tcp", address)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	return cfg, nil
}
