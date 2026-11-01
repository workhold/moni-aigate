// Package paymentruntime proves the identity and isolation of the local payment
// sandbox before either service opens a listener. It never installs schema/roles.
package paymentruntime

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type Component string

const (
	Mock     Component = "mock"
	Receiver Component = "receiver"
)

type Endpoint string

const (
	API          Endpoint = "api"
	Control      Endpoint = "control"
	ReceiverHTTP Endpoint = "receiver"
)

type TokenKind string

const (
	APIToken     TokenKind = "api"
	ControlToken TokenKind = "control"
)

var (
	ErrConfiguration = errors.New("mock_configuration_invalid")
	ErrEnvironment   = errors.New("mock_environment_invalid")
	ErrState         = errors.New("mock_state_missing")
	ErrRole          = errors.New("mock_role_invalid")
	ErrOwnershipLost = errors.New("mock_instance_unavailable")
)

type rolePin struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type schemaPin struct {
	Name    string  `json:"name"`
	OID     uint32  `json:"oid"`
	Owner   rolePin `json:"owner"`
	Runtime rolePin `json:"runtime"`
}
type databasePin struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type sqlPin struct {
	SchemaVersion int    `json:"schema_version"`
	SHA256        string `json:"sha256"`
}
type secretFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type keyMaterial struct {
	ID                string `json:"id"`
	MaterialBase64URL string `json:"material_base64_url"`
	Digest            string `json:"digest"`
	NotBefore         string `json:"not_before"`
	NotAfter          string `json:"not_after"`
}
type commonManifest struct {
	ManifestVersion  int           `json:"manifest_version"`
	ProtocolVersion  int           `json:"protocol_version"`
	Component        Component     `json:"component"`
	EnvironmentID    string        `json:"environment_id"`
	StateDirectoryID string        `json:"state_directory_id"`
	MerchantID       string        `json:"merchant_id"`
	PayeeLabel       string        `json:"payee_label"`
	Database         databasePin   `json:"database"`
	App              schemaPin     `json:"app"`
	Mock             schemaPin     `json:"mock"`
	Receiver         schemaPin     `json:"receiver"`
	AppProjector     rolePin       `json:"app_projector"`
	OwnSchemaSQL     sqlPin        `json:"own_schema_sql"`
	RuntimeDSNFile   secretFile    `json:"runtime_dsn_file"`
	NonceFile        secretFile    `json:"nonce_file"`
	CallbackURL      string        `json:"callback_url"`
	ActiveKeyID      string        `json:"active_key_id"`
	Keys             []keyMaterial `json:"keys"`
}
type mockManifest struct {
	commonManifest
	APIOrigin        string     `json:"api_origin"`
	ControlOrigin    string     `json:"control_origin"`
	APITokenFile     secretFile `json:"api_token_file"`
	ControlTokenFile secretFile `json:"control_token_file"`
}
type receiverManifest struct {
	commonManifest
	ListenOrigin string `json:"listen_origin"`
}
type keyWindow struct{ before, after time.Time }

// Config can only be constructed by Load. Its private fields must not be logged.
type Config struct {
	valid                                 bool
	manifest                              commonManifest
	origins                               map[Endpoint]string
	dsn                                   string
	nonceDigest, apiDigest, controlDigest string
	api, control                          [32]byte
	keys                                  *paymentproto.Keyring
	windows                               map[string]keyWindow
}

func (c Config) String() string {
	return "paymentruntime.Config(" + string(c.manifest.Component) + ",redacted)"
}
func (c Config) GoString() string { return c.String() }
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Component Component `json:"component"`
		Redacted  bool      `json:"redacted"`
	}{c.manifest.Component, true})
}

type Runtime struct {
	config    Config
	pool      *pgxpool.Pool
	ownership *ownership
	done      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

func (r *Runtime) String() string               { return "paymentruntime.Runtime(redacted)" }
func (r *Runtime) GoString() string             { return r.String() }
func (r *Runtime) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

// Pool is for reviewed in-process assembly, not a public configuration API. The
// pgx API can inspect its own connection configuration; it is not a secret vault.
func (r *Runtime) Pool() *pgxpool.Pool {
	if r == nil {
		return nil
	}
	return r.pool
}
func (r *Runtime) Component() Component {
	if r == nil {
		return ""
	}
	return r.config.manifest.Component
}
func (r *Runtime) Environment() paymentproto.Environment {
	if r == nil || !r.config.valid {
		return paymentproto.Environment{}
	}
	m := r.config.manifest
	return paymentproto.Environment{ID: m.EnvironmentID, MerchantID: m.MerchantID, PayeeLabel: m.PayeeLabel}
}
func (r *Runtime) Keyring() *paymentproto.Keyring {
	if r == nil {
		return nil
	}
	return r.config.keys
}
func (r *Runtime) ActiveKeyID() string {
	if r == nil || r.config.keys == nil {
		return ""
	}
	return r.config.keys.ActiveID()
}
func (r *Runtime) KeyWindow(id string) (time.Time, time.Time, bool) {
	if r == nil {
		return time.Time{}, time.Time{}, false
	}
	w, ok := r.config.windows[id]
	return w.before, w.after, ok
}
func (r *Runtime) Origin(e Endpoint) (string, bool) {
	if r == nil {
		return "", false
	}
	v, ok := r.config.origins[e]
	return v, ok
}
func (r *Runtime) CallbackURL() string {
	if r == nil {
		return ""
	}
	return r.config.manifest.CallbackURL
}
func (r *Runtime) TokenMatches(kind TokenKind, presented string) bool {
	if r == nil || !r.config.valid || r.Component() != Mock {
		return false
	}
	decoded, e := base64.RawURLEncoding.DecodeString(presented)
	if e != nil || len(decoded) != 32 || len(presented) != 43 || base64.RawURLEncoding.EncodeToString(decoded) != presented {
		return false
	}
	var expected [32]byte
	switch kind {
	case APIToken:
		expected = r.config.api
	case ControlToken:
		expected = r.config.control
	default:
		return false
	}
	return subtle.ConstantTimeCompare(decoded, expected[:]) == 1
}
func (r *Runtime) Done() <-chan struct{} {
	if r == nil || r.done == nil {
		c := make(chan struct{})
		close(c)
		return c
	}
	return r.done
}
func (r *Runtime) stop() { r.stopOnce.Do(func() { close(r.done) }) }
func (r *Runtime) Check(ctx context.Context) error {
	if r == nil || r.ownership == nil {
		return ErrOwnershipLost
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-r.done:
		return ErrOwnershipLost
	default:
	}
	if e := r.ownership.check(ctx); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.stop()
		return ErrOwnershipLost
	}
	return nil
}
