// Package paymentsandboxqa installs isolated local QA environments offline.
// Production services must never import it or initialize their own resources.
package paymentsandboxqa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

var ErrEnvironment = errors.New("payment_qa_environment_invalid")

type Environment struct {
	Protocol                                    paymentproto.Environment
	APIOrigin, ControlOrigin, ReceiverOrigin    string
	MockManifestPath, ReceiverManifestPath      string
	Schemas                                     map[string]string
	Roles                                       map[string]string
	TenantID                                    string
	fundingDigest                               string
	adminDSN, directory, stateDirectoryID       string
	passwords                                   map[string]string
	api, control, mockNonce, receiverNonce, key [32]byte
	dbOID                                       uint32
	roleOIDs, schemaOIDs                        map[string]uint32
	createdRoles, createdSchemas                []string
	mu                                          sync.Mutex
	closed                                      bool
}

func (*Environment) String() string               { return "paymentsandboxqa.Environment(redacted)" }
func (e *Environment) GoString() string           { return e.String() }
func (*Environment) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

func stage(s string) error    { return fmt.Errorf("payment_qa_%s_failed", s) }
func ident(s string) string   { return pgx.Identifier{s}.Sanitize() }
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func fresh() [32]byte {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("payment_qa_entropy_failed")
	}
	return b
}
func encoded(b [32]byte) string { return base64.RawURLEncoding.EncodeToString(b[:]) }
func digest(domain string, b [32]byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(b[:])
	return hex.EncodeToString(h.Sum(nil))
}

// Create owns only the newly generated objects. Caller stops all services before Close.
func Create(ctx context.Context, adminDSN, privateRoot string) (_ *Environment, result error) {
	u, err := url.Parse(adminDSN)
	if err != nil || u.Scheme != "postgres" || u.User == nil || u.User.Username() != "moni_test" || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.RawQuery != "sslmode=disable" || u.Fragment != "" || os.Getenv("PGSERVICE") != "" {
		return nil, ErrEnvironment
	}
	if !filepath.IsAbs(privateRoot) || filepath.Clean(privateRoot) != privateRoot {
		return nil, ErrEnvironment
	}
	if err = os.MkdirAll(privateRoot, 0700); err != nil {
		return nil, stage("directory")
	}
	dir, err := os.MkdirTemp(privateRoot, "payment-qa-")
	if err != nil {
		return nil, stage("directory")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, stage("directory")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	e := &Environment{Protocol: paymentproto.Environment{ID: uuid.NewString(), MerchantID: "merchant:" + uuid.NewString(), PayeeLabel: "Local sandbox QA"}, adminDSN: adminDSN, directory: dir, stateDirectoryID: uuid.NewString(),
		Schemas: map[string]string{"app": "payment_sandbox_" + suffix, "mock": "payment_mock_" + suffix, "receiver": "payment_receiver_" + suffix}, Roles: map[string]string{}, passwords: map[string]string{}, roleOIDs: map[string]uint32{}, schemaOIDs: map[string]uint32{}, api: fresh(), control: fresh(), mockNonce: fresh(), receiverNonce: fresh(), key: fresh()}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = e.Close(cleanup)
		}
	}()
	ports := map[string]bool{}
	for _, target := range []*string{&e.APIOrigin, &e.ControlOrigin, &e.ReceiverOrigin} {
		for {
			l, x := net.Listen("tcp4", "127.0.0.1:0")
			if x != nil {
				return nil, stage("port")
			}
			a := l.Addr().String()
			_ = l.Close()
			if !ports[a] {
				ports[a] = true
				*target = "http://" + a
				break
			}
		}
	}
	p, err := e.adminPool(ctx)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	var current, session string
	var super bool
	if p.QueryRow(ctx, `SELECT current_user,session_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&current, &session, &super, &e.dbOID) != nil || current != "moni_test" || session != current || !super {
		return nil, ErrEnvironment
	}
	for _, r := range []string{"app_owner", "app_runtime", "app_projector", "mock_owner", "mock_runtime", "receiver_owner", "receiver_runtime"} {
		name := "pm_" + r + "_" + suffix
		e.Roles[r] = name
		options := " NOLOGIN"
		if !strings.HasSuffix(r, "_owner") {
			e.passwords[r] = encoded(fresh())
			options = " LOGIN PASSWORD " + literal(e.passwords[r])
		}
		if _, err = p.Exec(ctx, "CREATE ROLE "+ident(name)+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"+options); err != nil {
			return nil, stage("role")
		}
		e.createdRoles = append(e.createdRoles, name)
		var oid uint32
		if p.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", name).Scan(&oid) != nil {
			return nil, stage("role_pin")
		}
		e.roleOIDs[r] = oid
	}
	for _, c := range []string{"app", "mock", "receiver"} {
		if _, err = p.Exec(ctx, "CREATE SCHEMA "+ident(e.Schemas[c])+" AUTHORIZATION "+ident(e.Roles[c+"_owner"])); err != nil {
			return nil, stage("schema")
		}
		e.createdSchemas = append(e.createdSchemas, e.Schemas[c])
		var oid uint32
		if p.QueryRow(ctx, "SELECT oid FROM pg_namespace WHERE nspname=$1", e.Schemas[c]).Scan(&oid) != nil {
			return nil, stage("schema_pin")
		}
		e.schemaOIDs[c] = oid
	}
	if err = e.install(ctx); err != nil {
		return nil, err
	}
	if err = e.fund(ctx); err != nil {
		return nil, err
	}
	if err = e.writeManifests(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Environment) adminPool(ctx context.Context) (*pgxpool.Pool, error) {
	return e.pool(ctx, "", "", false)
}

func (e *Environment) pool(ctx context.Context, role, schema string, owner bool) (*pgxpool.Pool, error) {
	raw := e.adminDSN
	if role != "" && !owner {
		u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(e.Roles[role], e.passwords[role]), RawQuery: "sslmode=disable"}
		raw = u.String()
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, stage("config")
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams = map[string]string{"synchronous_commit": "on", "client_encoding": "UTF8"}
	if schema != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = ident(e.Schemas[schema]) + ",pg_catalog,pg_temp"
	}
	if owner {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, x := c.Exec(ctx, "SET ROLE "+ident(e.Roles[role]))
			return x
		}
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, stage("pool")
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, stage("connect")
	}
	return p, nil
}

// OpenPool is an offline QA capability; services must use attested paymentruntime.
// Keys are app/mock/receiver (LOGIN) or app_owner/mock_owner/receiver_owner.
func (e *Environment) OpenPool(ctx context.Context, component string) (*pgxpool.Pool, error) {
	switch component {
	case "app", "mock", "receiver":
		return e.pool(ctx, component+"_runtime", component, false)
	case "projector":
		return e.pool(ctx, "app_projector", "app", false)
	case "app_owner", "mock_owner", "receiver_owner":
		return e.pool(ctx, component, strings.TrimSuffix(component, "_owner"), true)
	default:
		return nil, ErrEnvironment
	}
}
func (e *Environment) APIToken() string     { return encoded(e.api) }
func (e *Environment) ControlToken() string { return encoded(e.control) }
func (e *Environment) Keyring() (*paymentproto.Keyring, error) {
	return paymentproto.NewKeyring("qa-key", []paymentproto.KeySpec{{ID: "qa-key", Key: e.key[:], NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}})
}

func (e *Environment) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	p, err := e.adminPool(ctx)
	if err != nil {
		return err
	}
	defer p.Close()
	for i := len(e.createdSchemas) - 1; i >= 0; i-- {
		if _, err = p.Exec(ctx, "DROP SCHEMA IF EXISTS "+ident(e.createdSchemas[i])+" CASCADE"); err != nil {
			return stage("cleanup_schema")
		}
	}
	for i := len(e.createdRoles) - 1; i >= 0; i-- {
		if _, err = p.Exec(ctx, "DROP ROLE IF EXISTS "+ident(e.createdRoles[i])); err != nil {
			return stage("cleanup_role")
		}
	}
	if err = os.RemoveAll(e.directory); err != nil {
		return stage("cleanup_files")
	}
	e.passwords = nil
	e.api = [32]byte{}
	e.control = [32]byte{}
	e.key = [32]byte{}
	e.mockNonce = [32]byte{}
	e.receiverNonce = [32]byte{}
	e.closed = true
	return nil
}
