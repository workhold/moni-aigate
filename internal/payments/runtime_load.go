package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

var runtimeRoleName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
var runtimeKeyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func runtimeUUID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}
func runtimeLabel(s string) bool {
	if len(s) == 0 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func runtimeRef(s string) bool {
	if !runtimeLabel(s) {
		return false
	}
	v := strings.ToLower(s)
	if strings.HasPrefix(v, "sk-") || strings.HasPrefix(v, "mg_") || strings.HasPrefix(v, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func runtimeInstant(s string) (time.Time, error) {
	t, e := time.Parse("2006-01-02T15:04:05.000000Z", s)
	if e != nil || t.IsZero() || t.Year() < 1 || t.Year() > 9999 || t.UTC().Format("2006-01-02T15:04:05.000000Z") != s {
		return time.Time{}, ErrPaymentsConfiguration
	}
	return t.UTC(), nil
}
func runtimeOrigin(raw string, callback bool) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || strings.Contains(raw, "#") || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", ErrPaymentsConfiguration
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.ParseUint(port, 10, 16)
	if e != nil || pe != nil || n == 0 || host != "127.0.0.1" || port != strconv.FormatUint(n, 10) || u.Host != net.JoinHostPort(host, port) {
		return "", ErrPaymentsConfiguration
	}
	if callback {
		if u.Path != "/api/payments/sandbox/notifications" {
			return "", ErrPaymentsConfiguration
		}
	} else if u.Path != "" {
		return "", ErrPaymentsConfiguration
	}
	return "http://" + u.Host, nil
}
func runtimeMaterial32(raw []byte) ([32]byte, error) {
	var value [32]byte
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	v, e := base64.RawURLEncoding.DecodeString(string(raw))
	if e != nil || len(raw) != 43 || len(v) != 32 || base64.RawURLEncoding.EncodeToString(v) != string(raw) {
		return value, ErrPaymentsConfiguration
	}
	copy(value[:], v)
	return value, nil
}
func runtimeDomainDigest(domain string, v [32]byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(v[:])
	return hex.EncodeToString(h.Sum(nil))
}
func runtimeSHA(raw []byte) string    { return fmtRuntimeSHA(sha256.Sum256(raw)) }
func fmtRuntimeSHA(v [32]byte) string { return hex.EncodeToString(v[:]) }
func runtimeCanonicalProof(p runtimeProof) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(p) != nil {
		return nil, ErrPaymentsConfiguration
	}
	raw := b.Bytes()
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, ErrPaymentsConfiguration
	}
	raw = raw[:len(raw)-1]
	if len(raw) > 65536 {
		return nil, ErrPaymentsConfiguration
	}
	return append([]byte(nil), raw...), nil
}
func runtimeRoles(p runtimeProof) []runtimeRolePin {
	return []runtimeRolePin{p.App.Owner, p.App.Runtime, p.Mock.Owner, p.Mock.Runtime, p.Receiver.Owner, p.Receiver.Runtime, p.Worker, p.Projector}
}
func runtimeValidateProof(p runtimeProof) error {
	if p.ProofVersion != 1 || p.ProtocolVersion != 1 || !runtimeUUID(p.EnvironmentID) || !runtimeRef(p.MerchantID) || !runtimeLabel(p.PayeeLabel) || p.Database.Host != "127.0.0.1" || p.Database.Port != 55439 || p.Database.Name != "moni_test" || p.Database.OID == 0 {
		return ErrPaymentsConfiguration
	}
	for _, d := range []string{p.AppNonceDigest, p.WorkerNonceDigest, p.APITokenDigest} {
		if !runtimeHashText.MatchString(d) {
			return ErrPaymentsConfiguration
		}
	}
	schemas := map[uint32]bool{}
	suffix := ""
	for i, s := range []runtimeSchemaPin{p.App, p.Mock, p.Receiver} {
		prefix := []string{"payment_sandbox_", "payment_mock_", "payment_receiver_"}[i]
		if !regexp.MustCompile(`^`+prefix+`[0-9a-f]{32}$`).MatchString(s.Name) || s.OID == 0 || schemas[s.OID] || s.Owner.Login || !s.Runtime.Login {
			return ErrPaymentsConfiguration
		}
		part := strings.TrimPrefix(s.Name, prefix)
		if suffix != "" && part != suffix {
			return ErrPaymentsConfiguration
		}
		suffix = part
		schemas[s.OID] = true
	}
	ids := map[uint32]bool{}
	names := map[string]bool{}
	for _, r := range runtimeRoles(p) {
		if !runtimeRoleName.MatchString(r.Name) || r.OID == 0 || ids[r.OID] || names[r.Name] {
			return ErrPaymentsConfiguration
		}
		ids[r.OID] = true
		names[r.Name] = true
	}
	if !p.Worker.Login || !p.Projector.Login {
		return ErrPaymentsConfiguration
	}
	origins := map[string]bool{}
	for _, raw := range []string{p.APIOrigin, p.ControlOrigin, p.AppListenOrigin} {
		v, e := runtimeOrigin(raw, false)
		if e != nil || origins[v] {
			return ErrPaymentsConfiguration
		}
		origins[v] = true
	}
	callback, e := runtimeOrigin(p.CallbackURL, true)
	if e != nil || callback != p.AppListenOrigin {
		return ErrPaymentsConfiguration
	}
	if len(p.Keys) < 1 || len(p.Keys) > 128 {
		return ErrPaymentsConfiguration
	}
	prev := ""
	for _, k := range p.Keys {
		if !runtimeKeyID.MatchString(k.ID) || k.ID <= prev || !runtimeHashText.MatchString(k.Digest) {
			return ErrPaymentsConfiguration
		}
		before, e := runtimeInstant(k.NotBefore)
		if e != nil {
			return e
		}
		after, e := runtimeInstant(k.NotAfter)
		if e != nil || after.Before(before) {
			return ErrPaymentsConfiguration
		}
		prev = k.ID
	}
	return nil
}
func LoadSandboxOptions(ctx context.Context, path string) (Options, error) {
	return runtimeLoadOptions(ctx, path, applicationComponent)
}
func LoadWorkerOptions(ctx context.Context, path string) (Options, error) {
	return runtimeLoadOptions(ctx, path, workerComponent)
}
func runtimeLoadOptions(ctx context.Context, path string, component runtimeComponent) (Options, error) {
	if ctx.Err() != nil {
		return Options{}, ctx.Err()
	}
	dir, name, e := runtimeOpenPrivate(path)
	if e != nil {
		return Options{}, e
	}
	defer dir.close()
	raw, e := dir.read(name, 65536)
	if e != nil {
		return Options{}, e
	}
	var m runtimeManifest
	var worker runtimeWorkerManifest
	if component == applicationComponent {
		if runtimeStrictDecode(raw, &m) != nil || m.Component != "application" {
			return Options{}, ErrPaymentsConfiguration
		}
	} else if component == workerComponent {
		if runtimeStrictDecode(raw, &worker) != nil || worker.Component != "worker" {
			return Options{}, ErrPaymentsConfiguration
		}
		m = worker.runtimeManifest
	} else {
		return Options{}, ErrPaymentsConfiguration
	}
	if m.ManifestVersion != 1 || runtimeValidateProof(m.Proof) != nil {
		return Options{}, ErrPaymentsConfiguration
	}
	canonical, e := runtimeCanonicalProof(m.Proof)
	if e != nil || runtimeSHA(canonical) != m.ProofSHA256 {
		return Options{}, ErrPaymentsConfiguration
	}
	names := map[string]bool{name: true}
	specs := []runtimeSecretFile{m.NonceFile}
	if component == workerComponent {
		specs = append(specs, worker.RuntimeDSNFile, worker.APITokenFile)
	}
	for _, f := range specs {
		if !runtimeSecretName.MatchString(f.Name) || names[f.Name] {
			return Options{}, ErrPaymentsConfiguration
		}
		names[f.Name] = true
	}
	nonceRaw, e := dir.secret(m.NonceFile, 44)
	if e != nil {
		return Options{}, e
	}
	nonce, e := runtimeMaterial32(nonceRaw)
	if e != nil {
		return Options{}, e
	}
	domain := "moni-payment-application-state-v1"
	digest := m.Proof.AppNonceDigest
	if component == workerComponent {
		domain = "moni-payment-worker-state-v1"
		digest = m.Proof.WorkerNonceDigest
	}
	if runtimeDomainDigest(domain, nonce) != digest {
		return Options{}, ErrPaymentsEnvironment
	}
	o := Options{component: component, proof: m.Proof, proofBytes: canonical, nonceDigest: digest}
	seen := map[[32]byte]bool{nonce: true}
	if component == workerComponent {
		v, e := dir.secret(worker.APITokenFile, 44)
		if e != nil {
			return Options{}, e
		}
		token, e := runtimeMaterial32(v)
		if e != nil || seen[token] || runtimeDomainDigest("moni-mock-api-token-v1", token) != m.Proof.APITokenDigest {
			return Options{}, ErrPaymentsEnvironment
		}
		seen[token] = true
		o.apiToken = token
		dsn, e := dir.secret(worker.RuntimeDSNFile, 8192)
		if e != nil {
			return Options{}, e
		}
		o.workerDSN = string(dsn)
		if _, e = runtimePoolConfig(ctx, o.workerDSN, o, m.Proof.Worker); e != nil {
			return Options{}, e
		}
	}
	if len(m.Keys) != len(m.Proof.Keys) {
		return Options{}, ErrPaymentsConfiguration
	}
	var keys []paymentproto.KeySpec
	for i, k := range m.Keys {
		if len(k.MaterialBase64URL) != 43 {
			return Options{}, ErrPaymentsConfiguration
		}
		pin := m.Proof.Keys[i]
		v, e := runtimeMaterial32([]byte(k.MaterialBase64URL))
		if e != nil || k.ID != pin.ID || seen[v] || runtimeDomainDigest("moni-mock-signing-key-v1", v) != pin.Digest {
			return Options{}, ErrPaymentsConfiguration
		}
		seen[v] = true
		before, _ := runtimeInstant(pin.NotBefore)
		after, _ := runtimeInstant(pin.NotAfter)
		keys = append(keys, paymentproto.KeySpec{ID: k.ID, Key: append([]byte(nil), v[:]...), NotBefore: before, NotAfter: after})
		o.keyMaterials = append(o.keyMaterials, v)
	}
	o.keyring, e = paymentproto.NewKeyring(m.Keys[0].ID, keys)
	if e != nil {
		return Options{}, ErrPaymentsConfiguration
	}
	if ctx.Err() != nil {
		return Options{}, ctx.Err()
	}
	o.valid = true
	return o, nil
}
