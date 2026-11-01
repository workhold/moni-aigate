package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

func databaseConfig(raw string) (*pgxpool.Config, error) {
	u, e := url.Parse(raw)
	if e != nil || raw == "" || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.Fragment != "" {
		return nil, errStage
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return nil, errStage
	}
	for k, vs := range q {
		if k != "sslmode" || len(vs) != 1 || vs[0] != "disable" {
			return nil, errStage
		}
	}
	c, e := pgxpool.ParseConfig(raw)
	if e != nil {
		return nil, errStage
	}
	if c.ConnConfig.Host != "127.0.0.1" || c.ConnConfig.Port != 55439 || c.ConnConfig.Database != "moni_test" || c.ConnConfig.User != "moni_test" || len(c.ConnConfig.Fallbacks) != 0 || c.ConnConfig.TLSConfig != nil {
		return nil, errStage
	}
	c.ConnConfig.RuntimeParams = map[string]string{"search_path": "pg_catalog", "application_name": "c0_descriptor_candidate"}
	c.MaxConns = 1
	c.MinConns = 0
	c.ConnConfig.ConnectTimeout = 3 * time.Second
	return c, nil
}

func verifyDatabase(ctx context.Context, p *pgxpool.Pool) (int, error) {
	var version int
	var valid bool
	e := p.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,
 current_user=session_user AND current_user='moni_test' AND current_database()='moni_test'
 AND host(inet_server_addr())='127.0.0.1' AND inet_server_port()=55439
 AND r.rolcanlogin AND r.rolsuper AND d.datdba=r.oid
 FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&version, &valid)
	if e != nil || !valid || version/10000 != 16 {
		return 0, errStage
	}
	return version, nil
}

func randomProfile(sandbox bool) (*profile, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, errStage
	}
	prefix := "flcdg_" + hex.EncodeToString(b[:])
	return &profile{sandbox: sandbox, schema: prefix + "_s", owner: prefix + "_o", app: prefix + "_a", worker: prefix + "_w", projection: prefix + "_p", roleOIDs: map[string]uint32{}}, nil
}

func (p *profile) cleanup(ctx context.Context, admin *pgxpool.Pool) error {
	p.backendMu.Lock()
	backends := append([]backendIdentity(nil), p.closedBackends...)
	p.backendMu.Unlock()
	for _, b := range backends {
		for {
			var present bool
			if admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND application_name=$3 AND usename='moni_test' AND datname='moni_test')`, b.pid, b.started, b.application).Scan(&present) != nil {
				return errStage
			}
			if !present {
				break
			}
			// Only a precisely pinned backend whose connection this run has
			// already closed is eligible; never another test or current admin.
			if _, e := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND application_name=$3 AND usename='moni_test' AND datname='moni_test' AND pid<>pg_backend_pid()`, b.pid, b.started, b.application); e != nil {
				return errStage
			}
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errStage
			case <-timer.C:
			}
		}
	}
	if p.schemaCreated {
		if _, e := admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{p.schema}.Sanitize()+" CASCADE"); e != nil {
			return errStage
		}
	}
	for i := len(p.createdRoles) - 1; i >= 0; i-- {
		if _, e := admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{p.createdRoles[i]}.Sanitize()); e != nil {
			return errStage
		}
	}
	var gone bool
	name := ""
	if p.schemaCreated {
		name = p.schema
	}
	if admin.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=ANY($2::text[]))`, name, p.createdRoles).Scan(&gone) != nil || !gone {
		return errStage
	}
	return nil
}

func (p *profile) cleanupContext() (context.Context, context.CancelFunc) {
	p.backendMu.Lock()
	if p.cleanupDeadline.IsZero() {
		p.cleanupDeadline = time.Now().Add(10 * time.Second)
	}
	deadline := p.cleanupDeadline
	p.backendMu.Unlock()
	return context.WithDeadline(context.Background(), deadline)
}

func (p *profile) createObjects(ctx context.Context, admin *pgxpool.Pool) error {
	c, e := admin.Acquire(ctx)
	if e != nil {
		return errStage
	}
	var b backendIdentity
	b.application = "c0_descriptor_candidate"
	var tx pgx.Tx
	confirmed := false
	defer func() {
		if confirmed {
			c.Release()
			return
		}
		shared, sharedCancel := p.cleanupContext()
		defer sharedCancel()
		cleanup, cancel := context.WithTimeout(shared, 5*time.Second)
		defer cancel()
		if tx != nil {
			_ = tx.Rollback(cleanup)
		}
		raw := c.Hijack()
		_ = raw.Close(cleanup)
		if b.pid != 0 {
			p.backendMu.Lock()
			p.closedBackends = append(p.closedBackends, b)
			p.backendMu.Unlock()
		}
	}()
	if c.QueryRow(ctx, `SELECT pg_backend_pid(),backend_start FROM pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&b.pid, &b.started) != nil {
		return errStage
	}
	var absent bool
	names := []string{p.owner, p.app, p.worker, p.projection}
	if c.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$2)`, names, p.schema).Scan(&absent) != nil || !absent {
		return errStage
	}
	tx, e = c.Begin(ctx)
	if e != nil {
		return errStage
	}
	// Record intent before any CREATE. A lost success reply must not erase our
	// exact cleanup names; names were cryptographically chosen and proved absent.
	p.createdRoles = append([]string(nil), names...)
	p.schemaCreated = true
	for _, r := range []struct {
		name  string
		login bool
	}{{p.owner, false}, {p.app, true}, {p.worker, true}, {p.projection, true}} {
		flags := " NOLOGIN"
		if r.login {
			flags = " LOGIN"
		}
		if _, e = tx.Exec(ctx, "CREATE ROLE "+pgx.Identifier{r.name}.Sanitize()+flags+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); e != nil {
			return errStage
		}
		var oid uint32
		if tx.QueryRow(ctx, `SELECT oid FROM pg_roles WHERE rolname=$1`, r.name).Scan(&oid) != nil {
			return errStage
		}
		p.roleOIDs[r.name] = oid
	}
	if _, e = tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{p.schema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{p.owner}.Sanitize()); e != nil {
		return errStage
	}
	if tx.QueryRow(ctx, `SELECT oid FROM pg_namespace WHERE nspname=$1`, p.schema).Scan(&p.schemaOID) != nil {
		return errStage
	}
	if tx.Commit(ctx) != nil {
		return errStage
	}
	confirmed = true
	return nil
}

func installProfile(ctx context.Context, admin *pgxpool.Pool, cfg *pgxpool.Config, in inputs, sandbox bool) (result *profile, err error) {
	if ctx.Err() != nil {
		return nil, errStage
	}
	p, e := randomProfile(sandbox)
	if e != nil {
		return nil, e
	}
	defer func() {
		cleanup, cancel := p.cleanupContext()
		defer cancel()
		if p.cleanup(cleanup, admin) != nil {
			result = nil
			err = errStage
		}
	}()
	if p.createObjects(ctx, admin) != nil {
		return nil, errStage
	}
	cc := cfg.Copy()
	cc.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{p.schema}.Sanitize() + ",pg_catalog,pg_temp", "application_name": "c0_descriptor_owner"}
	cc.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		var b backendIdentity
		b.application = "c0_descriptor_owner"
		b.connection = c
		if c.QueryRow(ctx, `SELECT pg_backend_pid(),backend_start FROM pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&b.pid, &b.started) != nil {
			return errStage
		}
		p.backendMu.Lock()
		p.closedBackends = append(p.closedBackends, b)
		p.backendMu.Unlock()
		_, e := c.Exec(ctx, "SET ROLE "+pgx.Identifier{p.owner}.Sanitize())
		return e
	}
	own, e := pgxpool.NewWithConfig(ctx, cc)
	if e != nil {
		return nil, errStage
	}
	defer func() {
		cleanup, cancel := p.cleanupContext()
		defer cancel()
		p.backendMu.Lock()
		backends := append([]backendIdentity(nil), p.closedBackends...)
		p.backendMu.Unlock()
		for _, b := range backends {
			if b.connection != nil {
				_ = b.connection.Close(cleanup)
			}
		}
		own.Close()
	}()
	if migrations.Apply(ctx, own) != nil {
		return nil, errStage
	}
	if operations.ProvisionFor(ctx, own, p.schema, p.app, p.projection) != nil {
		return nil, errStage
	}
	if sandbox {
		if _, e = own.Exec(ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) VALUES('11111111-1111-4111-8111-111111111111','descriptor-fixture','descriptor-fixture',(SELECT oid FROM pg_database WHERE datname=current_database()),$1,repeat('0',64),$2,$3,$4)`, p.schemaOID, p.app, p.worker, p.projection); e != nil {
			return nil, errStage
		}
	}
	if sandbox && operations.ProvisionPaymentWorkerFor(ctx, own, p.schema, p.app, p.worker, p.projection) != nil {
		return nil, errStage
	}
	if p.base, p.baseRoles, p.baseIDs, e = snapshot(ctx, own, p); e != nil {
		return nil, e
	}
	if _, e = own.Exec(ctx, string(in.ddl)); e != nil {
		return nil, errStage
	}
	acl := string(in.acl)
	for _, v := range []struct{ key, value string }{{"fl_schema", p.schema}, {"fl_app", p.app}, {"fl_worker", p.worker}, {"fl_projector", p.projection}} {
		acl = strings.ReplaceAll(acl, `:"`+v.key+`"`, pgx.Identifier{v.value}.Sanitize())
	}
	if strings.Contains(acl, `:"`) {
		return nil, errStage
	}
	if _, e = own.Exec(ctx, acl); e != nil {
		return nil, errStage
	}
	var ids map[string]string
	if p.after, p.afterRoles, ids, e = snapshot(ctx, own, p); e != nil {
		return nil, e
	}
	for k, v := range p.baseIDs {
		if ids[k] != v {
			return nil, errStage
		}
	}
	return p, nil
}
