package payments

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModeSandbox Mode = "sandbox"
)

type Lifecycle struct {
	Mode     Mode
	Recovery bool
	Guard    func(context.Context) error
}

var (
	ErrPaymentsConfiguration   = errors.New("payments_configuration_invalid")
	ErrPaymentsEnvironment     = errors.New("payments_environment_invalid")
	ErrPaymentsLifecycleClosed = errors.New("payments_lifecycle_closed")
	ErrPaymentsRecovery        = errors.New("payments_recovery")
	ErrPaymentsGuard           = errors.New("payments_guard_unavailable")
	ErrPaymentsInvalidWork     = errors.New("payments_invalid_work")
	ErrPaymentsLeaseLost       = errors.New("payments_lease_lost")
	ErrPaymentsIntegrity       = errors.New("payments_integrity")
	ErrPaymentsUnavailable     = errors.New("payment_unavailable")
	ErrPaymentsOutcomeUnknown  = errors.New("payment_outcome_unknown")
	ErrPaymentsProtocol        = errors.New("payments_protocol_invalid")
	ErrPaymentsQueryConflict   = errors.New("payments_query_conflict")
	errSandboxTimeout          = errors.New("sandbox_timeout")
	errSandboxNetwork          = errors.New("sandbox_network")
)

type runtimeComponent uint8

const (
	applicationComponent runtimeComponent = iota + 1
	workerComponent
)

type runtimeRolePin struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type runtimeSchemaPin struct {
	Name    string         `json:"name"`
	OID     uint32         `json:"oid"`
	Owner   runtimeRolePin `json:"owner"`
	Runtime runtimeRolePin `json:"runtime"`
}
type runtimeDatabasePin struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type runtimeKeyPin struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}
type runtimeProof struct {
	ProofVersion      int                `json:"proof_version"`
	ProtocolVersion   int                `json:"protocol_version"`
	EnvironmentID     string             `json:"environment_id"`
	MerchantID        string             `json:"merchant_id"`
	PayeeLabel        string             `json:"payee_label"`
	Database          runtimeDatabasePin `json:"database"`
	App               runtimeSchemaPin   `json:"app"`
	Mock              runtimeSchemaPin   `json:"mock"`
	Receiver          runtimeSchemaPin   `json:"receiver"`
	Worker            runtimeRolePin     `json:"worker"`
	Projector         runtimeRolePin     `json:"projector"`
	AppNonceDigest    string             `json:"app_nonce_digest"`
	WorkerNonceDigest string             `json:"worker_nonce_digest"`
	APITokenDigest    string             `json:"api_token_digest"`
	APIOrigin         string             `json:"api_origin"`
	ControlOrigin     string             `json:"control_origin"`
	AppListenOrigin   string             `json:"app_listen_origin"`
	CallbackURL       string             `json:"callback_url"`
	Keys              []runtimeKeyPin    `json:"keys"`
}
type runtimeSecretFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type runtimeMaterial struct {
	ID                string `json:"id"`
	MaterialBase64URL string `json:"material_base64_url"`
}
type runtimeManifest struct {
	ManifestVersion int               `json:"manifest_version"`
	Component       string            `json:"component"`
	Proof           runtimeProof      `json:"proof"`
	ProofSHA256     string            `json:"proof_sha256"`
	NonceFile       runtimeSecretFile `json:"nonce_file"`
	Keys            []runtimeMaterial `json:"keys"`
}
type runtimeWorkerManifest struct {
	runtimeManifest
	RuntimeDSNFile runtimeSecretFile `json:"runtime_dsn_file"`
	APITokenFile   runtimeSecretFile `json:"api_token_file"`
}

// Options can only be made by a private-file loader or DisabledOptions. Binding
// returns an immutable snapshot; copying an unbound value is explicitly legal.
type Options struct {
	valid, disabled, bound bool
	component              runtimeComponent
	proof                  runtimeProof
	proofBytes             []byte
	keyring                *paymentproto.Keyring
	keyMaterials           [][32]byte
	nonceDigest            string
	apiToken               [32]byte
	workerDSN              string
	lifecycle              Lifecycle
}

func (Options) String() string               { return "payments.Options(redacted)" }
func (o Options) GoString() string           { return o.String() }
func (Options) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }
func DisabledOptions() Options {
	return Options{valid: true, disabled: true, component: applicationComponent}
}
func (o Options) BindLifecycle(l Lifecycle) (Options, error) {
	if !o.valid || o.bound || l.Guard == nil {
		return Options{}, ErrPaymentsConfiguration
	}
	if l.Mode == "" {
		l.Mode = ModeOff
	}
	if l.Mode != ModeOff && l.Mode != ModeSandbox || o.disabled && l.Mode != ModeOff {
		return Options{}, ErrPaymentsConfiguration
	}
	o.lifecycle = l
	o.bound = true
	return o, nil
}
func ValidateOptionsPair(a, w Options) error {
	if !a.valid || !w.valid || a.disabled || w.disabled || a.component != applicationComponent || w.component != workerComponent || a.bound != w.bound || !bytes.Equal(a.proofBytes, w.proofBytes) || len(a.keyMaterials) != len(w.keyMaterials) {
		return ErrPaymentsConfiguration
	}
	if a.bound && (a.lifecycle.Mode != w.lifecycle.Mode || a.lifecycle.Recovery != w.lifecycle.Recovery) {
		return ErrPaymentsConfiguration
	}
	for i := range a.keyMaterials {
		if subtle.ConstantTimeCompare(a.keyMaterials[i][:], w.keyMaterials[i][:]) != 1 {
			return ErrPaymentsConfiguration
		}
	}
	return nil
}

type paymentRuntime struct {
	pool    *pgxpool.Pool
	options Options
	fenced  atomic.Bool
}

func (r *paymentRuntime) String() string   { return "payments.runtime(redacted)" }
func (r *paymentRuntime) GoString() string { return r.String() }
func (*paymentRuntime) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Redacted bool `json:"redacted"`
	}{true})
}
func (r *paymentRuntime) mode() Mode {
	if r == nil {
		return ModeOff
	}
	return r.options.lifecycle.Mode
}
func (r *paymentRuntime) recovery() bool { return r != nil && r.options.lifecycle.Recovery }
func (r *paymentRuntime) hasEnvironment() bool {
	return r != nil && r.options.valid && !r.options.disabled
}
func (r *paymentRuntime) environment() paymentproto.Environment {
	if !r.hasEnvironment() {
		return paymentproto.Environment{}
	}
	p := r.options.proof
	return paymentproto.Environment{ID: p.EnvironmentID, MerchantID: p.MerchantID, PayeeLabel: p.PayeeLabel}
}
