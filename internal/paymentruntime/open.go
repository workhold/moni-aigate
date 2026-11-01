package paymentruntime

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pinConnection(ctx context.Context, conn *pgx.Conn, c Config) error {
	var database, user, session, address, encoding, syncCommit string
	var oid uint32
	var port int
	e := conn.QueryRow(ctx, `SELECT current_database(),current_user,session_user,
 (SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),
 host(inet_server_addr()),inet_server_port(),current_setting('server_encoding'),current_setting('synchronous_commit')`).Scan(&database, &user, &session, &oid, &address, &port, &encoding, &syncCommit)
	own := ownPin(c.manifest)
	if e != nil || database != "moni_test" || oid != c.manifest.Database.OID || user != own.Runtime.Name || session != user || address != "127.0.0.1" || port != 55439 || encoding != "UTF8" || syncCommit != "on" {
		return ErrEnvironment
	}
	return nil
}

// Open performs the complete startup proof without DDL, listeners, or workers.
// The supplied context bounds startup only; Done controls the opened lifetime.
func Open(ctx context.Context, c Config) (*Runtime, error) {
	if !c.valid {
		return nil, ErrConfiguration
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cfg, e := dsnConfig(c)
	if e != nil {
		return nil, e
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error { return pinConnection(ctx, conn, c) }
	p, e := pgxpool.NewWithConfig(startup, cfg)
	if e != nil {
		return nil, ErrEnvironment
	}
	r := &Runtime{config: c, pool: p, done: make(chan struct{}), closeDone: make(chan struct{})}
	success := false
	defer func() {
		if !success {
			r.stop()
			if r.ownership != nil {
				_ = r.ownership.close()
			}
			p.Close()
		}
	}()
	if e = attestCatalog(startup, p, c); e != nil {
		return nil, e
	}
	r.ownership, e = acquireOwner(startup, p, c, r.stop)
	if e != nil {
		return nil, e
	}
	if e = attestState(startup, p, c); e != nil {
		return nil, e
	}
	if e = r.Check(startup); e != nil {
		return nil, e
	}
	success = true
	return r, nil
}

// Close is idempotent. Components must first drain their HTTP/transaction/IO
// work; returning pool connections is the borrowing caller's responsibility.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.done == nil || r.closeDone == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.stop()
		go func() {
			if r.ownership != nil {
				r.closeErr = r.ownership.close()
			}
			if r.pool != nil {
				r.pool.Close()
			}
			close(r.closeDone)
		}()
	})
	select {
	case <-r.closeDone:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
