package paymentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

var roleName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

func canonicalUUID(s string) bool {
	id, e := uuid.Parse(s)
	return e == nil && id != uuid.Nil && id.String() == s
}
func validLabel(s string) bool {
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
func validRef(s string) bool {
	if !validLabel(s) {
		return false
	}
	l := strings.ToLower(s)
	if strings.HasPrefix(l, "sk-") || strings.HasPrefix(l, "mg_") || strings.HasPrefix(l, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func ownPin(m commonManifest) schemaPin {
	if m.Component == Mock {
		return m.Mock
	}
	return m.Receiver
}
func allRoles(m commonManifest) []rolePin {
	return []rolePin{m.App.Owner, m.App.Runtime, m.AppProjector, m.Mock.Owner, m.Mock.Runtime, m.Receiver.Owner, m.Receiver.Runtime}
}
func validatePins(m commonManifest, component Component) error {
	if component != Mock && component != Receiver {
		return ErrConfiguration
	}
	if m.ManifestVersion != 1 || m.ProtocolVersion != 1 || m.Component != component || !canonicalUUID(m.EnvironmentID) || !canonicalUUID(m.StateDirectoryID) || !validRef(m.MerchantID) || !validLabel(m.PayeeLabel) {
		return ErrConfiguration
	}
	if m.Database.Host != "127.0.0.1" || m.Database.Port != 55439 || m.Database.Name != "moni_test" || m.Database.OID == 0 {
		return ErrConfiguration
	}
	schemaNames := map[string]bool{}
	schemaOIDs := map[uint32]bool{}
	suffix := ""
	for i, p := range []schemaPin{m.App, m.Mock, m.Receiver} {
		prefix := []string{"payment_sandbox_", "payment_mock_", "payment_receiver_"}[i]
		if !regexp.MustCompile(`^`+prefix+`[0-9a-f]{32}$`).MatchString(p.Name) || p.OID == 0 || p.Owner.Login || !p.Runtime.Login || schemaNames[p.Name] || schemaOIDs[p.OID] {
			return ErrConfiguration
		}
		part := strings.TrimPrefix(p.Name, prefix)
		if suffix != "" && suffix != part {
			return ErrConfiguration
		}
		suffix = part
		schemaNames[p.Name] = true
		schemaOIDs[p.OID] = true
	}
	if !m.AppProjector.Login {
		return ErrConfiguration
	}
	roleNames := map[string]bool{}
	roleOIDs := map[uint32]bool{}
	for _, r := range allRoles(m) {
		if !roleName.MatchString(r.Name) || r.OID == 0 || roleNames[r.Name] || roleOIDs[r.OID] {
			return ErrConfiguration
		}
		roleNames[r.Name] = true
		roleOIDs[r.OID] = true
	}
	digest, e := paymentsandboxschema.Digest(paymentsandboxschema.Component(component))
	if e != nil || m.OwnSchemaSQL.SchemaVersion != 1 || m.OwnSchemaSQL.SHA256 != digest {
		return ErrConfiguration
	}
	return nil
}
func parseOrigin(s string, callback bool) (string, error) {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", ErrConfiguration
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.ParseUint(port, 10, 16)
	if e != nil || pe != nil || n == 0 || host != "127.0.0.1" || port != strconv.FormatUint(n, 10) || u.Host != net.JoinHostPort(host, port) {
		return "", ErrConfiguration
	}
	if callback {
		if u.Path != "/api/payments/sandbox/notifications" {
			return "", ErrConfiguration
		}
	} else if u.Path != "" && u.Path != "/" {
		return "", ErrConfiguration
	}
	return "http://" + u.Host, nil
}
func material32(raw []byte) ([32]byte, error) {
	var key [32]byte
	raw = []byte(strings.TrimSuffix(string(raw), "\n"))
	decoded, e := base64.RawURLEncoding.DecodeString(string(raw))
	if e != nil || len(raw) != 43 || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != string(raw) {
		return key, ErrConfiguration
	}
	copy(key[:], decoded)
	return key, nil
}
func domainDigest(domain string, material [32]byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(material[:])
	return hex.EncodeToString(h.Sum(nil))
}
func parseInstant(s string) (time.Time, error) {
	t, e := time.Parse("2006-01-02T15:04:05.000000Z", s)
	if e != nil || t.IsZero() || t.Year() < 1 || t.Year() > 9999 || t.UTC().Format("2006-01-02T15:04:05.000000Z") != s {
		return time.Time{}, ErrConfiguration
	}
	return t.UTC(), nil
}

// Load reads a single component's private files. It performs no database work.
func Load(ctx context.Context, path string, component Component) (Config, error) {
	if ctx.Err() != nil {
		return Config{}, ctx.Err()
	}
	d, name, e := openPrivate(path)
	if e != nil {
		return Config{}, e
	}
	defer d.close()
	raw, e := d.read(name, 64<<10)
	if e != nil {
		return Config{}, e
	}
	c := Config{origins: map[Endpoint]string{}, windows: map[string]keyWindow{}}
	var ds, ns, as, cs secretFile
	switch component {
	case Mock:
		var wire mockManifest
		if strictDecode(raw, &wire) != nil {
			return Config{}, ErrConfiguration
		}
		c.manifest = wire.commonManifest
		c.origins[API] = wire.APIOrigin
		c.origins[Control] = wire.ControlOrigin
		as = wire.APITokenFile
		cs = wire.ControlTokenFile
	case Receiver:
		var wire receiverManifest
		if strictDecode(raw, &wire) != nil {
			return Config{}, ErrConfiguration
		}
		c.manifest = wire.commonManifest
		c.origins[ReceiverHTTP] = wire.ListenOrigin
	default:
		return Config{}, ErrConfiguration
	}
	if e = validatePins(c.manifest, component); e != nil {
		return Config{}, e
	}
	cbOrigin, e := parseOrigin(c.manifest.CallbackURL, true)
	if e != nil {
		return Config{}, e
	}
	ports := map[string]bool{}
	for endpoint, value := range c.origins {
		origin, e := parseOrigin(value, false)
		if e != nil || ports[origin] {
			return Config{}, ErrConfiguration
		}
		ports[origin] = true
		if endpoint == ReceiverHTTP && origin != cbOrigin {
			return Config{}, ErrConfiguration
		}
		if component == Mock && origin == cbOrigin {
			return Config{}, ErrConfiguration
		}
	}
	ds = c.manifest.RuntimeDSNFile
	ns = c.manifest.NonceFile
	names := map[string]bool{name: true}
	specs := []secretFile{ds, ns}
	if component == Mock {
		specs = append(specs, as, cs)
	}
	for _, s := range specs {
		if names[s.Name] {
			return Config{}, ErrConfiguration
		}
		names[s.Name] = true
	}
	dsn, e := d.secret(ds, 8<<10)
	if e != nil {
		return Config{}, e
	}
	c.dsn = string(dsn)
	if _, e = dsnConfig(c); e != nil {
		return Config{}, e
	}
	nonceRaw, e := d.secret(ns, 44)
	if e != nil {
		return Config{}, e
	}
	nonce, e := material32(nonceRaw)
	if e != nil {
		return Config{}, e
	}
	c.nonceDigest = domainDigest("moni-"+string(component)+"-state-v1", nonce)
	seen := map[[32]byte]bool{nonce: true}
	if component == Mock {
		for _, s := range []struct {
			file secretFile
			kind TokenKind
		}{{as, APIToken}, {cs, ControlToken}} {
			b, e := d.secret(s.file, 44)
			if e != nil {
				return Config{}, e
			}
			v, e := material32(b)
			if e != nil || seen[v] {
				return Config{}, ErrConfiguration
			}
			seen[v] = true
			if s.kind == APIToken {
				c.api = v
				c.apiDigest = domainDigest("moni-mock-api-token-v1", v)
			} else {
				c.control = v
				c.controlDigest = domainDigest("moni-mock-control-token-v1", v)
			}
		}
	}
	if len(c.manifest.Keys) < 1 || len(c.manifest.Keys) > 128 {
		return Config{}, ErrConfiguration
	}
	keys := make([]paymentproto.KeySpec, 0, len(c.manifest.Keys))
	for _, k := range c.manifest.Keys {
		if len(k.MaterialBase64URL) != 43 {
			return Config{}, ErrConfiguration
		}
		v, e := material32([]byte(k.MaterialBase64URL))
		if e != nil || seen[v] || k.Digest != domainDigest("moni-mock-signing-key-v1", v) {
			return Config{}, ErrConfiguration
		}
		seen[v] = true
		before, e := parseInstant(k.NotBefore)
		if e != nil {
			return Config{}, e
		}
		after, e := parseInstant(k.NotAfter)
		if e != nil || after.Before(before) {
			return Config{}, ErrConfiguration
		}
		keys = append(keys, paymentproto.KeySpec{ID: k.ID, Key: append([]byte(nil), v[:]...), NotBefore: before, NotAfter: after})
		c.windows[k.ID] = keyWindow{before, after}
	}
	c.keys, e = paymentproto.NewKeyring(c.manifest.ActiveKeyID, keys)
	if e != nil {
		return Config{}, ErrConfiguration
	}
	if ctx.Err() != nil {
		return Config{}, ctx.Err()
	}
	c.valid = true
	return c, nil
}
