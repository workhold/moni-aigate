package fundinglotruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// ApplicationState represents one complete local APP-file loading snapshot.
// It contains no key material or open file capability and says nothing about
// the current database, peer runtimes, or subsequent replacement of files.
// Its zero value is invalid; copies share immutable owned state.
type ApplicationState struct{ state *applicationState }

type applicationState struct {
	proof          *fundinglotproof.CanonicalV1
	appNonceDigest string
}

func (ApplicationState) String() string               { return "fundinglotruntime.ApplicationState(redacted)" }
func (p ApplicationState) GoString() string           { return p.String() }
func (ApplicationState) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

// The absolute deadline is checked independently of the context timer. This is
// a success-admission bound, not a promise to forcibly interrupt filesystem IO.
type applicationBudget struct {
	ctx      context.Context
	cancel   context.CancelFunc
	deadline time.Time
}

func newApplicationBudget(ctx context.Context) (applicationBudget, error) {
	if ctx == nil {
		return applicationBudget{}, ErrRuntime
	}
	b := applicationBudget{ctx: ctx, deadline: time.Now().Add(5 * time.Second)}
	if caller, ok := ctx.Deadline(); ok && caller.Before(b.deadline) {
		b.deadline = caller
	}
	b.ctx, b.cancel = context.WithDeadline(ctx, b.deadline)
	if b.check() != nil {
		b.cancel()
		return applicationBudget{}, ErrRuntime
	}
	return b, nil
}

func (b applicationBudget) check() error {
	if b.ctx == nil || b.ctx.Err() != nil || !time.Now().Before(b.deadline) {
		return ErrRuntime
	}
	return nil
}

// LoadSandboxApplicationState verifies the entire historical APP manifest,
// its private files and every signing key, then retains only an owned canonical
// proof and the verified APP nonce digest. All errors are deliberately opaque.
func LoadSandboxApplicationState(ctx context.Context, absoluteManifestPath string) (*ApplicationState, error) {
	b, err := newApplicationBudget(ctx)
	if err != nil {
		return nil, ErrRuntime
	}
	defer b.cancel()
	dir, name, err := applicationOpenPrivate(b, absoluteManifestPath)
	if err != nil {
		return nil, ErrRuntime
	}
	defer dir.close()
	raw, err := dir.read(b, name, 65536)
	if err != nil {
		return nil, ErrRuntime
	}
	var m applicationManifest
	if applicationStrictDecode(b, raw, &m) != nil || m.ManifestVersion != 1 || m.Component != "application" || b.check() != nil {
		return nil, ErrRuntime
	}
	proof, err := fundinglotproof.EncodeV1(m.Proof)
	if err != nil || b.check() != nil || proof.SHA256() != m.ProofSHA256 {
		return nil, ErrRuntime
	}
	if !applicationSecretName.MatchString(m.NonceFile.Name) || m.NonceFile.Name == name || b.check() != nil {
		return nil, ErrRuntime
	}
	nonceRaw, err := dir.secret(b, m.NonceFile, 44)
	if err != nil {
		return nil, ErrRuntime
	}
	nonce, err := applicationMaterial32(b, bytes.TrimSuffix(nonceRaw, []byte{'\n'}))
	if err != nil || b.check() != nil {
		return nil, ErrRuntime
	}
	if applicationDomainDigest("moni-payment-application-state-v1", nonce) != m.Proof.AppNonceDigest || b.check() != nil || len(m.Keys) != len(m.Proof.Keys) {
		return nil, ErrRuntime
	}
	seen := map[[32]byte]bool{nonce: true}
	keys := make([]paymentproto.KeySpec, 0, len(m.Keys))
	for i, material := range m.Keys {
		if b.check() != nil || len(material.MaterialBase64URL) != 43 {
			return nil, ErrRuntime
		}
		pin := m.Proof.Keys[i]
		value, err := applicationMaterial32(b, []byte(material.MaterialBase64URL))
		if err != nil || material.ID != pin.ID || seen[value] || b.check() != nil {
			return nil, ErrRuntime
		}
		if applicationDomainDigest("moni-mock-signing-key-v1", value) != pin.Digest || b.check() != nil {
			return nil, ErrRuntime
		}
		seen[value] = true
		before, err := time.Parse("2006-01-02T15:04:05.000000Z", pin.NotBefore)
		if err != nil || b.check() != nil {
			return nil, ErrRuntime
		}
		after, err := time.Parse("2006-01-02T15:04:05.000000Z", pin.NotAfter)
		if err != nil || b.check() != nil {
			return nil, ErrRuntime
		}
		keys = append(keys, paymentproto.KeySpec{ID: material.ID, Key: append([]byte(nil), value[:]...), NotBefore: before, NotAfter: after})
		if b.check() != nil {
			return nil, ErrRuntime
		}
	}
	// Preserve historical keyring qualification without retaining its secrets or
	// checking whether the keys are currently within their signing intervals.
	if b.check() != nil {
		return nil, ErrRuntime
	}
	if _, err := paymentproto.NewKeyring(m.Keys[0].ID, keys); err != nil || b.check() != nil {
		return nil, ErrRuntime
	}
	if dir.close() != nil {
		return nil, ErrRuntime
	}
	result := &ApplicationState{state: &applicationState{proof: proof, appNonceDigest: m.Proof.AppNonceDigest}}
	if b.check() != nil {
		return nil, ErrRuntime
	}
	return result, nil
}

func applicationMaterial32(b applicationBudget, raw []byte) ([32]byte, error) {
	var result [32]byte
	if b.check() != nil || len(raw) != 43 {
		return result, ErrRuntime
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(raw))
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != string(raw) || b.check() != nil {
		return result, ErrRuntime
	}
	copy(result[:], decoded)
	return result, nil
}

func applicationDomainDigest(domain string, value [32]byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(value[:])
	return hex.EncodeToString(h.Sum(nil))
}
